package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"
)

func dashboardRead(t *testing.T, f *chatFixture, query string) map[string]any {
	t.Helper()
	w := f.call(t, f.first, "GET", "/api/v1/admin/dashboard"+query, nil, 200)
	if w.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("private dashboard response must not be cached")
	}
	return memberData(t, w)
}

func dashboardExec(t *testing.T, f *chatFixture, statement string, args ...any) {
	t.Helper()
	if _, err := f.a.DB.Exec(context.Background(), statement, args...); err != nil {
		t.Fatal(err)
	}
}

func dashboardCounts(t *testing.T, data map[string]any, views, favorites, consultations, members int) {
	t.Helper()
	engagement := data["engagement"].(map[string]any)
	totals := map[string]float64{"views": 0, "favorites": 0, "consultations": 0, "new_members": 0}
	for _, raw := range data["trend"].([]any) {
		day := raw.(map[string]any)
		for field := range totals {
			totals[field] += day[field].(float64)
		}
	}
	for field, want := range map[string]int{"views": views, "favorites": favorites, "consultations": consultations} {
		if engagement[field] != float64(want) || totals[field] != float64(want) {
			t.Fatalf("dashboard %s total/trend mismatch: got %v, %v want %d", field, engagement[field], totals[field], want)
		}
	}
	if totals["new_members"] != float64(members) || data["members"].(map[string]any)["new_in_period"] != float64(members) {
		t.Fatalf("dashboard member trend mismatch: %v", data["members"])
	}
}

func TestDashboardAccessValidationAndEmptySeries(t *testing.T) {
	f := newChatFixture(t)
	path := "/api/v1/admin/dashboard"
	statusIs(t, surfaceRequest(t, f.adm, "GET", path, "http://qingye-admin.test", nil), 401)
	f.call(t, f.member, "GET", path, nil, 401)
	statusIs(t, surfaceRequest(t, f.adm, "GET", path, "http://qingye-admin.test", nil, &http.Cookie{Name: adminSessionCookie, Value: f.cookies[f.member].Value}), 401)
	statusIs(t, surfaceRequest(t, f.adm, "GET", path+"?days=bad", "http://qingye-admin.test", nil, f.cookies[f.member]), 401)
	statusIs(t, surfaceRequest(t, f.adm, "GET", path, "http://qingye-admin.test", nil, f.cookies[f.member], f.cookies[f.first]), 200)
	statusIs(t, surfaceRequest(t, f.pub, "GET", path, "http://qingye.test", nil), 404)
	statusIs(t, surfaceRequest(t, f.pub, "GET", path, "http://qingye.test", nil, f.cookies[f.member]), 404)
	statusIs(t, surfaceRequest(t, f.pub, "GET", path, "http://qingye.test", nil, &http.Cookie{Name: sessionCookie, Value: f.cookies[f.first].Value}), 404)
	for _, query := range []string{"days=0", "days=1", "days=31", "days=365", "days=-7", "days=07", "days=7.0", "days=+7", "days=7%20", "days=", "rank_by=", "rank_by=price", "rank_by=Views", "days=7&days=30", "rank_by=views&rank_by=views", "limit=100", "days=%zz", "days=7;rank_by=views"} {
		f.call(t, f.first, "GET", path+"?"+query, nil, 400)
	}
	for _, days := range []int{7, 30} {
		for _, rank := range []string{"views", "favorites", "consultations"} {
			data := dashboardRead(t, f, fmt.Sprintf("?days=%d&rank_by=%s", days, rank))
			if data["timezone"] != "Asia/Shanghai" || len(data["trend"].([]any)) != days || data["range"].(map[string]any)["days"] != float64(days) {
				t.Fatal("invalid dashboard range", data)
			}
			ranking := data["ranking"].(map[string]any)
			if ranking["by"] != rank || ranking["limit"] != float64(10) || len(ranking["items"].([]any)) != 0 {
				t.Fatal("empty dashboard must have an empty ranking", ranking)
			}
			dashboardCounts(t, data, 0, 0, 0, 2)
		}
	}
	defaultData := dashboardRead(t, f, "")
	if defaultData["range"].(map[string]any)["days"] != float64(7) || defaultData["ranking"].(map[string]any)["by"] != "views" {
		t.Fatal("HTTP defaults differ from domain contract")
	}
}

