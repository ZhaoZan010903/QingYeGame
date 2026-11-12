package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

func productListBody(t *testing.T, r *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var out map[string]any
	if err := json.Unmarshal(r.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestAdminProductNumberedPagesAndFilters(t *testing.T) {
	a := integrationMemberApp(t)
	ctx := context.Background()
	_, err := a.DB.Exec(ctx, `INSERT INTO categories(id,name) VALUES('list-category','列表分类'); INSERT INTO products(id,product_code,category_id,title,trade_type,price_unit,status,source,created_at,updated_at,published_at,approved_version)
 SELECT 'list-'||i,'QL'||i,'list-category','商品'||i,CASE WHEN i%2=0 THEN 'rent' ELSE 'sale' END,CASE WHEN i%2=0 THEN 'day' ELSE 'account' END,'published',CASE WHEN i%2=0 THEN 'member' ELSE 'platform' END,now()-i*interval '1 hour',now()-i*interval '1 hour',now()-i*interval '1 day',1 FROM generate_series(1,45) i`)
	if err != nil {
		t.Fatal(err)
	}
	token := recommendationUser(t, a, "list-admin", "admin")
	member := recommendationUser(t, a, "list-member", "member")
	a.Config.Surface = "admin"
	a.Config.AllowedOrigins = []string{"http://qingye-admin.test"}
	h := memberHTTP{t: t, handler: a.Routes(), ip: "list-admin", origin: "http://qingye-admin.test"}
	h.call("GET", "/api/v1/admin/products?page=1", nil, member, 401)
	seen := map[string]bool{}
	for page := 1; page <= 3; page++ {
		out := productListBody(t, h.call("GET", fmt.Sprintf("/api/v1/admin/products?page=%d&limit=20&sort=published", page), nil, token, 200))
		items := out["data"].([]any)
		want := 20
		if page == 3 {
			want = 5
		}
		if len(items) != want || out["total"] != float64(45) || out["page"] != float64(page) {
			t.Fatalf("unexpected page %v", out)
		}
		for _, v := range items {
			p := v.(map[string]any)
			id := p["id"].(string)
			if seen[id] {
				t.Fatal("duplicate across pages")
			}
			seen[id] = true
			if p["created_at"] == nil || p["published_at"] == nil || p["updated_at"] == nil {
				t.Fatal("missing timestamps")
			}
		}
	}
	out := productListBody(t, h.call("GET", "/api/v1/admin/products?page=1&limit=50&trade_type=rent&source=member", nil, token, 200))
	if out["total"] != float64(22) {
		t.Fatal("filters not combined")
	}
	out = productListBody(t, h.call("GET", "/api/v1/admin/products?page=99&limit=20", nil, token, 200))
	if len(out["data"].([]any)) != 0 || out["total"] != float64(45) {
		t.Fatal("empty last page lost total")
	}
	h.call("GET", "/api/v1/admin/products?cursor=MjA&limit=20", nil, token, 200)
	for _, q := range []string{"page=0", "page=2&cursor=MjA", "limit=51", "sort=malicious", "source=invalid", "trade_type=all", "trash=yes"} {
		h.call("GET", "/api/v1/admin/products?"+q, nil, token, 422)
	}
}

func TestAdminProductTrashPreservesDataAndReviewBoundary(t *testing.T) {
	a := integrationMemberApp(t)
	ctx := context.Background()
	token := recommendationUser(t, a, "trash-admin", "admin")
	user := recommendationUser(t, a, "trash-owner", "member")
	_, err := a.DB.Exec(ctx, `INSERT INTO categories(id,name) VALUES('trash-category','分类');
 INSERT INTO products(id,product_code,category_id,title,trade_type,price_unit,status,source,owner_id,published_at,approved_version,description) VALUES('trash-p','TRASH1','trash-category','保留完整资料','sale','account','published','member','trash-owner',now()-interval '1 day',1,'完整介绍');
 INSERT INTO media_files(id,filename,storage_key,mime_type,byte_size,status) VALUES('trash-image','original.jpg','legacy/original.jpg','image/jpeg',100,'ready');
 INSERT INTO product_images(product_id,media_id,sort_order) VALUES('trash-p','trash-image',0)`)
	if err != nil {
		t.Fatal(err)
	}
	pub := memberHTTP{t: t, handler: a.Routes(), ip: "trash-public", origin: "http://qingye.test"}
	admin := App{DB: a.DB, Redis: a.Redis, Config: a.Config}
	admin.Config.Surface = "admin"
	admin.Config.AllowedOrigins = []string{"http://qingye-admin.test"}
	h := memberHTTP{t: t, handler: admin.Routes(), ip: "trash-admin", origin: "http://qingye-admin.test"}
	path := "/api/v1/admin/products/trash-p"
	input := map[string]any{"expected_version": 1, "reason": "测试移入回收站"}
	h.call("DELETE", path, input, user, 401)
	h.call("DELETE", path, map[string]any{"expected_version": 1}, token, 422)
	h.call("DELETE", path, map[string]any{"expected_version": 2, "reason": "旧版本"}, token, 409)
	h.call("DELETE", path, input, token, 200)
	pub.call("GET", "/api/v1/products/trash-p", nil, nil, 404)
	h.call("PATCH", path, map[string]any{"expected_version": 2}, token, 404)
	h.call("POST", path+"/publish", map[string]any{"expected_version": 2}, token, 404)
	out := productListBody(t, h.call("GET", "/api/v1/admin/products", nil, token, 200))
	if out["total"] != float64(0) {
		t.Fatal("trashed product still listed")
	}
	out = productListBody(t, h.call("GET", "/api/v1/admin/products?trash=1", nil, token, 200))
	if out["total"] != float64(1) {
		t.Fatal("trash missing")
	}
	p := out["data"].([]any)[0].(map[string]any)
	if p["image_total"] != float64(1) || p["title"] != "保留完整资料" || p["deleted_at"] == nil {
		t.Fatal("trash erased content")
	}
	if _, err = a.DB.Exec(ctx, `INSERT INTO products(id,product_code,category_id,trade_type,price_unit,deleted_at) VALUES('old-tombstone','TOMB','trash-category','sale','account',now())`); err != nil {
		t.Fatal(err)
	}
	h.call("POST", "/api/v1/admin/products/old-tombstone/restore", map[string]any{"expected_version": 1}, token, 404)
	h.call("POST", path+"/restore", map[string]any{"expected_version": 1}, token, 409)
	h.call("POST", path+"/restore", map[string]any{"expected_version": 2}, token, 200)
	pub.call("GET", "/api/v1/products/trash-p", nil, nil, 404)
	h.call("POST", path+"/publish", map[string]any{"expected_version": 3}, token, 409)
	p = memberData(t, h.call("GET", path, nil, token, 200))
	if p["status"] != "draft" || p["published_at"] == nil || p["description"] != "完整介绍" {
		t.Fatal("restore lost details or bypassed review")
	}
	var logs, images int
	a.DB.QueryRow(ctx, `SELECT count(*) FROM admin_logs WHERE resource_id='trash-p' AND action IN ('product.trash','product.restore')`).Scan(&logs)
	a.DB.QueryRow(ctx, `SELECT count(*) FROM product_images WHERE product_id='trash-p'`).Scan(&images)
	if logs != 2 || images != 1 {
		t.Fatal("audit/media references lost")
	}
}

func TestAdminConcurrentTrashOnlyOneWinner(t *testing.T) {
	a := integrationMemberApp(t)
	ctx := context.Background()
	token := recommendationUser(t, a, "trash-race-admin", "admin")
	_, err := a.DB.Exec(ctx, `INSERT INTO categories(id,name) VALUES('race-c','分类'); INSERT INTO products(id,product_code,category_id,trade_type,price_unit) VALUES('race-p','RACE','race-c','sale','account')`)
	if err != nil {
		t.Fatal(err)
	}
	a.Config.Surface = "admin"
	a.Config.AllowedOrigins = []string{"http://qingye-admin.test"}
	handler := a.Routes()
	var wg sync.WaitGroup
	codes := make(chan int, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r := httptest.NewRequest("DELETE", "/api/v1/admin/products/race-p", strings.NewReader(`{"expected_version":1,"reason":"测试并发"}`))
			r.AddCookie(token)
			r.Header.Set("Origin", "http://qingye-admin.test")
			r.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, r)
			codes <- w.Code
		}()
	}
	wg.Wait()
	close(codes)
	counts := map[int]int{}
	for c := range codes {
		counts[c]++
	}
	if counts[200] != 1 || counts[409] != 1 {
		t.Fatalf("unexpected concurrent results %v", counts)
	}
}
