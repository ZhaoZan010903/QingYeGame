package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"qingye/backend/internal/domain/catalog"
	"strings"
)

func ProductOrder(sort string) (string, error) {
	switch sort {
	case "", "featured":
		return "p.manual_rank ASC NULLS LAST,p.published_at DESC NULLS LAST,p.id", nil
	case "newest":
		return "p.published_at DESC NULLS LAST,p.id", nil
	case "hot", "recommended":
		return "COALESCE((SELECT score FROM product_hot_scores h WHERE h.product_id=p.id AND h.computed_at BETWEEN now()-interval '60 minutes' AND now()),0) DESC,p.published_at DESC NULLS LAST,p.id", nil
	case "price_asc", "price-up":
		return "p.price_amount ASC NULLS LAST,p.id", nil
	case "price_desc", "price-down":
		return "p.price_amount DESC NULLS LAST,p.id", nil
	}
	return "", errors.New("排序方式无效")
}

func (a *CatalogStore) Search(ctx context.Context, q catalog.SearchInput) ([]map[string]any, int, error) {
	return a.search(ctx, q, true)
}
func (a *CatalogStore) Section(ctx context.Context, q catalog.SearchInput) ([]map[string]any, error) {
	rows, _, err := a.search(ctx, q, false)
	return rows, err
}
func (a *CatalogStore) search(ctx context.Context, q catalog.SearchInput, countTotal bool) ([]map[string]any, int, error) {
	order, e := ProductOrder(q.Sort)
	if e != nil {
		return nil, 0, e
	}
	where := VisibleSQL
	args := []any{}
	add := func(sql string, v any) { args = append(args, v); where += " AND " + fmt.Sprintf(sql, len(args)) }
	if q.Query != "" {
		add("(p.title ILIKE '%%'||$%[1]d||'%%' OR p.product_code ILIKE '%%'||$%[1]d||'%%' OR p.attributes->>'server' ILIKE '%%'||$%[1]d||'%%')", q.Query)
	}
	if q.Category != "" {
		add("p.category_id=$%d", q.Category)
	}
	if q.TradeType != "" && q.TradeType != "all" {
		add("p.trade_type=$%d", q.TradeType)
	}
	priceSQL := "p.price_amount"
	if q.PriceUnit != "" && q.PriceUnit != "all" {
		args = append(args, q.PriceUnit)
		n := len(args)
		where += fmt.Sprintf(" AND (p.price_unit=$%[1]d OR EXISTS(SELECT 1 FROM jsonb_array_elements(p.rental_quotes) quote WHERE quote->>'unit'=$%[1]d))", n)
		priceSQL = fmt.Sprintf("COALESCE((SELECT (quote->>'amount')::numeric FROM jsonb_array_elements(p.rental_quotes) quote WHERE quote->>'unit'=$%[1]d LIMIT 1),CASE WHEN p.price_unit=$%[1]d THEN p.price_amount END)", n)
		order = strings.ReplaceAll(order, "p.price_amount", priceSQL)
	}
	if q.MinPrice != nil {
		add(priceSQL+" >= $%d::numeric", *q.MinPrice)
	}
	if q.MaxPrice != nil {
		add(priceSQL+" <= $%d::numeric", *q.MaxPrice)
	}
	for key, values := range q.Filters {
		if len(values) > 0 {
			args = append(args, key, values)
			where += fmt.Sprintf(" AND ((p.attributes->>$%[1]d)=ANY($%[2]d::text[]) OR (jsonb_typeof(p.attributes->$%[1]d)='array' AND (p.attributes->$%[1]d) ?| $%[2]d::text[]))", len(args)-1, len(args))
		}
	}
	if len(q.Weights) > 0 {
		order, e = PersonalizedOrder(q.Weights, &where, &args)
		if e != nil {
			return nil, 0, e
		}
	}
	items, total, err := a.productRows(ctx, where, args, order, q.Limit, q.Offset, true, countTotal)
	if err == nil && q.PriceUnit != "" && q.PriceUnit != "all" {
		for _, p := range items {
			if quotes, ok := p["rental_quotes"].([]any); ok {
				for _, raw := range quotes {
					quote, ok := raw.(map[string]any)
					if ok && quote["unit"] == q.PriceUnit {
						p["price_unit"] = q.PriceUnit
						p["price_amount"] = quote["amount"]
						break
					}
				}
			}
		}
	}
	return items, total, err
}

// 排序表达式固定，画像值只作为 JSON 参数绑定。
func PersonalizedOrder(weights map[string]map[string]float64, where *string, args *[]any) (string, error) {
	raw, e := json.Marshal(weights)
	if e != nil {
		return "", e
	}
	*args = append(*args, json.RawMessage(raw))
	n := len(*args)
	*where += fmt.Sprintf(" AND $%d::jsonb IS NOT NULL", n)
	order := fmt.Sprintf(`COALESCE(($%[1]d::jsonb->'itemcf'->>p.id)::float8,0) DESC,
 (6*COALESCE(($%[1]d::jsonb->'category'->>p.category_id)::float8,0)
 +2*COALESCE(($%[1]d::jsonb->'trade'->>p.trade_type)::float8,0)
 +3*COALESCE(($%[1]d::jsonb->'server'->>(p.attributes->>'server'))::float8,0)
 +COALESCE(($%[1]d::jsonb->'price'->>(CASE WHEN p.price_amount>0 AND p.price_unit<>'unknown' THEN p.price_unit||':'||floor(log(2,p.price_amount+1))::text ELSE '' END))::float8,0)) DESC,`, n)
	hot, _ := ProductOrder("hot")
	return order + hot, nil
}
