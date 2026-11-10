package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"
)

func (a *App) registerMember(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/me/favorites", a.memberList)
	mux.HandleFunc("PUT /api/v1/me/favorites/{id}", a.memberMutation(a.favoriteChange))
	mux.HandleFunc("DELETE /api/v1/me/favorites/{id}", a.memberMutation(a.favoriteChange))
	mux.HandleFunc("GET /api/v1/me/products", a.memberList)
	mux.HandleFunc("POST /api/v1/me/products", a.memberMutation(a.createDraft))
	mux.HandleFunc("GET /api/v1/me/products/{id}", a.memberDetail)
	mux.HandleFunc("PATCH /api/v1/me/products/{id}", a.memberMutation(a.editDraft))
	mux.HandleFunc("POST /api/v1/me/products/{id}/submit", a.memberMutation(a.memberTransition))
	mux.HandleFunc("POST /api/v1/me/products/{id}/withdraw", a.memberMutation(a.memberTransition))
	mux.HandleFunc("GET /api/v1/me/products/{id}/images", a.memberImageList)
	mux.HandleFunc("POST /api/v1/me/products/{id}/images", a.memberMutation(a.appendMemberImages))
	mux.HandleFunc("PATCH /api/v1/me/products/{id}/images/{media_id}", a.memberMutation(a.changeMemberImage))
	mux.HandleFunc("DELETE /api/v1/me/products/{id}/images/{media_id}", a.memberMutation(a.changeMemberImage))
}
func (a *App) registerAdminImages(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/admin/products/{id}/images", a.memberImageList)
	mux.HandleFunc("POST /api/v1/admin/products/{id}/images", a.memberMutation(a.appendMemberImages))
	mux.HandleFunc("PATCH /api/v1/admin/products/{id}/images/{media_id}", a.memberMutation(a.changeMemberImage))
	mux.HandleFunc("DELETE /api/v1/admin/products/{id}/images/{media_id}", a.memberMutation(a.changeMemberImage))
}

