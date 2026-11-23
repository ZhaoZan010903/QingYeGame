package legacysync

import (
	"bytes"
	"encoding/json"
	"fmt"
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
	return equalJSON(a, b)
}

func (i *Item) hold(notice string) {
	i.Action = "hold"
	i.After = nil
	i.Notices = append(i.Notices, notice)
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
	if err != nil || len(urls) != len(old.Images) {
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
		if old != nil {
			before := old.Attributes[key]
			if old.LastApplied == nil && before != nil && !equalJSON(before, original[key]) {
				continue
			}
			if old.LastApplied != nil && (!slices.Contains(old.Ownership.Attributes, key) ||
				!equalJSON(before, old.LastApplied.Attributes[key])) {
				continue
			}
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

func planRecord(r Record, old *Current, rules map[string]catalog.CategoryRules, codeCollision bool, now time.Time) (Item, []string) {
	item := Item{Action: "update", LegacyID: r.ID, Code: strings.TrimSpace(r.Code),
		SourcePrice: r.Price.String(), SourceUpdatedAt: TimePointer(r.Updated), Notices: []string{}}
	if old != nil {
		item.Before = &old.Product
		item.Code = old.Code
	}
	if len(item.SourcePrice) > 40 {
		item.SourcePrice = ""
	}
	if !legacycatalog.ValidSourceID(r.ID) || !productCode.MatchString(item.Code) || codeCollision {
		item.hold("商品编号缺失或冲突，核对商品映射")
		return item, nil
	}
	category, rental, known := categoryFor(strings.TrimSpace(r.Category))
	status := sourceStatus(r)
	if !known || status == "" || (category.Trade == "sale" && status == "rented") {
		item.hold("旧站分类或状态需要核对")
		return item, nil
	}
	title := strings.TrimSpace(r.Title)
	if r.Created.After(now.Add(time.Hour)) || r.Updated.After(now.Add(time.Hour)) {
		item.hold("旧站商品时间异常")
		return item, nil
	}
	if title == "" || utf8.RuneCountInString(title) > 1000 || strings.ContainsRune(title, 0) {
		item.hold("旧站标题缺失或格式异常")
		return item, nil
	}
	quote, unit := explicitQuote(r, category.Trade)
	var after Product
	var baseline legacycatalog.LegacyRecord
	if old == nil {
		item.Action = "create"
		if status != "published" {
			item.hold("旧站商品目前不可展示")
			return item, nil
		}
		if quote == nil {
			item.hold("旧价或租期不明确，补全后再上架")
			return item, nil
		}
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
		item.Ownership = Ownership{Price: true, Status: true, Title: true, Gallery: true}
	} else {
		if old.Deleted || !old.Legacy || old.Trade != category.Trade ||
			(old.SourceCategory != "" && old.SourceCategory != category.Name) {
			item.hold("商品已回收或交易分类已变化，核对后再处理")
			return item, nil
		}
		if old.Status != "published" && old.Status != "offline" && old.Status != "sold" && old.Status != "rented" {
			item.hold("商品正在新站编辑或审核")
			return item, nil
		}
		after = old.Product
		after.Attributes = maps.Clone(old.Attributes)
		if after.Attributes == nil {
			after.Attributes = map[string]any{}
		}
		_ = json.Unmarshal(old.Baseline, &baseline)
		priceChanged := rawAmount(baseline.Price.String()) != rawAmount(r.Price.String())
		if old.LastApplied != nil {
			item.Ownership = Ownership{
				Price:   old.Ownership.Price && samePrice(old.Product, *old.LastApplied),
				Status:  old.Ownership.Status && old.Status == old.LastApplied.Status && old.Approved == old.LastApplied.Approved,
				Title:   old.Ownership.Title && old.Title == old.LastApplied.Title,
				Gallery: old.Ownership.Gallery && slices.Equal(old.Images, old.LastApplied.Images),
			}
		} else {
			item.Ownership = Ownership{
				Price: (old.Trade == "sale" && old.Unit == "account" && old.Amount != nil &&
					amountKey(old.Amount) == rawAmount(baseline.Price.String())) ||
					(old.Amount == nil && old.Unit == "unknown" && len(old.Quotes) == 0),
				Title:   old.Title == strings.TrimSpace(baseline.Title),
				Gallery: galleryMatchesImport(old, baseline),
			}
		}
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
		switch {
		case old.Status == "published" && status != "published":
			after.Status, after.Approved, item.Ownership.Status = status, false, true
		case item.Ownership.Status && status != old.Status:
			after.Status, after.Approved = status, status == "published"
		}
	}
	if item.Ownership.Price && quote != nil && after.Trade == "rent" {
		after.Quotes = []catalog.RentalQuote{{Unit: unit, Amount: *quote}}
	}
	item.Ownership.Attributes = mergeSourceAttributes(&after, old, r.LegacyRecord, baseline)
	categoryRules, ok := rules[after.Category]
	input := catalog.ProductInput{
		CategoryID: after.Category, Title: after.Title, TradeType: after.Trade,
		PriceUnit: after.Unit, PriceAmount: after.Amount, Attributes: after.Attributes,
		Description: after.Description, RentalQuotes: after.Quotes, LegacyAttributes: true,
		RentalCategory: &after.RentalCategory,
	}
	if !ok || !categoryRules.Enabled {
		item.hold("新站对应分类不可用")
		return item, nil
	}
	if old == nil {
		if err := catalog.ValidateInput(input, true, categoryRules); err != nil {
			item.hold("需要补齐新站商品格式：" + err.Error())
			return item, nil
		}
	} else {
		input.PreviousAttributes = old.Attributes
		if err := catalog.ValidateBasicInput(input, true); err != nil {
			item.hold("需要核对已有商品资料：" + err.Error())
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
	if status != "published" {
		// An explicit visibility change is independent of a CDN failure.
		return item, nil
	}
	urls, err := recordImages(r)
	if err != nil || len(urls) == 0 {
		item.hold("旧站图片清单缺失或来源异常")
		return item, nil
	}
	if old != nil && !item.Ownership.Gallery {
		item.Notices = append(item.Notices, "新站图集已人工调整，保留现有顺序和图片")
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
			if item.Before != nil && item.After != nil {
				if item.Before.Status == "published" && item.After.Status != "published" {
					out.Removed++
				}
				if !samePrice(*item.Before, *item.After) {
					out.Priced++
				}
				if !slices.Equal(item.Before.Images, item.After.Images) {
					out.Imaged++
				}
			}
		case "missing":
			out.Missing++
		}
		if len(item.Notices) > 0 {
			out.Review++
		}
		for _, notice := range item.Notices {
			if strings.Contains(notice, "图片清单") || strings.Contains(notice, "图片读取或校验") {
				out.BadImage++
				break
			}
		}
	}
	return out
}

func reviewReason(summary Summary, existing int) string {
	switch {
	case summary.Missing >= max(12, (existing+9)/10):
		return fmt.Sprintf("本批有 %d 件在旧站列表中缺失，请先确认目录范围", summary.Missing)
	case summary.Removed >= max(5, (existing+19)/20):
		return "本批集中出现下架或成交，请核对后发布"
	case summary.Priced >= max(8, (existing+9)/10):
		return "本批集中出现报价变化，请核对后发布"
	case summary.BadImage >= 5:
		return "本批多件商品图集异常，请核对后发布"
	default:
		return ""
	}
}
