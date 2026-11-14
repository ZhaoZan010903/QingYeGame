package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/jackc/pgx/v5"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func purgeExec(t *testing.T, f *chatFixture, sql string, args ...any) {
	t.Helper()
	if _, err := f.a.DB.Exec(context.Background(), sql, append([]any{pgx.QueryExecModeSimpleProtocol}, args...)...); err != nil {
		t.Fatal(err)
	}
}

func purgeTrash(t *testing.T, f *chatFixture, id string, version int) {
	t.Helper()
	f.call(t, f.first, "DELETE", "/api/v1/admin/products/"+id, map[string]any{"expected_version": version, "reason": "隔离验收回收"}, 200)
}

func purgePreview(t *testing.T, f *chatFixture) map[string]any {
	t.Helper()
	w := f.call(t, f.first, "GET", "/api/v1/admin/products/trash/preview", nil, 200)
	if w.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("purge preview may be cached")
	}
	return memberData(t, w)
}

func purgeSingleInput(version int, requestID string) map[string]any {
	return map[string]any{"expected_version": version, "confirmation": "永久删除", "reason": "隔离验收清理", "request_id": requestID}
}

func purgeBulkInput(snapshot, requestID string) map[string]any {
	return map[string]any{"snapshot": snapshot, "confirmation": "清空回收站", "reason": "隔离验收清空", "request_id": requestID}
}

func TestAdminPurgeAccessAndExplicitConfirmation(t *testing.T) {
	f := newChatFixture(t)
	preview := "/api/v1/admin/products/trash/preview"
	path := "/api/v1/admin/products/" + f.product + "/purge"
	body := purgeSingleInput(1, "access")
	statusIs(t, surfaceRequest(t, f.adm, "GET", preview, "http://qingye-admin.test", nil), 401)
	f.call(t, f.member, "GET", preview, nil, 401)
	f.call(t, f.member, "POST", path, body, 401)
	statusIs(t, surfaceRequest(t, f.pub, "GET", preview, "http://qingye.test", nil, f.cookies[f.member]), 404)
	statusIs(t, surfaceRequest(t, f.pub, "POST", path, "http://qingye.test", body, f.cookies[f.member]), 404)
	statusIs(t, surfaceRequest(t, f.adm, "POST", path, "http://attacker.test", body, f.cookies[f.first]), 403)
	statusIs(t, surfaceRequest(t, f.adm, "POST", path, "http://qingye-admin.test", body, &http.Cookie{Name: adminSessionCookie, Value: f.cookies[f.member].Value}), 401)
	f.call(t, f.first, "GET", preview+"?q=one", nil, 400)
	f.call(t, f.first, "POST", path, body, 409)
	purgeTrash(t, f, f.product, 1)
	for _, bad := range []map[string]any{
		{"expected_version": 2, "request_id": "missing-confirmation"},
		{"expected_version": 2, "confirmation": "清空回收站", "request_id": "wrong-confirmation"},
		{"expected_version": 2, "confirmation": "永久删除"},
		{"expected_version": 0, "confirmation": "永久删除", "request_id": "bad-version"},
		{"expected_version": 2, "confirmation": "永久删除", "request_id": "long-reason", "reason": strings.Repeat("因", 501)},
	} {
		f.call(t, f.first, "POST", path, bad, 422)
	}
	f.call(t, f.first, "POST", path, map[string]any{"expected_version": 2, "confirmation": "永久删除", "request_id": "extra", "all": true}, 422)
	f.call(t, f.first, "POST", path, purgeSingleInput(1, "stale"), 409)
	f.call(t, f.first, "POST", "/api/v1/admin/products/trash/purge", purgeBulkInput("all", "bad-snapshot"), 422)
	purgeExec(t, f, `INSERT INTO products(id,product_code,category_id,trade_type,price_unit,deleted_at) VALUES('member-tombstone','OLD','chat-test','sale','account',now())`)
	f.call(t, f.first, "POST", "/api/v1/admin/products/member-tombstone/purge", purgeSingleInput(1, "tombstone"), 404)
	if got := purgePreview(t, f); got["total"] != float64(1) {
		t.Fatal("rejected requests changed trash", got)
	}
}

