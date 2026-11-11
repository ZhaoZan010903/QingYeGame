package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"
)

const legacyPriceMigration = "0001_confirm_legacy_price_units.sql"

func TestConfirmedLegacyPriceBoundaries(t *testing.T) {
	for _, category := range legacyCategories {
		for _, original := range []string{"0", "12", "12.30", "9999999999.99"} {
			unit, amount := confirmedLegacyPrice(&category, original, category.Units)
			if unit != category.PriceUnit || amount == nil || *amount != original {
				t.Fatalf("valid source price changed: unit=%s amount=%v", unit, amount)
			}
		}
		for _, original := range []string{"", "-1", "+1", "1e3", "1.234", "10000000000", " 12 ", "NaN", "联系客服"} {
			unit, amount := confirmedLegacyPrice(&category, original, category.Units)
			if unit != "unknown" || amount != nil {
				t.Fatalf("unsafe source price became comparable: %q", original)
			}
		}
	}
	if unit, amount := confirmedLegacyPrice(nil, "100", nil); unit != "unknown" || amount != nil {
		t.Fatal("unknown source category was inferred")
	}
	if unit, amount := confirmedLegacyPrice(&legacyCategories[1], "100", []string{"hour"}); unit != "unknown" || amount != nil {
		t.Fatal("confirmed rule overrode an exclusive operator configuration")
	}
	if unit, amount := confirmedLegacyPrice(&legacyCategories[1], "100", nil); unit != "day" || amount == nil {
		t.Fatal("unrestricted category rejected a confirmed rule")
	}
}

func prepareOldPricingSchema(t *testing.T, a *App) {
	t.Helper()
	ctx := context.Background()
	// Each integration app has its own disposable schema. Simulate a database
	// from before the new ordered migration, without touching any business data.
	if _, err := a.DB.Exec(ctx, `DELETE FROM schema_migrations WHERE version='0001_confirm_legacy_price_units.sql';
DELETE FROM admin_logs WHERE id='migration:0001_confirm_legacy_price_units';
INSERT INTO categories(id,name,trade_type,price_units) VALUES
 ('legacy-sale','QQ炫舞买号','sale','["account","unknown"]'),
 ('legacy-short-rent','时日租号','rent','["hour","day","unknown"]'),
 ('legacy-long-rent','周月租号','rent','["week","month","unknown"]'),
 ('custom-pricing','其他分类','all','["account","day","month"]');`); err != nil {
		t.Fatal(err)
	}
}