func (a *App) imageActor(w http.ResponseWriter, r *http.Request) (*User, bool) {
	if strings.HasPrefix(r.URL.Path, "/api/v1/admin/") {
		return a.requireAdmin(w, r)
	}
	return a.requireUser(w, r)
}
func imageAdmin(r *http.Request, u *User) bool {
	return strings.HasPrefix(r.URL.Path, "/api/v1/admin/") && u.Role == "admin"
}
func lockedImageOwner(ctx context.Context, tx pgx.Tx, r *http.Request, u *User, id string) (int, string, error) {
	if !imageAdmin(r, u) {
		return lockedOwner(ctx, tx, id, u.ID)
	}
	var version int
	var status string
	err := tx.QueryRow(ctx, `SELECT version,status FROM products WHERE id=$1 FOR UPDATE`, id).Scan(&version, &status)
	return version, status, err
}
func imageAudit(ctx context.Context, tx pgx.Tx, r *http.Request, u *User, id, action string) error {
	if !imageAdmin(r, u) {
		return nil
	}
	_, err := tx.Exec(ctx, `INSERT INTO admin_logs(id,user_id,action,resource_id) VALUES($1,$2,$3,$4)`, newID(), u.ID, action, id)
	return err
}
func memberPagination(w http.ResponseWriter, r *http.Request) (int, int, bool) {
	offset := 0
	limit := 20
	var err error
	if raw := r.URL.Query().Get("cursor"); raw != "" {
		offset, err = strconv.Atoi(raw)
		if err != nil || offset < 0 || offset > 1000000 {
			fail(w, 422, "INVALID_CURSOR", "分页参数无效")
			return 0, 0, false
		}
	}
	if raw := r.URL.Query().Get("limit"); raw != "" {
		limit, err = strconv.Atoi(raw)
		if err != nil || limit < 1 || limit > 50 {
			fail(w, 422, "INVALID_LIMIT", "每页应为1至50条")
			return 0, 0, false
		}
	}
	return offset, limit, true
}
func pageCursor(offset, limit, total int) any {
	if offset+limit < total {
		return strconv.Itoa(offset + limit)
	}
	return nil
}
func memberDBError(w http.ResponseWriter, err error) {
	if errors.Is(err, pgx.ErrNoRows) {
		fail(w, 404, "NOT_FOUND", "未找到资料")
	} else {
		fail(w, 503, "DATABASE_UNAVAILABLE", "服务暂不可用，请稍后重试")
	}
}
func (a *App) owned(r *http.Request, u *User, id string) bool {
	var yes bool
	err := a.DB.QueryRow(r.Context(), `SELECT EXISTS(SELECT 1 FROM products WHERE id=$1 AND owner_id=$2)`, id, u.ID).Scan(&yes)
	return err == nil && yes
}
func (a *App) memberDetail(w http.ResponseWriter, r *http.Request) {
	u, ok := a.requireUser(w, r)
	if !ok {
		return
	}
	id := r.PathValue("id")
	if !a.owned(r, u, id) {
		fail(w, 404, "NOT_FOUND", "未找到资料")
		return
	}
	a.memberProductResult(w, r, id)
}
func (a *App) memberProductResult(w http.ResponseWriter, r *http.Request, id string) {
	p, err := a.productView(r.Context(), id, true)
	if err != nil {
		memberDBError(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"data": p})
}
func (a *App) memberList(w http.ResponseWriter, r *http.Request) {
	u, ok := a.requireUser(w, r)
	if !ok {
		return
	}
	offset, limit, ok := memberPagination(w, r)
	if !ok {
		return
	}
	favorites := strings.HasSuffix(r.URL.Path, "/favorites")
	var count int
	var rows pgx.Rows
	var err error
	if favorites {
		err = a.DB.QueryRow(r.Context(), `SELECT count(*) FROM favorites WHERE user_id=$1 AND active`, u.ID).Scan(&count)
		if err == nil {
			rows, err = a.DB.Query(r.Context(), `SELECT product_id FROM favorites WHERE user_id=$1 AND active ORDER BY created_at DESC,product_id DESC LIMIT $2 OFFSET $3`, u.ID, limit, offset)
		}
	} else {
		status := r.URL.Query().Get("status")
		if status != "" && !strings.Contains("|draft|pending|published|rejected|offline|", "|"+status+"|") {
			fail(w, 422, "INVALID_STATUS", "状态无效")
			return
		}
		err = a.DB.QueryRow(r.Context(), `SELECT count(*) FROM products WHERE owner_id=$1 AND ($2='' OR status=$2)`, u.ID, status).Scan(&count)
		if err == nil {
			rows, err = a.DB.Query(r.Context(), `SELECT id FROM products WHERE owner_id=$1 AND ($2='' OR status=$2) ORDER BY updated_at DESC,id DESC LIMIT $3 OFFSET $4`, u.ID, status, limit, offset)
		}
	}
	if err != nil {
		memberDBError(w, err)
		return
	}
	ids := []string{}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			break
		}
		ids = append(ids, id)
	}
	rowErr := rows.Err()
	rows.Close()
	if err != nil || rowErr != nil {
		memberDBError(w, fmt.Errorf("list read failed"))
		return
	}
	// A page uses one bulk read and one cover image per product, independent of
	// gallery size; never issue a detail query for every card in the list.
	products := map[string]map[string]any{}
	if len(ids) > 0 {
		where := "p.id=ANY($1::text[])"
		args := []any{ids}
		if favorites {
			where += " AND " + visibleSQL
		} else {
			where += " AND p.owner_id=$2"
			args = append(args, u.ID)
		}
		batch, e := a.DB.Query(r.Context(), "SELECT "+fmt.Sprintf(productJSON, 1)+" FROM products p JOIN categories c ON c.id=p.category_id WHERE "+where, args...)
		if e != nil {
			memberDBError(w, e)
			return
		}
		for batch.Next() {
			var raw []byte
			if e = batch.Scan(&raw); e != nil {
				break
			}
			var p map[string]any
			if e = json.Unmarshal(raw, &p); e != nil {
				break
			}
			products[p["id"].(string)] = p
		}
		if e == nil {
			e = batch.Err()
		}
		batch.Close()
		if e != nil {
			memberDBError(w, e)
			return
		}
	}
	result := []map[string]any{}
	for _, id := range ids {
		p := products[id]
		if p == nil {
			if !favorites {
				continue
			}
			p = map[string]any{"id": id, "title": "该账号已停止公开", "status": "offline", "price_amount": nil, "images": []any{}, "image_total": 0}
		}
		if favorites {
			publicProductFields(p)
			p["favorite"] = true
		}
		result = append(result, p)
	}
	writeJSON(w, 200, map[string]any{"data": result, "total": count, "next_cursor": pageCursor(offset, limit, count)})
}
func (a *App) favoriteChange(w http.ResponseWriter, r *http.Request) {
	u, ok := a.requireUser(w, r)
	if !ok {
		return
	}
	id := r.PathValue("id")
	if r.Method == "PUT" {
		tag, err := a.DB.Exec(r.Context(), `INSERT INTO favorites(user_id,product_id) SELECT $1,p.id FROM products p JOIN categories c ON c.id=p.category_id WHERE p.id=$2 AND p.status='published' AND p.approved_version=p.version AND c.enabled ON CONFLICT(user_id,product_id) DO UPDATE SET active=true,updated_at=now()`, u.ID, id)
		if err != nil {
			memberDBError(w, err)
			return
		}
		if tag.RowsAffected() == 0 {
			fail(w, 404, "NOT_FOUND", "该商品当前不可收藏")
			return
		}
	} else {
		if _, err := a.DB.Exec(r.Context(), `UPDATE favorites SET active=false,updated_at=now() WHERE user_id=$1 AND product_id=$2`, u.ID, id); err != nil {
			memberDBError(w, err)
			return
		}
	}
	writeJSON(w, 200, map[string]any{"data": map[string]any{"ok": true}})
}