func TestAdminPurgeRedactsBusinessDataPreservesHistoryAndReplays(t *testing.T) {
	f := newChatFixture(t)
	other := "purge-untouched"
	f.addProduct(t, other)
	conversation := f.start(t, f.member, f.product)
	f.start(t, f.member, other)
	otherConversation := f.start(t, f.other, f.product)
	purgeExec(t, f, `UPDATE products SET owner_id=$1,legacy_id='purge-legacy',description='private description',attributes='{"server":"private server"}',review_note='private note',legacy_price='private price',legacy_category='private category',published_at=now(),manual_rank=1 WHERE id=$2;
 INSERT INTO media_files(id,owner_id,filename,storage_key,mime_type,byte_size,status,managed_upload) VALUES
 ('purge-current',$1,'current.png','uploads/purge-current.png','image/png',100,'ready',true),
 ('purge-review',$1,'review.png','uploads/purge-review.png','image/png',100,'ready',true);
 INSERT INTO product_images(product_id,media_id,sort_order) VALUES($2,'purge-current',0);
 INSERT INTO product_reviews(id,product_id,reviewer_id,decision,reason,version,snapshot) VALUES('purge-review-record',$2,$3,'published','private review',1,jsonb_build_object('title','private snapshot','images',jsonb_build_array(jsonb_build_object('id','purge-current'),jsonb_build_object('id','purge-review'))));
 INSERT INTO product_private_credentials(product_id,ciphertext) VALUES($2,'\x1234');
 INSERT INTO favorites(user_id,product_id) VALUES($1,$2);
 INSERT INTO product_views_daily(product_id,visitor_hash,viewed_date) VALUES($2,'purge-view',current_date);
 INSERT INTO product_hot_scores(product_id,batch_id) VALUES($2,'purge-hot');
 INSERT INTO product_item_similarities(source_id,target_id,score,common_viewers,batch_id,computed_at) VALUES($2,$4,0.5,2,'purge-sim',now()),($4,$2,0.5,2,'purge-sim',now());
 INSERT INTO idempotency(user_id,scope,request_id,body_hash,response) VALUES($1,'create-product','old-create','old-hash',jsonb_build_object('product_id',$2::text));
 INSERT INTO site_settings(key,value) VALUES('home_presentation',jsonb_build_object('slides',jsonb_build_array(jsonb_build_object('productId',$2::text,'title','target override'),jsonb_build_object('title','pure decoration','imageId','decorative'),jsonb_build_object('productId',$4::text,'title','untouched override')))) ON CONFLICT(key) DO UPDATE SET value=EXCLUDED.value`, f.member, f.product, f.first, other)
	purgeExec(t, f, `INSERT INTO media_files(id,owner_id,filename,storage_key,mime_type,byte_size,status,purpose,conversation_id) VALUES('purge-chat-image',$1,'chat.png','uploads/purge-chat-image.png','image/png',100,'ready','chat',$2);
 WITH inserted AS (INSERT INTO chat_messages(conversation_id,sender_id,sender_role,client_id,kind,body) VALUES($2,$1,'member','keep-text-and-image','image','keep this conversation text') RETURNING id)
 INSERT INTO chat_message_images(message_id,media_id,position) SELECT id,'purge-chat-image',0 FROM inserted`, f.member, conversation)
	purgeTrash(t, f, f.product, 1)
	preview := purgePreview(t, f)
	if preview["image_count"] != float64(2) {
		t.Fatal("review-only media omitted from preview", preview)
	}
	path := "/api/v1/admin/products/" + f.product + "/purge"
	body := purgeSingleInput(2, "single-success")
	first := f.call(t, f.first, "POST", path, body, 200)
	result := memberData(t, first)
	if result["purged_count"] != float64(1) || result["cleanup_pending"] != true || result["image_count"] != float64(2) {
		t.Fatal("incorrect purge receipt", result)
	}
	replay := f.call(t, f.first, "POST", path, body, 200)
	if first.Body.String() != replay.Body.String() {
		t.Fatal("successful purge retry did not replay original response")
	}
	body["reason"] = "different reason"
	f.call(t, f.first, "POST", path, body, 409)
	body["reason"] = "隔离验收清理"
	f.call(t, f.first, "POST", "/api/v1/admin/products/"+other+"/purge", body, 409)
	f.call(t, f.second, "POST", path, body, 404)
	f.call(t, f.first, "POST", "/api/v1/admin/products/"+f.product+"/restore", map[string]any{"expected_version": 3}, 404)
	f.call(t, f.first, "GET", "/api/v1/admin/products/"+f.product, nil, 404)
	f.call(t, f.member, "GET", "/api/v1/products/"+f.product, nil, 404)
	var intact bool
	if err := f.a.DB.QueryRow(context.Background(), `SELECT purged_at IS NOT NULL AND deleted_at IS NOT NULL AND NOT deleted_by_admin AND version=3 AND legacy_id='purge-legacy' AND product_code=$1 AND title='' AND description='' AND attributes='{}' AND rental_quotes='[]' AND price_amount IS NULL AND price_unit='unknown' AND owner_id IS NULL AND approved_version IS NULL AND manual_rank IS NULL AND review_note='' AND legacy_payload IS NULL AND legacy_price IS NULL AND legacy_category IS NULL AND published_at IS NULL FROM products WHERE id=$1`, f.product).Scan(&intact); err != nil || !intact {
		t.Fatal("product business data retained or identity lost", err)
	}
	if err := f.a.DB.QueryRow(context.Background(), `SELECT
 NOT EXISTS(SELECT 1 FROM product_images WHERE product_id=$1) AND NOT EXISTS(SELECT 1 FROM product_private_credentials WHERE product_id=$1)
 AND NOT EXISTS(SELECT 1 FROM favorites WHERE product_id=$1) AND NOT EXISTS(SELECT 1 FROM product_views_daily WHERE product_id=$1)
 AND NOT EXISTS(SELECT 1 FROM product_hot_scores WHERE product_id=$1) AND NOT EXISTS(SELECT 1 FROM product_consultations WHERE product_id=$1)
 AND NOT EXISTS(SELECT 1 FROM product_item_similarities WHERE source_id=$1 OR target_id=$1)
 AND EXISTS(SELECT 1 FROM product_reviews WHERE product_id=$1 AND snapshot='{}' AND reason='' AND decision='published' AND reviewer_id=$2)
 AND EXISTS(SELECT 1 FROM idempotency WHERE scope='create-product' AND request_id='old-create' AND response->>'product_id'=$1)
 AND (SELECT count(*)=1 FROM admin_logs WHERE resource_id=$1 AND action='product.purge')`, f.product, f.first).Scan(&intact); err != nil || !intact {
		t.Fatal("related business records or audit/replay boundary wrong", err)
	}
	if err := f.a.DB.QueryRow(context.Background(), `SELECT
 (SELECT product_snapshot->>'id'=$2 FROM chat_conversations WHERE id=$3)
 AND (SELECT product_snapshot->>'title'='商品已永久删除' AND product_snapshot->>'cover_url'='' AND product_snapshot->'price'='null'::jsonb FROM chat_conversations WHERE id=$4)
 AND NOT EXISTS(SELECT 1 FROM chat_messages WHERE kind='product' AND product_snapshot->>'id'=$1 AND (product_snapshot->>'title'<>'商品已永久删除' OR product_snapshot->>'cover_url'<>'' OR product_snapshot ? 'server'))
 AND EXISTS(SELECT 1 FROM chat_messages WHERE conversation_id=$3 AND body='keep this conversation text')
 AND EXISTS(SELECT 1 FROM chat_message_images WHERE media_id='purge-chat-image')
 AND EXISTS(SELECT 1 FROM media_files WHERE id='purge-chat-image' AND deleted_at IS NULL)
 AND (SELECT count(*)=2 FROM media_files WHERE id IN ('purge-current','purge-review') AND deleted_at IS NOT NULL)
 AND EXISTS(SELECT 1 FROM products WHERE id=$2 AND title='咨询商品' AND purged_at IS NULL)`, f.product, other, conversation, otherConversation).Scan(&intact); err != nil || !intact {
		t.Fatal("chat history, other product or review-only media boundary wrong", err)
	}
	if err := f.a.DB.QueryRow(context.Background(), `SELECT value->'slides'=jsonb_build_array(jsonb_build_object('title','pure decoration','imageId','decorative'),jsonb_build_object('productId',$1::text,'title','untouched override')) FROM site_settings WHERE key='home_presentation'`, other).Scan(&intact); err != nil || !intact {
		t.Fatal("purge removed a non-target presentation slide", err)
	}
	if got := purgePreview(t, f); got["total"] != float64(0) {
		t.Fatal("purged tombstone remained in recycle bin", got)
	}
}

