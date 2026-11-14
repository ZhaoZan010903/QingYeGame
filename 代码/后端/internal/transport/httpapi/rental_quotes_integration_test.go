package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"qingye/backend/internal/domain/catalog"
	"sync"
	"testing"
)

func r212RentalFixture(t *testing.T) *chatFixture {
	t.Helper()
	f := newChatFixture(t)
	_, err := f.a.DB.Exec(context.Background(), `INSERT INTO categories(id,name,trade_type,price_units) VALUES('r212-rent','租赁测试','rent','["hour","day","week","month","period"]'); INSERT INTO product_dictionary_options(dictionary_key,value,label) VALUES('server','r212-server','测试区服')`)
	if err != nil {
		t.Fatal(err)
	}
	return f
}
func r212RentalInput() catalog.ProductInput {
	return catalog.ProductInput{CategoryID: "r212-rent", Title: "夜间出租测试", TradeType: "rent", PriceUnit: "unknown", Description: "完整租赁账号资料", RequestID: newID(), Attributes: map[string]any{"server": "r212-server", "gender": "female", "kind": "new-kind"}, RentalQuotes: []catalog.RentalQuote{
		{Unit: "period", Amount: "20", Label: "包夜"},
		{Unit: "period", Amount: "8", Label: "夜间", StartTime: "22:00", EndTime: "07:00", BillingUnit: "hour"},
		{Unit: "period", Amount: "140", Label: "早七晚七", StartTime: "07:00", EndTime: "19:00", BillingUnit: "week"},
		{Unit: "day", Amount: "25"},
	}}
}
func r212Version(p map[string]any) int { return int(p["version"].(float64)) }
func r212Anonymous(t *testing.T, f *chatFixture, path string, want int) *httptest.ResponseRecorder {
	t.Helper()
	w := surfaceRequest(t, f.pub, "GET", path, "", nil)
	statusIs(t, w, want)
	return w
}
func r212List(t *testing.T, w *httptest.ResponseRecorder) []map[string]any {
	t.Helper()
	var result struct {
		Data  []map[string]any `json:"data"`
		Total int              `json:"total"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if len(result.Data) != result.Total {
		t.Fatalf("unexpected list count: %s", w.Body.String())
	}
	return result.Data
}

func TestR212MemberClassificationAndHistoricalKind(t *testing.T) {
	f := r212RentalFixture(t)
	input := r212RentalInput()
	for _, classification := range []string{"hour_day", "week_month", ""} {
		input.RentalCategory = &classification
		f.call(t, f.member, "POST", "/api/v1/me/products", input, 403)
	}
	input.RentalCategory = nil
	p := memberData(t, f.call(t, f.member, "POST", "/api/v1/me/products", input, 200))
	id := p["id"].(string)
	if p["rental_category"] != "" || p["price_unit"] != "period" || p["price_amount"] != "20.00" {
		t.Fatalf("unexpected primary/category: %#v", p)
	}
	if _, ok := p["attributes"].(map[string]any)["kind"]; ok {
		t.Fatal("new removed kind was persisted")
	}
	quotes := p["rental_quotes"].([]any)
	if len(quotes) != 4 || quotes[0].(map[string]any)["billing_unit"] != "period" || quotes[1].(map[string]any)["start_time"] != "22:00" || quotes[2].(map[string]any)["billing_unit"] != "week" {
		t.Fatal("quote fields did not roundtrip", quotes)
	}
	if _, err := f.a.DB.Exec(context.Background(), `UPDATE products SET attributes=attributes||'{"kind":"historical-code"}'::jsonb,rental_category='week_month' WHERE id=$1`, id); err != nil {
		t.Fatal(err)
	}
	input.ExpectedVersion = r212Version(p)
	input.Attributes["kind"] = "overwrite"
	p = memberData(t, f.call(t, f.member, "PATCH", "/api/v1/me/products/"+id, input, 200))
	if p["rental_category"] != "week_month" || p["attributes"].(map[string]any)["kind"] != "historical-code" {
		t.Fatal("member edit overwrote protected historical/classification fields")
	}
	input.ExpectedVersion = r212Version(p)
	overwrite := "hour_day"
	input.RentalCategory = &overwrite
	f.call(t, f.member, "PATCH", "/api/v1/me/products/"+id, input, 403)
	input.RentalCategory = nil
	delete(input.Attributes, "kind")
	p = memberData(t, f.call(t, f.first, "PATCH", "/api/v1/admin/products/"+id, input, 200))
	if p["rental_category"] != "week_month" || p["attributes"].(map[string]any)["kind"] != "historical-code" {
		t.Fatal("admin omission erased retained fields")
	}
	input.ExpectedVersion = r212Version(p)
	input.RentalQuotes[1].EndTime = "22:00"
	f.call(t, f.first, "PATCH", "/api/v1/admin/products/"+id, input, 422)
}

func TestR212ApprovalClassificationIsAtomic(t *testing.T) {
	f := r212RentalFixture(t)
	input := r212RentalInput()
	p := memberData(t, f.call(t, f.member, "POST", "/api/v1/me/products", input, 200))
	id := p["id"].(string)
	mid := f.image(t, f.member, "product", "")
	p = memberData(t, f.call(t, f.member, "POST", "/api/v1/me/products/"+id+"/images", map[string]any{"expected_version": r212Version(p), "request_id": newID(), "media_ids": []string{mid}}, 200))
	p = memberData(t, f.call(t, f.member, "POST", "/api/v1/me/products/"+id+"/submit", map[string]any{"expected_version": r212Version(p)}, 200))
	version := r212Version(p)
	path := "/api/v1/admin/products/" + id + "/review"
	f.call(t, f.first, "POST", path, map[string]any{"expected_version": version, "decision": "approve"}, 422)
	f.call(t, f.first, "POST", path, map[string]any{"expected_version": version, "decision": "approve", "rental_category": "unknown"}, 422)
	var wg sync.WaitGroup
	results := make(chan *httptest.ResponseRecorder, 2)
	for i, actor := range []string{f.first, f.second} {
		wg.Add(1)
		go func(actor, category string) {
			defer wg.Done()
			results <- surfaceRequest(t, f.adm, "POST", path, "http://qingye-admin.test", map[string]any{"expected_version": version, "decision": "approve", "rental_category": category}, f.cookies[actor])
		}(actor, []string{"hour_day", "week_month"}[i])
	}
	wg.Wait()
	close(results)
	passed, conflicts := 0, 0
	for result := range results {
		switch result.Code {
		case 200:
			passed++
		case 409:
			conflicts++
		default:
			t.Fatalf("approval status %d: %s", result.Code, result.Body.String())
		}
	}
	if passed != 1 || conflicts != 1 {
		t.Fatalf("approval concurrency: passed=%d conflicts=%d", passed, conflicts)
	}
	var category, snapshotCategory, auditCategory string
	var current, approved, reviews int
	err := f.a.DB.QueryRow(context.Background(), `SELECT rental_category,version,approved_version,(SELECT count(*) FROM product_reviews WHERE product_id=p.id AND decision='published'),(SELECT snapshot->>'rental_category' FROM product_reviews WHERE product_id=p.id AND decision='published'),(SELECT detail->>'rental_category' FROM admin_logs WHERE resource_id=p.id AND action='product.review') FROM products p WHERE id=$1`, id).Scan(&category, &current, &approved, &reviews, &snapshotCategory, &auditCategory)
	if err != nil {
		t.Fatal(err)
	}
	if category == "" || snapshotCategory != category || auditCategory != category || reviews != 1 || current != version+1 || approved != current {
		t.Fatal("approval snapshot, classification and audit were not atomic")
	}
	public := memberData(t, r212Anonymous(t, f, "/api/v1/products/"+id, 200))
	if public["rental_category"] != category || len(public["rental_quotes"].([]any)) != 4 {
		t.Fatal("public projection lost classification or quotes")
	}
	// Existing classification is retained when an administrator republishes without a new choice.
	p = memberData(t, f.call(t, f.first, "POST", "/api/v1/admin/products/"+id+"/status", map[string]any{"expected_version": current, "action": "offline", "reason": "复测"}, 200))
	p = memberData(t, f.call(t, f.first, "POST", "/api/v1/admin/products/"+id+"/publish", map[string]any{"expected_version": r212Version(p)}, 200))
	if p["rental_category"] != category {
		t.Fatal("publication omission lost classification")
	}
}

func TestR212AdminDraftAndPublicationClassification(t *testing.T) {
	f := r212RentalFixture(t)
	input := r212RentalInput()
	empty := ""
	input.RentalCategory = &empty
	p := memberData(t, f.call(t, f.first, "POST", "/api/v1/admin/products", input, 200))
	id := p["id"].(string)
	mid := f.image(t, f.first, "product", "")
	p = memberData(t, f.call(t, f.first, "POST", "/api/v1/admin/products/"+id+"/images", map[string]any{"expected_version": r212Version(p), "request_id": newID(), "media_ids": []string{mid}}, 200))
	f.call(t, f.first, "POST", "/api/v1/admin/products/"+id+"/publish", map[string]any{"expected_version": r212Version(p)}, 422)
	p = memberData(t, f.call(t, f.first, "POST", "/api/v1/admin/products/"+id+"/publish", map[string]any{"expected_version": r212Version(p), "rental_category": "week_month"}, 200))
	if p["rental_category"] != "week_month" {
		t.Fatal("publication category not saved")
	}
	list := r212List(t, f.call(t, f.first, "GET", "/api/v1/admin/products?rental_category=week_month", nil, 200))
	if len(list) != 1 || list[0]["id"] != id {
		t.Fatal("admin category filter mismatch")
	}
	f.call(t, f.first, "GET", "/api/v1/admin/products?rental_category=unknown", nil, 422)
	f.call(t, f.first, "POST", "/api/v1/admin/products/"+f.product+"/status", map[string]any{"expected_version": 1, "action": "offline", "reason": "出售分类验证", "rental_category": "hour_day"}, 422)
	f.call(t, f.first, "POST", "/api/v1/admin/categories", map[string]any{"code": "r212-period", "name": "时段类别", "trade_type": "rent", "price_units": []string{"period"}, "enabled": true}, 200)
	f.call(t, f.first, "POST", "/api/v1/admin/categories", map[string]any{"code": "r212-sale", "name": "出售类别", "trade_type": "sale", "price_units": []string{"period"}, "enabled": true}, 422)
}

func TestR212PeriodSearchUsesMinimumWithinSameBasis(t *testing.T) {
	f := r212RentalFixture(t)
	ctx := context.Background()
	fixtures := []struct {
		id, category string
		quotes       []catalog.RentalQuote
	}{
		{"r212-a", "hour_day", []catalog.RentalQuote{{Unit: "period", Amount: "90", Label: "长包"}, {Unit: "period", Amount: "40", Label: "短包", BillingUnit: "period"}, {Unit: "period", Amount: "8", Label: "夜间", StartTime: "22:00", EndTime: "07:00", BillingUnit: "hour"}, {Unit: "day", Amount: "25"}}},
		{"r212-b", "week_month", []catalog.RentalQuote{{Unit: "period", Amount: "60", Label: "白天"}}},
		{"r212-c", "hour_day", []catalog.RentalQuote{{Unit: "period", Amount: "5", Label: "夜间", BillingUnit: "hour"}}},
	}
	for _, fixture := range fixtures {
		_, err := f.a.DB.Exec(ctx, `INSERT INTO products(id,product_code,category_id,title,trade_type,price_unit,price_amount,status,source,version,approved_version,rental_category,rental_quotes) VALUES($1,$1,'r212-rent',$1,'rent','period',$2,'published','platform',1,1,$3,$4)`, fixture.id, fixture.quotes[0].Amount, fixture.category, fixture.quotes)
		if err != nil {
			t.Fatal(err)
		}
	}
	cases := []struct {
		query       string
		ids, prices []string
	}{
		{"price_unit=period&sort=price_asc", []string{"r212-a", "r212-b"}, []string{"40", "60"}},
		{"price_unit=period&sort=price_desc", []string{"r212-b", "r212-a"}, []string{"60", "40"}},
		{"price_unit=period&max_price=50&sort=price_asc", []string{"r212-a"}, []string{"40"}},
		{"price_unit=period&min_price=45&sort=price_asc", []string{"r212-b"}, []string{"60"}},
		{"price_unit=period&period_basis=hour&sort=price_asc", []string{"r212-c", "r212-a"}, []string{"5", "8"}},
		{"price_unit=day&sort=price_asc", []string{"r212-a"}, []string{"25"}},
		{"rental_category=week_month&price_unit=period&sort=price_asc", []string{"r212-b"}, []string{"60"}},
	}
	for _, test := range cases {
		list := r212List(t, r212Anonymous(t, f, "/api/v1/products?"+test.query, 200))
		if len(list) != len(test.ids) {
			t.Fatalf("%s returned %d rows", test.query, len(list))
		}
		for i, row := range list {
			if row["id"] != test.ids[i] || row["price_amount"] != test.prices[i] {
				t.Fatalf("%s unexpected result %v", test.query, row)
			}
		}
		for _, row := range list {
			if row["id"] == "r212-a" && len(row["rental_quotes"].([]any)) != 4 {
				t.Fatal("search pruned other prices")
			}
		}
	}
	list := r212List(t, r212Anonymous(t, f, "/api/v1/products?rental_category=hour_day", 200))
	if len(list) != 2 {
		t.Fatal("category incorrectly tied to primary billing unit")
	}
	for _, query := range []string{"rental_category=invalid", "rental_category=hour_day&trade_type=sale", "price_unit=period&period_basis=account", "price_unit=day&period_basis=hour", "price_unit=bogus"} {
		r212Anonymous(t, f, "/api/v1/products?"+query, 422)
	}
}

func TestR212ChatSnapshotRetainsPeriodConditions(t *testing.T) {
	f := r212RentalFixture(t)
	ctx := context.Background()
	quotes := []catalog.RentalQuote{
		{Unit: "period", Amount: "8", Label: "夜间", StartTime: "22:00", EndTime: "07:00", BillingUnit: "hour"},
		{Unit: "period", Amount: "20", Label: "包夜", BillingUnit: "period"},
		{Unit: "day", Amount: "25"},
	}
	if _, err := f.a.DB.Exec(ctx, `UPDATE products SET category_id='r212-rent',trade_type='rent',price_unit='period',price_amount=8,rental_category='hour_day',rental_quotes=$2 WHERE id=$1`, f.product, quotes); err != nil {
		t.Fatal(err)
	}
	f.online(t, f.first, true)
	id := f.start(t, f.member, f.product)
	check := func(snapshot map[string]any) {
		t.Helper()
		if snapshot["rental_category"] != "hour_day" || snapshot["unit"] != "period" {
			t.Fatal("chat lost rental classification", snapshot)
		}
		rows, ok := snapshot["rental_quotes"].([]any)
		if !ok || len(rows) != 3 {
			t.Fatal("chat lost complete quote list", snapshot)
		}
		primary := rows[0].(map[string]any)
		if primary["amount"] != "8" || primary["billing_unit"] != "hour" || primary["label"] != "夜间" || primary["start_time"] != "22:00" || primary["end_time"] != "07:00" {
			t.Fatal("chat lost the primary period's rate or conditions", primary)
		}
	}
	for _, actor := range []string{f.member, f.first} {
		check(f.detail(t, actor, id)["product"].(map[string]any))
		prefix := "/api/v1/chat"
		if actor == f.first {
			prefix = "/api/v1/admin/chat"
		}
		page := f.call(t, actor, "GET", prefix+"/conversations/"+id+"/messages", nil, 200)
		var result struct {
			Data []struct {
				Product map[string]any `json:"product"`
			} `json:"data"`
		}
		if err := json.Unmarshal(page.Body.Bytes(), &result); err != nil || len(result.Data) != 1 {
			t.Fatal("missing chat product card", page.Body.String(), err)
		}
		check(result.Data[0].Product)
	}
	// Editing the live product must not rewrite historical chat message snapshots.
	quotes[0].Amount = "99"
	if _, err := f.a.DB.Exec(ctx, `UPDATE products SET price_amount=99,rental_quotes=$2 WHERE id=$1`, f.product, quotes); err != nil {
		t.Fatal(err)
	}
	var historical map[string]any
	if err := f.a.DB.QueryRow(ctx, `SELECT product_snapshot FROM chat_messages WHERE conversation_id=$1 AND kind='product'`, id).Scan(&historical); err != nil {
		t.Fatal(err)
	}
	check(historical)
}

func TestR212LegacyCategoryMigrationPreservesQuotesAndReviewHistory(t *testing.T) {
	a := integrationMemberApp(t)
	ctx := context.Background()
	// This rollback and rerun affects only integrationMemberApp's private schema.
	_, err := a.DB.Exec(ctx, `ALTER TABLE products DROP COLUMN rental_category CASCADE; ALTER TABLE products DROP CONSTRAINT products_price_unit_check; ALTER TABLE products ADD CONSTRAINT products_price_unit_check CHECK(price_unit IN ('unknown','account','hour','day','week','month')); DELETE FROM schema_migrations WHERE version='0017_rental_quote_periods.sql'; DELETE FROM admin_logs WHERE id='migration:0017_rental_quote_periods'; INSERT INTO categories(id,name,trade_type,price_units) VALUES('r212-old','旧租赁','rent','["day"]'),('r212-unrestricted','不限','rent','[]'),('r212-sale','出售','sale','["account"]');
 INSERT INTO products(id,product_code,category_id,title,trade_type,price_unit,price_amount,source,legacy_category,status,version,approved_version,rental_quotes) VALUES
 ('r212-short','r212-short','r212-old','旧时日','rent','day',25,'legacy',' 时日租号 ','published',2,2,'[{"unit":"day","amount":"25"}]'),
 ('r212-long','r212-long','r212-old','旧周月','rent','day',50,'legacy','周月租号','draft',4,2,'[{"unit":"day","amount":"50"}]'),
 ('r212-manual','r212-manual','r212-old','手工','rent','day',60,'platform','时日租号','published',8,8,'[{"unit":"day","amount":"60"}]'),
 ('r212-unknown','r212-unknown','r212-old','未知','rent','day',70,'legacy','夜间时日租号','published',2,2,'[]'),
 ('r212-deleted','r212-deleted','r212-old','删除','rent','day',80,'legacy','周月租号','draft',2,NULL,'[]');
 UPDATE products SET deleted_at=now() WHERE id='r212-deleted';
 INSERT INTO product_reviews(id,product_id,decision,version,snapshot) VALUES('r212-review','r212-short','published',2,'{"title":"历史快照","price_amount":"25"}');`)
	if err != nil {
		t.Fatal(err)
	}
	var beforeQuotes, beforeSnapshot string
	if err = a.DB.QueryRow(ctx, `SELECT jsonb_agg(jsonb_build_array(id,price_amount,price_unit,rental_quotes) ORDER BY id)::text FROM products WHERE id LIKE 'r212-%'`).Scan(&beforeQuotes); err != nil {
		t.Fatal(err)
	}
	if err = a.DB.QueryRow(ctx, `SELECT snapshot::text FROM product_reviews WHERE id='r212-review'`).Scan(&beforeSnapshot); err != nil {
		t.Fatal(err)
	}
	for run := 0; run < 2; run++ {
		if err = a.Migrate(ctx); err != nil {
			t.Fatal(err)
		}
		for _, test := range []struct {
			id, category string
			version      int
		}{{"r212-short", "hour_day", 3}, {"r212-long", "week_month", 5}, {"r212-manual", "", 8}, {"r212-unknown", "", 2}, {"r212-deleted", "", 2}} {
			var category string
			var version int
			if err = a.DB.QueryRow(ctx, `SELECT rental_category,version FROM products WHERE id=$1`, test.id).Scan(&category, &version); err != nil {
				t.Fatal(err)
			}
			if category != test.category || version != test.version {
				t.Fatalf("migration run %d changed %s unexpectedly: %s v%d", run, test.id, category, version)
			}
		}
	}
	var afterQuotes, afterSnapshot string
	var approved, oldApproved, changed int
	if err = a.DB.QueryRow(ctx, `SELECT jsonb_agg(jsonb_build_array(id,price_amount,price_unit,rental_quotes) ORDER BY id)::text FROM products WHERE id LIKE 'r212-%'`).Scan(&afterQuotes); err != nil {
		t.Fatal(err)
	}
	if err = a.DB.QueryRow(ctx, `SELECT snapshot::text FROM product_reviews WHERE id='r212-review'`).Scan(&afterSnapshot); err != nil {
		t.Fatal(err)
	}
	if beforeQuotes != afterQuotes || beforeSnapshot != afterSnapshot {
		t.Fatal("migration changed quotes or review history")
	}
	if err = a.DB.QueryRow(ctx, `SELECT (SELECT approved_version FROM products WHERE id='r212-short'),(SELECT approved_version FROM products WHERE id='r212-long'),(detail->>'updated_products')::int FROM admin_logs WHERE id='migration:0017_rental_quote_periods'`).Scan(&approved, &oldApproved, &changed); err != nil {
		t.Fatal(err)
	}
	if approved != 3 || oldApproved != 2 || changed != 2 {
		t.Fatal("migration approval/version evidence invalid")
	}
	for id, want := range map[string]string{"r212-old": "[day period]", "r212-unrestricted": "[]", "r212-sale": "[account]"} {
		var units []string
		if err = a.DB.QueryRow(ctx, `SELECT price_units FROM categories WHERE id=$1`, id).Scan(&units); err != nil {
			t.Fatal(err)
		}
		if fmt.Sprint(units) != want {
			t.Fatal("migration narrowed or expanded the wrong category", id, units)
		}
	}
}
