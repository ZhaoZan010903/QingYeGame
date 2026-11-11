package httpapi

import (
	"context"
	"fmt"
	"qingye/backend/internal/domain/catalog"
	"qingye/backend/internal/domain/fault"
	"qingye/backend/internal/domain/management"
	"qingye/backend/internal/domain/member"
	"sync"
	"testing"
	"time"
)

func TestHotScoreExcludesSelfDisabledAndAdminFavorites(t *testing.T) {
	a := integrationMemberApp(t)
	ctx := context.Background()
	recommendationCategory(t, a, "favorite-category", true)
	recommendationProduct(t, a, "favorite-product", "favorite-category", "member", "published", time.Now().Add(-time.Hour), true)
	owner := recommendationUser(t, a, "favorite-owner", "member")
	recommendationUser(t, a, "favorite-member", "member")
	recommendationUser(t, a, "favorite-disabled", "member")
	recommendationUser(t, a, "favorite-admin", "admin")
	if _, err := a.DB.Exec(ctx, "UPDATE products SET owner_id='favorite-owner' WHERE id='favorite-product'"); err != nil {
		t.Fatal(err)
	}
	h := memberHTTP{t: t, handler: a.Routes(), ip: "self-favorite", origin: "http://qingye.test"}
	h.call("PUT", "/api/v1/me/favorites/favorite-product", nil, owner, 200)
	if _, err := a.DB.Exec(ctx, `INSERT INTO favorites(user_id,product_id) VALUES('favorite-member','favorite-product'),('favorite-disabled','favorite-product'),('favorite-admin','favorite-product'); UPDATE users SET enabled=false WHERE id='favorite-disabled'`); err != nil {
		t.Fatal(err)
	}
	if err := a.RefreshRecommendations(ctx); err != nil {
		t.Fatal(err)
	}
	if score := recommendationScores(t, a)["favorite-product"]; score[0] != 5 || score[2] != 1 {
		t.Fatalf("non-independent favorites inflated popularity: %v", score)
	}
	var active bool
	if err := a.DB.QueryRow(ctx, "SELECT active FROM favorites WHERE user_id='favorite-owner' AND product_id='favorite-product'").Scan(&active); err != nil || !active {
		t.Fatalf("owner bookmark was removed: %v", err)
	}
}

func TestAdminTemplateRejectsDedicatedFieldNames(t *testing.T) {
	a := integrationMemberApp(t)
	token := recommendationUser(t, a, "template-admin", "admin")
	admin := App{DB: a.DB, Redis: a.Redis, Config: a.Config}
	admin.Config.Surface = "admin"
	admin.Config.AllowedOrigins = []string{"http://qingye-admin.test"}
	h := memberHTTP{t: t, handler: admin.Routes(), ip: "reserved-template", origin: "http://qingye-admin.test"}
	input := management.CategoryInput{Code: "reserved-template", Name: "配置校验", TradeType: "sale", PriceUnits: []string{"account"}, Enabled: true}
	for _, key := range []string{"custom_tags", "private_credentials", "qq_account"} {
		input.Filters = []catalog.FilterGroup{{Key: key, Label: "专用字段", Type: "text"}}
		h.call("POST", "/api/v1/admin/categories", input, token, 422)
	}
	input.Filters = []catalog.FilterGroup{{Key: "account_style", Label: "装扮风格", Type: "text"}}
	h.call("POST", "/api/v1/admin/categories", input, token, 200)
}

func TestOppositeImageBatchesHaveNoDeadlock(t *testing.T) {
	a := integrationMemberApp(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	recommendationUser(t, a, "batch-owner", "member")
	_, err := a.DB.Exec(ctx, `INSERT INTO categories(id,name) VALUES('batch-category','并发图集');
 INSERT INTO products(id,product_code,owner_id,category_id,trade_type,price_unit) VALUES
 ('batch-one','batch-one','batch-owner','batch-category','sale','account'),
 ('batch-two','batch-two','batch-owner','batch-category','sale','account');
 INSERT INTO media_files(id,owner_id,status) VALUES('batch-media-a','batch-owner','ready'),('batch-media-b','batch-owner','ready');
 CREATE FUNCTION slow_batch_insert() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN
 PERFORM pg_sleep(0.2); RETURN NEW; END $$;
 CREATE TRIGGER concurrent_batch_window BEFORE INSERT ON product_images FOR EACH ROW EXECUTE FUNCTION slow_batch_insert()`)
	if err != nil {
		t.Fatal(err)
	}
	type outcome struct {
		product string
		err     error
	}
	results := make(chan outcome, 2)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i, product := range []string{"batch-one", "batch-two"} {
		wg.Add(1)
		go func(index int, id string) {
			defer wg.Done()
			<-start
			ids := []string{"batch-media-a", "batch-media-b"}
			if index == 1 {
				ids[0], ids[1] = ids[1], ids[0]
			}
			_, e := a.memberService().AppendImages(ctx, member.Actor{ID: "batch-owner", Role: "member"}, id, member.AppendImagesInput{MediaIDs: ids, ExpectedVersion: 1, RequestID: fmt.Sprintf("opposite-batch-%d", index)})
			results <- outcome{id, e}
		}(i, product)
	}
	close(start)
	wg.Wait()
	close(results)
	winner := ""
	conflicts := 0
	for result := range results {
		if result.err == nil {
			winner = result.product
			continue
		}
		f, ok := fault.As(result.err)
		if !ok || f.Code != "MEDIA_ALREADY_LINKED" {
			t.Fatalf("unexpected concurrency failure: %v", result.err)
		}
		conflicts++
	}
	if winner == "" || conflicts != 1 {
		t.Fatalf("expected one complete batch and one ownership conflict, winner=%s conflicts=%d", winner, conflicts)
	}
	var ordered []string
	var total, owners int
	if err = a.DB.QueryRow(ctx, `SELECT count(*),count(DISTINCT product_id) FROM product_images`).Scan(&total, &owners); err != nil || total != 2 || owners != 1 {
		t.Fatalf("partial batch committed: %d/%d %v", total, owners, err)
	}
	if err = a.DB.QueryRow(ctx, `SELECT array_agg(media_id ORDER BY sort_order) FROM product_images WHERE product_id=$1`, winner).Scan(&ordered); err != nil {
		t.Fatal(err)
	}
	first := "batch-media-a"
	if winner == "batch-two" {
		first = "batch-media-b"
	}
	if len(ordered) != 2 || ordered[0] != first {
		t.Fatalf("lock ordering changed display ordering: %v", ordered)
	}
}
