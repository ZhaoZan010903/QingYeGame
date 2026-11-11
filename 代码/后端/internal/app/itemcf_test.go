package app

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func itemCFView(t *testing.T, a *App, viewer, product string, at time.Time) {
	t.Helper()
	_, err := a.DB.Exec(context.Background(), `INSERT INTO product_views_daily(product_id,visitor_hash,viewed_date,created_at,last_seen_at)
 VALUES($1,$2,($3::timestamptz AT TIME ZONE 'Asia/Shanghai')::date,$3,$3)
 ON CONFLICT(product_id,visitor_hash,viewed_date) DO UPDATE SET last_seen_at=EXCLUDED.last_seen_at`, product, viewer, at)
	if err != nil {
		t.Fatal(err)
	}
}
func itemCFPair(t *testing.T, a *App, from, to string) (float64, int) {
	t.Helper()
	var score float64
	var common int
	err := a.DB.QueryRow(context.Background(), "SELECT score,common_viewers FROM product_item_similarities WHERE source_id=$1 AND target_id=$2", from, to).Scan(&score, &common)
	if err != nil {
		t.Fatal(err)
	}
	return score, common
}

func TestItemCFHybridRankingAndPagination(t *testing.T) {
	a, h, one, two := personalFixture(t)
	ctx := context.Background()
	h.call("POST", "/api/v1/products/sale-old/view", nil, one, 200)
	for _, peer := range []string{"peer-a", "peer-b"} {
		for _, id := range []string{"sale-old", "rent-old"} {
			itemCFView(t, a, peer, id, time.Now().Add(-time.Minute))
		}
	}
	// The same person browsing on more days is still one co-viewer.
	for day := 1; day < 5; day++ {
		for _, id := range []string{"sale-old", "rent-old"} {
			itemCFView(t, a, "peer-a", id, time.Now().Add(-time.Duration(day)*24*time.Hour))
		}
	}
	if err := a.RefreshItemCF(ctx); err != nil {
		t.Fatal(err)
	}
	score, common := itemCFPair(t, a, "sale-old", "rent-old")
	if common != 2 || math.Abs(score-(2/math.Sqrt(6)*2/5)) > 1e-9 {
		t.Fatal("distinct-viewer cosine/shrinkage incorrect", score, common)
	}
	got := personalList(t, h, one, "recommended")
	if strings.Join(got, ",") != "rent-old,sale-new,sale-old,rent-new" {
		t.Fatal("ItemCF must precede attribute fill, exactly once", got)
	}
	if got := personalList(t, h, one, "recommended&category=sale"); strings.Join(got, ",") != "sale-new,sale-old" {
		t.Fatal("filter bypass", got)
	}
	if got := personalList(t, h, one, "recommended&price_unit=account&min_price=500"); len(got) != 2 {
		t.Fatal("price filter bypass", got)
	}
	if got := personalList(t, h, two, "recommended"); got[0] != "rent-new" {
		t.Fatal("cold user inherited another user's CF", got)
	}
	for _, sort := range []string{"hot", "newest"} {
		if got := personalList(t, h, one, sort); got[0] != "rent-new" {
			t.Fatal("independent sort changed", sort, got)
		}
	}
	first := h.call("GET", "/api/v1/products?sort=recommended&limit=2", nil, one, 200)
	var page struct {
		Data []map[string]any `json:"data"`
		Next string           `json:"next_cursor"`
	}
	if err := json.Unmarshal(first.Body.Bytes(), &page); err != nil {
		t.Fatal(err)
	}
	if page.Next == "" {
		t.Fatal("pagination cursor missing", first.Body.String())
	}
	next := personalList(t, h, one, "recommended&limit=2&cursor="+page.Next)
	if strings.Join(next, ",") != strings.Join(got[2:], ",") {
		t.Fatal("unstable fill pagination", next)
	}
	home := memberData(t, h.call("GET", "/api/v1/home", nil, one, 200))
	recommended := home["recommended"].([]any)
	if recommended[0].(map[string]any)["id"] != "rent-old" || home["personalization_source"] != "browsing" {
		t.Fatal("homepage did not use ItemCF")
	}
	for _, private := range []string{"peer-a", "peer-b", "itemcf-v1", "common_viewers", "visitor_hash"} {
		if strings.Contains(string(mustJSON(home)), private) {
			t.Fatal("private training data leaked", private)
		}
	}
	h.call("PATCH", "/api/v1/recommendations/profile", map[string]bool{"enabled": false}, one, 200)
	if got := personalList(t, h, one, "recommended"); got[0] != "rent-new" {
		t.Fatal("optout still using CF", got)
	}
	h.call("PATCH", "/api/v1/recommendations/profile", map[string]bool{"enabled": true, "reset": true}, one, 200)
	if got := personalList(t, h, one, "recommended"); got[0] != "rent-new" {
		t.Fatal("reset still using CF", got)
	}
	h.call("POST", "/api/v1/products/sale-old/view", nil, one, 200)
	h.call("POST", "/api/v1/products/rent-old/view", nil, one, 200)
	request := httptest.NewRequest("GET", "/", nil)
	request.AddCookie(one)
	profile, err := a.interestProfile(request)
	if err != nil {
		t.Fatal(err)
	}
	scores, err := a.itemCFScores(ctx, profile.products)
	if err != nil || len(scores) != 0 {
		t.Fatal("already-viewed CF candidates not excluded", scores, err)
	}
}

