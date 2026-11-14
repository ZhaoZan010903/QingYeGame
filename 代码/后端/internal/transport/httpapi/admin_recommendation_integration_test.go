package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"qingye/backend/internal/domain/catalog"
	"qingye/backend/internal/domain/fault"
	"qingye/backend/internal/domain/management"
	"qingye/backend/internal/infrastructure/postgres"
	"strings"
	"sync"
	"testing"
	"time"
)

func recommendationPatch(t *testing.T, f *chatFixture, id string, boost int, version int64, want int) map[string]any {
	t.Helper()
	w := f.call(t, f.first, "PATCH", "/api/v1/admin/products/"+id+"/recommendation", map[string]any{"boost": boost, "expected_version": version}, want)
	if w.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("recommendation response can be cached")
	}
	if want != 200 {
		return nil
	}
	return memberData(t, w)
}

func recommendationContent(t *testing.T, f *chatFixture, id string) string {
	t.Helper()
	var data string
	if err := f.a.DB.QueryRow(context.Background(), `SELECT (to_jsonb(p)-ARRAY['recommendation_boost','recommendation_version'])::text FROM products p WHERE id=$1`, id).Scan(&data); err != nil {
		t.Fatal(err)
	}
	return data
}

func noRecommendationFields(t *testing.T, raw string) {
	t.Helper()
	if strings.Contains(raw, "recommendation_boost") || strings.Contains(raw, "recommendation_version") {
		t.Fatal("operational recommendation fields escaped management projection", raw)
	}
}

func TestAdminRecommendationAtomicVersionAuditAndContent(t *testing.T) {
	f := newChatFixture(t)
	ctx := context.Background()
	purgeExec(t, f, `UPDATE products SET owner_id=$1,published_at=now()-interval '5 days',description='recommendation fixture' WHERE id=$2`, f.member, f.product)
	before := recommendationContent(t, f, f.product)
	result := recommendationPatch(t, f, f.product, 80, 1, 200)
	if result["recommendation_boost"] != float64(80) || result["recommendation_version"] != float64(2) || result["id"] != f.product {
		t.Fatal(result)
	}
	if recommendationContent(t, f, f.product) != before {
		t.Fatal("boost changed content, approval, state or timestamps")
	}
	recommendationPatch(t, f, f.product, 10, 1, 409)
	var audits int
	var detail map[string]any
	if err := f.a.DB.QueryRow(ctx, `SELECT count(*),max(detail::text)::jsonb FROM admin_logs WHERE action='product.recommendation' AND resource_id=$1`, f.product).Scan(&audits, &detail); err != nil || audits != 1 {
		t.Fatal(audits, err)
	}
	if detail["boost"] != float64(80) || detail["previous_boost"] != float64(0) || detail["version"] != float64(2) || detail["previous_version"] != float64(1) {
		t.Fatal("incomplete audit", detail)
	}
	var wg sync.WaitGroup
	codes := make(chan int, 16)
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func(value int) {
			defer wg.Done()
			w := surfaceRequest(t, f.adm, "PATCH", "/api/v1/admin/products/"+f.product+"/recommendation", "http://qingye-admin.test", map[string]any{"boost": value, "expected_version": 2}, f.cookies[f.first])
			codes <- w.Code
		}(i)
	}
	wg.Wait()
	close(codes)
	successes := 0
	for code := range codes {
		if code == 200 {
			successes++
		} else if code != 409 {
			t.Fatalf("concurrent write returned %d", code)
		}
	}
	if successes != 1 {
		t.Fatalf("concurrent successes %d", successes)
	}
	if err := f.a.DB.QueryRow(ctx, `SELECT count(*) FROM admin_logs WHERE action='product.recommendation' AND resource_id=$1`, f.product).Scan(&audits); err != nil || audits != 2 {
		t.Fatal("concurrent audit not atomic", audits, err)
	}
	if recommendationContent(t, f, f.product) != before {
		t.Fatal("concurrent boost changed product content")
	}
	recommendationPatch(t, f, f.product, 0, 3, 200)
}

