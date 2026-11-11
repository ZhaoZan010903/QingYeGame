package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
)

var codePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,63}$`)

type filterOption struct {
	Value   string `json:"value"`
	Label   string `json:"label"`
	Enabled *bool  `json:"enabled,omitempty"`
}
type filterGroup struct {
	Key      string         `json:"key"`
	Label    string         `json:"label"`
	Multiple bool           `json:"multiple,omitempty"`
	Type     string         `json:"type,omitempty"`
	Enabled  *bool          `json:"enabled,omitempty"`
	Options  []filterOption `json:"options"`
}

func validateFilters(groups []filterGroup) error {
	if len(groups) > 30 {
		return errors.New("筛选分组不能超过30个")
	}
	seen := map[string]bool{}
	for _, g := range groups {
		if !codePattern.MatchString(g.Key) || seen[g.Key] || len(g.Options) > 200 || utf8.RuneCountInString(g.Label) > 40 {
			return errors.New("筛选分组编码、标题或数量无效")
		}
		seen[g.Key] = true
		opts := map[string]bool{}
		for _, o := range g.Options {
			if o.Value == "" || len(o.Value) > 200 || opts[o.Value] || utf8.RuneCountInString(o.Label) > 80 {
				return errors.New("筛选选项值、标题或数量无效")
			}
			opts[o.Value] = true
		}
	}
	return nil
}
func (a *App) configRows(w http.ResponseWriter, r *http.Request, query string) {
	rows, e := a.jsonRows(r.Context(), query)
	if e != nil {
		a.dbError(w, e)
		return
	}
	writeJSON(w, 200, map[string]any{"data": rows})
}
func (a *App) adminCategories(w http.ResponseWriter, r *http.Request) {
	a.configRows(w, r, "SELECT to_jsonb(c) FROM categories c ORDER BY sort_order,id")
}
func (a *App) adminContacts(w http.ResponseWriter, r *http.Request) {
	a.configRows(w, r, "SELECT to_jsonb(c)||jsonb_build_object('qr_url',CASE WHEN qr_media_id IS NOT NULL THEN '/api/v1/media/'||qr_media_id||'/file' ELSE qr_url END) FROM contacts c ORDER BY id")
}
func (a *App) adminPages(w http.ResponseWriter, r *http.Request) {
	a.configRows(w, r, "SELECT to_jsonb(p) FROM content_pages p ORDER BY slug")
}
func (a *App) adminSettings(w http.ResponseWriter, r *http.Request) {
	rows, e := a.jsonRows(r.Context(), "SELECT to_jsonb(s) FROM site_settings s WHERE key IN ('home_presentation','recommendation') ORDER BY key")
	if e != nil {
		a.dbError(w, e)
		return
	}
	found := false
	for _, row := range rows {
		if row["key"] == "recommendation" {
			found = true
		}
	}
	if !found {
		rows = append(rows, map[string]any{"key": "recommendation", "value": defaultRecommendations()})
	}
	writeJSON(w, 200, map[string]any{"data": rows})
}

// Configuration writes and their audit entry commit together.
func (a *App) configWrite(w http.ResponseWriter, r *http.Request, resource, id, sql string, args ...any) {
	actor, _ := a.currentUser(r)
	tx, e := a.DB.Begin(r.Context())
	if e != nil {
		a.dbError(w, e)
		return
	}
	defer tx.Rollback(r.Context())
	var b []byte
	e = tx.QueryRow(r.Context(), sql, args...).Scan(&b)
	if errors.Is(e, pgx.ErrNoRows) {
		fail(w, 404, "NOT_FOUND", "配置不存在")
		return
	}
	if e == nil {
		_, e = tx.Exec(r.Context(), "INSERT INTO admin_logs(id,user_id,action,resource_id) VALUES($1,$2,$3,$4)", newID(), actor.ID, resource+".save", id)
	}
	if e == nil {
		e = tx.Commit(r.Context())
	}
	if e != nil {
		a.dbError(w, e)
		return
	}
	writeJSON(w, 200, map[string]any{"data": json.RawMessage(b)})
}
func (a *App) saveCategory(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Code       string        `json:"code"`
		Name       string        `json:"name"`
		TradeType  string        `json:"trade_type"`
		PriceUnits []string      `json:"price_units"`
		Filters    []filterGroup `json:"filter_config"`
		SortOrder  int           `json:"sort_order"`
		Enabled    bool          `json:"enabled"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	id := r.PathValue("id")
	if id == "" {
		id = in.Code
	}
	if !codePattern.MatchString(id) || strings.TrimSpace(in.Name) == "" || utf8.RuneCountInString(in.Name) > 60 {
		fail(w, 422, "INVALID_CATEGORY", "请检查分类编码和名称")
		return
	}
	if r.Method == "PUT" {
		var existingType string
		err := a.DB.QueryRow(r.Context(), "SELECT trade_type FROM categories WHERE id=$1", id).Scan(&existingType)
		if errors.Is(err, pgx.ErrNoRows) {
			fail(w, 404, "NOT_FOUND", "分类不存在")
			return
		}
		if err != nil {
			a.dbError(w, err)
			return
		}
		if in.TradeType != "" && in.TradeType != existingType {
			fail(w, 422, "IMMUTABLE_TRADE_TYPE", "已有分类的交易类型不可修改，请新建分类")
			return
		}
		in.TradeType = existingType
	}
	if in.TradeType != "sale" && in.TradeType != "rent" && in.TradeType != "all" {
		fail(w, 422, "INVALID_CATEGORY", "交易类型无效")
		return
	}
	for _, u := range in.PriceUnits {
		if !strings.Contains("|account|hour|day|week|month|unknown|", "|"+u+"|") || in.TradeType == "sale" && u != "account" && u != "unknown" || in.TradeType == "rent" && u == "account" {
			fail(w, 422, "INVALID_CATEGORY", "报价单位不适用于该分类")
			return
		}
	}
	if e := validateFilters(in.Filters); e != nil {
		fail(w, 422, "INVALID_FILTERS", e.Error())
		return
	}
	// Existing product attributes remain interpretable when an option is retired.
	if r.Method == "PUT" {
		var raw []byte
		_ = a.DB.QueryRow(r.Context(), "SELECT filter_config FROM categories WHERE id=$1", id).Scan(&raw)
		var old []filterGroup
		_ = json.Unmarshal(raw, &old)
		newOptions := map[string]map[string]bool{}
		for _, g := range in.Filters {
			newOptions[g.Key] = map[string]bool{}
			for _, o := range g.Options {
				newOptions[g.Key][o.Value] = true
			}
		}
		for _, g := range old {
			for _, o := range g.Options {
				if !newOptions[g.Key][o.Value] {
					var used bool
					e := a.DB.QueryRow(r.Context(), "SELECT EXISTS(SELECT 1 FROM products WHERE category_id=$1 AND (attributes->>$2=$3 OR (attributes->$2) ? $3))", id, g.Key, o.Value).Scan(&used)
					if e != nil {
						a.dbError(w, e)
						return
					}
					if used {
						fail(w, 409, "FILTER_IN_USE", "已有商品引用此选项，请设置 enabled:false 停用并保留编码")
						return
					}
				}
			}
		}
	}
	units, _ := json.Marshal(in.PriceUnits)
	filters, _ := json.Marshal(in.Filters)
	if in.PriceUnits == nil {
		units = []byte("[]")
	}
	if in.Filters == nil {
		filters = []byte("[]")
	}
	if r.Method == "POST" {
		var exists bool
		_ = a.DB.QueryRow(r.Context(), "SELECT EXISTS(SELECT 1 FROM categories WHERE id=$1)", id).Scan(&exists)
		if exists {
			fail(w, 409, "CATEGORY_EXISTS", "分类编码已存在")
			return
		}
		a.configWrite(w, r, "category", id, `INSERT INTO categories(id,name,trade_type,price_units,filter_config,sort_order,enabled) VALUES($1,$2,$3,$4,$5,$6,$7) RETURNING to_jsonb(categories)`, id, in.Name, in.TradeType, units, filters, in.SortOrder, in.Enabled)
	} else {
		a.configWrite(w, r, "category", id, `UPDATE categories SET name=$2,price_units=$3,filter_config=$4,sort_order=$5,enabled=$6 WHERE id=$1 RETURNING to_jsonb(categories)`, id, in.Name, units, filters, in.SortOrder, in.Enabled)
	}
}
func (a *App) saveContact(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Name      string   `json:"name"`
		Purpose   []string `json:"purpose"`
		Channel   string   `json:"channel"`
		Value     string   `json:"contact_value"`
		QRMediaID *string  `json:"qr_media_id"`
		Hours     string   `json:"hours"`
		Enabled   bool     `json:"enabled"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	if strings.TrimSpace(in.Name) == "" || utf8.RuneCountInString(in.Name) > 60 || len(in.Value) > 500 || len(in.Hours) > 200 || !strings.Contains("|wechat|qq|phone|url|", "|"+in.Channel+"|") {
		fail(w, 422, "INVALID_CONTACT", "请检查客服名称和联系方式")
		return
	}
	if in.Channel == "url" {
		u, e := url.Parse(in.Value)
		if e != nil || u.Host == "" || u.Scheme != "https" && u.Scheme != "http" {
			fail(w, 422, "INVALID_CONTACT", "网站地址须为 http 或 https")
			return
		}
	}
	if len(in.Purpose) > 5 {
		fail(w, 422, "INVALID_CONTACT", "用途过多")
		return
	}
	for _, p := range in.Purpose {
		if !strings.Contains("|general|sale|rent|listing|support|", "|"+p+"|") {
			fail(w, 422, "INVALID_CONTACT", "用途无效")
			return
		}
	}
	if in.QRMediaID != nil && *in.QRMediaID != "" {
		actor, _ := a.currentUser(r)
		var valid bool
		e := a.DB.QueryRow(r.Context(), `SELECT EXISTS(SELECT 1 FROM media_files m WHERE m.id=$1 AND m.status='ready'
AND (m.owner_id=$2 OR EXISTS(SELECT 1 FROM contacts c WHERE c.id=$3 AND c.qr_media_id=m.id)))`, *in.QRMediaID, actor.ID, r.PathValue("id")).Scan(&valid)
		if e != nil {
			a.dbError(w, e)
			return
		}
		if !valid {
			fail(w, 422, "INVALID_MEDIA", "请选择本人已上传的二维码素材")
			return
		}
	} else {
		in.QRMediaID = nil
	}
	purpose, _ := json.Marshal(in.Purpose)
	if in.Purpose == nil {
		purpose = []byte("[]")
	}
	id := r.PathValue("id")
	if id == "" {
		id = newID()
		a.configWrite(w, r, "contact", id, `INSERT INTO contacts(id,name,purpose,channel,contact_value,qr_media_id,hours,enabled) VALUES($1,$2,$3,$4,$5,$6,$7,$8) RETURNING to_jsonb(contacts)`, id, in.Name, purpose, in.Channel, in.Value, in.QRMediaID, in.Hours, in.Enabled)
	} else {
		a.configWrite(w, r, "contact", id, `UPDATE contacts SET name=$2,purpose=$3,channel=$4,contact_value=$5,qr_media_id=$6,hours=$7,enabled=$8 WHERE id=$1 RETURNING to_jsonb(contacts)`, id, in.Name, purpose, in.Channel, in.Value, in.QRMediaID, in.Hours, in.Enabled)
	}
}
func (a *App) savePage(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Title     string `json:"title"`
		Body      string `json:"body"`
		Published bool   `json:"published"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	slug := r.PathValue("slug")
	if !codePattern.MatchString(slug) || strings.TrimSpace(in.Title) == "" || utf8.RuneCountInString(in.Title) > 100 || len(in.Body) > 100000 {
		fail(w, 422, "INVALID_PAGE", "请检查页面标识、标题和正文长度")
		return
	}
	a.configWrite(w, r, "page", slug, `INSERT INTO content_pages(slug,title,body,published) VALUES($1,$2,$3,$4) ON CONFLICT(slug) DO UPDATE SET title=EXCLUDED.title,body=EXCLUDED.body,published=EXCLUDED.published,version=content_pages.version+1 RETURNING to_jsonb(content_pages)`, slug, in.Title, in.Body, in.Published)
}