func TestItemCFTrainingBoundariesAndFallbacks(t *testing.T) {
	a, h, one, _ := personalFixture(t)
	ctx := context.Background()
	now := time.Now().Add(-time.Second)
	h.call("POST", "/api/v1/products/sale-old/view", nil, one, 200)
	for _, peer := range []string{"active-1", "active-2", "off", "reset", "too-old", "future"} {
		at := now
		if peer == "too-old" {
			at = now.Add(-31 * 24 * time.Hour)
		}
		if peer == "future" {
			at = now.Add(time.Hour)
		}
		for _, id := range []string{"sale-old", "rent-old", "private"} {
			itemCFView(t, a, peer, id, at)
		}
	}
	_, err := a.DB.Exec(ctx, `INSERT INTO recommendation_preferences(visitor_hash,enabled,reset_at) VALUES('off',false,'1970-01-01'),('reset',true,now())`)
	if err != nil {
		t.Fatal(err)
	}
	if err = a.RefreshItemCF(ctx); err != nil {
		t.Fatal(err)
	}
	_, n := itemCFPair(t, a, "sale-old", "rent-old")
	if n != 2 {
		t.Fatal("training window/optout/reset ignored", n)
	}
	var hidden int
	if err = a.DB.QueryRow(ctx, "SELECT count(*) FROM product_item_similarities WHERE source_id='private' OR target_id='private'").Scan(&hidden); err != nil || hidden != 0 {
		t.Fatal("hidden product trained", hidden, err)
	}
	// Visibility is checked at read time, without waiting for the next batch.
	if _, err = a.DB.Exec(ctx, "UPDATE products SET status='offline' WHERE id='rent-old'"); err != nil {
		t.Fatal(err)
	}
	if got := personalList(t, h, one, "recommended"); got[0] != "sale-new" || strings.Contains(strings.Join(got, ","), "rent-old") {
		t.Fatal("offline target exposed", got)
	}
	if _, err = a.DB.Exec(ctx, "UPDATE products SET status='published' WHERE id='rent-old';UPDATE categories SET enabled=false WHERE id='rental'"); err != nil {
		t.Fatal(err)
	}
	if got := personalList(t, h, one, "recommended"); len(got) != 2 {
		t.Fatal("disabled category exposed", got)
	}
	if _, err = a.DB.Exec(ctx, "UPDATE categories SET enabled=true;UPDATE products SET approved_version=NULL WHERE id='rent-old'"); err != nil {
		t.Fatal(err)
	}
	if got := personalList(t, h, one, "recommended"); got[0] != "sale-new" {
		t.Fatal("unreviewed version exposed", got)
	}
	if _, err = a.DB.Exec(ctx, "UPDATE products SET approved_version=version WHERE id='rent-old';UPDATE product_item_similarities SET computed_at=now()-interval '3 hours'"); err != nil {
		t.Fatal(err)
	}
	if got := personalList(t, h, one, "recommended"); got[0] != "sale-new" {
		t.Fatal("stale batch used", got)
	}
	if _, err = a.DB.Exec(ctx, "UPDATE product_item_similarities SET computed_at=now()+interval '1 hour'"); err != nil {
		t.Fatal(err)
	}
	if got := personalList(t, h, one, "recommended"); got[0] != "sale-new" {
		t.Fatal("future batch used", got)
	}
	if _, err = a.DB.Exec(ctx, "DROP TABLE product_item_similarities"); err != nil {
		t.Fatal(err)
	}
	if got := personalList(t, h, one, "recommended"); got[0] != "sale-new" {
		t.Fatal("derived index outage broke attributes", got)
	}
}

