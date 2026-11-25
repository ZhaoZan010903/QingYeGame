package legacysync

import (
	"bytes"
	"encoding/json"
	"maps"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"qingye/backend/internal/domain/catalog"
	"qingye/backend/internal/domain/legacycatalog"
)

var productCode = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{2,31}$`)

var (
	quoteAfterUnit  = regexp.MustCompile(`(小时租|时租|日租|周租|月租)[：: ¥￥]*([0-9]+(?:\.[0-9]+)?)`)
	quoteBeforeUnit = regexp.MustCompile(`([0-9]+(?:\.[0-9]+)?)[ ]*元[ ]*/[ ]*(小时|天|日|周|月)`)
	quoteUnits      = map[string]string{
		"hour": "hour", "day": "day", "week": "week", "month": "month",
		"小时": "hour", "时租": "hour", "小时租": "hour",
		"天": "day", "日": "day", "日租": "day",
		"周": "week", "周租": "week", "月": "month", "月租": "month",
	}
)

func rawAmount(value string) string {
	if !catalog.MoneyPattern.MatchString(value) {
		return ""
	}
	integer, decimal, _ := strings.Cut(value, ".")
	integer = strings.TrimLeft(integer, "0")
	if integer == "" {
		integer = "0"
	}
	return integer + "." + (decimal + "00")[:2]
}

func amountKey(amount *string) string {
	if amount == nil {
		return ""
	}
	return rawAmount(*amount)
}

func explicitQuote(record Record, trade string) (*string, string) {
	amount := rawAmount(record.Price.String())
	if amount == "" || amount == "0.00" {
		return nil, "unknown"
	}
	if trade == "sale" {
		return &amount, "account"
	}
	if unit := quoteUnits[strings.TrimSpace(record.PriceUnit)]; unit != "" {
		return &amount, unit
	}
	// A numeric SaaS price or a broad category such as 周月租号 is insufficient.
	// Match an explicit rate in the title only when its money equals the feed price.
	title := strings.TrimSpace(record.Title)
	units := map[string]bool{}
	for _, m := range quoteAfterUnit.FindAllStringSubmatch(title, -1) {
		if rawAmount(m[2]) == amount {
			units[quoteUnits[m[1]]] = true
		}
	}
	for _, m := range quoteBeforeUnit.FindAllStringSubmatch(title, -1) {
		if rawAmount(m[1]) == amount {
			units[quoteUnits[m[2]]] = true
		}
	}
	if len(units) == 1 {
		for unit := range units {
			return &amount, unit
		}
	}
	return nil, "unknown"
}

func sourceStatus(record Record) string {
	if record.Deleted {
		return "offline"
	}
	switch strings.TrimSpace(record.Status) {
	case "出售中", "上架中", "已上架":
		return "published"
	case "下架", "已下架":
		return "offline"
	case "已售", "已售出", "已出售":
		return "sold"
	case "已出租", "租用中":
		return "rented"
	default:
		return ""
	}
}

func categoryFor(name string) (legacycatalog.LegacyCategory, string, bool) {
	for _, category := range legacycatalog.LegacyCategories {
		if category.Name == name {
			rental := ""
			switch category.ID {
			case "legacy-short-rent":
				rental = "hour_day"
			case "legacy-long-rent":
				rental = "week_month"
			}
			return category, rental, true
		}
	}
	return legacycatalog.LegacyCategory{}, "", false
}

func samePrice(a, b Product) bool {
	return amountKey(a.Amount) == amountKey(b.Amount) && a.Unit == b.Unit && slices.Equal(a.Quotes, b.Quotes)
}

func equalJSON(a, b any) bool {
	x, errX := json.Marshal(a)
	y, errY := json.Marshal(b)
	return errX == nil && errY == nil && bytes.Equal(x, y)
}

func sameProduct(a, b Product) bool {
	if !slices.Equal(a.Images, b.Images) {
		return false
	}
	// Galleries can contain hundreds of IDs. They are already compared above.
	a.Images, b.Images = nil, nil
	return equalJSON(a, b)
}

func (i *Item) hold(notice string) {
	i.Action = "hold"
	i.After = nil
	i.Notices = append(i.Notices, notice)
}

func (i *Item) block(reason string) {
	i.hold(reason)
	i.Blocking = true
}

func omitUnchanged(item Item) Item {
	if item.After != nil && item.Before != nil && sameProduct(*item.After, *item.Before) {
		if len(item.Notices) == 0 {
			return Item{}
		}
		item.Action, item.After = "hold", nil
	}
	return item
}

func recordImages(r Record) ([]string, error) {
	if len(r.Content) > 1<<20 {
		return nil, ErrImage
	}
	urls := []string{}
	seen := map[string]bool{}
	for _, part := range legacycatalog.LegacyImageURLs(r.Content) {
		u, err := legacycatalog.CanonicalImageURL(part)
		if err != nil {
			return nil, err
		}
		if !seen[u] {
			seen[u] = true
			urls = append(urls, u)
		}
	}
	return urls, nil
}

func galleryMatchesImport(old *Current, source legacycatalog.LegacyRecord) bool {
	urls, err := recordImages(Record{LegacyRecord: source})
	return err == nil && galleryMatchesURLs(old, urls)
}

func galleryMatchesURLs(old *Current, urls []string) bool {
	if len(urls) != len(old.Images) {
		return false
	}
	for i, url := range urls {
		if old.ImagesByURL[url] != old.Images[i] {
			return false
		}
	}
	return true
}

func mergeSourceAttributes(after *Product, old *Current, record, baseline legacycatalog.LegacyRecord) []string {
	original := legacycatalog.LegacyAttributes(baseline)
	keys := []string{}
	for key, value := range legacycatalog.LegacyAttributes(record) {
		if !sourceOwnsAttribute(old, key, original[key]) {
			continue
		}
		keys = append(keys, key)
		switch v := value.(type) {
		case string:
			if strings.TrimSpace(v) != "" {
				after.Attributes[key] = v
			}
		case []string:
			if len(v) > 0 {
				after.Attributes[key] = v
			}
		}
	}
	slices.Sort(keys)
	return keys
}

func sourceOwnsAttribute(old *Current, key string, original any) bool {
	if old == nil || old.SyncHidden && old.Status == "draft" {
		return true
	}
	if prev := old.LastApplied; prev != nil {
		return slices.Contains(old.Ownership.Attributes, key) && equalJSON(old.Attributes[key], prev.Attributes[key])
	}
	return old.Attributes[key] == nil || equalJSON(old.Attributes[key], original)
}

func sourceOwnership(old *Current, baseline legacycatalog.LegacyRecord) Ownership {
	if old.SyncHidden && old.Status == "draft" {
		// A rolled-back insertion is still the same source-owned product.
		return Ownership{Price: true, Title: true, Gallery: true}
	}
	if prev := old.LastApplied; prev != nil {
		return Ownership{
			Price:   old.Ownership.Price && samePrice(old.Product, *prev),
			Title:   old.Ownership.Title && old.Title == prev.Title,
			Gallery: old.Ownership.Gallery && slices.Equal(old.Images, prev.Images),
		}
	}
	return Ownership{
		Price: (old.Trade == "sale" && old.Unit == "account" && old.Amount != nil &&
			amountKey(old.Amount) == rawAmount(baseline.Price.String())) ||
			(old.Amount == nil && old.Unit == "unknown" && len(old.Quotes) == 0),
		Title:   old.Title == strings.TrimSpace(baseline.Title),
		Gallery: galleryMatchesImport(old, baseline),
	}
}

func visibilityChange(item Item, old *Current, status string) Item {
	var baseline legacycatalog.LegacyRecord
	_ = json.Unmarshal(old.Baseline, &baseline)
	item.Ownership = sourceOwnership(old, baseline)
	// The current source record can be absent. Retain the last set of trusted
	// source keys except fields manually changed before this status update.
	if old.LastApplied != nil {
		for _, key := range old.Ownership.Attributes {
			if sourceOwnsAttribute(old, key, nil) {
				item.Ownership.Attributes = append(item.Ownership.Attributes, key)
			}
		}
	} else {
		for key, original := range legacycatalog.LegacyAttributes(baseline) {
			if sourceOwnsAttribute(old, key, original) {
				item.Ownership.Attributes = append(item.Ownership.Attributes, key)
			}
		}
		slices.Sort(item.Ownership.Attributes)
	}
	next := old.Product
	next.Status, next.Approved, next.SyncHidden = status, false, true
	item.Before, item.After = &old.Product, &next
	if old.Status == "published" && old.Approved {
		item.Action = "offline"
	} else {
		item.Action = "update"
	}
	return item
}

func planMissing(old *Current) Item {
	return visibilityChange(Item{
		LegacyID: old.LegacyID, Code: old.Code,
		Notices: []string{"两次完整读取均未找到该商品，按旧站目录下架"},
	}, old, "offline")
}

func planRecord(r Record, old *Current, rules map[string]catalog.CategoryRules, codeCollision bool, now time.Time) (Item, []string) {
	code := strings.ToUpper(strings.TrimSpace(r.Code))
	item := Item{LegacyID: r.ID, Code: code,
		SourcePrice: r.Price.String(), SourceUpdatedAt: TimePointer(r.Updated), Notices: []string{}}
	if old != nil {
		item.Before = &old.Product
		item.Code = old.Code
	}
	if len(item.SourcePrice) > 40 {
		item.SourcePrice = ""
	}
	if !legacycatalog.ValidSourceID(r.ID) || !productCode.MatchString(code) || codeCollision ||
		(old != nil && (!old.Legacy || !strings.EqualFold(code, old.Code))) {
		if old == nil && !productCode.MatchString(code) {
			item.Code = ""
		}
		item.block("商品编号或旧站 ID 无法唯一匹配，请核对商品映射")
		return item, nil
	}
	category, rental, known := categoryFor(strings.TrimSpace(r.Category))
	status := sourceStatus(r)
	if !known || status == "" || (category.Trade == "sale" && status == "rented") {
		item.block("旧站分类或状态无法识别")
		return item, nil
	}
	if old != nil {
		if old.Trade != category.Trade || (old.SourceCategory != "" && old.SourceCategory != category.Name) {
			item.block("新旧站交易分类不一致，请核对商品映射")
			return item, nil
		}
		if old.Deleted || !(old.Status == "published" && old.Approved || old.SyncHidden) {
			if status == "published" {
				item.hold("已保留新站人工下架、回收或审核状态")
			}
			return item, nil
		}
		if status != "published" {
			if old.Status == status || old.Status == "draft" || !rules[old.Category].Enabled {
				return item, nil
			}
			return visibilityChange(item, old, status), nil
		}
	} else if status != "published" {
		return item, nil
	}
	title := strings.TrimSpace(r.Title)
	if r.Created.After(now.Add(time.Hour)) || r.Updated.After(now.Add(time.Hour)) {
		item.block("旧站商品时间异常")
		return item, nil
	}
	if title == "" || utf8.RuneCountInString(title) > 1000 || strings.ContainsRune(title, 0) {
		item.block("旧站标题缺失或格式异常")
		return item, nil
	}
	quote, unit := explicitQuote(r, category.Trade)
	var after Product
	var baseline legacycatalog.LegacyRecord
	if old == nil {
		item.Action = "create"
		after = Product{
			ID: legacycatalog.DeterministicID("legacy-product:" + r.ID), LegacyID: r.ID, Code: item.Code,
			Category: category.ID, SourceCategory: category.Name, Trade: category.Trade,
			RentalCategory: rental, Attributes: map[string]any{}, Title: title, Description: title,
			Unit: unit, Amount: quote, Quotes: []catalog.RentalQuote{}, Status: status, Approved: true,
			Version: 1, CreatedAt: r.Created.UTC(), Images: []string{},
		}
		if after.CreatedAt.IsZero() {
			after.CreatedAt = now.UTC().Truncate(time.Microsecond)
		}
		item.Ownership = Ownership{Price: true, Title: true, Gallery: true}
		if quote == nil {
			item.Notices = append(item.Notices, "旧站报价未确认，前台显示联系客服")
		}
	} else {
		after = old.Product
		item.Action = "update"
		if old.SyncHidden {
			item.Action = "restore"
			after.Status, after.Approved, after.SyncHidden = "published", true, false
		}
		after.Attributes = maps.Clone(old.Attributes)
		if after.Attributes == nil {
			after.Attributes = map[string]any{}
		}
		_ = json.Unmarshal(old.Baseline, &baseline)
		priceChanged := rawAmount(baseline.Price.String()) != rawAmount(r.Price.String())
		item.Ownership = sourceOwnership(old, baseline)
		if item.Ownership.Title {
			after.Title = title
			if after.Description == "" || after.Description == old.Title {
				after.Description = title
			}
		} else if title != old.Title {
			item.Notices = append(item.Notices, "已保留新站人工编辑的标题")
		}
		if quote != nil && item.Ownership.Price {
			after.Amount, after.Unit = quote, unit
		} else if priceChanged && (quote == nil || (amountKey(old.Amount) != amountKey(quote) || old.Unit != unit)) {
			if quote == nil {
				item.Notices = append(item.Notices, "旧价或租期不明确，已保留新站生效报价")
			} else {
				item.Notices = append(item.Notices, "旧站报价有变化，已保留新站人工报价")
			}
		}
	}
	if item.Ownership.Price && quote != nil && after.Trade == "rent" {
		after.Quotes = []catalog.RentalQuote{{Unit: unit, Amount: *quote}}
	}
	item.Ownership.Attributes = mergeSourceAttributes(&after, old, r.LegacyRecord, baseline)
	categoryRules, ok := rules[after.Category]
	input := after.Input()
	if !ok || !categoryRules.Enabled || categoryRules.TradeType != "all" && categoryRules.TradeType != after.Trade {
		item.block("新站对应分类不可用或交易类型不一致")
		return item, nil
	}
	if old == nil {
		if err := catalog.ValidateInput(input, true, categoryRules); err != nil {
			item.block("需要补齐新站商品格式：" + err.Error())
			return item, nil
		}
	} else {
		input.PreviousAttributes = old.Attributes
		if err := catalog.ValidateBasicInput(input, true); err != nil {
			item.block("需要核对已有商品资料：" + err.Error())
			return item, nil
		}
		if !samePrice(old.Product, after) {
			if err := catalog.ValidateInput(input, true, categoryRules); err != nil {
				after.Amount, after.Unit, after.Quotes = old.Amount, old.Unit, old.Quotes
				item.Notices = append(item.Notices, "旧站报价需要适配分类规则，已保留新站生效报价")
			}
		}
	}
	item.After = &after
	if old != nil && !item.Ownership.Gallery {
		if !old.hasReadyGallery() {
			item.block("现有图集不完整，请先修复新站图片")
		} else if !galleryMatchesImport(old, r.LegacyRecord) {
			item.Notices = append(item.Notices, "已保留新站人工维护的图集")
		}
		return item, nil
	}
	urls, err := recordImages(r)
	if err != nil || len(urls) == 0 {
		if old != nil && old.hasReadyGallery() {
			item.Notices = append(item.Notices, "旧站图片清单异常，已保留新站的完整图集")
		} else {
			item.block("缺少有效的图片清单，无法上架")
		}
		return item, nil
	}
	if old != nil && galleryMatchesURLs(old, urls) {
		return item, nil
	}
	return item, urls
}

func summarize(items []Item) Summary {
	var out Summary
	for _, item := range items {
		switch item.Action {
		case "create":
			out.Created++
			out.Imaged++
		case "update":
			out.Updated++
		case "restore":
			out.Restored++
		case "offline":
			out.Removed++
		case "missing":
			out.Missing++
		}
		if len(item.Notices) > 0 {
			out.Review++
		}
		if item.Before != nil && item.After != nil {
			if !samePrice(*item.Before, *item.After) {
				out.Priced++
			}
			if !slices.Equal(item.Before.Images, item.After.Images) {
				out.Imaged++
			}
		}
	}
	return out
}
