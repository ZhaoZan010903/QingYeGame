package management

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/url"
	"qingye/backend/internal/domain/catalog"
	"qingye/backend/internal/domain/fault"
	"regexp"
	"slices"
	"strings"
	"unicode/utf8"
)

type HomeSlide struct {
	ProductID        string   `json:"productId"`
	Title            string   `json:"title"`
	Description      string   `json:"description"`
	TradeLabel       string   `json:"tradeLabel"`
	ServerLabel      string   `json:"serverLabel"`
	PriceLabel       string   `json:"priceLabel"`
	ImageID          string   `json:"imageId,omitempty"`
	ImagePositionX   float64  `json:"imagePositionX"`
	ImagePositionY   float64  `json:"imagePositionY"`
	ImageFit         string   `json:"imageFit"`
	Layout           string   `json:"layout"`
	TextAlign        string   `json:"textAlign"`
	VerticalAlign    string   `json:"verticalAlign"`
	Placement        string   `json:"placement,omitempty"`
	TextPositionX    *float64 `json:"textPositionX,omitempty"`
	TextPositionY    *float64 `json:"textPositionY,omitempty"`
	TextWidth        *float64 `json:"textWidth,omitempty"`
	TitleSize        *float64 `json:"titleSize,omitempty"`
	DescriptionSize  *float64 `json:"descriptionSize,omitempty"`
	FontFamily       string   `json:"fontFamily,omitempty"`
	TitleColor       string   `json:"titleColor,omitempty"`
	DescriptionColor string   `json:"descriptionColor,omitempty"`
}

var CodePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,63}$`)

type Actor struct{ ID, Role string }

func ValidateActor(a Actor) error {
	if a.ID == "" {
		return fault.New(fault.Unauthenticated, "UNAUTHENTICATED", "请先登录")
	}
	if a.Role != "admin" {
		return fault.New(fault.Forbidden, "FORBIDDEN", "需要管理员权限")
	}
	return nil
}

type CategoryInput struct {
	Code       string                `json:"code"`
	Name       string                `json:"name"`
	TradeType  string                `json:"trade_type"`
	PriceUnits []string              `json:"price_units"`
	Filters    []catalog.FilterGroup `json:"filter_config"`
	SortOrder  int                   `json:"sort_order"`
	Enabled    bool                  `json:"enabled"`
}
type ContactInput struct {
	Name      string   `json:"name"`
	Purpose   []string `json:"purpose"`
	Channel   string   `json:"channel"`
	Value     string   `json:"contact_value"`
	QRMediaID *string  `json:"qr_media_id"`
	Hours     string   `json:"hours"`
	Enabled   bool     `json:"enabled"`
}
type PageInput struct {
	Title     string `json:"title"`
	Body      string `json:"body"`
	Published bool   `json:"published"`
}
type SettingInput struct {
	Value json.RawMessage `json:"value"`
}
type UserInput struct {
	Enabled *bool `json:"enabled"`
}

func ValidateFilters(groups []catalog.FilterGroup) error {
	if len(groups) > 30 {
		return errors.New("筛选分组不能超过30个")
	}
	seen := map[string]bool{}
	for _, g := range groups {
		if g.Key == "custom_tags" || catalog.IsPrivateAttributeKey(g.Key) {
			return errors.New("自定义标签及私密账号字段请在专用发布表单设置中管理")
		}
		if g.Type != "" && g.Type != "text" && g.Type != "select" && g.Type != "multiselect" && g.Type != "checkbox" {
			return errors.New("字段类型无效")
		}
		if g.Required && g.Enabled != nil && !*g.Enabled || utf8.RuneCountInString(g.Placeholder) > 200 {
			return errors.New("必填字段不能停用或提示过长")
		}
		if !CodePattern.MatchString(g.Key) || seen[g.Key] || len(g.Options) > 200 || utf8.RuneCountInString(g.Label) > 40 {
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
func ValidatePresentation(raw []byte) error {
	var p HomePresentation
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if e := d.Decode(&p); e != nil {
		return errors.New("首页配置结构无效")
	}
	limit := 12
	if p.MaxSlides != nil {
		limit = *p.MaxSlides
	}
	if limit < 1 || limit > 30 || len(p.Slides) > limit {
		return errors.New("轮播上限须为1至30张，且不能低于当前轮播数量")
	}
	slideIDs := map[string]bool{}
	for _, s := range p.Slides {
		if s.ProductID == "" || len(s.ProductID) > 100 || slideIDs[s.ProductID] || strings.TrimSpace(s.Title) == "" || utf8.RuneCountInString(s.Title) > 100 || utf8.RuneCountInString(s.Description) > 300 || utf8.RuneCountInString(s.TradeLabel) > 24 || utf8.RuneCountInString(s.ServerLabel) > 60 || utf8.RuneCountInString(s.PriceLabel) > 60 || s.ImagePositionX < 0 || s.ImagePositionX > 100 || s.ImagePositionY < 0 || s.ImagePositionY > 100 || !slices.Contains([]string{"cover", "contain"}, s.ImageFit) || !slices.Contains([]string{"image-left", "image-right", "overlay"}, s.Layout) || !slices.Contains([]string{"left", "center", "right"}, s.TextAlign) || !slices.Contains([]string{"top", "center", "bottom"}, s.VerticalAlign) {
			return errors.New("轮播商品、文案或布局参数无效")
		}
		slideIDs[s.ProductID] = true
		if e := validateSlideStyle(s); e != nil {
			return e
		}
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

type HomeSection struct {
	Source      string `json:"source"`
	Title       string `json:"title"`
	Description string `json:"description"`
	Limit       int    `json:"limit"`
}
type HomePresentation struct {
	MaxSlides *int        `json:"maxSlides,omitempty"`
	Slides    []HomeSlide `json:"slides,omitempty"`
	Editorial struct {
		Title       string `json:"title"`
		Description string `json:"description"`
		IntervalMS  int    `json:"intervalMs"`
	} `json:"editorial"`
	Sections  []HomeSection `json:"sections"`
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

// 所有写入口共用状态规则，避免管理端绕开审核。
type TransitionInput struct {
	ExpectedVersion int    `json:"expected_version"`
	Decision        string `json:"decision"`
	Action          string `json:"action"`
	Reason          string `json:"reason"`
}

func Transition(status, source, operation string, in TransitionInput) (target, action string, err error) {
	switch operation {
	case "review":
		action = "review"
		if status == "pending" {
			if in.Decision == "approve" {
				target = "published"
			}
			if in.Decision == "reject" && strings.TrimSpace(in.Reason) != "" {
				target = "rejected"
			}
		}
	case "publish":
		action = "publish"
		if status == "offline" || status == "sold" || status == "rented" || source != "member" && (status == "draft" || status == "rejected") {
			target = "published"
		}
	case "status":
		action = in.Action
		if action == "submit" && (status == "draft" || status == "rejected" || status == "offline") {
			target = "pending"
		}
		if (action == "offline" || action == "sold" || action == "rented") && status == "published" && strings.TrimSpace(in.Reason) != "" {
			target = action
		}
	}
	if target == "" {
		err = fault.New(fault.Conflict, "INVALID_STATE", "当前状态不允许该操作；退回或下架必须填写原因")
	}
	return
}

func ValidateContact(in ContactInput) error {
	if strings.TrimSpace(in.Name) == "" || utf8.RuneCountInString(in.Name) > 60 || len(in.Value) > 500 || len(in.Hours) > 200 || !slices.Contains([]string{"wechat", "qq", "phone", "url"}, in.Channel) {
		return fault.New(fault.Invalid, "INVALID_CONTACT", "请检查客服名称和联系方式")
	}
	if in.Channel == "url" {
		u, e := url.Parse(in.Value)
		if e != nil || u.Host == "" || u.Scheme != "https" && u.Scheme != "http" {
			return fault.New(fault.Invalid, "INVALID_CONTACT", "网站地址须为 http 或 https")
		}
	}
	if len(in.Purpose) > 5 {
		return fault.New(fault.Invalid, "INVALID_CONTACT", "用途过多")
	}
	for _, p := range in.Purpose {
		if !slices.Contains([]string{"general", "sale", "rent", "listing", "support"}, p) {
			return fault.New(fault.Invalid, "INVALID_CONTACT", "用途无效")
		}
	}
	return nil
}

func ValidatePage(slug string, in PageInput) error {
	if !CodePattern.MatchString(slug) || strings.TrimSpace(in.Title) == "" || utf8.RuneCountInString(in.Title) > 100 || len(in.Body) > 100000 {
		return fault.New(fault.Invalid, "INVALID_PAGE", "请检查页面标识、标题和正文长度")
	}
	return nil
}

func ValidateCategoryRules(in CategoryInput) error {
	if in.TradeType != "sale" && in.TradeType != "rent" && in.TradeType != "all" {
		return fault.New(fault.Invalid, "INVALID_CATEGORY", "交易类型无效")
	}
	for _, u := range in.PriceUnits {
		if !slices.Contains([]string{"account", "hour", "day", "week", "month", "unknown"}, u) || in.TradeType == "sale" && u != "account" && u != "unknown" || in.TradeType == "rent" && u == "account" {
			return fault.New(fault.Invalid, "INVALID_CATEGORY", "报价单位不适用于该分类")
		}
	}
	if e := ValidateFilters(in.Filters); e != nil {
		return fault.New(fault.Invalid, "INVALID_FILTERS", e.Error())
	}
	return nil
}

func ValidateCategoryIdentity(id string, in CategoryInput) error {
	if !CodePattern.MatchString(id) || strings.TrimSpace(in.Name) == "" || utf8.RuneCountInString(in.Name) > 60 {
		return fault.New(fault.Invalid, "INVALID_CATEGORY", "请检查分类编码和名称")
	}
	return nil
}

type ProductQuery struct {
	Status, Source, Query string
	Limit, Offset         int
}

type RemovedOption struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

func RemovedOptions(old, next []catalog.FilterGroup) []RemovedOption {
	current := map[string]map[string]bool{}
	for _, g := range next {
		current[g.Key] = map[string]bool{}
		for _, o := range g.Options {
			current[g.Key][o.Value] = true
		}
	}
	removed := []RemovedOption{}
	for _, g := range old {
		for _, o := range g.Options {
			if !current[g.Key][o.Value] {
				removed = append(removed, RemovedOption{Key: g.Key, Value: o.Value})
			}
		}
	}
	return removed
}

type PublishField struct {
	Label       string `json:"label"`
	Placeholder string `json:"placeholder"`
	Required    bool   `json:"required"`
	Enabled     bool   `json:"enabled"`
}
type PublishForm struct {
	Fields map[string]PublishField `json:"fields"`
}

func DefaultPublishForm() PublishForm {
	return PublishForm{Fields: map[string]PublishField{
		"title":               {Label: "账号标题", Placeholder: "填写账号标题", Required: true, Enabled: true},
		"description":         {Label: "账号介绍", Placeholder: "介绍账号特点，勿填写账号密码", Required: true, Enabled: true},
		"custom_tags":         {Label: "自定义标签", Enabled: true},
		"private_credentials": {Label: "账号交接资料", Placeholder: "仅授权管理员可核验，不公开展示", Enabled: true},
	}}
}
func ValidatePublishForm(raw []byte) error {
	var form PublishForm
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if err := d.Decode(&form); err != nil {
		return errors.New("发布表单配置格式无效")
	}
	if len(form.Fields) != 4 {
		return errors.New("请保留四组发布表单字段")
	}
	for k := range DefaultPublishForm().Fields {
		f, ok := form.Fields[k]
		if !ok || strings.TrimSpace(f.Label) == "" || utf8.RuneCountInString(f.Label) > 40 || utf8.RuneCountInString(f.Placeholder) > 200 || f.Required && !f.Enabled || (k == "title" || k == "description") && (!f.Required || !f.Enabled) {
			return errors.New("字段文案无效，标题和介绍必须保留必填")
		}
	}
	return nil
}