func TestDashboardCurrentInventoryAndTodoSemantics(t *testing.T) {
	f := newChatFixture(t)
	dashboardExec(t, f, `INSERT INTO categories(id,name,enabled) VALUES('dash-disabled-category','停用分类',false);
 INSERT INTO products(id,product_code,category_id,trade_type,price_unit,status,approved_version)
 SELECT 'dash-state-'||state,'STATE-'||state,'chat-test',CASE WHEN state='rented' THEN 'rent' ELSE 'sale' END,
 CASE WHEN state='rented' THEN 'day' ELSE 'account' END,state,1
 FROM unnest(ARRAY['draft','pending','publishing','published','rejected','rejected_final','offline','sold','rented']) AS state;
 INSERT INTO products(id,product_code,category_id,trade_type,price_unit,status,approved_version) VALUES
 ('dash-disabled-category-p','DISABLED','dash-disabled-category','sale','account','published',1),
 ('dash-unapproved','UNAPPROVED','chat-test','sale','account','published',NULL),
 ('dash-trash','TRASH','chat-test','sale','account','draft',NULL),
 ('dash-tombstone','TOMBSTONE','chat-test','sale','account','draft',NULL);
 UPDATE products SET deleted_at=now(),deleted_by_admin=(id='dash-trash') WHERE id IN ('dash-trash','dash-tombstone');`)
	dashboardExec(t, f, `UPDATE users SET enabled=false WHERE id=$1`, f.other)
	dashboardExec(t, f, `INSERT INTO nickname_requests(id,user_id,previous_nickname,nickname) VALUES('dash-nickname',$1,'原名','申请名')`, f.member)
	dashboardExec(t, f, `INSERT INTO chat_conversations(id,member_id,product_id,product_snapshot) VALUES('dash-waiting',$1,$2,'{}')`, f.member, f.product)
	dashboardExec(t, f, `INSERT INTO chat_conversations(id,member_id,product_id,product_snapshot,merged_into) VALUES('dash-merged',$1,$2,'{}','dash-waiting')`, f.member, f.product)
	data := dashboardRead(t, f, "")
	p := data["products"].(map[string]any)
	for field, want := range map[string]float64{"total": 12, "visible": 2, "sale": 11, "rent": 1, "trash": 1} {
		if p[field] != want {
			t.Fatalf("inventory %s got %v want %v", field, p[field], want)
		}
	}
	statuses := p["statuses"].(map[string]any)
	var total float64
	for field, raw := range statuses {
		count := raw.(float64)
		total += count
		want := float64(1)
		if field == "published" {
			want = 4
		}
		if count != want {
			t.Fatalf("status %s got %v want %v", field, count, want)
		}
	}
	if len(statuses) != 9 || total != p["total"] {
		t.Fatal("status distribution does not sum to nondeleted total")
	}
	// Published rows can be hidden by category or by a missing approval.
	var published struct {
		Data []struct {
			ID      string `json:"id"`
			Visible *bool  `json:"is_publicly_visible"`
		} `json:"data"`
		Total int `json:"total"`
	}
	w := f.call(t, f.first, "GET", "/api/v1/admin/products?status=published", nil, 200)
	if err := json.Unmarshal(w.Body.Bytes(), &published); err != nil || published.Total != 4 || len(published.Data) != 4 {
		t.Fatalf("unexpected published rows: %+v, %v", published, err)
	}
	want := map[string]bool{f.product: true, "dash-state-published": true, "dash-disabled-category-p": false, "dash-unapproved": false}
	for _, row := range published.Data {
		if visible, ok := want[row.ID]; !ok || row.Visible == nil || *row.Visible != visible {
			t.Fatalf("incorrect public visibility in admin list: %+v", row)
		}
	}
	seen := map[string]bool{}
	for page := 1; page <= 2; page++ {
		w = f.call(t, f.first, "GET", fmt.Sprintf("/api/v1/admin/products?visibility=public&limit=1&page=%d", page), nil, 200)
		if err := json.Unmarshal(w.Body.Bytes(), &published); err != nil || float64(published.Total) != p["visible"] || len(published.Data) != 1 {
			t.Fatalf("drill-down does not match dashboard: %+v, %v", published, err)
		}
		row := published.Data[0]
		if !want[row.ID] || seen[row.ID] || row.Visible == nil || !*row.Visible {
			t.Fatalf("unexpected visible row on page %d: %+v", page, row)
		}
		seen[row.ID] = true
	}
	f.call(t, f.first, "GET", "/api/v1/admin/products?visibility=invalid", nil, 422)
	todos := data["todos"].(map[string]any)
	for _, field := range []string{"pending_products", "publishing_products", "pending_nicknames", "waiting_conversations"} {
		if todos[field] != float64(1) {
			t.Fatal("todo count includes merged or wrong-state work", field, todos)
		}
	}
	members := data["members"].(map[string]any)
	if members["total"] != float64(2) || members["enabled"] != float64(1) || members["disabled"] != float64(1) || members["new_in_period"] != float64(2) {
		t.Fatal("member totals include administrators or lose disabled accounts", members)
	}
}