func TestAdminBulkPurgeRejectsEveryStaleSnapshotAtomically(t *testing.T) {
	for _, mutation := range []string{"new", "restore", "version", "purge"} {
		t.Run(mutation, func(t *testing.T) {
			f := newChatFixture(t)
			f.addProduct(t, "second-trash")
			purgeTrash(t, f, f.product, 1)
			purgeTrash(t, f, "second-trash", 1)
			old := purgePreview(t, f)["snapshot"].(string)
			switch mutation {
			case "new":
				f.addProduct(t, "new-trash")
				purgeTrash(t, f, "new-trash", 1)
			case "restore":
				f.call(t, f.first, "POST", "/api/v1/admin/products/"+f.product+"/restore", map[string]any{"expected_version": 2}, 200)
			case "version":
				purgeExec(t, f, `UPDATE products SET version=version+1 WHERE id=$1`, f.product)
			case "purge":
				f.call(t, f.first, "POST", "/api/v1/admin/products/"+f.product+"/purge", purgeSingleInput(2, "earlier-single"), 200)
			}
			w := f.call(t, f.first, "POST", "/api/v1/admin/products/trash/purge", purgeBulkInput(old, "stale-bulk"), 409)
			if !strings.Contains(w.Body.String(), "TRASH_SNAPSHOT_CHANGED") {
				t.Fatal("wrong conflict", w.Body.String())
			}
			var safe bool
			if err := f.a.DB.QueryRow(context.Background(), `SELECT deleted_by_admin AND purged_at IS NULL AND title='咨询商品' FROM products WHERE id='second-trash'`).Scan(&safe); err != nil || !safe {
				t.Fatal("stale bulk partially deleted", err)
			}
			current := purgePreview(t, f)
			body := purgeBulkInput(current["snapshot"].(string), "fresh-bulk")
			first := f.call(t, f.first, "POST", "/api/v1/admin/products/trash/purge", body, 200)
			if memberData(t, first)["purged_count"] != current["total"] {
				t.Fatal("bulk deleted outside reviewed set")
			}
			replay := f.call(t, f.first, "POST", "/api/v1/admin/products/trash/purge", body, 200)
			if first.Body.String() != replay.Body.String() {
				t.Fatal("bulk retry lost original receipt")
			}
		})
	}
}