func TestAdminRecommendationAccessValidationAndActorRecheck(t *testing.T) {
	f := newChatFixture(t)
	path := "/api/v1/admin/products/" + f.product + "/recommendation"
	valid := map[string]any{"boost": 10, "expected_version": 1}
	statusIs(t, surfaceRequest(t, f.adm, "PATCH", path, "http://qingye-admin.test", valid), 401)
	f.call(t, f.member, "PATCH", path, valid, 401)
	statusIs(t, surfaceRequest(t, f.pub, "PATCH", path, "http://qingye.test", valid, f.cookies[f.member]), 404)
	statusIs(t, surfaceRequest(t, f.adm, "PATCH", path, "http://attacker.test", valid, f.cookies[f.first]), 403)
	statusIs(t, surfaceRequest(t, f.adm, "PATCH", path, "http://qingye-admin.test", valid, &http.Cookie{Name: adminSessionCookie, Value: f.cookies[f.member].Value}), 401)
	for _, value := range []any{-1, 101, 1.5, "10", true, nil} {
		w := f.call(t, f.first, "PATCH", path, map[string]any{"boost": value, "expected_version": 1}, 422)
		if !strings.Contains(w.Body.String(), "INVALID_RECOMMENDATION_BOOST") {
			t.Fatal(w.Body.String())
		}
	}
	for _, body := range []map[string]any{{"expected_version": 1}, {"boost": 0}, {"boost": 0, "expected_version": 0}, {"boost": 0, "expected_version": "1"}, {"boost": 0, "expected_version": 1, "status": "published"}} {
		f.call(t, f.first, "PATCH", path, body, 422)
	}
	recommendationPatch(t, f, "missing", 10, 1, 404)
	// Simulate an actor authenticated before a concurrent disable. Repository
	// authorization must not rely on the earlier middleware snapshot.
	purgeExec(t, f, `UPDATE users SET enabled=false WHERE id=$1`, f.first)
	boost := 10
	repo := &postgres.ManagementStore{DB: f.a.DB, NewID: newID}
	_, err := repo.Recommendation(context.Background(), management.Actor{ID: f.first, Role: "admin"}, f.product, management.RecommendationInput{Boost: &boost, ExpectedVersion: 1})
	if failure, ok := fault.As(err); !ok || failure.Kind != fault.Forbidden {
		t.Fatal("stale actor mutated product", err)
	}
	var unchanged bool
	if err = f.a.DB.QueryRow(context.Background(), `SELECT recommendation_boost=0 AND recommendation_version=1 AND NOT EXISTS(SELECT 1 FROM admin_logs WHERE action='product.recommendation') FROM products WHERE id=$1`, f.product).Scan(&unchanged); err != nil || !unchanged {
		t.Fatal("rejected writes changed durable state", err)
	}
}

