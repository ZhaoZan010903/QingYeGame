package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"qingye/backend/internal/domain/catalog"
	"qingye/backend/internal/infrastructure/postgres"
	"reflect"
	"strings"
	"testing"
	"time"
)

func boostOrderIDs(t *testing.T, a *App, in catalog.SearchInput) []string {
	t.Helper()
	if in.Limit == 0 {
		in.Limit = 50
	}
	rows, _, err := (&postgres.CatalogStore{DB: a.DB}).Search(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	ids := make([]string, 0, len(rows))
	for _, row := range rows {
		ids = append(ids, row["id"].(string))
	}
	return ids
}

func boostQueryIDs(t *testing.T, a *App, query string, args ...any) []string {
	t.Helper()
	rows, err := a.DB.Query(context.Background(), query, args...)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	ids := []string{}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	if err = rows.Err(); err != nil {
		t.Fatal(err)
	}
	return ids
}

func boostExec(t *testing.T, a *App, query string, args ...any) {
	t.Helper()
	if _, err := a.DB.Exec(context.Background(), query, args...); err != nil {
		t.Fatal(err)
	}
}

func TestRecommendationBoostZeroPreservesEveryLegacyOrder(t *testing.T) {
	a := integrationMemberApp(t)
	recommendationCategory(t, a, "boost-zero", true)
	fixed := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for i, id := range []string{"lowest-int", "zero", "near", "ten-thousand", "old-outside", "highest-int", "unranked-a", "unranked-b"} {
		recommendationProduct(t, a, id, "boost-zero", "platform", "published", fixed.Add(time.Duration(i%3)*time.Hour), true)
	}
	boostExec(t, a, `UPDATE products SET manual_rank=CASE id WHEN 'lowest-int' THEN -2147483648 WHEN 'zero' THEN 0 WHEN 'near' THEN 2 WHEN 'ten-thousand' THEN 10000 WHEN 'old-outside' THEN 20000 WHEN 'highest-int' THEN 2147483647 END,price_amount=CASE WHEN id='unranked-a' THEN NULL ELSE length(id)*10 END;
 INSERT INTO product_hot_scores(product_id,batch_id,score,computed_at) SELECT id,'zero-fixture',length(id)%3,now() FROM products`)
	oldHot := `COALESCE((SELECT score FROM product_hot_scores h WHERE h.product_id=p.id AND h.computed_at BETWEEN now()-interval '60 minutes' AND now()),0)`
	oldOrders := map[string]string{
		"featured":    "p.manual_rank ASC NULLS LAST,p.published_at DESC NULLS LAST,p.id",
		"hot":         oldHot + " DESC,p.published_at DESC NULLS LAST,p.id",
		"recommended": oldHot + " DESC,p.published_at DESC NULLS LAST,p.id",
		"newest":      "p.published_at DESC NULLS LAST,p.id",
		"price_asc":   "p.price_amount ASC NULLS LAST,p.id",
		"price_desc":  "p.price_amount DESC NULLS LAST,p.id",
	}
	for mode, order := range oldOrders {
		t.Run(mode, func(t *testing.T) {
			expected := boostQueryIDs(t, a, "SELECT p.id FROM products p JOIN categories c ON c.id=p.category_id WHERE "+postgres.VisibleSQL+" ORDER BY "+order)
			got := boostOrderIDs(t, a, catalog.SearchInput{Sort: mode})
			if !reflect.DeepEqual(got, expected) {
				t.Fatalf("zero boost changed %s: %v != %v", mode, got, expected)
			}
		})
	}
	weights := map[string]map[string]float64{"itemcf": {"near": 0.4, "zero": 0.4, "highest-int": 0.2}, "category": {"boost-zero": 1}, "server": {"fixture-region": 1}, "trade": {"sale": 1}}
	// The fixed attributes tie, so original personalization is ItemCF then heat/date/id.
	for _, cf := range []map[string]float64{weights["itemcf"], {}} {
		weights["itemcf"] = cf
		raw, _ := json.Marshal(cf)
		expected := boostQueryIDs(t, a, "SELECT p.id FROM products p JOIN categories c ON c.id=p.category_id WHERE "+postgres.VisibleSQL+" ORDER BY COALESCE(($1::jsonb->>p.id)::float8,0) DESC,"+oldOrders["hot"], raw)
		got := boostOrderIDs(t, a, catalog.SearchInput{Sort: "recommended", Weights: weights})
		if !reflect.DeepEqual(got, expected) {
			t.Fatalf("zero boost changed personalization: %v != %v", got, expected)
		}
	}
}

func TestRecommendationBoostAddsToHotWithoutOverridingNaturalStrength(t *testing.T) {
	a := integrationMemberApp(t)
	recommendationCategory(t, a, "boost-hot", true)
	now := time.Now().UTC()
	for i, id := range []string{"strong", "near", "weak", "fresh"} {
		recommendationProduct(t, a, id, "boost-hot", "platform", "published", now.Add(time.Duration(i-4)*time.Hour), true)
	}
	boostExec(t, a, `INSERT INTO product_hot_scores(product_id,batch_id,score,computed_at) VALUES('strong','fixture',250,now()),('near','fixture',30,now()),('weak','fixture',1,now()),('fresh','fixture',0,now())`)
	baseline := boostOrderIDs(t, a, catalog.SearchInput{Sort: "hot"})
	boostExec(t, a, `UPDATE products SET recommendation_boost=100,recommendation_version=recommendation_version+1 WHERE id='weak'`)
	if got := boostOrderIDs(t, a, catalog.SearchInput{Sort: "hot"}); strings.Join(got, ",") != "strong,weak,near,fresh" {
		t.Fatal("boost-first or absent additive order", got)
	}
	boostExec(t, a, `UPDATE products SET recommendation_boost=0,recommendation_version=recommendation_version+1`)
	if got := boostOrderIDs(t, a, catalog.SearchInput{Sort: "hot"}); !reflect.DeepEqual(got, baseline) {
		t.Fatal("clearing boost did not restore natural order", got)
	}
	boostExec(t, a, `UPDATE products SET recommendation_boost=31,recommendation_version=recommendation_version+1 WHERE id='fresh'`)
	if got := boostOrderIDs(t, a, catalog.SearchInput{Sort: "hot"}); strings.Join(got, ",") != "strong,fresh,near,weak" {
		t.Fatal("small boost did not cross nearby natural scores", got)
	}
	for _, age := range []string{"now()-interval '61 minutes'", "now()+interval '1 hour'"} {
		boostExec(t, a, `UPDATE product_hot_scores SET computed_at=`+age+` WHERE product_id='strong'`)
		if got := boostOrderIDs(t, a, catalog.SearchInput{Sort: "hot"}); strings.Join(got, ",") != "fresh,near,weak,strong" {
			t.Fatal("invalid heat freshness influenced boosted order", got)
		}
	}
	var stored int
	if err := a.DB.QueryRow(context.Background(), `SELECT score FROM product_hot_scores WHERE product_id='strong'`).Scan(&stored); err != nil || stored != 250 {
		t.Fatal("boost rewrote natural heat", stored, err)
	}
}

func TestRecommendationBoostFeaturedAndUnchangedManualCarouselPriceNewest(t *testing.T) {
	a := integrationMemberApp(t)
	recommendationCategory(t, a, "boost-featured", true)
	at := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)
	for i, id := range []string{"far", "near", "boosted", "unranked"} {
		recommendationProduct(t, a, id, "boost-featured", "platform", "published", at.Add(time.Duration(i)*time.Hour), true)
	}
	boostExec(t, a, `UPDATE products SET manual_rank=CASE id WHEN 'far' THEN 1 WHEN 'near' THEN 9900 WHEN 'boosted' THEN 9950 END,price_amount=CASE id WHEN 'far' THEN 40 WHEN 'near' THEN 30 WHEN 'boosted' THEN 20 ELSE 10 END`)
	unchanged := map[string][]string{}
	for _, mode := range []string{"newest", "price_asc", "price_desc"} {
		unchanged[mode] = boostOrderIDs(t, a, catalog.SearchInput{Sort: mode})
	}
	boostExec(t, a, `UPDATE products SET recommendation_boost=100,recommendation_version=recommendation_version+1 WHERE id IN ('boosted','unranked')`)
	if got := boostOrderIDs(t, a, catalog.SearchInput{Sort: "featured"}); strings.Join(got, ",") != "far,boosted,near,unranked" {
		t.Fatal("featured is not natural rank score plus bonus", got)
	}
	for mode, expected := range unchanged {
		if got := boostOrderIDs(t, a, catalog.SearchInput{Sort: mode}); !reflect.DeepEqual(got, expected) {
			t.Fatal("boost affected", mode, got)
		}
	}
	rows, err := (&postgres.CatalogStore{DB: a.DB}).Featured(context.Background(), json.RawMessage(`{"slides":[{"productId":"near"},{"productId":"far"},{"productId":"boosted"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 3 || rows[0]["id"] != "near" || rows[1]["id"] != "far" || rows[2]["id"] != "boosted" {
		t.Fatal("boost reordered manual carousel", rows)
	}
}

func TestRecommendationBoostPersonalizedScoresFiltersAndPagination(t *testing.T) {
	a := integrationMemberApp(t)
	recommendationCategory(t, a, "preferred", true)
	recommendationCategory(t, a, "other", true)
	for i, id := range []string{"strong", "near", "attribute", "filtered-cf", "tie-a", "tie-b"} {
		category := "other"
		if id == "attribute" {
			category = "preferred"
		}
		recommendationProduct(t, a, id, category, "platform", "published", time.Date(2026, 1, 1, 0, i, 0, 0, time.UTC), true)
	}
	weights := map[string]map[string]float64{"itemcf": {"strong": 0.99, "near": 0.4, "filtered-cf": 0.8}, "category": {"preferred": 1}}
	boostExec(t, a, `UPDATE products SET recommendation_boost=50,recommendation_version=recommendation_version+1 WHERE id='near'`)
	got := boostOrderIDs(t, a, catalog.SearchInput{Sort: "recommended", Weights: weights})
	if got[0] != "strong" || got[1] != "near" || got[2] != "filtered-cf" {
		t.Fatal("ItemCF strength was overridden by positive-boost grouping", got)
	}
	boostExec(t, a, `UPDATE products SET recommendation_boost=60,recommendation_version=recommendation_version+1 WHERE id='near'`)
	got = boostOrderIDs(t, a, catalog.SearchInput{Sort: "recommended", Weights: weights})
	if got[0] != "near" || got[1] != "strong" {
		t.Fatal("ItemCF boost did not add to normalized score", got)
	}
	// Equal combined score must use natural ItemCF, not boost again.
	weights["itemcf"]["strong"] = 1
	got = boostOrderIDs(t, a, catalog.SearchInput{Sort: "recommended", Weights: weights})
	if got[0] != "strong" || got[1] != "near" {
		t.Fatal("combined tie lost original ItemCF ordering", got)
	}
	all := got
	paged := []string{}
	for offset := 0; offset < len(all); offset += 2 {
		paged = append(paged, boostOrderIDs(t, a, catalog.SearchInput{Sort: "recommended", Weights: weights, Limit: 2, Offset: offset})...)
	}
	if !reflect.DeepEqual(all, paged) {
		t.Fatal("pagination changed candidate score mode or tie order", all, paged)
	}
	// A positive ItemCF only outside the filter must not suppress attributes.
	weights["itemcf"] = map[string]float64{"filtered-cf": 1}
	boostExec(t, a, `UPDATE products SET title=CASE WHEN id IN ('attribute','tie-a') THEN 'filtered candidates' ELSE title END,attributes=CASE WHEN id='tie-a' THEN '{"server":"match"}'::jsonb ELSE attributes END,recommendation_boost=CASE WHEN id='tie-a' THEN 30 ELSE 0 END,recommendation_version=recommendation_version+1`)
	weights["server"] = map[string]float64{"match": 1}
	got = boostOrderIDs(t, a, catalog.SearchInput{Query: "filtered candidates", Sort: "recommended", Weights: weights})
	if strings.Join(got, ",") != "tie-a,attribute" {
		t.Fatal("filtered candidate fallback did not use attributes 25+30 over 50", got)
	}
	// With no ItemCF anywhere, the attribute formula and natural heat tie order remain.
	weights["itemcf"] = map[string]float64{}
	boostExec(t, a, `UPDATE products SET recommendation_boost=25,recommendation_version=recommendation_version+1 WHERE id='tie-a'`)
	got = boostOrderIDs(t, a, catalog.SearchInput{Query: "filtered candidates", Sort: "recommended", Weights: weights})
	if strings.Join(got, ",") != "attribute,tie-a" {
		t.Fatal("combined attribute tie lost original natural attribute ordering", got)
	}
}

func TestRecommendationBoostColdStartOptOutAndHomeSections(t *testing.T) {
	a, h, one, _ := personalFixture(t)
	boostExec(t, a, `UPDATE products SET recommendation_boost=100,recommendation_version=recommendation_version+1 WHERE id='sale-old'`)
	for _, cookie := range []*http.Cookie{nil, one} {
		if got := personalList(t, h, cookie, "recommended"); got[0] != "sale-old" {
			t.Fatal("cold-start failed to add operational score", got)
		}
	}
	h.call("POST", "/api/v1/products/rent-old/view", nil, one, 200)
	h.call("PATCH", "/api/v1/recommendations/profile", map[string]bool{"enabled": false}, one, 200)
	if got := personalList(t, h, one, "recommended"); got[0] != "sale-old" {
		t.Fatal("opt-out ignored boosted natural hot order", got)
	}
	home := memberData(t, h.call("GET", "/api/v1/home", nil, one, 200))
	for _, section := range []string{"hot", "recommended"} {
		rows, ok := home[section].([]any)
		if !ok || len(rows) == 0 || rows[0].(map[string]any)["id"] != "sale-old" {
			t.Fatal("home section differs from catalog", section, home)
		}
	}
}

func TestRecommendationBoostBoundedCandidateQueryCost(t *testing.T) {
	a := integrationMemberApp(t)
	recommendationCategory(t, a, "boost-scale", true)
	// Measure both the present catalog size and a larger synthetic fixture in an
	// isolated schema. Wall times are evidence, not a production capacity claim.
	for _, size := range []int{576, 6000} {
		boostExec(t, a, `INSERT INTO products(id,product_code,category_id,title,trade_type,price_unit,price_amount,status,source,version,approved_version,published_at,attributes,recommendation_boost)
 SELECT 'scale-'||n,'S'||n,'boost-scale','scale product '||n,'sale','account',n,'published','platform',1,1,now()-n*interval '1 minute','{"server":"fixture-region"}',n%101 FROM generate_series(1,$1::int) n ON CONFLICT(id) DO NOTHING`, size)
		weights := map[string]map[string]float64{"itemcf": {}, "category": {"boost-scale": 1}, "server": {"fixture-region": 1}}
		for i := 1; i <= 50; i++ {
			weights["itemcf"][fmt.Sprintf("scale-%d", i)] = float64(i) / 50
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		start := time.Now()
		rows, total, err := (&postgres.CatalogStore{DB: a.DB}).Search(ctx, catalog.SearchInput{Sort: "recommended", Weights: weights, Limit: 24, Offset: size / 2})
		elapsed := time.Since(start)
		cancel()
		if err != nil || total != size || len(rows) != 24 {
			t.Fatalf("%d candidates: rows=%d total=%d err=%v", size, len(rows), total, err)
		}
		t.Logf("R23 personalized full card query: candidates=%d offset=%d rows=%d elapsed=%s", size, size/2, len(rows), elapsed)
	}
}

func TestRecommendationBoostManagementReportMatchesCurrentHot(t *testing.T) {
	f := newChatFixture(t)
	for _, id := range []string{"report-fresh", "report-stale", "report-future", "report-no-snapshot"} {
		f.addProduct(t, id)
	}
	boostExec(t, f.a, `UPDATE products SET recommendation_boost=CASE id WHEN 'report-fresh' THEN 5 WHEN 'report-stale' THEN 40 WHEN 'report-future' THEN 20 WHEN 'report-no-snapshot' THEN 50 ELSE 0 END,recommendation_version=recommendation_version+1;
 INSERT INTO product_hot_scores(product_id,batch_id,score,computed_at) VALUES('report-fresh','fixture',100,now()),('report-stale','fixture',999,now()-interval '61 minutes'),('report-future','fixture',888,now()+interval '1 hour')`)
	raw := f.call(t, f.first, "GET", "/api/v1/admin/recommendations", nil, 200)
	var result struct {
		Data []map[string]any `json:"data"`
	}
	if err := json.Unmarshal(raw.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	ids := []string{}
	byID := map[string]map[string]any{}
	for _, row := range result.Data {
		id := row["product_id"].(string)
		ids = append(ids, id)
		byID[id] = row
	}
	hot := boostOrderIDs(t, f.a, catalog.SearchInput{Sort: "hot"})
	if !reflect.DeepEqual(ids, hot) {
		t.Fatal("management report diverges from public hot", ids, hot)
	}
	for _, test := range []struct {
		id                              string
		snapshot, natural, boost, total float64
		fresh                           bool
	}{
		{"report-fresh", 100, 100, 5, 105, true},
		{"report-stale", 999, 0, 40, 40, false},
		{"report-future", 888, 0, 20, 20, false},
		{"report-no-snapshot", 0, 0, 50, 50, false},
	} {
		row := byID[test.id]
		if row["score"] != test.snapshot || row["natural_score"] != test.natural || row["recommendation_boost"] != test.boost || row["hot_score"] != test.total || row["score_fresh"] != test.fresh {
			t.Fatal("report confuses natural batch and current total", test.id, row)
		}
	}
}
