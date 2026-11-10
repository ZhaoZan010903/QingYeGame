package app

import (
	"encoding/json"
	"errors"
	"github.com/jackc/pgx/v5"
	"net/http"
	"strconv"
	"strings"
	"unicode/utf8"
)

func itoa(v int) string { return strconv.Itoa(v) }
func (a *App) adminOnly(h http.HandlerFunc) http.HandlerFunc {
	return a.memberMutation(func(w http.ResponseWriter, r *http.Request) {
		if _, ok := a.requireAdmin(w, r); ok {
			h(w, r)
		}
	})
}
func (a *App) registerAdmin(m *http.ServeMux) {
	m.HandleFunc("GET /api/v1/admin/products", a.adminOnly(a.adminProducts))
	m.HandleFunc("GET /api/v1/admin/products/{id}", a.adminOnly(a.adminProduct))
	m.HandleFunc("POST /api/v1/admin/products", a.adminOnly(a.adminSaveProduct))
	m.HandleFunc("PATCH /api/v1/admin/products/{id}", a.adminOnly(a.adminSaveProduct))
	for _, action := range []string{"review", "status", "publish"} {
		m.HandleFunc("POST /api/v1/admin/products/{id}/"+action, a.adminOnly(a.adminTransition))
	}
	m.HandleFunc("PUT /api/v1/admin/products/{id}/featured", a.adminOnly(a.adminFeatured))
	m.HandleFunc("GET /api/v1/admin/categories", a.adminOnly(a.adminCategories))
	m.HandleFunc("POST /api/v1/admin/categories", a.adminOnly(a.saveCategory))
	m.HandleFunc("PUT /api/v1/admin/categories/{id}", a.adminOnly(a.saveCategory))
	m.HandleFunc("GET /api/v1/admin/contacts", a.adminOnly(a.adminContacts))
	m.HandleFunc("POST /api/v1/admin/contacts", a.adminOnly(a.saveContact))
	m.HandleFunc("PUT /api/v1/admin/contacts/{id}", a.adminOnly(a.saveContact))
	m.HandleFunc("GET /api/v1/admin/pages", a.adminOnly(a.adminPages))
	m.HandleFunc("PUT /api/v1/admin/pages/{slug}", a.adminOnly(a.savePage))
	m.HandleFunc("GET /api/v1/admin/settings", a.adminOnly(a.adminSettings))
	m.HandleFunc("PUT /api/v1/admin/settings/{key}", a.adminOnly(a.saveSetting))
	m.HandleFunc("GET /api/v1/admin/users", a.adminOnly(a.adminUsers))
	m.HandleFunc("PATCH /api/v1/admin/users/{id}", a.adminOnly(a.updateUser))
	m.HandleFunc("GET /api/v1/admin/recommendations", a.adminOnly(a.adminRecommendations))
	m.HandleFunc("POST /api/v1/admin/recommendations/refresh", a.adminOnly(a.refreshRecommendations))
}
func (a *App) adminProducts(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit, offset, e := decodePage(q)
	if e != nil {
		fail(w, 422, "INVALID_CURSOR", e.Error())
		return
	}
	where := "true"
	args := []any{}
	for _, key := range []string{"status", "source"} {
		if v := q.Get(key); v != "" && v != "all" {
			args = append(args, v)
			where += " AND p." + key + "=$" + itoa(len(args))
		}
	}
	if v := q.Get("q"); v != "" {
		args = append(args, v)
		where += " AND (p.title ILIKE '%'||$" + itoa(len(args)) + "||'%' OR p.product_code ILIKE '%'||$" + itoa(len(args)) + "||'%')"
	}
	items, total, e := a.productRows(r.Context(), where, args, "p.updated_at DESC,p.id", limit, offset)
	if e != nil {
		a.dbError(w, e)
		return
	}
	writeJSON(w, 200, map[string]any{"data": items, "total": total, "next_cursor": nextPage(offset, limit, total)})
}
func (a *App) adminProduct(w http.ResponseWriter, r *http.Request) {
	p, e := a.productView(r.Context(), r.PathValue("id"), true)
	if errors.Is(e, pgx.ErrNoRows) {
		fail(w, 404, "NOT_FOUND", "商品不存在")
		return
	}
	if e != nil {
		a.dbError(w, e)
		return
	}
	writeJSON(w, 200, map[string]any{"data": p})
}
func (a *App) adminSaveProduct(w http.ResponseWriter, r *http.Request) {
	var in ProductInput
	if !readJSON(w, r, &in) {
		return
	}
	check := in
	if r.PathValue("id") != "" && utf8.RuneCountInString(in.Title) > 100 {
		var original, source string
		if a.DB.QueryRow(r.Context(), "SELECT title,source FROM products WHERE id=$1", r.PathValue("id")).Scan(&original, &source) == nil && source == "legacy" && original == in.Title {
			check.Title = string([]rune(original)[:100])
		}
	}
	if e := a.validateProductInput(r.Context(), check, false); e != nil {
		fail(w, 422, "INVALID_PRODUCT", e.Error())
		return
	}
	attrs, _ := json.Marshal(in.Attributes)
	if in.Attributes == nil {
		attrs = []byte("{}")
	}
	actor, _ := a.currentUser(r)
	tx, e := a.DB.Begin(r.Context())
	if e != nil {
		a.dbError(w, e)
		return
	}
	defer tx.Rollback(r.Context())
	id := r.PathValue("id")
	if id == "" {
		replay, hash, replayErr := replayProduct(r.Context(), tx, actor.ID, "admin-create", in.RequestID, in)
		if replayErr != nil {
			fail(w, 409, "IDEMPOTENCY_CONFLICT", replayErr.Error())
			return
		}
		if replay != "" {
			tx.Rollback(r.Context())
			p, err := a.productView(r.Context(), replay, true)
			if err != nil {
				a.dbError(w, err)
				return
			}
			writeJSON(w, 200, map[string]any{"data": p})
			return
		}
		id = newID()
		_, e = tx.Exec(r.Context(), `INSERT INTO products(id,product_code,category_id,title,trade_type,price_unit,price_amount,attributes,description,source) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,'platform')`, id, "QY-"+strings.ToUpper(strings.ReplaceAll(id, "-", "")[:10]), in.CategoryID, strings.TrimSpace(in.Title), in.TradeType, in.PriceUnit, in.PriceAmount, attrs, in.Description)
		if e == nil {
			e = recordProductReplay(r.Context(), tx, actor.ID, "admin-create", in.RequestID, hash, id)
		}
	} else {
		var version int
		e = tx.QueryRow(r.Context(), "SELECT version FROM products WHERE id=$1 FOR UPDATE", id).Scan(&version)
		if errors.Is(e, pgx.ErrNoRows) {
			fail(w, 404, "NOT_FOUND", "商品不存在")
			return
		}
		if e == nil && version != in.ExpectedVersion {
			fail(w, 409, "VERSION_CONFLICT", "商品已更新，请重新加载")
			return
		}
		if e == nil {
			_, e = tx.Exec(r.Context(), `UPDATE products SET category_id=$2,title=$3,trade_type=$4,price_unit=$5,price_amount=$6,attributes=$7,description=$8,status='draft',version=version+1,approved_version=NULL,review_note='',updated_at=now() WHERE id=$1`, id, in.CategoryID, strings.TrimSpace(in.Title), in.TradeType, in.PriceUnit, in.PriceAmount, attrs, in.Description)
		}
	}
	if e != nil {
		a.dbError(w, e)
		return
	}
	_, e = tx.Exec(r.Context(), "INSERT INTO admin_logs(id,user_id,action,resource_id) VALUES($1,$2,'product.save',$3)", newID(), actor.ID, id)
	if e == nil {
		e = tx.Commit(r.Context())
	}
	if e != nil {
		a.dbError(w, e)
		return
	}
	p, e := a.productView(r.Context(), id, true)
	if e != nil {
		a.dbError(w, e)
		return
	}
	writeJSON(w, 200, map[string]any{"data": p})
}
func (a *App) adminTransition(w http.ResponseWriter, r *http.Request) {
	var in struct {
		ExpectedVersion int    `json:"expected_version"`
		Decision        string `json:"decision"`
		Action          string `json:"action"`
		Reason          string `json:"reason"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	id := r.PathValue("id")
	actor, _ := a.currentUser(r)
	tx, e := a.DB.Begin(r.Context())
	if e != nil {
		a.dbError(w, e)
		return
	}
	defer tx.Rollback(r.Context())
	var status, source, title, description string
	var version int
	e = tx.QueryRow(r.Context(), "SELECT status,source,title,description,version FROM products WHERE id=$1 FOR UPDATE", id).Scan(&status, &source, &title, &description, &version)
	if errors.Is(e, pgx.ErrNoRows) {
		fail(w, 404, "NOT_FOUND", "商品不存在")
		return
	}
	if e != nil {
		a.dbError(w, e)
		return
	}
	if version != in.ExpectedVersion {
		fail(w, 409, "VERSION_CONFLICT", "商品版本已变化")
		return
	}
	target := ""
	action := ""
	switch {
	case strings.HasSuffix(r.URL.Path, "/review"):
		action = "review"
		if status == "pending" {
			if in.Decision == "approve" {
				target = "published"
			}
			if in.Decision == "reject" && strings.TrimSpace(in.Reason) != "" {
				target = "rejected"
			}
		}
	case strings.HasSuffix(r.URL.Path, "/publish"):
		action = "publish"
		if source != "member" && (status == "draft" || status == "rejected" || status == "offline") {
			target = "published"
		}
	default:
		action = in.Action
		if action == "submit" && (status == "draft" || status == "rejected" || status == "offline") {
			target = "pending"
		}
		if action == "offline" && status == "published" && strings.TrimSpace(in.Reason) != "" {
			target = "offline"
		}
	}
	if target == "" {
		fail(w, 409, "INVALID_STATE", "当前状态不允许该操作；退回或下架必须填写原因")
		return
	}
	if target == "pending" || target == "published" {
		var input ProductInput
		e = tx.QueryRow(r.Context(), "SELECT category_id,title,trade_type,price_unit,price_amount::text,attributes,description FROM products WHERE id=$1", id).Scan(&input.CategoryID, &input.Title, &input.TradeType, &input.PriceUnit, &input.PriceAmount, &input.Attributes, &input.Description)
		if e != nil {
			a.dbError(w, e)
			return
		}
		if source == "legacy" && utf8.RuneCountInString(input.Title) > 100 {
			input.Title = string([]rune(input.Title)[:100])
		}
		if e = a.validateProductInputWith(r.Context(), tx, input, true); e != nil {
			fail(w, 422, "INVALID_PRODUCT", e.Error())
			return
		}
		var ready, total int
		var enabled bool
		e = tx.QueryRow(r.Context(), "SELECT c.enabled FROM products p JOIN categories c ON c.id=p.category_id WHERE p.id=$1", id).Scan(&enabled)
		if e == nil {
			e = tx.QueryRow(r.Context(), "SELECT count(*) FILTER(WHERE m.status='ready'),count(*) FROM product_images pi JOIN media_files m ON m.id=pi.media_id WHERE pi.product_id=$1", id).Scan(&ready, &total)
		}
		if e != nil {
			a.dbError(w, e)
			return
		}
		if !enabled || strings.TrimSpace(title) == "" || strings.TrimSpace(description) == "" || ready == 0 || ready != total {
			fail(w, 422, "INCOMPLETE_PRODUCT", "请补全资料并等待全部图片就绪")
			return
		}
	}
	_, e = tx.Exec(r.Context(), `UPDATE products SET status=$2,version=version+1,approved_version=CASE WHEN $2='published' THEN version+1 ELSE NULL END,review_note=$3,published_at=CASE WHEN $2='published' THEN COALESCE(published_at,now()) ELSE published_at END,updated_at=now() WHERE id=$1`, id, target, strings.TrimSpace(in.Reason))
	if e == nil {
		_, e = tx.Exec(r.Context(), `INSERT INTO product_reviews(id,product_id,reviewer_id,decision,reason,version,snapshot)
SELECT $1,id,$2,$3,$4,version,(to_jsonb(products)-'legacy_payload')||jsonb_build_object('images',
COALESCE((SELECT jsonb_agg(jsonb_build_object('id',m.id,'sort_order',pi.sort_order,'alt',m.filename) ORDER BY pi.sort_order,m.id) FROM product_images pi JOIN media_files m ON m.id=pi.media_id WHERE pi.product_id=products.id),'[]'::jsonb))
FROM products WHERE id=$5`, newID(), actor.ID, target, in.Reason, id)
	}
	if e == nil {
		_, e = tx.Exec(r.Context(), "INSERT INTO admin_logs(id,user_id,action,resource_id) VALUES($1,$2,$3,$4)", newID(), actor.ID, "product."+action, id)
	}
	if e == nil {
		e = tx.Commit(r.Context())
	}
	if e != nil {
		a.dbError(w, e)
		return
	}
	p, e := a.productView(r.Context(), id, true)
	if e != nil {
		a.dbError(w, e)
		return
	}
	writeJSON(w, 200, map[string]any{"data": p})
}
func (a *App) adminFeatured(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Rank *int `json:"manual_rank"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	if in.Rank != nil && (*in.Rank < 0 || *in.Rank > 10000) {
		fail(w, 422, "INVALID_RANK", "精选顺序无效")
		return
	}
	actor, _ := a.currentUser(r)
	tx, e := a.DB.Begin(r.Context())
	if e != nil {
		a.dbError(w, e)
		return
	}
	defer tx.Rollback(r.Context())
	tag, e := tx.Exec(r.Context(), "UPDATE products SET manual_rank=$2 WHERE id=$1 AND status='published' AND approved_version=version", r.PathValue("id"), in.Rank)
	if e != nil {
		a.dbError(w, e)
		return
	}
	if tag.RowsAffected() == 0 {
		fail(w, 409, "INVALID_STATE", "仅已展示商品可以设置精选")
		return
	}
	_, e = tx.Exec(r.Context(), "INSERT INTO admin_logs(id,user_id,action,resource_id,detail) VALUES($1,$2,'product.featured',$3,$4)", newID(), actor.ID, r.PathValue("id"), map[string]any{"manual_rank": in.Rank})
	if e == nil {
		e = tx.Commit(r.Context())
	}
	if e != nil {
		a.dbError(w, e)
		return
	}
	a.adminProduct(w, r)
}
