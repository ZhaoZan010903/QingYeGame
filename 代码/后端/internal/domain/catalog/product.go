package catalog

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"
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

var MoneyPattern = regexp.MustCompile(`^\d{1,10}(\.\d{1,2})?$`)

type FilterOption struct {
	Value   string `json:"value"`
	Label   string `json:"label"`
	Enabled *bool  `json:"enabled,omitempty"`
}
type FilterGroup struct {
	Key      string         `json:"key"`
	Label    string         `json:"label"`
	Multiple bool           `json:"multiple,omitempty"`
	Type     string         `json:"type,omitempty"`
	Enabled  *bool          `json:"enabled,omitempty"`
	Options  []FilterOption `json:"options"`
}

type CategoryRules struct {
	Enabled             bool
	TradeType           string
	PriceUnits, Filters []byte
}

func ValidateBasicInput(p ProductInput, complete bool) error {
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
	var groups []FilterGroup
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
	"attributes": true, "description": true, "status": true, "source": true, "version": true, "manual_rank": true,
	"legacy_price": true, "legacy_category": true, "verification_status": true, "created_at": true, "updated_at": true,
	"published_at": true, "price_amount": true, "price_note": true, "image_total": true, "images": true,
}

// Catalog cards do not render the full introduction. Keep titles and attributes
// intact; detail and editorial requests continue to return the full description.
func PublicSummary(p map[string]any) { PublicFields(p); delete(p, "description") }