// A transaction advisory lock serializes the first use of an idempotency key.
func replayProduct(ctx context.Context, tx pgx.Tx, user, scope, key string, body any) (string, string, error) {
	if len(key) < 8 || len(key) > 128 {
		return "", "", fmt.Errorf("request_id must be 8–128 characters")
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return "", "", err
	}
	hash := tokenHash(string(raw))
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, user+":"+scope+":"+key); err != nil {
		return "", "", err
	}
	var oldHash string
	var result map[string]any
	err = tx.QueryRow(ctx, `SELECT body_hash,response FROM idempotency WHERE user_id=$1 AND scope=$2 AND request_id=$3`, user, scope, key).Scan(&oldHash, &result)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", hash, nil
	}
	if err != nil {
		return "", "", err
	}
	if oldHash != hash {
		return "", "", fmt.Errorf("idempotency key reused with another payload")
	}
	id, _ := result["product_id"].(string)
	return id, hash, nil
}
func recordProductReplay(ctx context.Context, tx pgx.Tx, user, scope, key, hash, id string) error {
	_, err := tx.Exec(ctx, `INSERT INTO idempotency(user_id,scope,request_id,body_hash,response) VALUES($1,$2,$3,$4,$5)`, user, scope, key, hash, map[string]any{"product_id": id})
	return err
}
func (a *App) createDraft(w http.ResponseWriter, r *http.Request) {
	u, ok := a.requireUser(w, r)
	if !ok {
		return
	}
	var input ProductInput
	if !readJSON(w, r, &input) {
		return
	}
	if input.Attributes == nil {
		input.Attributes = map[string]any{}
	}
	if err := a.validateProductInput(r.Context(), input, false); err != nil {
		fail(w, 422, "INVALID_PRODUCT", err.Error())
		return
	}
	tx, err := a.DB.Begin(r.Context())
	if err != nil {
		memberDBError(w, err)
		return
	}
	defer tx.Rollback(r.Context())
	replay, hash, err := replayProduct(r.Context(), tx, u.ID, "create-product", input.RequestID, input)
	if err != nil {
		fail(w, 409, "IDEMPOTENCY_CONFLICT", err.Error())
		return
	}
	if replay != "" {
		tx.Rollback(r.Context())
		a.memberProductResult(w, r, replay)
		return
	}
	id := newID()
	code := "QY-" + strings.ToUpper(strings.ReplaceAll(id, "-", "")[:10])
	_, err = tx.Exec(r.Context(), `INSERT INTO products(id,product_code,owner_id,category_id,title,trade_type,price_amount,price_unit,attributes,description,source) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,'member')`, id, code, u.ID, input.CategoryID, input.Title, input.TradeType, input.PriceAmount, input.PriceUnit, input.Attributes, input.Description)
	if err == nil {
		err = recordProductReplay(r.Context(), tx, u.ID, "create-product", input.RequestID, hash, id)
	}
	if err == nil {
		err = tx.Commit(r.Context())
	}
	if err != nil {
		memberDBError(w, err)
		return
	}
	a.memberProductResult(w, r, id)
}
func lockedOwner(ctx context.Context, tx pgx.Tx, id, user string) (int, string, error) {
	var version int
	var status string
	err := tx.QueryRow(ctx, `SELECT version,status FROM products WHERE id=$1 AND owner_id=$2 FOR UPDATE`, id, user).Scan(&version, &status)
	return version, status, err
}
func checkMemberVersion(w http.ResponseWriter, actual, wanted int) bool {
	if wanted < 1 || actual != wanted {
		fail(w, 409, "VERSION_CONFLICT", "资料已更新，请重新加载后操作")
		return false
	}
	return true
}
func (a *App) editDraft(w http.ResponseWriter, r *http.Request) {
	u, ok := a.requireUser(w, r)
	if !ok {
		return
	}
	var input ProductInput
	if !readJSON(w, r, &input) {
		return
	}
	if input.Attributes == nil {
		input.Attributes = map[string]any{}
	}
	if err := a.validateProductInput(r.Context(), input, false); err != nil {
		fail(w, 422, "INVALID_PRODUCT", err.Error())
		return
	}
	id := r.PathValue("id")
	tx, err := a.DB.Begin(r.Context())
	if err != nil {
		memberDBError(w, err)
		return
	}
	defer tx.Rollback(r.Context())
	version, _, err := lockedOwner(r.Context(), tx, id, u.ID)
	if err != nil {
		memberDBError(w, err)
		return
	}
	if !checkMemberVersion(w, version, input.ExpectedVersion) {
		return
	}
	_, err = tx.Exec(r.Context(), `UPDATE products SET category_id=$1,title=$2,trade_type=$3,price_amount=$4,price_unit=$5,attributes=$6,description=$7,status='draft',version=version+1,approved_version=NULL,review_note='',updated_at=now() WHERE id=$8`, input.CategoryID, input.Title, input.TradeType, input.PriceAmount, input.PriceUnit, input.Attributes, input.Description, id)
	if err == nil {
		err = tx.Commit(r.Context())
	}
	if err != nil {
		memberDBError(w, err)
		return
	}
	a.memberProductResult(w, r, id)
}
func (a *App) memberTransition(w http.ResponseWriter, r *http.Request) {
	u, ok := a.requireUser(w, r)
	if !ok {
		return
	}
	var input struct {
		ExpectedVersion int `json:"expected_version"`
	}
	if !readJSON(w, r, &input) {
		return
	}
	id := r.PathValue("id")
	tx, err := a.DB.Begin(r.Context())
	if err != nil {
		memberDBError(w, err)
		return
	}
	defer tx.Rollback(r.Context())
	version, status, err := lockedOwner(r.Context(), tx, id, u.ID)
	if err != nil {
		memberDBError(w, err)
		return
	}
	if !checkMemberVersion(w, version, input.ExpectedVersion) {
		return
	}
	next := "draft"
	if strings.HasSuffix(r.URL.Path, "/submit") {
		if status != "draft" && status != "rejected" && status != "offline" {
			fail(w, 409, "INVALID_STATUS", "当前状态不能提交审核")
			return
		}
		var p ProductInput
		err = tx.QueryRow(r.Context(), `SELECT category_id,title,trade_type,price_amount::text,price_unit,attributes,description FROM products WHERE id=$1`, id).Scan(&p.CategoryID, &p.Title, &p.TradeType, &p.PriceAmount, &p.PriceUnit, &p.Attributes, &p.Description)
		if err != nil {
			memberDBError(w, err)
			return
		}
		if err = a.validateProductInputWith(r.Context(), tx, p, true); err != nil {
			fail(w, 422, "INCOMPLETE_PRODUCT", err.Error())
			return
		}
		var count int
		err = tx.QueryRow(r.Context(), `SELECT count(*) FROM product_images pi JOIN media_files m ON m.id=pi.media_id WHERE pi.product_id=$1 AND m.status='ready'`, id).Scan(&count)
		if err != nil {
			memberDBError(w, err)
			return
		}
		if count == 0 {
			fail(w, 422, "IMAGE_REQUIRED", "请至少添加一张有效图片")
			return
		}
		next = "pending"
	} else {
		if status == "published" {
			next = "offline"
		} else if status != "pending" {
			fail(w, 409, "INVALID_STATUS", "当前状态不能撤回")
			return
		}
	}
	_, err = tx.Exec(r.Context(), `UPDATE products SET status=$1,version=version+1,approved_version=NULL,review_note='',updated_at=now() WHERE id=$2`, next, id)
	if err == nil {
		err = tx.Commit(r.Context())
	}
	if err != nil {
		memberDBError(w, err)
		return
	}
	a.memberProductResult(w, r, id)
}
func (a *App) memberImageList(w http.ResponseWriter, r *http.Request) {
	u, ok := a.imageActor(w, r)
	if !ok {
		return
	}
	if r.URL.Query().Has("review_id") {
		if !imageAdmin(r, u) {
			fail(w, 403, "FORBIDDEN", "审核记录仅管理员可查看")
			return
		}
		a.reviewImageList(w, r)
		return
	}
	id := r.PathValue("id")
	if !imageAdmin(r, u) && !a.owned(r, u, id) {
		fail(w, 404, "NOT_FOUND", "未找到图集")
		return
	}
	offset, limit, ok := memberPagination(w, r)
	if !ok {
		return
	}
	tx, err := a.DB.BeginTx(r.Context(), pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		memberDBError(w, err)
		return
	}
	defer tx.Rollback(r.Context())
	var version, total int
	err = tx.QueryRow(r.Context(), `SELECT version FROM products WHERE id=$1 AND (owner_id=$2 OR $3)`, id, u.ID, imageAdmin(r, u)).Scan(&version)
	if err != nil {
		memberDBError(w, err)
		return
	}
	if raw := r.URL.Query().Get("expected_version"); raw != "" {
		wanted, _ := strconv.Atoi(raw)
		if !checkMemberVersion(w, version, wanted) {
			return
		}
	}
	if err = tx.QueryRow(r.Context(), `SELECT count(*) FROM product_images WHERE product_id=$1`, id).Scan(&total); err != nil {
		memberDBError(w, err)
		return
	}
	rows, err := tx.Query(r.Context(), `SELECT m.id,m.filename,m.status FROM product_images pi JOIN media_files m ON m.id=pi.media_id WHERE pi.product_id=$1 ORDER BY pi.sort_order,m.id LIMIT $2 OFFSET $3`, id, limit, offset)
	if err != nil {
		memberDBError(w, err)
		return
	}
	data := []map[string]any{}
	for rows.Next() {
		var mid, name, state string
		if err = rows.Scan(&mid, &name, &state); err != nil {
			break
		}
		item := map[string]any{"id": mid, "filename": name, "status": state}
		if state == "ready" {
			item["url"] = "/api/v1/media/" + mid + "/file"
			item["thumbnail_url"] = "/api/v1/media/" + mid + "/file?variant=thumb"
		}
		data = append(data, item)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		memberDBError(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"data": data, "total": total, "version": version, "next_cursor": pageCursor(offset, limit, total)})
}