func TestR23DashboardReviewQueuesMatchListsAcrossRangesAndTransitions(t *testing.T) {
	f := newChatFixture(t)
	dashboardExec(t, f, `INSERT INTO categories(id,name,enabled) VALUES('dash-review-disabled','停用但仍有待审商品',false);
 INSERT INTO products(id,product_code,category_id,title,trade_type,price_unit,status,source,created_at,updated_at) VALUES
 ('dash-review-old','REVIEW-OLD','chat-test','九十天前待审核','sale','account','pending','member',now()-interval '90 days',now()-interval '90 days'),
 ('dash-review-new','REVIEW-NEW','chat-test','新待审核','sale','account','pending','platform',now(),now()),
 ('dash-review-category','REVIEW-CATEGORY','dash-review-disabled','分类停用待审核','sale','account','pending','member',now()-interval '90 days',now()-interval '90 days'),
 ('dash-review-rejected','REVIEW-REJECTED','chat-test','已处理','sale','account','rejected','member',now(),now()),
 ('dash-review-trash','REVIEW-TRASH','chat-test','回收站待审状态','sale','account','pending','member',now(),now());
 UPDATE products SET deleted_at=now(),deleted_by_admin=true WHERE id='dash-review-trash';`)
	dashboardExec(t, f, `UPDATE products SET owner_id=$1 WHERE id='dash-review-old'`, f.member)
	dashboardExec(t, f, `UPDATE products SET owner_id=$1 WHERE id='dash-review-category'`, f.other)
	dashboardExec(t, f, `UPDATE users SET enabled=false WHERE id=$1`, f.other)
	dashboardExec(t, f, `INSERT INTO nickname_requests(id,user_id,previous_nickname,nickname,created_at) VALUES
 ('dash-review-nickname-old',$1,'聊天验收','旧待审昵称',now()-interval '90 days'),
 ('dash-review-nickname-disabled',$2,'聊天验收','停用会员待审',now())`, f.member, f.other)
	dashboardExec(t, f, `INSERT INTO nickname_requests(id,user_id,previous_nickname,nickname,status,reviewed_by,reviewed_at) VALUES
 ('dash-review-nickname-approved',$1,'旧名','已批准','approved',$2,now()),
 ('dash-review-nickname-rejected',$1,'旧名','已拒绝','rejected',$2,now())`, f.member, f.first)

	assertQueues := func(wantProducts, wantNicknames int) {
		t.Helper()
		// Compare HTTP totals, including later pages, rather than duplicating the SQL predicates.
		for _, page := range []int{1, 2} {
			var products struct {
				Total int `json:"total"`
			}
			w := f.call(t, f.first, "GET", fmt.Sprintf("/api/v1/admin/products?status=pending&page=%d&limit=1", page), nil, 200)
			if err := json.Unmarshal(w.Body.Bytes(), &products); err != nil {
				t.Fatal(err)
			}
			var nicknames struct {
				Pagination struct {
					Total int `json:"total"`
				} `json:"pagination"`
			}
			w = f.call(t, f.first, "GET", fmt.Sprintf("/api/v1/admin/nickname-requests?status=pending&page=%d&limit=1", page), nil, 200)
			if err := json.Unmarshal(w.Body.Bytes(), &nicknames); err != nil {
				t.Fatal(err)
			}
			if products.Total != wantProducts || nicknames.Pagination.Total != wantNicknames {
				t.Fatalf("review list totals differ on page %d: products=%d nicknames=%d want=%d/%d", page, products.Total, nicknames.Pagination.Total, wantProducts, wantNicknames)
			}
			for _, days := range []int{7, 30} {
				data := dashboardRead(t, f, fmt.Sprintf("?days=%d", days))
				todos := data["todos"].(map[string]any)
				if todos["pending_products"] != float64(products.Total) || todos["pending_nicknames"] != float64(nicknames.Pagination.Total) {
					t.Fatalf("%d-day dashboard narrowed or expanded current review queues: %v, list totals=%d/%d", days, todos, products.Total, nicknames.Pagination.Total)
				}
			}
		}
	}
	assertQueues(3, 2)
	f.call(t, f.first, "POST", "/api/v1/admin/products/dash-review-old/review", map[string]any{"expected_version": 1, "decision": "reject", "reason": "请补充资料"}, 200)
	f.call(t, f.first, "POST", "/api/v1/admin/nickname-requests/dash-review-nickname-disabled/review", map[string]any{"decision": "rejected", "note": "账户已停用"}, 200)
	assertQueues(2, 1)
	f.call(t, f.first, "POST", "/api/v1/admin/nickname-requests/dash-review-nickname-old/review", map[string]any{"decision": "approved"}, 200)
	assertQueues(2, 0)
}

