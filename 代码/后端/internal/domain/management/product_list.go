package management

import (
	"qingye/backend/internal/domain/catalog"
	"qingye/backend/internal/domain/fault"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"
)

// Numbered pages are an admin-only addition; existing cursor consumers remain valid.
func DecodeProductPage(limitValue, pageValue, cursor string) (int, int, error) {
	limit, offset, err := catalog.DecodePage(limitValue, cursor)
	if err != nil || pageValue == "" {
		return limit, offset, err
	}
	page, err := strconv.Atoi(pageValue)
	if err != nil || page < 1 || page > 1000000/limit+1 || cursor != "" {
		return 0, 0, fault.New(fault.Invalid, "INVALID_PAGE", "页码无效，且不能与游标同时使用")
	}
	return limit, (page - 1) * limit, nil
}

func ValidateProductQuery(in ProductQuery) error {
	if in.Limit < 1 || in.Limit > 50 || in.Offset < 0 || in.Offset > 1000000 {
		return fault.New(fault.Invalid, "INVALID_PAGE", "分页参数无效")
	}
	if !slices.Contains([]string{"", "all", "draft", "pending", "publishing", "published", "rejected", "rejected_final", "offline", "sold", "rented"}, in.Status) {
		return fault.New(fault.Invalid, "INVALID_STATUS", "商品状态无效")
	}
	if !slices.Contains([]string{"", "all", "member", "platform", "legacy"}, in.Source) || !slices.Contains([]string{"", "sale", "rent"}, in.TradeType) || !slices.Contains([]string{"", "updated", "published", "created"}, in.Sort) || utf8.RuneCountInString(in.Query) > 200 {
		return fault.New(fault.Invalid, "INVALID_QUERY", "商品筛选条件无效")
	}
	return nil
}

func ValidateTrashInput(in TransitionInput, restore bool) error {
	if in.ExpectedVersion < 1 || utf8.RuneCountInString(in.Reason) > 500 || !restore && strings.TrimSpace(in.Reason) == "" {
		return fault.New(fault.Invalid, "INVALID_PRODUCT_ACTION", "请填写删除原因（最多500字），并使用当前商品版本")
	}
	return nil
}