type homeSection struct {
	Source      string `json:"source"`
	Title       string `json:"title"`
	Description string `json:"description"`
	Limit       int    `json:"limit"`
}
type homePresentation struct {
	Editorial struct {
		Title       string `json:"title"`
		Description string `json:"description"`
		IntervalMS  int    `json:"intervalMs"`
	} `json:"editorial"`
	Sections  []homeSection `json:"sections"`
	Shortcuts []struct {
		Href        string `json:"href"`
		Title       string `json:"title"`
		Description string `json:"description"`
		Icon        string `json:"icon"`
	} `json:"shortcuts"`
	Publishing struct {
		Title       string `json:"title"`
		Description string `json:"description"`
		Action      string `json:"action"`
	} `json:"publishing"`
}

func validatePresentation(raw []byte) error {
	var p homePresentation
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if e := d.Decode(&p); e != nil {
		return errors.New("首页配置结构无效")
	}
	textOK := func(s string, max int) bool { return strings.TrimSpace(s) != "" && utf8.RuneCountInString(s) <= max }
	if !textOK(p.Editorial.Title, 32) || utf8.RuneCountInString(p.Editorial.Description) > 120 || p.Editorial.IntervalMS < 4000 || p.Editorial.IntervalMS > 30000 || !textOK(p.Publishing.Title, 60) || utf8.RuneCountInString(p.Publishing.Description) > 120 || !textOK(p.Publishing.Action, 24) {
		return errors.New("首页文案或轮播间隔无效")
	}
	seen := map[string]bool{}
	for _, s := range p.Sections {
		if s.Source != "hot" && s.Source != "newest" && s.Source != "recommended" || seen[s.Source] || s.Limit < 1 || s.Limit > 24 || !textOK(s.Title, 32) || utf8.RuneCountInString(s.Description) > 120 {
			return errors.New("分区来源、数量或文案无效")
		}
		seen[s.Source] = true
	}
	if len(p.Shortcuts) > 4 {
		return errors.New("快捷入口最多4个")
	}
	for _, s := range p.Shortcuts {
		u, e := url.Parse(s.Href)
		if e != nil || u.IsAbs() || u.Host != "" || (u.Path != "/accounts" && u.Path != "/publish") || strings.ContainsAny(s.Href, "\\ \t\n#") || u.Path == "/publish" && u.RawQuery != "" || !textOK(s.Title, 24) || utf8.RuneCountInString(s.Description) > 60 || s.Icon != "arrow" && s.Icon != "plus" {
			return errors.New("快捷入口仅允许账号集市和发布页")
		}
	}
	return nil
}
func (a *App) saveSetting(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Value json.RawMessage `json:"value"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	key := r.PathValue("key")
	if key != "home_presentation" && key != "recommendation" {
		fail(w, 422, "UNKNOWN_SETTING", "此配置项尚未开放编辑")
		return
	}
	var err error
	if key == "home_presentation" {
		err = validatePresentation(in.Value)
	} else {
		_, err = parseRecommendations(in.Value)
	}
	if err != nil {
		fail(w, 422, "INVALID_SETTING", err.Error())
		return
	}
	a.configWrite(w, r, "setting", key, `INSERT INTO site_settings(key,value) VALUES($1,$2) ON CONFLICT(key) DO UPDATE SET value=EXCLUDED.value,updated_at=now() RETURNING to_jsonb(site_settings)`, key, in.Value)
}

func (a *App) adminUsers(w http.ResponseWriter, r *http.Request) {
	limit, offset, e := decodePage(r.URL.Query())
	if e != nil {
		fail(w, 422, "INVALID_CURSOR", e.Error())
		return
	}
	q := r.URL.Query().Get("q")
	if len(q) > 100 {
		fail(w, 422, "INVALID_QUERY", "查询过长")
		return
	}
	var total int
	e = a.DB.QueryRow(r.Context(), "SELECT count(*) FROM users WHERE nickname ILIKE '%'||$1||'%' OR phone=$1", q).Scan(&total)
	if e != nil {
		a.dbError(w, e)
		return
	}
	rows, e := a.jsonRows(r.Context(), `SELECT jsonb_build_object('id',id,'nickname',nickname,'phone_masked',left(phone,3)||'****'||right(phone,4),'role',role,'enabled',enabled,'created_at',created_at) FROM users WHERE nickname ILIKE '%'||$1||'%' OR phone=$1 ORDER BY created_at DESC,id LIMIT $2 OFFSET $3`, q, limit, offset)
	if e != nil {
		a.dbError(w, e)
		return
	}
	writeJSON(w, 200, map[string]any{"data": rows, "total": total, "next_cursor": nextPage(offset, limit, total)})
}
func (a *App) updateUser(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Enabled *bool `json:"enabled"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	actor, _ := a.currentUser(r)
	id := r.PathValue("id")
	if in.Enabled == nil || id == actor.ID {
		fail(w, 422, "INVALID_USER", "不能通过此接口停用当前管理员")
		return
	}
	tx, e := a.DB.Begin(r.Context())
	if e != nil {
		a.dbError(w, e)
		return
	}
	defer tx.Rollback(r.Context())
	_, e = tx.Exec(r.Context(), "SELECT pg_advisory_xact_lock(1834181)")
	if e != nil {
		a.dbError(w, e)
		return
	}
	var role string
	e = tx.QueryRow(r.Context(), "SELECT role FROM users WHERE id=$1 FOR UPDATE", id).Scan(&role)
	if errors.Is(e, pgx.ErrNoRows) {
		fail(w, 404, "NOT_FOUND", "用户不存在")
		return
	}
	if e != nil {
		a.dbError(w, e)
		return
	}
	if role == "admin" && !*in.Enabled {
		var other int
		e = tx.QueryRow(r.Context(), "SELECT count(*) FROM users WHERE role='admin' AND enabled AND id<>$1", id).Scan(&other)
		if e != nil {
			a.dbError(w, e)
			return
		}
		if other == 0 {
			fail(w, 409, "LAST_ADMIN", "需保留至少一位可用管理员")
			return
		}
	}
	_, e = tx.Exec(r.Context(), "UPDATE users SET enabled=$2,updated_at=now() WHERE id=$1", id, *in.Enabled)
	if e == nil && !*in.Enabled {
		_, e = tx.Exec(r.Context(), "DELETE FROM sessions WHERE user_id=$1", id)
	}
	if e == nil {
		_, e = tx.Exec(r.Context(), "INSERT INTO admin_logs(id,user_id,action,resource_id,detail) VALUES($1,$2,'user.enabled',$3,$4)", newID(), actor.ID, id, map[string]bool{"enabled": *in.Enabled})
	}
	if e == nil {
		e = tx.Commit(r.Context())
	}
	if e != nil {
		a.dbError(w, e)
		return
	}
	writeJSON(w, 200, map[string]any{"data": map[string]any{"id": id, "enabled": *in.Enabled}})
}
func (a *App) adminRecommendations(w http.ResponseWriter, r *http.Request) {
	rows, e := a.jsonRows(r.Context(), "SELECT to_jsonb(h)||jsonb_build_object('title',p.title) FROM product_hot_scores h JOIN products p ON p.id=h.product_id JOIN categories c ON c.id=p.category_id WHERE "+visibleSQL+" ORDER BY score DESC,p.published_at DESC NULLS LAST,p.id LIMIT 50")
	if e != nil {
		a.dbError(w, e)
		return
	}
	config, e := loadRecommendations(r.Context(), a.DB)
	if e != nil {
		a.dbError(w, e)
		return
	}
	var state map[string]any
	_ = a.DB.QueryRow(r.Context(), "SELECT value FROM site_settings WHERE key='recommendation_state'").Scan(&state)
	writeJSON(w, 200, map[string]any{"data": rows, "rule_version": state["rule_version"], "computed_at": state["computed_at"], "applied_config": state["config"], "config": config, "rule": "可配置的近期有效浏览、首次收藏与新品加分；测试建议参数"})
}
func (a *App) refreshRecommendations(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 30*1000000000)
	defer cancel()
	if e := a.RefreshRecommendations(ctx); e != nil {
		a.dbError(w, e)
		return
	}
	a.adminRecommendations(w, r)
}