func TestDashboardEffectiveInteractionRelations(t *testing.T) {
	f := newChatFixture(t)
	dashboardExec(t, f, `UPDATE users SET created_at=now()-interval '90 days';
 INSERT INTO users(id,phone,nickname,password_hash,role,enabled,created_at) VALUES
 ('dash-disabled','dash-disabled','停用会员','unused','member',false,now()-interval '90 days'),
 ('dash-future-fact','dash-future-fact','未来事实','unused','member',true,now()-interval '90 days');
 INSERT INTO products(id,product_code,category_id,title,trade_type,price_unit,status) VALUES
 ('dash-offline','DASH-OFFLINE','chat-test','下架仍有历史互动','sale','account','offline'),
 ('dash-deleted','DASH-DELETED','chat-test','已删除','sale','account','draft');
 UPDATE products SET deleted_at=now(),deleted_by_admin=true WHERE id='dash-deleted';`)
	dashboardExec(t, f, `UPDATE products SET owner_id=$1 WHERE id=$2`, f.member, f.product)
	for _, actor := range []string{f.member, f.other, f.first, "dash-disabled"} {
		dashboardExec(t, f, `INSERT INTO favorites(user_id,product_id) VALUES($1,$2)`, actor, f.product)
		dashboardExec(t, f, `INSERT INTO product_consultations(member_id,product_id) VALUES($1,$2)`, actor, f.product)
	}
	dashboardExec(t, f, `INSERT INTO favorites(user_id,product_id) VALUES($1,'dash-offline'),($1,'dash-deleted')`, f.other)
	dashboardExec(t, f, `INSERT INTO favorites(user_id,product_id,active) VALUES($1,'dash-offline',false)`, f.member)
	dashboardExec(t, f, `INSERT INTO product_consultations(member_id,product_id) VALUES($1,'dash-offline'),($1,'dash-deleted')`, f.other)
	dashboardExec(t, f, `INSERT INTO favorites(user_id,product_id,created_at) VALUES('dash-future-fact',$1,now()+interval '1 day')`, f.product)
	dashboardExec(t, f, `INSERT INTO product_consultations(member_id,product_id,first_consulted_at) VALUES('dash-future-fact',$1,now()+interval '1 day')`, f.product)
	dashboardExec(t, f, `INSERT INTO product_views_daily(product_id,visitor_hash,viewed_date) VALUES($1,'visitor-a',(now() AT TIME ZONE 'Asia/Shanghai')::date),($1,'visitor-b',(now() AT TIME ZONE 'Asia/Shanghai')::date),('dash-offline','visitor-a',(now() AT TIME ZONE 'Asia/Shanghai')::date),('dash-deleted','visitor-a',(now() AT TIME ZONE 'Asia/Shanghai')::date)`, f.product)
	dashboardExec(t, f, `INSERT INTO product_views_daily(product_id,visitor_hash,viewed_date) VALUES($1,'visitor-a',(now() AT TIME ZONE 'Asia/Shanghai')::date) ON CONFLICT DO NOTHING`, f.product)
	dashboardExec(t, f, `INSERT INTO product_views_daily(product_id,visitor_hash,viewed_date,created_at) VALUES($1,'future-created',(now() AT TIME ZONE 'Asia/Shanghai')::date,now()+interval '1 day'),($1,'future-date',(now() AT TIME ZONE 'Asia/Shanghai')::date+1,now())`, f.product)
	data := dashboardRead(t, f, "")
	dashboardCounts(t, data, 3, 2, 2, 0)
	items := data["ranking"].(map[string]any)["items"].([]any)
	if len(items) != 2 || items[0].(map[string]any)["id"] != f.product || items[1].(map[string]any)["id"] != "dash-offline" {
		t.Fatal("ranking omitted historical interaction on a current offline product", items)
	}
	// A cancellation only removes the current effective relation; no action history is inferred.
	dashboardExec(t, f, `UPDATE favorites SET active=false,updated_at=now() WHERE user_id=$1 AND product_id=$2`, f.other, f.product)
	dashboardCounts(t, dashboardRead(t, f, ""), 3, 1, 2, 0)
	dashboardExec(t, f, `UPDATE users SET enabled=false WHERE id=$1`, f.other)
	dashboardCounts(t, dashboardRead(t, f, ""), 3, 0, 0, 0)
	dashboardExec(t, f, `UPDATE users SET enabled=true WHERE id=$1`, f.other)
	dashboardExec(t, f, `UPDATE favorites SET active=true,updated_at=now() WHERE user_id=$1 AND product_id=$2`, f.other, f.product)
	dashboardCounts(t, dashboardRead(t, f, ""), 3, 2, 2, 0)
}