func TestLegacyPriceMigrationPreservesHistoryAndPublication(t *testing.T) {
	a := integrationMemberApp(t)
	prepareOldPricingSchema(t, a)
	ctx := context.Background()
	type priceCase struct {
		id, category, sourceCategory, trade, source, unit, original, status string
		amount                                                              *string
		version                                                             int
		approved                                                            *int
		wantUnit, wantAmount                                                string
		mapped                                                              bool
	}
	approved := 7
	base := priceCase{category: "legacy-sale", sourceCategory: "QQ炫舞买号", trade: "sale", source: "legacy", unit: "unknown", original: "12.30", status: "published", version: 7, approved: &approved}
	cases := []priceCase{}
	add := func(id string, change func(*priceCase)) {
		p := base
		p.id = id
		change(&p)
		cases = append(cases, p)
	}
	add("sale", func(p *priceCase) { p.wantUnit, p.wantAmount, p.mapped = "account", "12.30", true })
	add("day-pending", func(p *priceCase) {
		p.category, p.sourceCategory, p.trade, p.status, p.approved = "legacy-short-rent", "时日租号", "rent", "pending", nil
		p.wantUnit, p.wantAmount, p.mapped = "day", "12.30", true
	})
	add("month-offline", func(p *priceCase) {
		p.category, p.sourceCategory, p.trade, p.status, p.approved = "legacy-long-rent", "周月租号", "rent", "offline", nil
		p.wantUnit, p.wantAmount, p.mapped = "month", "12.30", true
	})
	for index, value := range []string{"", "-1", "1.234", "1e3", "10000000000", "联系客服"} {
		add(fmt.Sprintf("invalid-%d", index), func(p *priceCase) { p.original = value })
	}
	add("manual-unit", func(p *priceCase) { p.unit = "account" })
	manualAmount := "9.50"
	add("manual-amount", func(p *priceCase) { p.amount = &manualAmount })
	add("manual-confirmed", func(p *priceCase) { p.unit, p.amount = "account", &manualAmount })
	add("reclassified", func(p *priceCase) { p.category = "custom-pricing" })
	add("unknown-source-category", func(p *priceCase) { p.sourceCategory = "账号出售待核对" })
	add("missing-source-category", func(p *priceCase) { p.sourceCategory = "" })
	add("different-trade", func(p *priceCase) { p.trade = "rent" })
	add("platform-product", func(p *priceCase) { p.source = "platform" })
	for _, p := range cases {
		_, err := a.DB.Exec(ctx, `INSERT INTO products(id,legacy_id,product_code,category_id,title,trade_type,source,price_unit,price_amount,legacy_price,legacy_category,legacy_payload,status,version,approved_version,published_at)
VALUES($1,$1,$1,$2,$1,$3,$4,$5,$6,$7,$8,'{"original":"unchanged"}',$9,$10,$11,'2026-01-02T03:04:05Z')`,
			p.id, p.category, p.trade, p.source, p.unit, p.amount, p.original, p.sourceCategory, p.status, p.version, p.approved)
		if err != nil {
			t.Fatal(err)
		}
	}
	if _, err := a.DB.Exec(ctx, `INSERT INTO product_reviews(id,product_id,decision,version,snapshot)
VALUES('old-review','sale','published',7,'{"price_unit":"unknown","price_amount":null,"version":7}')`); err != nil {
		t.Fatal(err)
	}
	if err := a.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	for _, p := range cases {
		var unit, original, category, source, status string
		var amount *string
		var version int
		var approved *int
		var published time.Time
		var raw map[string]any
		err := a.DB.QueryRow(ctx, `SELECT price_unit,price_amount::text,legacy_price,legacy_category,source,status,version,approved_version,published_at,legacy_payload FROM products WHERE id=$1`, p.id).
			Scan(&unit, &amount, &original, &category, &source, &status, &version, &approved, &published, &raw)
		if err != nil {
			t.Fatal(err)
		}
		if original != p.original || category != p.sourceCategory || source != p.source || status != p.status || raw["original"] != "unchanged" || published.UTC().Format(time.RFC3339) != "2026-01-02T03:04:05Z" {
			t.Fatalf("%s: source/history/publication was changed", p.id)
		}
		if p.mapped {
			if unit != p.wantUnit || amount == nil || *amount != p.wantAmount || version != p.version+1 {
				t.Fatalf("%s: confirmed quote/version mismatch: %s %v v%d", p.id, unit, amount, version)
			}
			if p.approved == nil && approved != nil || p.approved != nil && (approved == nil || *approved != *p.approved+1) {
				t.Fatalf("%s: approval did not follow the previous approval state", p.id)
			}
		} else if unit != p.unit || version != p.version || amount == nil && p.amount != nil || amount != nil && (p.amount == nil || *amount != *p.amount) {
			t.Fatalf("%s: unmapped or operator-owned quote overwritten", p.id)
		}
	}
	h := memberHTTP{t: t, handler: a.Routes(), ip: "192.0.2.91"}
	h.call("GET", "/api/v1/products/sale", nil, nil, 200)
	h.call("GET", "/api/v1/products/sale/images?expected_version=7", nil, nil, 409)
	h.call("GET", "/api/v1/products/sale/images?expected_version=8", nil, nil, 200)
	for _, category := range legacyCategories {
		var units []string
		if err := a.DB.QueryRow(ctx, "SELECT price_units FROM categories WHERE id=$1", category.ID).Scan(&units); err != nil || len(units) != 1 || units[0] != category.PriceUnit {
			t.Fatalf("category units not updated: %s %v %v", category.ID, units, err)
		}
	}
	var snapshot map[string]any
	if err := a.DB.QueryRow(ctx, "SELECT snapshot FROM product_reviews WHERE id='old-review'").Scan(&snapshot); err != nil || snapshot["price_unit"] != "unknown" || snapshot["version"] != float64(7) {
		t.Fatalf("immutable review changed: %v %v", snapshot, err)
	}
	// A later deliberate reset must not be overwritten by rerunning migrate.
	if _, err := a.DB.Exec(ctx, "UPDATE products SET price_unit='unknown',price_amount=NULL,version=9,approved_version=9 WHERE id='sale'"); err != nil {
		t.Fatal(err)
	}
	if err := a.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	var unit string
	var version, migrations, audited int
	if err := a.DB.QueryRow(ctx, "SELECT price_unit,version FROM products WHERE id='sale'").Scan(&unit, &version); err != nil || unit != "unknown" || version != 9 {
		t.Fatalf("migration replay overwrote an operator change: %s %d %v", unit, version, err)
	}
	if err := a.DB.QueryRow(ctx, "SELECT count(*) FROM schema_migrations WHERE version=$1", legacyPriceMigration).Scan(&migrations); err != nil || migrations != 1 {
		t.Fatalf("migration marker invalid: %d %v", migrations, err)
	}
	if err := a.DB.QueryRow(ctx, "SELECT (detail->>'updated_products')::int FROM admin_logs WHERE id='migration:0001_confirm_legacy_price_units'").Scan(&audited); err != nil || audited != 3 {
		t.Fatalf("migration audit invalid: %d %v", audited, err)
	}
}