func TestAdminPurgeConcurrencyAndTrashSetLock(t *testing.T) {
	f := newChatFixture(t)
	purgeTrash(t, f, f.product, 1)
	path := "/api/v1/admin/products/" + f.product + "/purge"
	raw, _ := json.Marshal(purgeSingleInput(2, "same-concurrent-request"))
	var wg sync.WaitGroup
	codes := make(chan int, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r := httptest.NewRequest("POST", path, strings.NewReader(string(raw)))
			r.AddCookie(f.cookies[f.first])
			r.Header.Set("Origin", "http://qingye-admin.test")
			r.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			f.adm.ServeHTTP(w, r)
			codes <- w.Code
		}()
	}
	wg.Wait()
	close(codes)
	for code := range codes {
		if code != 200 {
			t.Fatal("concurrent same-body retry failed", code)
		}
	}
	var count int
	if err := f.a.DB.QueryRow(context.Background(), `SELECT count(*) FROM admin_logs WHERE action='product.purge' AND resource_id=$1`, f.product).Scan(&count); err != nil || count != 1 {
		t.Fatal("concurrent replay duplicated mutation", err, count)
	}
	f.addProduct(t, "locked-trash")
	purgeTrash(t, f, "locked-trash", 1)
	snapshot := purgePreview(t, f)["snapshot"].(string)
	tx, err := f.a.DB.Begin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(context.Background())
	if _, err = tx.Exec(context.Background(), `SELECT pg_advisory_xact_lock(hashtextextended('admin-product-trash',0))`); err != nil {
		t.Fatal(err)
	}
	raw, _ = json.Marshal(purgeBulkInput(snapshot, "wait-for-new-entry"))
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		r := httptest.NewRequest("POST", "/api/v1/admin/products/trash/purge", strings.NewReader(string(raw)))
		r.AddCookie(f.cookies[f.first])
		r.Header.Set("Origin", "http://qingye-admin.test")
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		f.adm.ServeHTTP(w, r)
		done <- w
	}()
	select {
	case w := <-done:
		t.Fatalf("bulk ignored shared trash-set lock: %d", w.Code)
	case <-time.After(50 * time.Millisecond):
	}
	if _, err = tx.Exec(context.Background(), `INSERT INTO products(id,product_code,category_id,title,trade_type,price_unit,deleted_at,deleted_by_admin) VALUES('later-trash','LATER','chat-test','later','sale','account',now(),true)`); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(context.Background()); err != nil {
		t.Fatal(err)
	}
	select {
	case w := <-done:
		if w.Code != 409 || !strings.Contains(w.Body.String(), "TRASH_SNAPSHOT_CHANGED") {
			t.Fatal("late entry was not rejected", w.Code, w.Body.String())
		}
	case <-time.After(5 * time.Second):
		t.Fatal("bulk did not finish after releasing trash lock")
	}
	if got := purgePreview(t, f); got["total"] != float64(2) {
		t.Fatal("lock race partially cleared trash", got)
	}
}

