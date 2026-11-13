package catalog

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"
)

type RentalQuote struct {
	Unit   string `json:"unit"`
	Amount string `json:"amount"`
}
type PrivateCredentials struct {
	QQAccount  string `json:"qq_account"`
	QQPassword string `json:"qq_password"`
}
type ProductInput struct {
	PreviousAttributes    map[string]any      `json:"-"`
	LegacyAttributes      bool                `json:"-"`
	HasPrivateCredentials bool                `json:"-"`
	RentalQuotes          []RentalQuote       `json:"rental_quotes"`
	PrivateCredentials    *PrivateCredentials `json:"private_credentials,omitempty"`
	CategoryID            string              `json:"category_id"`
	Title                 string              `json:"title"`
	TradeType             string              `json:"trade_type"`
	PriceUnit             string              `json:"price_unit"`
	PriceAmount           *string             `json:"price_amount"`
	Attributes            map[string]any      `json:"attributes"`
	Description           string              `json:"description"`
	ExpectedVersion       int                 `json:"expected_version"`
	RequestID             string              `json:"request_id"`
}

var MoneyPattern = regexp.MustCompile(`^\d{1,10}(\.\d{1,2})?$`)

type FilterOption struct {
	Value   string `json:"value"`
	Label   string `json:"label"`
	Enabled *bool  `json:"enabled,omitempty"`
}
type FilterGroup struct {
	Key         string         `json:"key"`
	Label       string         `json:"label"`
	Required    bool           `json:"required,omitempty"`
	Placeholder string         `json:"placeholder,omitempty"`
	Multiple    bool           `json:"multiple,omitempty"`
	Type        string         `json:"type,omitempty"`
	Enabled     *bool          `json:"enabled,omitempty"`
	Options     []FilterOption `json:"options"`
}

type CategoryRules struct {
	Enabled             bool
	TradeType           string
	PriceUnits, Filters []byte
}

