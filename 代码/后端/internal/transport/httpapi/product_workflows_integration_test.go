package httpapi

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"qingye/backend/internal/domain/catalog"
	"qingye/backend/internal/domain/management"
	"qingye/backend/internal/domain/member"
	"qingye/backend/internal/infrastructure/postgres"
	"strings"
	"sync"
	"testing"
)

func TestR15PrivateCredentialsAndRentalQuotes(t *testing.T) {
	a := integrationMemberApp(t)
	ctx := context.Background()
	a.Config.ProductCredentialKey = base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{9}, 32))
	_, err := a.DB.Exec(ctx, `INSERT INTO categories(id,name,trade_type,price_units) VALUES('r15-rent','出租','rent','["day"]')`)
	if err != nil {
		t.Fatal(err)
	}
	token := recommendationUser(t, a, "r15-member", "member")
	adminToken := recommendationUser(t, a, "r15-admin", "admin")
	h := memberHTTP{t: t, handler: a.Routes(), ip: "r15-user", origin: "http://qingye.test"}
	amount := "25"
	input := catalog.ProductInput{CategoryID: "r15-rent", Title: "租赁测试", TradeType: "rent", PriceUnit: "day", PriceAmount: &amount, Description: "完整资料", RequestID: newID(), Attributes: map[string]any{"custom_tags": []string{"稀有服饰"}}, RentalQuotes: []catalog.RentalQuote{{Unit: "day", Amount: "25"}, {Unit: "month", Amount: "350"}}, PrivateCredentials: &catalog.PrivateCredentials{QQAccount: "12345678", QQPassword: "private-fixture-secret"}}
	// Additional quote units must follow the same template as the primary quote.
	h.call("POST", "/api/v1/me/products", input, token, 422)
	if _, err = a.DB.Exec(ctx, `UPDATE categories SET price_units='["day","month"]' WHERE id='r15-rent'`); err != nil {
		t.Fatal(err)
	}
	response := h.call("POST", "/api/v1/me/products", input, token, 200)
	p := memberData(t, response)
	id := p["id"].(string)
	if strings.Contains(response.Body.String(), "private-fixture-secret") || strings.Contains(response.Body.String(), "12345678") || p["has_private_credentials"] != true {
		t.Fatal("private information escaped normal response")
	}
	if !strings.HasPrefix(p["product_code"].(string), "QYZH") {
		t.Fatal("incorrect automatic code")
	}
	replay := memberData(t, h.call("POST", "/api/v1/me/products", input, token, 200))
	if replay["id"] != id {
		t.Fatal("replay created duplicate")
	}
	var ciphertext []byte
	if err = a.DB.QueryRow(ctx, "SELECT ciphertext FROM product_private_credentials WHERE product_id=$1", id).Scan(&ciphertext); err != nil || bytes.Contains(ciphertext, []byte("private-fixture-secret")) {
		t.Fatal("credentials not encrypted")
	}
	admin := App{DB: a.DB, Redis: a.Redis, Config: a.Config}
	admin.Config.Surface = "admin"
	admin.Config.AllowedOrigins = []string{"http://qingye-admin.test"}
	ah := memberHTTP{t: t, handler: admin.Routes(), ip: "r15-admin", origin: "http://qingye-admin.test"}
	ah.call("GET", "/api/v1/admin/products/"+id+"/private-credentials", nil, token, 401)
	secret := ah.call("GET", "/api/v1/admin/products/"+id+"/private-credentials", nil, adminToken, 200)
	if !strings.Contains(secret.Body.String(), "private-fixture-secret") || secret.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("explicit admin access failed")
	}
	var audits int
	if err = a.DB.QueryRow(ctx, "SELECT count(*) FROM admin_logs WHERE action='product.credentials.read' AND resource_id=$1 AND detail='{}'::jsonb", id).Scan(&audits); err != nil || audits != 1 {
		t.Fatal("safe audit missing")
	}
	if _, err = a.DB.Exec(ctx, "UPDATE products SET status='published',approved_version=version WHERE id=$1", id); err != nil {
		t.Fatal(err)
	}
	public := h.call("GET", "/api/v1/products/"+id, nil, nil, 200)
	if strings.Contains(public.Body.String(), "has_private_credentials") || strings.Contains(public.Body.String(), "private-fixture-secret") {
		t.Fatal("public private fields")
	}
	search := h.call("GET", "/api/v1/products?price_unit=month&min_price=300&max_price=400&sort=price_asc", nil, nil, 200)
	if !strings.Contains(search.Body.String(), id) || !strings.Contains(search.Body.String(), `"price_amount":"350"`) {
		t.Fatalf("multi quote search failed: %s", search.Body.String())
	}
	input.PrivateCredentials = nil
	input.ExpectedVersion = int(p["version"].(float64))
	input.RequestID = ""
	// Narrowing a template preserves published history but blocks incompatible edits/reviews.
	if _, err = a.DB.Exec(ctx, `UPDATE categories SET price_units='["day"]' WHERE id='r15-rent'`); err != nil {
		t.Fatal(err)
	}
	h.call("GET", "/api/v1/products/"+id, nil, nil, 200)
	h.call("PATCH", "/api/v1/me/products/"+id, input, token, 422)
	ah.call("PATCH", "/api/v1/admin/products/"+id, input, adminToken, 422)
	if _, err = a.DB.Exec(ctx, "UPDATE products SET status='pending' WHERE id=$1", id); err != nil {
		t.Fatal(err)
	}
	ah.call("POST", "/api/v1/admin/products/"+id+"/review", map[string]any{"expected_version": input.ExpectedVersion, "decision": "approve"}, adminToken, 422)
	if _, err = a.DB.Exec(ctx, `UPDATE categories SET price_units='["day","month"]' WHERE id='r15-rent'`); err != nil {
		t.Fatal(err)
	}
	changedTrade := input
	changedTrade.TradeType = "sale"
	h.call("PATCH", "/api/v1/me/products/"+id, changedTrade, token, 422)
	ah.call("PATCH", "/api/v1/admin/products/"+id, changedTrade, adminToken, 422)
	h.call("PATCH", "/api/v1/me/products/"+id, input, token, 200)
	if _, err = a.DB.Exec(ctx, "UPDATE products SET status='offline' WHERE id=$1", id); err != nil {
		t.Fatal(err)
	}
	target, _, err := management.Transition("offline", "member", "publish", management.TransitionInput{})
	if err != nil || target != "published" {
		t.Fatal("member relisting blocked")
	}
}
func TestR15ConcurrentProductIdentifiers(t *testing.T) {
	a := integrationMemberApp(t)
	ctx := context.Background()
	_, err := a.DB.Exec(ctx, `INSERT INTO categories(id,name,trade_type,price_units) VALUES('r15-sale','出售','sale','["account"]')`)
	if err != nil {
		t.Fatal(err)
	}
	recommendationUser(t, a, "r15-number-member", "member")
	store := postgres.MemberStore{DB: a.DB, NewID: newID}
	var wg sync.WaitGroup
	results := make(chan string, 12)
	errs := make(chan error, 12)
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			out, err := store.Create(ctx, member.Actor{ID: "r15-number-member", Role: "member"}, catalog.ProductInput{CategoryID: "r15-sale", Title: "编号", TradeType: "sale", PriceUnit: "account", Description: "资料", RequestID: fmt.Sprintf("number-request-%d", n)})
			if err != nil {
				errs <- err
				return
			}
			results <- out["data"].(map[string]any)["product_code"].(string)
		}(i)
	}
	wg.Wait()
	close(results)
	close(errs)
	for err := range errs {
		t.Error(err)
	}
	seen := map[string]bool{}
	for code := range results {
		if seen[code] || !strings.HasPrefix(code, "QYCH") {
			t.Fatal("duplicate or incorrect identifier")
		}
		seen[code] = true
	}
	if len(seen) != 12 {
		t.Fatal("creation failed")
	}
}