// Review snapshots are immutable and have their own version. Pagination stays
// inside PostgreSQL, so a large historical gallery is never materialized in Go.
func (a *App) reviewImageList(w http.ResponseWriter, r *http.Request) {
	offset, limit, ok := memberPagination(w, r)
	if !ok {
		return
	}
	id, review := r.PathValue("id"), r.URL.Query().Get("review_id")
	var total, version int
	err := a.DB.QueryRow(r.Context(), `SELECT version,CASE WHEN jsonb_typeof(snapshot->'images')='array' THEN jsonb_array_length(snapshot->'images') ELSE -1 END FROM product_reviews WHERE id=$1 AND product_id=$2`, review, id).Scan(&version, &total)
	if err != nil {
		memberDBError(w, err)
		return
	}
	if total < 0 {
		fail(w, 422, "REVIEW_GALLERY_UNAVAILABLE", "该历史审核记录未保存图集快照，无法还原历史图片")
		return
	}
	if raw := r.URL.Query().Get("expected_version"); raw != "" {
		wanted, _ := strconv.Atoi(raw)
		if !checkMemberVersion(w, version, wanted) {
			return
		}
	}
	rows, err := a.DB.Query(r.Context(), `SELECT item->>'id',COALESCE(item->>'alt',''),COALESCE(m.status,'missing')
 FROM product_reviews pr CROSS JOIN LATERAL jsonb_array_elements(pr.snapshot->'images') WITH ORDINALITY AS images(item,position)
 LEFT JOIN media_files m ON m.id=item->>'id' WHERE pr.id=$1 AND pr.product_id=$2 ORDER BY position LIMIT $3 OFFSET $4`, review, id, limit, offset)
	if err != nil {
		memberDBError(w, err)
		return
	}
	data := []map[string]any{}
	for rows.Next() {
		var mid, alt, status string
		if err = rows.Scan(&mid, &alt, &status); err != nil {
			break
		}
		item := map[string]any{"id": mid, "alt": alt, "status": status}
		if status == "ready" {
			item["url"] = "/api/v1/media/" + mid + "/file"
			item["thumbnail_url"] = "/api/v1/media/" + mid + "/file?variant=thumb"
		}
		data = append(data, item)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		memberDBError(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"data": data, "total": total, "version": version, "review_id": review, "next_cursor": pageCursor(offset, limit, total)})
}
func (a *App) appendMemberImages(w http.ResponseWriter, r *http.Request) {
	u, ok := a.imageActor(w, r)
	if !ok {
		return
	}
	var input struct {
		MediaIDs        []string `json:"media_ids"`
		ExpectedVersion int      `json:"expected_version"`
		RequestID       string   `json:"request_id"`
	}
	if !readJSON(w, r, &input) {
		return
	}
	if len(input.MediaIDs) < 1 || len(input.MediaIDs) > 20 {
		fail(w, 422, "BATCH_SIZE", "每批追加1至20张，可继续分批添加")
		return
	}
	id := r.PathValue("id")
	tx, err := a.DB.Begin(r.Context())
	if err != nil {
		memberDBError(w, err)
		return
	}
	defer tx.Rollback(r.Context())
	replay, hash, err := replayProduct(r.Context(), tx, u.ID, "append:"+id, input.RequestID, input)
	if err != nil {
		fail(w, 409, "IDEMPOTENCY_CONFLICT", err.Error())
		return
	}
	if replay != "" {
		tx.Rollback(r.Context())
		a.memberProductResult(w, r, replay)
		return
	}
	version, _, err := lockedImageOwner(r.Context(), tx, r, u, id)
	if err != nil {
		memberDBError(w, err)
		return
	}
	if !checkMemberVersion(w, version, input.ExpectedVersion) {
		return
	}
	var sort int
	err = tx.QueryRow(r.Context(), `SELECT COALESCE(max(sort_order),0) FROM product_images WHERE product_id=$1`, id).Scan(&sort)
	if err != nil {
		memberDBError(w, err)
		return
	}
	seen := map[string]bool{}
	for _, mid := range input.MediaIDs {
		if seen[mid] {
			continue
		}
		seen[mid] = true
		var owner, state string
		err = tx.QueryRow(r.Context(), `SELECT COALESCE(owner_id,''),status FROM media_files WHERE id=$1 FOR UPDATE`, mid).Scan(&owner, &state)
		if err != nil || owner != u.ID || state != "ready" {
			fail(w, 422, "MEDIA_NOT_READY", "只能使用本人已完成校验的图片")
			return
		}
		var linked string
		err = tx.QueryRow(r.Context(), `SELECT product_id FROM product_images WHERE media_id=$1`, mid).Scan(&linked)
		if err == nil {
			if linked == id {
				continue
			}
			fail(w, 422, "MEDIA_ALREADY_LINKED", "图片已关联其他商品")
			return
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			memberDBError(w, err)
			return
		}
		sort++
		if _, err = tx.Exec(r.Context(), `INSERT INTO product_images(product_id,media_id,sort_order) VALUES($1,$2,$3)`, id, mid, sort); err != nil {
			memberDBError(w, err)
			return
		}
	}
	_, err = tx.Exec(r.Context(), `UPDATE products SET version=version+1,status='draft',approved_version=NULL,review_note='',updated_at=now() WHERE id=$1`, id)
	if err == nil {
		err = recordProductReplay(r.Context(), tx, u.ID, "append:"+id, input.RequestID, hash, id)
	}
	if err == nil {
		err = imageAudit(r.Context(), tx, r, u, id, "images.append")
	}
	if err == nil {
		err = tx.Commit(r.Context())
	}
	if err != nil {
		memberDBError(w, err)
		return
	}
	a.memberProductResult(w, r, id)
}
func (a *App) changeMemberImage(w http.ResponseWriter, r *http.Request) {
	u, ok := a.imageActor(w, r)
	if !ok {
		return
	}
	var input struct {
		ExpectedVersion int     `json:"expected_version"`
		BeforeMediaID   *string `json:"before_media_id"`
	}
	if !readJSON(w, r, &input) {
		return
	}
	id, mid := r.PathValue("id"), r.PathValue("media_id")
	tx, err := a.DB.Begin(r.Context())
	if err != nil {
		memberDBError(w, err)
		return
	}
	defer tx.Rollback(r.Context())
	version, _, err := lockedImageOwner(r.Context(), tx, r, u, id)
	if err != nil {
		memberDBError(w, err)
		return
	}
	if !checkMemberVersion(w, version, input.ExpectedVersion) {
		return
	}
	rows, err := tx.Query(r.Context(), `SELECT media_id FROM product_images WHERE product_id=$1 ORDER BY sort_order,media_id`, id)
	if err != nil {
		memberDBError(w, err)
		return
	}
	ids := []string{}
	found := false
	for rows.Next() {
		var value string
		if err = rows.Scan(&value); err != nil {
			break
		}
		if value == mid {
			found = true
		} else {
			ids = append(ids, value)
		}
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		memberDBError(w, err)
		return
	}
	if !found {
		fail(w, 404, "NOT_FOUND", "图片不存在")
		return
	}
	if r.Method == "DELETE" {
		_, err = tx.Exec(r.Context(), `DELETE FROM product_images WHERE product_id=$1 AND media_id=$2`, id, mid)
	} else {
		at := len(ids)
		if input.BeforeMediaID != nil {
			if *input.BeforeMediaID == mid {
				writeJSON(w, 200, map[string]any{"data": map[string]any{"version": version}})
				return
			}
			at = -1
			for i, value := range ids {
				if value == *input.BeforeMediaID {
					at = i
					break
				}
			}
			if at < 0 {
				fail(w, 409, "VERSION_CONFLICT", "目标图片已变化")
				return
			}
		}
		ids = append(ids, "")
		copy(ids[at+1:], ids[at:])
		ids[at] = mid
		for i, value := range ids {
			if _, err = tx.Exec(r.Context(), `UPDATE product_images SET sort_order=$1 WHERE product_id=$2 AND media_id=$3`, i+1, id, value); err != nil {
				break
			}
		}
	}
	if err == nil {
		_, err = tx.Exec(r.Context(), `UPDATE products SET version=version+1,status='draft',approved_version=NULL,review_note='',updated_at=now() WHERE id=$1`, id)
	}
	if err == nil {
		err = imageAudit(r.Context(), tx, r, u, id, "images."+strings.ToLower(r.Method))
	}
	if err == nil {
		err = tx.Commit(r.Context())
	}
	if err != nil {
		memberDBError(w, err)
		return
	}
	a.memberProductResult(w, r, id)
}