func TestItemCFBoundedHistoryTopKAndAtomicRefresh(t *testing.T) {
	a := integrationMemberApp(t)
	ctx := context.Background()
	now := time.Now().Add(-time.Second)
	recommendationCategory(t, a, "items", true)
	for i := 0; i < 55; i++ {
		id := fmt.Sprintf("p%02d", i)
		recommendationProduct(t, a, id, "items", "platform", "published", now, true)
		for _, peer := range []string{"u1", "u2"} {
			itemCFView(t, a, peer, id, now.Add(time.Duration(i-55)*time.Minute))
		}
	}
	if err := a.RefreshItemCF(ctx); err != nil {
		t.Fatal(err)
	}
	var count, maxEdges, excluded int
	if err := a.DB.QueryRow(ctx, `SELECT count(*),count(*) FILTER(WHERE source_id<'p05' OR target_id<'p05') FROM product_item_similarities`).Scan(&count, &excluded); err != nil {
		t.Fatal(err)
	}
	if err := a.DB.QueryRow(ctx, `SELECT max(n) FROM (SELECT count(*) n FROM product_item_similarities GROUP BY source_id)x`).Scan(&maxEdges); err != nil {
		t.Fatal(err)
	}
	if count != 1500 || maxEdges != 30 || excluded != 0 {
		t.Fatal("history/TopK limits failed", count, maxEdges, excluded)
	}
	var before string
	if err := a.DB.QueryRow(ctx, "SELECT value->>'batch_id' FROM site_settings WHERE key='itemcf_state'").Scan(&before); err != nil {
		t.Fatal(err)
	}
	lock, err := a.DB.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = lock.Exec(ctx, "SELECT pg_advisory_xact_lock($1)", itemCFLock); err != nil {
		t.Fatal(err)
	}
	if err = a.RefreshItemCF(ctx); err != nil {
		t.Fatal(err)
	}
	_ = lock.Rollback(ctx)
	var after string
	if err = a.DB.QueryRow(ctx, "SELECT value->>'batch_id' FROM site_settings WHERE key='itemcf_state'").Scan(&after); err != nil || after != before {
		t.Fatal("overlapping worker replaced snapshot", after, err)
	}
	// Deliberate failure after the transactional delete must preserve all edges.
	_, err = a.DB.Exec(ctx, `CREATE FUNCTION reject_itemcf() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'forced refresh failure'; END $$;
 CREATE TRIGGER reject_itemcf BEFORE INSERT ON product_item_similarities FOR EACH ROW EXECUTE FUNCTION reject_itemcf()`)
	if err != nil {
		t.Fatal(err)
	}
	if err = a.RefreshItemCF(ctx); err == nil {
		t.Fatal("fault injection failed")
	}
	if err = a.DB.QueryRow(ctx, "SELECT count(*) FROM product_item_similarities WHERE batch_id=$1", before).Scan(&count); err != nil || count != 1500 {
		t.Fatal("failed batch erased complete snapshot", count, err)
	}
	if _, err = a.DB.Exec(ctx, "DROP TRIGGER reject_itemcf ON product_item_similarities;DELETE FROM product_views_daily WHERE visitor_hash='u2'"); err != nil {
		t.Fatal(err)
	}
	if err = a.RefreshItemCF(ctx); err != nil {
		t.Fatal(err)
	}
	if err = a.DB.QueryRow(ctx, "SELECT count(*) FROM product_item_similarities").Scan(&count); err != nil || count != 0 {
		t.Fatal("one viewer fabricated collaboration or old edges lingered", count, err)
	}
}