func TestLegacyPricingPreservesCustomCategoryConfiguration(t *testing.T) {
	a := integrationMemberApp(t)
	prepareOldPricingSchema(t, a)
	ctx := context.Background()
	if _, err := a.DB.Exec(ctx, `UPDATE categories SET price_units='["hour"]' WHERE id='legacy-short-rent';
INSERT INTO products(id,product_code,category_id,legacy_category,trade_type,price_unit,legacy_price,source)
VALUES('exclusive-configuration','exclusive-configuration','legacy-short-rent','时日租号','rent','unknown','50','legacy');
INSERT INTO products(id,product_code,category_id,trade_type,price_unit,price_amount,source)
VALUES('manual-week','manual-week','legacy-long-rent','rent','week',99,'legacy');`); err != nil {
		t.Fatal(err)
	}
	if err := a.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	for id, want := range map[string]string{"legacy-short-rent": `["hour"]`, "legacy-long-rent": `["week","month","unknown"]`} {
		var raw json.RawMessage
		if err := a.DB.QueryRow(ctx, "SELECT price_units FROM categories WHERE id=$1", id).Scan(&raw); err != nil {
			t.Fatal(err)
		}
		if strings.ReplaceAll(string(raw), " ", "") != want {
			t.Fatalf("operator unit configuration overwritten: %s %s", id, raw)
		}
	}
	var unit string
	var amount *string
	if err := a.DB.QueryRow(ctx, "SELECT price_unit,price_amount::text FROM products WHERE id='exclusive-configuration'").Scan(&unit, &amount); err != nil || unit != "unknown" || amount != nil {
		t.Fatalf("migration ignored exclusive category configuration: %s %v %v", unit, amount, err)
	}
}

