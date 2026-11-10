package app

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
)

type ProductInput struct {
	CategoryID      string         `json:"category_id"`
	Title           string         `json:"title"`
	TradeType       string         `json:"trade_type"`
	PriceUnit       string         `json:"price_unit"`
	PriceAmount     *string        `json:"price_amount"`
	Attributes      map[string]any `json:"attributes"`
	Description     string         `json:"description"`
	ExpectedVersion int            `json:"expected_version"`
	RequestID       string         `json:"request_id"`
}

var moneyPattern = regexp.MustCompile(`^\d{1,10}(\.\d{1,2})?$`)

func (a *App) validateProductInput(ctx context.Context, p ProductInput, complete bool) error {
	return a.validateProductInputWith(ctx, a.DB, p, complete)
}

type rowQuerier interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}

func (a *App) validateProductInputWith(ctx context.Context, q rowQuerier, p ProductInput, complete bool) error {
	if p.TradeType != "sale" && p.TradeType != "rent" {
		return errors.New("请选择出售或出租")
	}
	units := map[string]bool{"account": true, "hour": true, "day": true, "week": true, "month": true, "unknown": true}
	if !units[p.PriceUnit] || p.TradeType == "sale" && p.PriceUnit != "account" && p.PriceUnit != "unknown" || p.TradeType == "rent" && p.PriceUnit == "account" {
		return errors.New("报价单位与交易类型不符")
	}
	if p.PriceAmount != nil && (!moneyPattern.MatchString(*p.PriceAmount) || p.PriceUnit == "unknown") {
		return errors.New("请填写有效的价格并确认报价单位")
	}
	if utf8.RuneCountInString(p.Title) > 100 || utf8.RuneCountInString(p.Description) > 10000 {
		return errors.New("标题或说明过长")
	}
	if complete && (strings.TrimSpace(p.Title) == "" || strings.TrimSpace(p.Description) == "") {
		return errors.New("请补全标题及账号介绍")
	}
	if len(p.Attributes) > 40 {
		return errors.New("属性字段过多")
	}
	if b, _ := json.Marshal(p.Attributes); len(b) > 16000 {
		return errors.New("属性内容过长")
	}
	var enabled bool
	var typ string
	var raw, filterRaw []byte
	if e := q.QueryRow(ctx, "SELECT enabled,trade_type,price_units,filter_config FROM categories WHERE id=$1", p.CategoryID).Scan(&enabled, &typ, &raw, &filterRaw); e != nil || !enabled {
		return errors.New("分类不存在或已停用")
	}
	if typ != "all" && typ != p.TradeType {
		return errors.New("分类与交易类型不符")
	}
	var configured []string
	_ = json.Unmarshal(raw, &configured)
	if p.PriceUnit != "unknown" && len(configured) > 0 {
		valid := false
		for _, u := range configured {
			if u == p.PriceUnit {
				valid = true
			}
		}
		if !valid {
			return errors.New("此分类不支持该报价单位")
		}
	}
	var groups []filterGroup
	if err := json.Unmarshal(filterRaw, &groups); err != nil {
		return errors.New("分类筛选配置无效")
	}
	for _, g := range groups {
		value, exists := p.Attributes[g.Key]
		if !exists || value == nil {
			continue
		}
		if s, ok := value.(string); ok && s == "" {
			continue
		}
		if g.Enabled != nil && !*g.Enabled {
			continue
		}
		allowed := map[string]bool{}
		for _, o := range g.Options {
			if o.Enabled == nil || *o.Enabled {
				allowed[o.Value] = true
			}
		}
		values := []string{}
		switch v := value.(type) {
		case string:
			values = append(values, v)
		case []string:
			values = v
		case []any:
			for _, element := range v {
				s, ok := element.(string)
				if !ok {
					return errors.New("属性选项须为字符串")
				}
				values = append(values, s)
			}
		default:
			return errors.New("属性值类型与分类配置不符")
		}
		if !g.Multiple && len(values) > 1 {
			return fmt.Errorf("%s只允许选择一个选项", g.Label)
		}
		selected := map[string]bool{}
		for _, v := range values {
			if selected[v] {
				return fmt.Errorf("%s包含重复选项", g.Label)
			}
			selected[v] = true
			if !allowed[v] {
				return fmt.Errorf("%s包含不存在或停用的选项", g.Label)
			}
		}
	}
	return nil
}

const visibleSQL = "p.status='published' AND p.approved_version=p.version AND c.enabled=true"