func TestItemCFConcurrentReadsDuringRefresh(t *testing.T) {
	a, h, one, two := personalFixture(t)
	ctx := context.Background()
	h.call("POST", "/api/v1/products/sale-old/view", nil, one, 200)
	for _, peer := range []string{"peer1", "peer2"} {
		for _, id := range []string{"sale-old", "rent-old"} {
			itemCFView(t, a, peer, id, time.Now().Add(-time.Minute))
		}
	}
	if err := a.RefreshItemCF(ctx); err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 5; i++ {
			if err := a.RefreshItemCF(ctx); err != nil {
				t.Error(err)
			}
		}
	}()
	for worker := 0; worker < 10; worker++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			cookie, want := one, "rent-old"
			if worker%2 == 1 {
				cookie, want = two, "rent-new"
			}
			for i := 0; i < 10; i++ {
				r := httptest.NewRequest("GET", "/api/v1/products?sort=recommended&limit=2", nil)
				r.AddCookie(cookie)
				w := httptest.NewRecorder()
				h.handler.ServeHTTP(w, r)
				var data struct {
					Data []struct {
						ID string `json:"id"`
					} `json:"data"`
				}
				if err := json.Unmarshal(w.Body.Bytes(), &data); err != nil || w.Code != 200 || len(data.Data) == 0 || data.Data[0].ID != want {
					t.Errorf("snapshot/identity mismatch worker=%d status=%d body=%s", worker, w.Code, w.Body.String())
				}
			}
		}(worker)
	}
	wg.Wait()
	t.Logf("100 reads/10 workers with 5 refreshes: %s", time.Since(started))
}

func TestItemCFBoundedBatchPerformance(t *testing.T) {
	a := integrationMemberApp(t)
	ctx := context.Background()
	recommendationCategory(t, a, "scale", true)
	_, err := a.DB.Exec(ctx, `INSERT INTO products(id,product_code,category_id,title,trade_type,price_unit,description,status,source,version,approved_version,published_at)
 SELECT 'scale-'||n,'SCALE-'||n,'scale','性能测试账号','sale','account','隔离验收数据','published','platform',1,1,now()-interval '1 day' FROM generate_series(0,999)n;
 INSERT INTO product_views_daily(product_id,visitor_hash,viewed_date,created_at,last_seen_at)
 SELECT 'scale-'||((u*7+j)%1000),'scale-visitor-'||u,(now() AT TIME ZONE 'Asia/Shanghai')::date,now()-interval '1 minute',now()-interval '1 minute'
 FROM generate_series(1,2000)u CROSS JOIN generate_series(0,19)j`)
	if err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	if err = a.RefreshItemCF(ctx); err != nil {
		t.Fatal(err)
	}
	var edges, maximum int
	var bytes int64
	if err = a.DB.QueryRow(ctx, `SELECT count(*),pg_total_relation_size('product_item_similarities') FROM product_item_similarities`).Scan(&edges, &bytes); err != nil {
		t.Fatal(err)
	}
	if err = a.DB.QueryRow(ctx, `SELECT max(n) FROM (SELECT count(*) n FROM product_item_similarities GROUP BY source_id)x`).Scan(&maximum); err != nil {
		t.Fatal(err)
	}
	if edges != 30000 || maximum != 30 {
		t.Fatal("scale snapshot exceeded bounded output", edges, maximum)
	}
	t.Logf("1000 products / 2000 identities / 40000 daily views: batch=%s, edges=%d, table+indexes=%d bytes", time.Since(started), edges, bytes)
}
