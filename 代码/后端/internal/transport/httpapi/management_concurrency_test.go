package httpapi

import (
	"context"
	"encoding/json"
	"qingye/backend/internal/domain/catalog"
	"qingye/backend/internal/domain/fault"
	"qingye/backend/internal/domain/management"
	"qingye/backend/internal/domain/member"
	"sync"
	"testing"
	"time"
)

func categoryFixture(t *testing.T, a *App) (management.Actor, member.Actor, management.CategoryInput) {
	t.Helper()
	recommendationUser(t, a, "category-admin", "admin")
	recommendationUser(t, a, "category-member", "member")
	admin := management.Actor{ID: "category-admin", Role: "admin"}
	user := member.Actor{ID: "category-member", Role: "member"}
	input := management.CategoryInput{Code: "concurrent-category", Name: "并发分类", TradeType: "sale", PriceUnits: []string{"account"}, Enabled: true, Filters: []catalog.FilterGroup{{Key: "region", Label: "区服", Options: []catalog.FilterOption{{Value: "east", Label: "华东"}}}}}
	if _, err := a.managementService().SaveCategory(context.Background(), admin, "", true, input); err != nil {
		t.Fatal(err)
	}
	return admin, user, input
}

func TestCategoryRemovalWaitsForProductCommit(t *testing.T) {
	a := integrationMemberApp(t)
	admin, user, category := categoryFixture(t, a)
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	blocker, err := a.DB.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer blocker.Rollback(context.Background())
	// 让真实创建用例停在INSERT，观察它是否仍持有分类共享锁。
	if _, err = blocker.Exec(ctx, `LOCK TABLE products IN SHARE MODE`); err != nil {
		t.Fatal(err)
	}
	productDone := make(chan error, 1)
	go func() {
		_, e := a.memberService().Create(ctx, user, catalog.ProductInput{CategoryID: category.Code, Title: "并发商品", TradeType: "sale", PriceUnit: "account", Attributes: map[string]any{"region": "east"}, RequestID: "category-race-create"})
		productDone <- e
	}()
	for {
		var waiting bool
		err = a.DB.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_locks WHERE relation='products'::regclass AND mode='RowExclusiveLock' AND NOT granted)`).Scan(&waiting)
		if err != nil {
			t.Fatal(err)
		}
		if waiting {
			break
		}
		select {
		case e := <-productDone:
			t.Fatalf("create did not reach insert: %v", e)
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-time.After(10 * time.Millisecond):
		}
	}
	category.Filters[0].Options = []catalog.FilterOption{}
	configDone := make(chan error, 1)
	go func() {
		_, e := a.managementService().SaveCategory(ctx, admin, category.Code, false, category)
		configDone <- e
	}()
	select {
	case e := <-configDone:
		t.Fatalf("configuration bypassed uncommitted product: %v", e)
	case <-time.After(100 * time.Millisecond):
	}
	if err = blocker.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err = <-productDone; err != nil {
		t.Fatalf("create failed: %v", err)
	}
	f, ok := fault.As(<-configDone)
	if !ok || f.Code != "FILTER_IN_USE" {
		t.Fatalf("expected FILTER_IN_USE, got %#v", f)
	}
	var raw []byte
	if err = a.DB.QueryRow(ctx, `SELECT filter_config FROM categories WHERE id=$1`, category.Code).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var groups []catalog.FilterGroup
	if err = json.Unmarshal(raw, &groups); err != nil || len(groups) != 1 || len(groups[0].Options) != 1 {
		t.Fatalf("referenced option was removed: %s", raw)
	}
}

func TestConcurrentCategoryCreationHasOneSuccess(t *testing.T) {
	a := integrationMemberApp(t)
	recommendationUser(t, a, "category-admin", "admin")
	actor := management.Actor{ID: "category-admin", Role: "admin"}
	input := management.CategoryInput{Code: "duplicate-category", Name: "重复分类", TradeType: "sale", PriceUnits: []string{"account"}, Enabled: true}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	start := make(chan struct{})
	results := make(chan error, 8)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, err := a.managementService().SaveCategory(ctx, actor, "", true, input)
			results <- err
		}()
	}
	close(start)
	wg.Wait()
	close(results)
	success, conflicts := 0, 0
	for err := range results {
		if err == nil {
			success++
			continue
		}
		f, ok := fault.As(err)
		if !ok || f.Code != "CATEGORY_EXISTS" {
			t.Fatalf("unexpected race error: %v", err)
		}
		conflicts++
	}
	if success != 1 || conflicts != 7 {
		t.Fatalf("success=%d conflicts=%d", success, conflicts)
	}
	var audits int
	if err := a.DB.QueryRow(ctx, `SELECT count(*) FROM admin_logs WHERE action='category.save' AND resource_id=$1`, input.Code).Scan(&audits); err != nil || audits != 1 {
		t.Fatalf("audits=%d err=%v", audits, err)
	}
}