func TestAdminPurgeRollbackIncludesMetadataMediaAndReplay(t *testing.T) {
	f := newChatFixture(t)
	purgeExec(t, f, `INSERT INTO media_files(id,owner_id,storage_key,status,managed_upload) VALUES('rollback-media',$1,'uploads/rollback-media.png','ready',true);
 INSERT INTO product_images(product_id,media_id,sort_order) VALUES($2,'rollback-media',0);
 INSERT INTO product_reviews(id,product_id,decision,version,snapshot) VALUES('rollback-review',$2,'published',1,'{"title":"original snapshot"}')`, f.member, f.product)
	purgeTrash(t, f, f.product, 1)
	purgeExec(t, f, `CREATE FUNCTION reject_purge_receipt() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.scope='admin-product-purge' THEN RAISE EXCEPTION 'isolated purge rollback'; END IF; RETURN NEW; END $$;
 CREATE TRIGGER reject_purge_receipt BEFORE INSERT ON idempotency FOR EACH ROW EXECUTE FUNCTION reject_purge_receipt()`)
	f.call(t, f.first, "POST", "/api/v1/admin/products/"+f.product+"/purge", purgeSingleInput(2, "rollback"), 503)
	var intact bool
	if err := f.a.DB.QueryRow(context.Background(), `SELECT
 EXISTS(SELECT 1 FROM products WHERE id=$1 AND deleted_by_admin AND purged_at IS NULL AND version=2 AND title='咨询商品')
 AND EXISTS(SELECT 1 FROM product_images WHERE product_id=$1 AND media_id='rollback-media')
 AND EXISTS(SELECT 1 FROM media_files WHERE id='rollback-media' AND deleted_at IS NULL AND status='ready')
 AND EXISTS(SELECT 1 FROM product_reviews WHERE id='rollback-review' AND snapshot->>'title'='original snapshot')
 AND NOT EXISTS(SELECT 1 FROM admin_logs WHERE action='product.purge' AND resource_id=$1)
 AND NOT EXISTS(SELECT 1 FROM idempotency WHERE scope='admin-product-purge')`, f.product).Scan(&intact); err != nil || !intact {
		t.Fatal("failed purge leaked partial transaction", err)
	}
}