// Explicit projection avoids materializing the large private legacy payload for
// each card, and prevents future internal columns from silently becoming API fields.
const productJSON = `jsonb_build_object(
 'id',p.id,'product_code',p.product_code,'category_id',p.category_id,'title',p.title,
 'trade_type',p.trade_type,'price_unit',p.price_unit,'attributes',p.attributes,'description',p.description,
 'status',p.status,'source',p.source,'version',p.version,'approved_version',p.approved_version,
 'manual_rank',p.manual_rank,'review_note',p.review_note,'legacy_price',p.legacy_price,
 'legacy_category',p.legacy_category,'verification_status',p.verification_status,
 'created_at',p.created_at,'updated_at',p.updated_at,'published_at',p.published_at,
 'price_amount',p.price_amount::text,'price_note',CASE WHEN p.price_unit='unknown' THEN '原站报价，单位待核对' ELSE '' END,
 'image_total',(SELECT count(*) FROM product_images pi WHERE pi.product_id=p.id),
 'images',COALESCE((SELECT jsonb_agg(i.obj ORDER BY i.sort_order,i.media_id) FROM (
 SELECT pi.sort_order,pi.media_id,jsonb_build_object('id',m.id,'alt',m.filename,'status',m.status,
 'url',CASE WHEN m.status='ready' THEN '/api/v1/media/'||m.id||'/file' ELSE NULL END,
 'thumbnail_url',CASE WHEN m.status='ready' THEN '/api/v1/media/'||m.id||'/file?variant=thumb' ELSE NULL END) obj
 FROM product_images pi JOIN media_files m ON m.id=pi.media_id WHERE pi.product_id=p.id ORDER BY pi.sort_order,pi.media_id LIMIT %d) i),'[]'::jsonb))`

func (a *App) productView(ctx context.Context, id string, private bool) (map[string]any, error) {
	where := "p.id=$1"
	if !private {
		where += " AND " + visibleSQL
	}
	var data []byte
	e := a.DB.QueryRow(ctx, "SELECT "+fmt.Sprintf(productJSON, 20)+" FROM products p JOIN categories c ON c.id=p.category_id WHERE "+where, id).Scan(&data)
	if e != nil {
		return nil, e
	}
	var out map[string]any
	e = json.Unmarshal(data, &out)
	if !private {
		publicProductFields(out)
	}
	return out, e
}
func publicProductFields(p map[string]any) {
	for _, key := range []string{"owner_id", "legacy_id", "legacy_payload", "review_note", "approved_version"} {
		delete(p, key)
	}
}