func TestR15ConfigStatesAndManualSlides(t *testing.T) {
	a := integrationMemberApp(t)
	ctx := context.Background()
	adminToken := recommendationUser(t, a, "r15-config-admin", "admin")
	userToken := recommendationUser(t, a, "r15-config-user", "member")
	admin := App{DB: a.DB, Redis: a.Redis, Config: a.Config}
	admin.Config.Surface = "admin"
	admin.Config.AllowedOrigins = []string{"http://qingye-admin.test"}
	ah := memberHTTP{t: t, handler: admin.Routes(), ip: "r15-config-admin", origin: "http://qingye-admin.test"}
	h := memberHTTP{t: t, handler: a.Routes(), ip: "r15-config-user", origin: "http://qingye.test"}
	_, err := a.DB.Exec(ctx, `INSERT INTO categories(id,name,trade_type,price_units) VALUES('r15-config-sale','出售','sale','["account"]')`)
	if err != nil {
		t.Fatal(err)
	}
	form := management.DefaultPublishForm()
	tags := form.Fields["custom_tags"]
	tags.Required = true
	form.Fields["custom_tags"] = tags
	ah.call("PUT", "/api/v1/admin/settings/publish_form", map[string]any{"value": form}, adminToken, 200)
	settings := ah.call("GET", "/api/v1/admin/settings", nil, adminToken, 200)
	if !strings.Contains(settings.Body.String(), `"key":"publish_form"`) || !strings.Contains(settings.Body.String(), `"custom_tags"`) {
		t.Fatal("saved publish form missing from administrator settings")
	}
	h.call("GET", "/api/v1/publish-form", nil, nil, 200)
	input := catalog.ProductInput{CategoryID: "r15-config-sale", Title: "测试完整商品", TradeType: "sale", PriceUnit: "account", Description: "测试介绍", RequestID: newID()}
	p := memberData(t, h.call("POST", "/api/v1/me/products", input, userToken, 200))
	id := p["id"].(string)
	mid := newID()
	_, err = a.DB.Exec(ctx, `INSERT INTO media_files(id,status) VALUES($1,'ready')`, mid)
	if err != nil {
		t.Fatal(err)
	}
	_, err = a.DB.Exec(ctx, `INSERT INTO product_images(product_id,media_id,sort_order) VALUES($1,$2,1)`, id, mid)
	if err != nil {
		t.Fatal(err)
	}
	h.call("POST", "/api/v1/me/products/"+id+"/submit", map[string]any{"expected_version": 1}, userToken, 422)
	input.Attributes = map[string]any{"custom_tags": []string{"限定装扮"}}
	input.ExpectedVersion = 1
	h.call("PATCH", "/api/v1/me/products/"+id, input, userToken, 200)
	h.call("POST", "/api/v1/me/products/"+id+"/submit", map[string]any{"expected_version": 2}, userToken, 200)
	ah.call("POST", "/api/v1/admin/products/"+id+"/review", map[string]any{"expected_version": 3, "decision": "approve"}, adminToken, 200)
	ah.call("POST", "/api/v1/admin/products/"+id+"/status", map[string]any{"expected_version": 4, "action": "rented", "reason": "状态测试"}, adminToken, 422)
	ah.call("POST", "/api/v1/admin/products/"+id+"/status", map[string]any{"expected_version": 4, "action": "sold", "reason": "售出登记"}, adminToken, 200)
	h.call("GET", "/api/v1/products/"+id, nil, nil, 404)
	ah.call("POST", "/api/v1/admin/products/"+id+"/publish", map[string]any{"expected_version": 5}, adminToken, 200)
	cfg := map[string]any{"editorial": map[string]any{"title": "本期甄选", "description": "", "intervalMs": 6000}, "sections": []any{}, "shortcuts": []any{}, "publishing": map[string]any{"title": "发布", "description": "", "action": "发布账号"}, "slides": []map[string]any{{"productId": id, "title": "运营独立标题", "description": "独立介绍", "tradeLabel": "账号出售", "serverLabel": "华东一区", "priceLabel": "咨询报价", "imageId": mid, "imagePositionX": 50, "imagePositionY": 50, "imageFit": "contain", "layout": "image-right", "textAlign": "left", "verticalAlign": "center"}}}
	ah.call("PUT", "/api/v1/admin/settings/home_presentation", map[string]any{"value": cfg}, adminToken, 200)
	home := h.call("GET", "/api/v1/home", nil, nil, 200)
	if !strings.Contains(home.Body.String(), "运营独立标题") || !strings.Contains(home.Body.String(), mid) {
		t.Fatal("manual slide missing")
	}
	cfg["slides"].([]map[string]any)[0]["imageId"] = "another-product-image"
	ah.call("PUT", "/api/v1/admin/settings/home_presentation", map[string]any{"value": cfg}, adminToken, 422)
	cfg["slides"] = []any{}
	ah.call("PUT", "/api/v1/admin/settings/home_presentation", map[string]any{"value": cfg}, adminToken, 200)
	_, err = a.DB.Exec(ctx, "UPDATE products SET manual_rank=1 WHERE id=$1", id)
	if err != nil {
		t.Fatal(err)
	}
	home = h.call("GET", "/api/v1/home", nil, nil, 200)
	if !strings.Contains(home.Body.String(), `"featured":[]`) {
		t.Fatal("manual rank unexpectedly auto-filled carousel")
	}
}
func TestR15IdentifierAllocatorAfterLegacyImport(t *testing.T) {
	a := integrationMemberApp(t)
	ctx := context.Background()
	_, err := a.DB.Exec(ctx, `INSERT INTO categories(id,name) VALUES('legacy','旧分类');INSERT INTO products(id,product_code,category_id,trade_type,price_unit,source) VALUES('legacy-a','QYCH557','legacy','sale','account','legacy'),('legacy-b','QYCH557','legacy','sale','account','legacy'),('legacy-c','QYZH2835','legacy','rent','month','legacy')`)
	if err != nil {
		t.Fatal(err)
	}
	if err = a.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	recommendationUser(t, a, "r15-migration-user", "member")
	store := postgres.MemberStore{DB: a.DB, NewID: newID}
	for _, test := range []struct{ trade, unit, code string }{{"sale", "account", "QYCH558"}, {"rent", "month", "QYZH2836"}} {
		result, err := store.Create(ctx, member.Actor{ID: "r15-migration-user", Role: "member"}, catalog.ProductInput{CategoryID: "legacy", TradeType: test.trade, PriceUnit: test.unit, RequestID: newID()})
		if err != nil {
			t.Fatal(err)
		}
		if result["data"].(map[string]any)["product_code"] != test.code {
			t.Fatal("legacy sequence continuation failed")
		}
	}
	var count int
	if err = a.DB.QueryRow(ctx, "SELECT count(*) FROM products WHERE product_code='QYCH557'").Scan(&count); err != nil || count != 2 {
		t.Fatal("legacy duplicate codes changed")
	}
}