func TestAdminPurgeResourceLimitsAndEmptyReplay(t *testing.T) {
	t.Run("empty", func(t *testing.T) {
		f := newChatFixture(t)
		preview := purgePreview(t, f)
		if preview["total"] != float64(0) || preview["image_count"] != float64(0) {
			t.Fatal("empty preview incorrect", preview)
		}
		body := purgeBulkInput(preview["snapshot"].(string), "empty-bulk")
		w := f.call(t, f.first, "POST", "/api/v1/admin/products/trash/purge", body, 200)
		if memberData(t, w)["purged_count"] != float64(0) {
			t.Fatal("empty bulk mutated data")
		}
		f.call(t, f.first, "POST", "/api/v1/admin/products/trash/purge", body, 200)
	})
	t.Run("products", func(t *testing.T) {
		f := newChatFixture(t)
		purgeExec(t, f, `INSERT INTO products(id,product_code,category_id,trade_type,price_unit,deleted_at,deleted_by_admin) SELECT 'limit-'||i,'LIMIT-'||i,'chat-test','sale','account',now(),true FROM generate_series(1,1001) i`)
		f.call(t, f.first, "GET", "/api/v1/admin/products/trash/preview", nil, 422)
		w := f.call(t, f.first, "POST", "/api/v1/admin/products/trash/purge", purgeBulkInput(strings.Repeat("0", 64), "too-many-products"), 422)
		if !strings.Contains(w.Body.String(), "TRASH_PURGE_LIMIT_EXCEEDED") {
			t.Fatal("wrong resource-limit result")
		}
		var count int
		if err := f.a.DB.QueryRow(context.Background(), `SELECT count(*) FROM products WHERE deleted_by_admin AND purged_at IS NULL`).Scan(&count); err != nil || count != 1001 {
			t.Fatal("oversized bulk partially deleted", err, count)
		}
	})
	t.Run("images", func(t *testing.T) {
		f := newChatFixture(t)
		purgeExec(t, f, `INSERT INTO product_reviews(id,product_id,decision,version,snapshot) SELECT 'image-limit',$1,'published',1,jsonb_build_object('images',jsonb_agg(jsonb_build_object('id','limit-image-'||i))) FROM generate_series(1,100001) i`, f.product)
		purgeTrash(t, f, f.product, 1)
		f.call(t, f.first, "GET", "/api/v1/admin/products/trash/preview", nil, 422)
		f.call(t, f.first, "POST", fmt.Sprintf("/api/v1/admin/products/%s/purge", f.product), purgeSingleInput(2, "too-many-images"), 422)
		var intact bool
		if err := f.a.DB.QueryRow(context.Background(), `SELECT deleted_by_admin AND purged_at IS NULL FROM products WHERE id=$1`, f.product).Scan(&intact); err != nil || !intact {
			t.Fatal("oversized media purge changed product", err)
		}
	})
}