func TestAdminRecommendationConcurrentDisableAndAuditRollback(t *testing.T) {
	f := newChatFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	tx, err := f.a.DB.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(context.Background())
	if _, err = tx.Exec(ctx, `UPDATE users SET enabled=false WHERE id=$1`, f.first); err != nil {
		t.Fatal(err)
	}
	repo := &postgres.ManagementStore{DB: f.a.DB, NewID: newID}
	result := make(chan error, 1)
	go func() {
		boost := 30
		_, e := repo.Recommendation(ctx, management.Actor{ID: f.first, Role: "admin"}, f.product, management.RecommendationInput{Boost: &boost, ExpectedVersion: 1})
		result <- e
	}()
	select {
	case e := <-result:
		t.Fatalf("write bypassed actor lock: %v", e)
	case <-time.After(75 * time.Millisecond):
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if e := <-result; e == nil {
		t.Fatal("concurrently disabled actor succeeded")
	} else if failure, ok := fault.As(e); !ok || failure.Kind != fault.Forbidden {
		t.Fatal(e)
	}
	purgeExec(t, f, `UPDATE users SET enabled=true WHERE id=$1;
 CREATE FUNCTION block_recommendation_audit() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.action='product.recommendation' THEN RAISE EXCEPTION 'isolated audit failure'; END IF; RETURN NEW; END $$;
 CREATE TRIGGER block_recommendation_audit BEFORE INSERT ON admin_logs FOR EACH ROW EXECUTE FUNCTION block_recommendation_audit()`, f.first)
	before := recommendationContent(t, f, f.product)
	recommendationPatch(t, f, f.product, 70, 1, 503)
	var intact bool
	if err = f.a.DB.QueryRow(ctx, `SELECT recommendation_boost=0 AND recommendation_version=1 AND NOT EXISTS(SELECT 1 FROM admin_logs WHERE action='product.recommendation') FROM products WHERE id=$1`, f.product).Scan(&intact); err != nil || !intact || recommendationContent(t, f, f.product) != before {
		t.Fatal("failed audit partially committed recommendation", err)
	}
}

func TestAdminRecommendationProjectionAndSnapshots(t *testing.T) {
	f := newChatFixture(t)
	purgeExec(t, f, `UPDATE products SET owner_id=$1,description='完整商品资料' WHERE id=$2`, f.member, f.product)
	recommendationPatch(t, f, f.product, 35, 1, 200)
	for _, path := range []string{"/api/v1/admin/products", "/api/v1/admin/products/" + f.product} {
		raw := f.call(t, f.first, "GET", path, nil, 200).Body.String()
		if !strings.Contains(raw, `"recommendation_boost":35`) || !strings.Contains(raw, `"recommendation_version":2`) {
			t.Fatal("admin projection missing", raw)
		}
	}
	for _, path := range []string{"/api/v1/products", "/api/v1/products/" + f.product, "/api/v1/home", "/api/v1/me/products", "/api/v1/me/products/" + f.product} {
		noRecommendationFields(t, f.call(t, f.member, "GET", path, nil, 200).Body.String())
	}
	conversation := f.start(t, f.other, f.product)
	noRecommendationFields(t, f.call(t, f.other, "GET", "/api/v1/chat/conversations/"+conversation, nil, 200).Body.String())
	f.call(t, f.first, "POST", "/api/v1/admin/products/"+f.product+"/status", map[string]any{"expected_version": 1, "action": "offline", "reason": "snapshot privacy"}, 200)
	var snapshot string
	if err := f.a.DB.QueryRow(context.Background(), `SELECT snapshot::text FROM product_reviews WHERE product_id=$1 ORDER BY created_at DESC LIMIT 1`, f.product).Scan(&snapshot); err != nil {
		t.Fatal(err)
	}
	noRecommendationFields(t, snapshot)
	// Shared member create/edit/image responses must never acquire the admin projection.
	input := catalog.ProductInput{CategoryID: "chat-test", Title: "会员草稿", TradeType: "sale", PriceUnit: "account", RequestID: newID(), Attributes: map[string]any{}}
	created := f.call(t, f.member, "POST", "/api/v1/me/products", input, 200)
	noRecommendationFields(t, created.Body.String())
	p := memberData(t, created)
	id := p["id"].(string)
	recommendationPatch(t, f, id, 45, 1, 200)
	input.ExpectedVersion = int(p["version"].(float64))
	input.Title = "编辑会员草稿"
	edited := f.call(t, f.member, "PATCH", "/api/v1/me/products/"+id, input, 200)
	noRecommendationFields(t, edited.Body.String())
	p = memberData(t, edited)
	purgeExec(t, f, `INSERT INTO media_files(id,owner_id,filename,mime_type,byte_size,storage_key,status) VALUES('boost-member-image',$1,'avatar.png','image/png',1,'test/boost-member.png','ready')`, f.member)
	imageResponse := f.call(t, f.member, "POST", "/api/v1/me/products/"+id+"/images", map[string]any{"expected_version": p["version"], "request_id": newID(), "media_ids": []string{"boost-member-image"}}, 200)
	noRecommendationFields(t, imageResponse.Body.String())
}

func TestAdminRecommendationDoesNotRunMediaLifecycleForOperationalWrite(t *testing.T) {
	f := newChatFixture(t)
	staging := &lifecycleMemoryStore{files: map[string][]byte{}}
	lifecycleEnroll(t, f.a, f.product, f.first, "boost-managed-image", staging)
	// The product was already published when a staging object was attached.
	// A normal update would move it to publishing; boost must not do that.
	before := recommendationContent(t, f, f.product)
	var mediaBefore, mediaAfter string
	query := `SELECT jsonb_agg(to_jsonb(o) ORDER BY media_id,variant)::text FROM media_objects o WHERE product_id=$1`
	if err := f.a.DB.QueryRow(context.Background(), query, f.product).Scan(&mediaBefore); err != nil {
		t.Fatal(err)
	}
	recommendationPatch(t, f, f.product, 90, 1, 200)
	if recommendationContent(t, f, f.product) != before {
		t.Fatal("boost altered media-backed publication")
	}
	if err := f.a.DB.QueryRow(context.Background(), query, f.product).Scan(&mediaAfter); err != nil || mediaAfter != mediaBefore {
		t.Fatal("boost altered media tasks", err)
	}
	// Even expired managed media does not turn a read-time score into a content write.
	purgeExec(t, f, `UPDATE media_objects SET force_delete=true WHERE product_id=$1`, f.product)
	before = recommendationContent(t, f, f.product)
	recommendationPatch(t, f, f.product, 0, 2, 200)
	if recommendationContent(t, f, f.product) != before {
		t.Fatal("expired media changed publication during boost")
	}
	if _, err := f.a.DB.Exec(context.Background(), `UPDATE products SET title=title||'changed' WHERE id=$1`, f.product); err == nil {
		t.Fatal("content update bypassed original media protection")
	}
}

func TestAdminRecommendationDeletedAndPurgedTargets(t *testing.T) {
	f := newChatFixture(t)
	recommendationPatch(t, f, f.product, 60, 1, 200)
	purgeTrash(t, f, f.product, 1)
	recommendationPatch(t, f, f.product, 70, 2, 404)
	f.call(t, f.first, "POST", "/api/v1/admin/products/"+f.product+"/purge", purgeSingleInput(2, "boost-purge"), 200)
	recommendationPatch(t, f, f.product, 70, 2, 404)
	var cleared bool
	if err := f.a.DB.QueryRow(context.Background(), `SELECT purged_at IS NOT NULL AND recommendation_boost=0 AND recommendation_version=3 FROM products WHERE id=$1`, f.product).Scan(&cleared); err != nil || !cleared {
		t.Fatal("purge did not clear operational score", err)
	}
}

func TestAdminSaleCategoryDefaultAndReplay(t *testing.T) {
	f := newChatFixture(t)
	purgeExec(t, f, `UPDATE categories SET enabled=false;
 INSERT INTO categories(id,name,trade_type,price_units,sort_order,enabled) VALUES
 ('disabled-first','disabled','sale','["account"]',-10,false),
 ('rent-first','rent','rent','["account"]',-9,true),
 ('wrong-unit','wrong unit','sale','["day"]',-8,true),
 ('sale-a','sale a','all','[]',1,true),
 ('sale-b','sale b','sale','["account"]',1,true)`)
	in := catalog.ProductInput{Title: "出售默认分类", TradeType: "sale", PriceUnit: "account", Attributes: map[string]any{}, RequestID: newID()}
	p := memberData(t, f.call(t, f.first, "POST", "/api/v1/admin/products", in, 200))
	if p["category_id"] != "sale-a" {
		t.Fatal("did not choose stable compatible category", p)
	}
	purgeExec(t, f, `UPDATE categories SET sort_order=-20 WHERE id='sale-b'`)
	replay := memberData(t, f.call(t, f.first, "POST", "/api/v1/admin/products", in, 200))
	if replay["id"] != p["id"] || replay["category_id"] != "sale-a" {
		t.Fatal("reordered categories invalidated create replay")
	}
	in.CategoryID = "sale-a"
	in.ExpectedVersion = int(p["version"].(float64))
	in.Title = "保留既有出售分类"
	p = memberData(t, f.call(t, f.first, "PATCH", "/api/v1/admin/products/"+p["id"].(string), in, 200))
	if p["category_id"] != "sale-a" {
		t.Fatal("edit reassigned category")
	}
	for _, category := range []string{"disabled-first", "missing", "wrong-unit", "rent-first"} {
		in.CategoryID = category
		in.RequestID = newID()
		f.call(t, f.first, "POST", "/api/v1/admin/products", in, 422)
	}
	in.CategoryID = ""
	in.RequestID = newID()
	purgeExec(t, f, `UPDATE categories SET enabled=false WHERE id IN ('sale-a','sale-b')`)
	w := f.call(t, f.first, "POST", "/api/v1/admin/products", in, 422)
	if !strings.Contains(w.Body.String(), "SALE_CATEGORY_UNAVAILABLE") {
		t.Fatal(w.Body.String())
	}
	// Default selection is admin-sale-create only.
	f.call(t, f.member, "POST", "/api/v1/me/products", in, 422)
	in.TradeType = "rent"
	in.PriceUnit = "day"
	in.RequestID = newID()
	f.call(t, f.first, "POST", "/api/v1/admin/products", in, 422)
}

func TestAdminRentalClassificationPersistsOnDraftSave(t *testing.T) {
	f := r212RentalFixture(t)
	in := r212RentalInput()
	category := "hour_day"
	in.RentalCategory = &category
	p := memberData(t, f.call(t, f.first, "POST", "/api/v1/admin/products", in, 200))
	if p["rental_category"] != category {
		t.Fatal("create lost rental category")
	}
	category = "week_month"
	in.ExpectedVersion = int(p["version"].(float64))
	p = memberData(t, f.call(t, f.first, "PATCH", "/api/v1/admin/products/"+p["id"].(string), in, 200))
	if p["rental_category"] != category {
		t.Fatal("draft save lost rental category")
	}
	raw, _ := json.Marshal(p["rental_quotes"])
	if !strings.Contains(string(raw), "period") {
		t.Fatal("classification damaged rental quotes")
	}
}