func TestDashboardBeijingDateBoundariesAndFutureExclusion(t *testing.T) {
	f := newChatFixture(t)
	ctx := context.Background()
	var first7, first30 time.Time
	if err := f.a.DB.QueryRow(ctx, `SELECT (((now() AT TIME ZONE 'Asia/Shanghai')::date-6)::timestamp AT TIME ZONE 'Asia/Shanghai'),(((now() AT TIME ZONE 'Asia/Shanghai')::date-29)::timestamp AT TIME ZONE 'Asia/Shanghai')`).Scan(&first7, &first30); err != nil {
		t.Fatal(err)
	}
	dashboardExec(t, f, `UPDATE users SET created_at=now()-interval '90 days'`)
	dashboardExec(t, f, `UPDATE users SET created_at=$2 WHERE id=$1`, f.member, first7)
	dashboardExec(t, f, `UPDATE users SET created_at=$2 WHERE id=$1`, f.other, first7.Add(-time.Microsecond))
	dashboardExec(t, f, `INSERT INTO users(id,phone,nickname,password_hash,role,created_at) VALUES('dash-new-future','dash-new-future','未来会员','unused','member',now()+interval '1 day')`)
	for i, when := range []time.Time{first7, first7.Add(-time.Microsecond), first30} {
		dashboardExec(t, f, `INSERT INTO product_views_daily(product_id,visitor_hash,viewed_date,created_at) VALUES($1,$2,($3::timestamptz AT TIME ZONE 'Asia/Shanghai')::date,$3)`, f.product, fmt.Sprintf("boundary-%d", i), when)
	}
	for i, actor := range []string{f.member, f.other} {
		when := first7.Add(-time.Duration(i) * time.Microsecond)
		dashboardExec(t, f, `INSERT INTO favorites(user_id,product_id,created_at,updated_at) VALUES($1,$2,$3,now())`, actor, f.product, when)
		dashboardExec(t, f, `INSERT INTO product_consultations(member_id,product_id,first_consulted_at,last_consulted_at) VALUES($1,$2,$3,now())`, actor, f.product, when)
	}
	dashboardExec(t, f, `INSERT INTO product_views_daily(product_id,visitor_hash,viewed_date,created_at) VALUES($1,'future-clock',(now() AT TIME ZONE 'Asia/Shanghai')::date,now()+interval '1 day'),($1,'future-day',(now() AT TIME ZONE 'Asia/Shanghai')::date+1,now())`, f.product)
	for _, days := range []int{7, 30} {
		data := dashboardRead(t, f, fmt.Sprintf("?days=%d", days))
		rangeData := data["range"].(map[string]any)
		start, err := time.Parse(time.RFC3339Nano, rangeData["start_at"].(string))
		if err != nil {
			t.Fatal(err)
		}
		end, err := time.Parse(time.RFC3339Nano, rangeData["end_at"].(string))
		if err != nil {
			t.Fatal(err)
		}
		if rangeData["end_at"] != data["generated_at"] || start.UTC().Hour() != 16 || start.UTC().Minute() != 0 || end.Before(start) {
			t.Fatal("dashboard did not use Beijing midnight and one database cutoff", rangeData)
		}
		series := data["trend"].([]any)
		if len(series) != days || series[0].(map[string]any)["date"] != rangeData["start_date"] || series[days-1].(map[string]any)["date"] != rangeData["end_date"] {
			t.Fatal("date series does not cover the full inclusive period")
		}
		for i := 1; i < len(series); i++ {
			previous, _ := time.Parse("2006-01-02", series[i-1].(map[string]any)["date"].(string))
			current, _ := time.Parse("2006-01-02", series[i].(map[string]any)["date"].(string))
			if current.Sub(previous) != 24*time.Hour {
				t.Fatal("missing zero-filled day")
			}
		}
		if days == 7 {
			dashboardCounts(t, data, 1, 1, 1, 1)
		} else {
			dashboardCounts(t, data, 3, 2, 2, 2)
		}
	}
}