func ValidateBasicInput(p ProductInput, complete bool) error {
	if err := ValidateExtendedInput(p); err != nil {
		return err
	}
	if p.TradeType != "sale" && p.TradeType != "rent" {
		return errors.New("请选择出售或出租")
	}
	units := map[string]bool{"account": true, "hour": true, "day": true, "week": true, "month": true, "unknown": true}
	if !units[p.PriceUnit] || p.TradeType == "sale" && p.PriceUnit != "account" && p.PriceUnit != "unknown" || p.TradeType == "rent" && p.PriceUnit == "account" {
		return errors.New("报价单位与交易类型不符")
	}
	if p.PriceAmount != nil && (!MoneyPattern.MatchString(*p.PriceAmount) || p.PriceUnit == "unknown") {
		return errors.New("请填写有效的价格并确认报价单位")
	}
	if utf8.RuneCountInString(p.Title) > 1000 || utf8.RuneCountInString(p.Description) > 10000 {
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
	return nil
}

func ValidateInput(p ProductInput, complete bool, category CategoryRules) error {
	if err := ValidateBasicInput(p, complete); err != nil {
		return err
	}
	if !category.Enabled {
		return errors.New("分类不存在或已停用")
	}
	typ, raw, filterRaw := category.TradeType, category.PriceUnits, category.Filters
	if typ != "all" && typ != p.TradeType {
		return errors.New("分类与交易类型不符")
	}
	var configured []string
	if err := json.Unmarshal(raw, &configured); err != nil {
		return errors.New("分类报价单位配置无效")
	}
	// An empty legacy configuration imposes no additional unit restriction.
	if len(configured) > 0 {
		allowedUnits := make(map[string]bool, len(configured))
		for _, u := range configured {
			allowedUnits[u] = true
		}
		if p.PriceUnit != "unknown" && !allowedUnits[p.PriceUnit] {
			return errors.New("此分类不支持该报价单位")
		}
		for _, quote := range p.RentalQuotes {
			if !allowedUnits[quote.Unit] {
				return errors.New("租赁报价含此分类未启用的单位，请核对报价设置")
			}
		}
	}
	var groups []FilterGroup
	if err := json.Unmarshal(filterRaw, &groups); err != nil {
		return errors.New("分类筛选配置无效")
	}
	for _, g := range groups {
		value, exists := p.Attributes[g.Key]
		if complete && g.Required && (g.Enabled == nil || *g.Enabled) && (!exists || value == nil || strings.TrimSpace(fmt.Sprint(value)) == "") {
			return fmt.Errorf("请填写%s", g.Label)
		}
		if !exists || value == nil {
			continue
		}
		if s, ok := value.(string); ok && s == "" {
			continue
		}
		if g.Enabled != nil && !*g.Enabled {
			continue
		}
		if g.Type == "text" {
			if text, ok := value.(string); !ok || utf8.RuneCountInString(text) > 200 {
				return fmt.Errorf("%s须为200字以内的文本", g.Label)
			}
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
		if complete && g.Required && len(values) == 0 {
			return fmt.Errorf("请填写%s", g.Label)
		}
		if !g.Multiple && g.Type != "multiselect" && len(values) > 1 {
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

func PublicFields(p map[string]any) {
	// 显式白名单防止未来新增内部字段被公开接口意外返回。
	for key := range p {
		if !publicFields[key] {
			delete(p, key)
		}
	}
}

var publicFields = map[string]bool{
	"id": true, "product_code": true, "category_id": true, "title": true, "trade_type": true, "price_unit": true,
	"attributes": true, "attribute_labels": true, "description": true, "status": true, "source": true, "version": true, "manual_rank": true,
	"legacy_price": true, "legacy_category": true, "verification_status": true, "created_at": true, "updated_at": true,
	"published_at": true, "rental_quotes": true, "consultation_count": true, "price_amount": true, "price_note": true, "image_total": true, "images": true,
}

// Catalog cards do not render the full introduction. Keep titles and attributes
// intact; detail and editorial requests continue to return the full description.
func PublicSummary(p map[string]any) { PublicFields(p); delete(p, "description") }

// 固定游戏名称与独立私密字段避免账号密码混入可公开的属性。
func NormalizeInput(p *ProductInput) {
	if p.Attributes == nil {
		p.Attributes = map[string]any{}
	}
	p.Attributes["game"] = "QQ炫舞"
	if len(p.RentalQuotes) > 0 {
		p.PriceUnit = p.RentalQuotes[0].Unit
		amount := p.RentalQuotes[0].Amount
		p.PriceAmount = &amount
	}
}
func ValidateExtendedInput(p ProductInput) error {
	if len(p.RentalQuotes) > 4 || p.TradeType == "sale" && len(p.RentalQuotes) > 0 {
		return errors.New("出售不支持租赁报价，出租最多四种单位")
	}
	seen := map[string]bool{}
	for _, q := range p.RentalQuotes {
		if (q.Unit != "hour" && q.Unit != "day" && q.Unit != "week" && q.Unit != "month") || seen[q.Unit] || !MoneyPattern.MatchString(q.Amount) {
			return errors.New("租赁报价单位重复或金额无效")
		}
		seen[q.Unit] = true
	}
	if p.PrivateCredentials != nil {
		c := p.PrivateCredentials
		if !regexp.MustCompile(`^[1-9][0-9]{4,14}$`).MatchString(c.QQAccount) || len(c.QQPassword) < 1 || len(c.QQPassword) > 128 {
			return errors.New("请同时填写有效的QQ账号与密码")
		}
	}
	for k := range p.Attributes {
		if IsPrivateAttributeKey(k) {
			return errors.New("私密账号资料请填写在专用保密栏")
		}
	}
	if tags, ok := p.Attributes["custom_tags"]; ok {
		var values []string
		switch v := tags.(type) {
		case []string:
			values = v
		case []any:
			for _, x := range v {
				t, ok := x.(string)
				if !ok {
					return errors.New("自定义标签须为文本")
				}
				values = append(values, t)
			}
		default:
			return errors.New("自定义标签格式无效")
		}
		oldRaw, _ := json.Marshal(p.PreviousAttributes["custom_tags"])
		newRaw, _ := json.Marshal(values)
		if len(values) > 20 && string(oldRaw) != string(newRaw) {
			return errors.New("自定义标签最多20个；历史超量标签可原样保留或删减")
		}
		oldTags := map[string]bool{}
		var retained []string
		_ = json.Unmarshal(oldRaw, &retained)
		for _, tag := range retained {
			oldTags[tag] = true
		}
		seen := map[string]bool{}
		for _, t := range values {
			if strings.TrimSpace(t) == "" || (utf8.RuneCountInString(t) > 5 && !oldTags[t]) || seen[t] {
				return errors.New("新标签不能重复且每个最多5个字")
			}
			seen[t] = true
		}
	}
	return nil
}

// Private account fields share one reserved namespace across product and template writes.
func IsPrivateAttributeKey(key string) bool {
	lower := strings.ToLower(key)
	compact := strings.NewReplacer("_", "", "-", "").Replace(lower)
	return strings.Contains(lower, "password") || strings.Contains(lower, "secret") || strings.Contains(lower, "credential") || strings.Contains(compact, "qqaccount") || strings.Contains(lower, "密码") || strings.Contains(lower, "qq账号")
}