func TestLegacyImportRespectsExclusiveCategoryUnits(t *testing.T) {
	a := integrationMemberApp(t)
	prepareOldPricingSchema(t, a)
	ctx := context.Background()
	if _, err := a.DB.Exec(ctx, `UPDATE categories SET price_units='["hour"]' WHERE id='legacy-short-rent'`); err != nil {
		t.Fatal(err)
	}
	raw := `[{"_id":"exclusive-import","price":50,"categorys_name":"时日租号","status":"出售中","title":"自定义分类不被覆盖"}]`
	if count, err := a.ImportLegacy(ctx, strings.NewReader(raw)); err != nil || count != 1 {
		t.Fatalf("import %d %v", count, err)
	}
	var unit, original string
	var amount *string
	if err := a.DB.QueryRow(ctx, "SELECT price_unit,price_amount::text,legacy_price FROM products WHERE legacy_id='exclusive-import'").Scan(&unit, &amount, &original); err != nil || unit != "unknown" || amount != nil || original != "50" {
		t.Fatalf("import ignored exclusive category configuration: %s %v %s %v", unit, amount, original, err)
	}
	var allowed []string
	if err := a.DB.QueryRow(ctx, "SELECT price_units FROM categories WHERE id='legacy-short-rent'").Scan(&allowed); err != nil || len(allowed) != 1 || allowed[0] != "hour" {
		t.Fatalf("import overwrote operator units: %v %v", allowed, err)
	}
}

func TestLegacyImportsUseConfirmedUnitsAndDoNotOverwriteCorrections(t *testing.T) {
	a := integrationMemberApp(t)
	ctx := context.Background()
	records := []map[string]any{}
	for index, category := range legacyCategories {
		records = append(records, map[string]any{"_id": fmt.Sprintf("new-import-%d", index), "title": "新快照报价", "price": 200.50, "categorys_name": category.Name, "product_id": "PRICE-TEST", "status": "出售中"})
	}
	records = append(records, map[string]any{"_id": "invalid-import-price", "title": "不推断异常价", "price": 1.234, "categorys_name": "时日租号", "product_id": "PRICE-INVALID", "status": "出售中"})
	raw, _ := json.Marshal(records)
	if count, err := a.ImportLegacy(ctx, strings.NewReader(string(raw))); err != nil || count != 4 {
		t.Fatalf("import %d %v", count, err)
	}
	for index, category := range legacyCategories {
		id := deterministicID(fmt.Sprintf("legacy-product:new-import-%d", index))
		var unit, amount, original string
		var version int
		if err := a.DB.QueryRow(ctx, "SELECT price_unit,price_amount::text,legacy_price,version FROM products WHERE id=$1", id).Scan(&unit, &amount, &original, &version); err != nil || unit != category.PriceUnit || amount != "200.50" || original != "200.5" || version != 1 {
			t.Fatalf("new import mapping wrong: %s %s %s v%d %v", unit, amount, original, version, err)
		}
	}
	var unit string
	var amount *string
	if err := a.DB.QueryRow(ctx, "SELECT price_unit,price_amount::text FROM products WHERE legacy_id='invalid-import-price'").Scan(&unit, &amount); err != nil || unit != "unknown" || amount != nil {
		t.Fatalf("invalid price inferred: %s %v %v", unit, amount, err)
	}
	h := memberHTTP{t: t, handler: a.Routes(), ip: "192.0.2.92"}
	w := h.call("GET", "/api/v1/products?price_unit=day&sort=price_asc&min_price=199&max_price=201", nil, nil, 200)
	var response struct{ Total int }
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil || response.Total != 1 {
		t.Fatalf("confirmed day quote not comparable: total=%d %v", response.Total, err)
	}
	if _, err := a.DB.Exec(ctx, "UPDATE products SET price_amount=333,price_unit='month' WHERE legacy_id='new-import-2'"); err != nil {
		t.Fatal(err)
	}
	if count, err := a.ImportLegacy(ctx, strings.NewReader(string(raw))); err != nil || count != 0 {
		t.Fatalf("reimport: %d %v", count, err)
	}
	var confirmed string
	if err := a.DB.QueryRow(ctx, "SELECT price_amount::text FROM products WHERE legacy_id='new-import-2'").Scan(&confirmed); err != nil || confirmed != "333.00" {
		t.Fatalf("reimport overwrote corrected amount: %s %v", confirmed, err)
	}
	if _, err := a.ImportLegacy(ctx, strings.NewReader(`[{"_id":"unclassified","price":100,"categorys_name":"未确认分类"}]`)); err == nil {
		t.Fatal("unclassified source was silently mapped")
	}
}