func TestDashboardTopTenStableOrderingPrivacyAndReadOnly(t *testing.T) {
	f := newChatFixture(t)
	for i := 0; i < 12; i++ {
		id, code := fmt.Sprintf("dash-rank-%02d", i), "A-DUPLICATE"
		if i < 2 {
			code = "Z-DUPLICATE"
		}
		dashboardExec(t, f, `INSERT INTO products(id,product_code,category_id,title,trade_type,price_unit,status,legacy_payload) VALUES($1,$2,'chat-test','排行合成商品','sale','account','offline','{"never_return":"legacy payload"}')`, id, code)
		dashboardExec(t, f, `INSERT INTO product_views_daily(product_id,visitor_hash,viewed_date) VALUES($1,'rank-visitor',(now() AT TIME ZONE 'Asia/Shanghai')::date)`, id)
		dashboardExec(t, f, `INSERT INTO favorites(user_id,product_id) VALUES($1,$2)`, f.other, id)
		dashboardExec(t, f, `INSERT INTO product_consultations(member_id,product_id) VALUES($1,$2)`, f.other, id)
	}
	dashboardExec(t, f, `INSERT INTO product_views_daily(product_id,visitor_hash,viewed_date) VALUES('dash-rank-11','rank-second',(now() AT TIME ZONE 'Asia/Shanghai')::date)`)
	snapshot := func() string {
		t.Helper()
		var value string
		err := f.a.DB.QueryRow(context.Background(), `SELECT jsonb_build_object('products',(SELECT jsonb_agg(to_jsonb(p) ORDER BY id) FROM products p),'favorites',(SELECT jsonb_agg(to_jsonb(f) ORDER BY user_id,product_id) FROM favorites f),'views',(SELECT jsonb_agg(to_jsonb(v) ORDER BY product_id,visitor_hash,viewed_date) FROM product_views_daily v),'consultations',(SELECT jsonb_agg(to_jsonb(q) ORDER BY product_id,member_id) FROM product_consultations q),'logs',(SELECT count(*) FROM admin_logs))::text`).Scan(&value)
		if err != nil {
			t.Fatal(err)
		}
		return value
	}
	before := snapshot()
	allowed := map[string]bool{"id": true, "product_code": true, "title": true, "trade_type": true, "rental_category": true, "status": true, "views": true, "favorites": true, "consultations": true}
	for _, rank := range []string{"views", "favorites", "consultations"} {
		data := dashboardRead(t, f, "?rank_by="+rank)
		items := data["ranking"].(map[string]any)["items"].([]any)
		if len(items) != 10 {
			t.Fatal("ranking is not bounded to ten", len(items))
		}
		for i, raw := range items {
			item := raw.(map[string]any)
			expected := i + 2
			if rank == "views" {
				if i == 0 {
					expected = 11
				} else {
					expected = i + 1
				}
			}
			if item["id"] != fmt.Sprintf("dash-rank-%02d", expected) {
				t.Fatalf("unstable %s ordering at %d: %v", rank, i, item)
			}
			if len(item) != len(allowed) {
				t.Fatal("unexpected ranking projection", item)
			}
			for key := range item {
				if !allowed[key] {
					t.Fatal("private or heavy field in dashboard", key)
				}
			}
		}
		raw, _ := json.Marshal(data)
		for _, secret := range []string{"visitor_hash", "user_id", "owner_id", "legacy_payload", "password", "phone", "price_amount", "images"} {
			if strings.Contains(string(raw), `"`+secret+`"`) {
				t.Fatal("dashboard exposed unnecessary data", secret)
			}
		}
		dashboardCounts(t, data, 13, 12, 12, 2)
	}
	if after := snapshot(); after != before {
		t.Fatal("dashboard GET mutated business or interaction data")
	}
}
