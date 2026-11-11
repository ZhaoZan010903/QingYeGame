package catalog

import (
	"qingye/backend/internal/domain/fault"
	"strconv"
	"strings"
)

type SearchInput struct {
	Query, Category, TradeType, PriceUnit, Sort string
	MinPrice, MaxPrice                          *string
	Filters                                     map[string][]string
	Limit, Offset                               int
	Weights                                     map[string]map[string]float64
}

func (q SearchInput) Validate() error {
	if q.Limit < 1 || q.Limit > 50 || q.Offset < 0 || q.Offset > 1000000 {
		return fault.New(fault.Invalid, "INVALID_CURSOR", "分页大小或游标无效")
	}
	if len(q.Query) > 200 {
		return fault.New(fault.Invalid, "QUERY_TOO_LONG", "关键词过长")
	}
	switch q.Sort {
	case "", "featured", "hot", "recommended", "newest", "price_asc", "price-up", "price_desc", "price-down":
	default:
		return fault.New(fault.Invalid, "INVALID_SORT", "排序方式无效")
	}
	if q.TradeType != "" && q.TradeType != "all" && q.TradeType != "sale" && q.TradeType != "rent" {
		return fault.New(fault.Invalid, "INVALID_TRADE", "交易类型无效")
	}
	if (q.MinPrice != nil || q.MaxPrice != nil || strings.HasPrefix(q.Sort, "price")) && (q.PriceUnit == "" || q.PriceUnit == "all" || q.PriceUnit == "unknown") {
		return fault.New(fault.Invalid, "PRICE_UNIT_REQUIRED", "先选择已确认的报价单位再比较价格")
	}
	for _, v := range []*string{q.MinPrice, q.MaxPrice} {
		if v != nil && !MoneyPattern.MatchString(*v) {
			return fault.New(fault.Invalid, "INVALID_PRICE", "价格范围无效")
		}
	}
	if q.MinPrice != nil && q.MaxPrice != nil {
		lo, _ := strconv.ParseFloat(*q.MinPrice, 64)
		hi, _ := strconv.ParseFloat(*q.MaxPrice, 64)
		if lo > hi {
			return fault.New(fault.Invalid, "INVALID_PRICE", "最低价不能大于最高价")
		}
	}
	if len(q.Filters) > 30 {
		return fault.New(fault.Invalid, "INVALID_FILTERS", "筛选格式无效")
	}
	for _, values := range q.Filters {
		if len(values) > 40 {
			return fault.New(fault.Invalid, "INVALID_FILTERS", "筛选项过多")
		}
	}
	return nil
}
func (q SearchInput) ValidateFilters(allowed map[string]map[string]bool) error {
	for key, values := range q.Filters {
		for _, v := range values {
			if !allowed[key][v] {
				return fault.New(fault.Invalid, "INVALID_FILTERS", "筛选项不存在")
			}
		}
	}
	return nil
}