// Catalog cards do not render the full introduction. Keep titles and attributes
// intact; detail and editorial requests continue to return the full description.
func publicProductSummary(p map[string]any) { publicProductFields(p); delete(p, "description") }
func decodePage(q url.Values) (int, int, error) {
	limit := 20
	if s := q.Get("limit"); s != "" {
		v, e := strconv.Atoi(s)
		if e != nil || v < 1 || v > 50 {
			return 0, 0, errors.New("分页大小应为1至50")
		}
		limit = v
	}
	offset := 0
	if s := q.Get("cursor"); s != "" {
		b, e := base64.RawURLEncoding.DecodeString(s)
		if e != nil {
			return 0, 0, errors.New("分页游标无效")
		}
		v, e := strconv.Atoi(string(b))
		if e != nil || v < 0 || v > 1000000 {
			return 0, 0, errors.New("分页游标无效")
		}
		offset = v
	}
	return limit, offset, nil
}
func nextPage(offset, limit, total int) any {
	if offset+limit >= total {
		return nil
	}
	return base64.RawURLEncoding.EncodeToString([]byte(strconv.Itoa(offset + limit)))
}
func (a *App) productRows(ctx context.Context, where string, args []any, order string, limit, offset int) ([]map[string]any, int, error) {
	var total int
	if e := a.DB.QueryRow(ctx, "SELECT count(*) FROM products p JOIN categories c ON c.id=p.category_id WHERE "+where, args...).Scan(&total); e != nil {
		return nil, 0, e
	}
	args = append(args, limit, offset)
	query := "SELECT " + fmt.Sprintf(productJSON, 1) + " FROM products p JOIN categories c ON c.id=p.category_id WHERE " + where + " ORDER BY " + order + fmt.Sprintf(" LIMIT $%d OFFSET $%d", len(args)-1, len(args))
	rows, e := a.DB.Query(ctx, query, args...)
	if e != nil {
		return nil, 0, e
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var b []byte
		if e = rows.Scan(&b); e != nil {
			return nil, 0, e
		}
		var m map[string]any
		if e = json.Unmarshal(b, &m); e != nil {
			return nil, 0, e
		}
		out = append(out, m)
	}
	return out, total, rows.Err()
}
func productOrder(sort string) (string, error) {
	switch sort {
	case "", "featured":
		return "p.manual_rank ASC NULLS LAST,p.published_at DESC NULLS LAST,p.id", nil
	case "newest":
		return "p.published_at DESC NULLS LAST,p.id", nil
	case "hot":
		return "COALESCE((SELECT score FROM product_hot_scores h WHERE h.product_id=p.id AND h.computed_at>=now()-interval '60 minutes'),0) DESC,p.published_at DESC NULLS LAST,p.id", nil
	case "price_asc", "price-up":
		return "p.price_amount ASC NULLS LAST,p.id", nil
	case "price_desc", "price-down":
		return "p.price_amount DESC NULLS LAST,p.id", nil
	}
	return "", errors.New("排序方式无效")
}
func (a *App) publicProducts(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit, offset, e := decodePage(q)
	if e != nil {
		fail(w, 422, "INVALID_CURSOR", e.Error())
		return
	}
	order, e := productOrder(q.Get("sort"))
	if e != nil {
		fail(w, 422, "INVALID_SORT", e.Error())
		return
	}
	where := visibleSQL
	args := []any{}
	add := func(sql string, v any) { args = append(args, v); where += " AND " + fmt.Sprintf(sql, len(args)) }
	if v := q.Get("q"); v != "" {
		if len(v) > 200 {
			fail(w, 422, "QUERY_TOO_LONG", "关键词过长")
			return
		}
		add("(p.title ILIKE '%%'||$%[1]d||'%%' OR p.product_code ILIKE '%%'||$%[1]d||'%%' OR p.attributes->>'server' ILIKE '%%'||$%[1]d||'%%')", v)
	}
	if v := q.Get("category"); v != "" {
		add("p.category_id=$%d", v)
	}
	if v := q.Get("trade_type"); v != "" && v != "all" {
		if v != "sale" && v != "rent" {
			fail(w, 422, "INVALID_TRADE", "交易类型无效")
			return
		}
		add("p.trade_type=$%d", v)
	}
	unit := q.Get("price_unit")
	priceSort := strings.HasPrefix(q.Get("sort"), "price")
	hasPrice := q.Has("min_price") || q.Has("max_price") || priceSort
	if hasPrice && (unit == "" || unit == "all" || unit == "unknown") {
		fail(w, 422, "PRICE_UNIT_REQUIRED", "先选择已确认的报价单位再比较价格")
		return
	}
	if unit != "" && unit != "all" {
		add("p.price_unit=$%d", unit)
	}
	for _, x := range []struct{ k, op string }{{"min_price", ">="}, {"max_price", "<="}} {
		if q.Has(x.k) {
			v := q.Get(x.k)
			if !moneyPattern.MatchString(v) {
				fail(w, 422, "INVALID_PRICE", "价格范围无效")
				return
			}
			add("p.price_amount"+x.op+"$%d::numeric", v)
		}
	}
	if q.Has("min_price") && q.Has("max_price") {
		lo, _ := strconv.ParseFloat(q.Get("min_price"), 64)
		hi, _ := strconv.ParseFloat(q.Get("max_price"), 64)
		if lo > hi {
			fail(w, 422, "INVALID_PRICE", "最低价不能大于最高价")
			return
		}
	}
	if raw := q.Get("filters"); raw != "" {
		var filters map[string][]string
		if e = json.Unmarshal([]byte(raw), &filters); e != nil || len(filters) > 30 {
			fail(w, 422, "INVALID_FILTERS", "筛选格式无效")
			return
		}
		allowed, e := a.filterOptions(r.Context())
		if e != nil {
			a.dbError(w, e)
			return
		}
		for key, values := range filters {
			if len(values) > 40 {
				fail(w, 422, "INVALID_FILTERS", "筛选项过多")
				return
			}
			for _, v := range values {
				if !allowed[key][v] {
					fail(w, 422, "INVALID_FILTERS", "筛选项不存在")
					return
				}
			}
			if len(values) > 0 {
				args = append(args, key, values)
				where += fmt.Sprintf(" AND ((p.attributes->>$%[1]d)=ANY($%[2]d::text[]) OR (jsonb_typeof(p.attributes->$%[1]d)='array' AND (p.attributes->$%[1]d) ?| $%[2]d::text[]))", len(args)-1, len(args))
			}
		}
	}
	items, total, e := a.productRows(r.Context(), where, args, order, limit, offset)
	if e != nil {
		a.dbError(w, e)
		return
	}
	for _, p := range items {
		publicProductSummary(p)
	}
	writeJSON(w, 200, map[string]any{"data": items, "total": total, "next_cursor": nextPage(offset, limit, total)})
}
func (a *App) filterOptions(ctx context.Context) (map[string]map[string]bool, error) {
	rows, e := a.DB.Query(ctx, "SELECT filter_config FROM categories WHERE enabled")
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := map[string]map[string]bool{}
	for rows.Next() {
		var b []byte
		if e = rows.Scan(&b); e != nil {
			return nil, e
		}
		var groups []filterGroup
		_ = json.Unmarshal(b, &groups)
		for _, g := range groups {
			if g.Enabled != nil && !*g.Enabled {
				continue
			}
			if out[g.Key] == nil {
				out[g.Key] = map[string]bool{}
			}
			for _, o := range g.Options {
				if o.Enabled != nil && !*o.Enabled {
					continue
				}
				out[g.Key][o.Value] = true
			}
		}
	}
	return out, rows.Err()
}
func (a *App) publicProduct(w http.ResponseWriter, r *http.Request) {
	p, e := a.productView(r.Context(), r.PathValue("id"), false)
	if errors.Is(e, pgx.ErrNoRows) {
		var status string
		_ = a.DB.QueryRow(r.Context(), "SELECT status FROM products WHERE id=$1", r.PathValue("id")).Scan(&status)
		if status == "offline" {
			fail(w, 410, "OFFLINE", "此账号已下架")
		} else {
			fail(w, 404, "NOT_FOUND", "商品不存在或尚未公开")
		}
		return
	}
	if e != nil {
		a.dbError(w, e)
		return
	}
	writeJSON(w, 200, map[string]any{"data": p})
}
func (a *App) publicImages(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	tx, e := a.DB.BeginTx(r.Context(), pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if e != nil {
		a.dbError(w, e)
		return
	}
	defer tx.Rollback(r.Context())
	var version int
	e = tx.QueryRow(r.Context(), "SELECT p.version FROM products p JOIN categories c ON c.id=p.category_id WHERE p.id=$1 AND "+visibleSQL, id).Scan(&version)
	if errors.Is(e, pgx.ErrNoRows) {
		fail(w, 404, "NOT_FOUND", "商品不存在或已停止公开")
		return
	}
	if e != nil {
		a.dbError(w, e)
		return
	}
	if v := r.URL.Query().Get("expected_version"); v != "" && v != strconv.Itoa(version) {
		fail(w, 409, "VERSION_CONFLICT", "图集已更新，请重新加载")
		return
	}
	limit, offset, e := decodePage(r.URL.Query())
	if e != nil {
		fail(w, 422, "INVALID_CURSOR", e.Error())
		return
	}
	items, total, e := imageRowsWith(r.Context(), tx, id, limit, offset)
	if e != nil {
		a.dbError(w, e)
		return
	}
	if e = tx.Commit(r.Context()); e != nil {
		a.dbError(w, e)
		return
	}
	writeJSON(w, 200, map[string]any{"data": items, "total": total, "next_cursor": nextPage(offset, limit, total), "version": version})
}
func (a *App) imageRows(ctx context.Context, id string, limit, offset int) ([]map[string]any, int, error) {
	return imageRowsWith(ctx, a.DB, id, limit, offset)
}

type rowsQuerier interface {
	rowQuerier
	Query(context.Context, string, ...any) (pgx.Rows, error)
}

func imageRowsWith(ctx context.Context, q rowsQuerier, id string, limit, offset int) ([]map[string]any, int, error) {
	var total int
	if e := q.QueryRow(ctx, "SELECT count(*) FROM product_images WHERE product_id=$1", id).Scan(&total); e != nil {
		return nil, 0, e
	}
	rows, e := q.Query(ctx, "SELECT m.id,m.filename,m.status FROM product_images pi JOIN media_files m ON m.id=pi.media_id WHERE pi.product_id=$1 ORDER BY pi.sort_order,pi.media_id LIMIT $2 OFFSET $3", id, limit, offset)
	if e != nil {
		return nil, 0, e
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var id, name, status string
		if e = rows.Scan(&id, &name, &status); e != nil {
			return nil, 0, e
		}
		item := map[string]any{"id": id, "alt": name, "status": status}
		if status == "ready" {
			item["url"] = "/api/v1/media/" + id + "/file"
			item["thumbnail_url"] = "/api/v1/media/" + id + "/file?variant=thumb"
		}
		out = append(out, item)
	}
	return out, total, rows.Err()
}
