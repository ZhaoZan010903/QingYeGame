package httpapi

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"qingye/backend/internal/domain/catalog"
	"qingye/backend/internal/domain/fault"
	"qingye/backend/internal/domain/management"
	"qingye/backend/internal/domain/media"
	"qingye/backend/internal/domain/member"
	"qingye/backend/internal/infrastructure/postgres"
	"qingye/backend/internal/maintenance"
	"strings"
	"sync"
	"testing"
	"time"
)

func r16Fixture(t *testing.T) (*App, member.Actor, catalog.ProductInput) {
	t.Helper()
	a := integrationMemberApp(t)
	ctx := context.Background()
	recommendationUser(t, a, "r16-member", "member")
	_, err := a.DB.Exec(ctx, `INSERT INTO categories(id,name,trade_type,price_units) VALUES('r16-sale','出售','sale','["account"]'); INSERT INTO product_dictionary_options(dictionary_key,value,label) VALUES('server','r16-east','东区'),('server','r16-west','西区')`)
	if err != nil {
		t.Fatal(err)
	}
	return a, member.Actor{ID: "r16-member", Role: "member"}, catalog.ProductInput{CategoryID: "r16-sale", Title: "测试资产", Description: "完整介绍", TradeType: "sale", PriceUnit: "account", RequestID: newID(), Attributes: map[string]any{"server": "r16-east", "gender": "female", "level": "", "kind": "", "custom_tags": []string{"漂亮"}}}
}
func r16Draft(t *testing.T, a *App, u member.Actor, in catalog.ProductInput) map[string]any {
	t.Helper()
	in.RequestID = newID()
	out, err := a.memberService().Create(context.Background(), u, in)
	if err != nil {
		t.Fatal(err)
	}
	return out["data"].(map[string]any)
}

func TestR16DictionariesSingleChoiceAndConcurrentUpdates(t *testing.T) {
	a, u, in := r16Fixture(t)
	ctx := context.Background()
	recommendationUser(t, a, "r16-admin", "admin")
	store := postgres.ManagementStore{DB: a.DB, NewID: newID}
	actor := management.Actor{ID: "r16-admin", Role: "admin"}
	in.Attributes["server"] = []string{"r16-east", "r16-west"}
	if _, err := a.memberService().Create(ctx, u, in); err == nil {
		t.Fatal("multi-select server accepted")
	}
	in.Attributes["server"] = "r16-east"
	p := r16Draft(t, a, u, in)
	update := catalog.DictionaryInput{ExpectedVersion: 1, Options: []catalog.DictionaryOption{{Value: "r16-east", Label: "东区", Enabled: false}, {Value: "r16-west", Label: "西区", Enabled: true}}}
	start := make(chan struct{})
	results := make(chan error, 2)
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, e := store.SaveDictionary(ctx, actor, "server", update)
			results <- e
		}()
	}
	close(start)
	wg.Wait()
	close(results)
	ok, conflict := 0, 0
	for err := range results {
		if err == nil {
			ok++
		} else if f, yes := fault.As(err); yes && f.Code == "VERSION_CONFLICT" {
			conflict++
		} else {
			t.Fatal(err)
		}
	}
	if ok != 1 || conflict != 1 {
		t.Fatalf("lost version protection %d/%d", ok, conflict)
	}
	groups, err := (&postgres.CatalogStore{DB: a.DB}).Dictionaries(ctx, false)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(groups)
	if strings.Contains(string(raw), "r16-east") {
		t.Fatal("disabled dictionary option leaked into choices")
	}
	in.RequestID = newID()
	if _, err = a.memberService().Create(ctx, u, in); err == nil {
		t.Fatal("disabled choice accepted for new record")
	}
	in.ExpectedVersion = int(p["version"].(float64))
	in.Title = "仅修改标题"
	if _, err = a.memberService().Edit(ctx, u, p["id"].(string), in); err != nil {
		t.Fatalf("old disabled choice not preserved: %v", err)
	}
	update.ExpectedVersion = 2
	update.Options = update.Options[1:]
	if _, err = store.SaveDictionary(ctx, actor, "server", update); err == nil {
		t.Fatal("referenced value removed")
	}
}

func TestR16TagsAndDraftDeletionBoundary(t *testing.T) {
	a, u, in := r16Fixture(t)
	ctx := context.Background()
	in.Attributes["custom_tags"] = []string{"一二三四五六"}
	if _, err := a.memberService().Create(ctx, u, in); err == nil {
		t.Fatal("new six-character tag accepted")
	}
	in.Attributes["custom_tags"] = []string{"一二三四五"}
	p := r16Draft(t, a, u, in)
	id := p["id"].(string)
	_, err := a.DB.Exec(ctx, `UPDATE products SET attributes=jsonb_set(attributes,'{custom_tags}','["保留旧的超长标签"]') WHERE id=$1`, id)
	if err != nil {
		t.Fatal(err)
	}
	in.Attributes["custom_tags"] = []string{"保留旧的超长标签"}
	in.ExpectedVersion = 1
	p2, err := a.memberService().Edit(ctx, u, id, in)
	if err != nil {
		t.Fatal(err)
	}
	version := int(p2["data"].(map[string]any)["version"].(float64))
	other := member.Actor{ID: "another-member", Role: "member"}
	if _, err = a.memberService().DeleteDraft(ctx, other, id, version); err == nil {
		t.Fatal("non-owner deletion accepted")
	}
	if _, err = a.memberService().DeleteDraft(ctx, u, id, version-1); err == nil {
		t.Fatal("stale deletion accepted")
	}
	if _, err = a.memberService().DeleteDraft(ctx, u, id, version); err != nil {
		t.Fatal(err)
	}
	if _, err = a.memberService().Detail(ctx, u, id); err == nil {
		t.Fatal("deleted draft remains readable")
	}
	in.Attributes["custom_tags"] = []string{"正常"}
	p = r16Draft(t, a, u, in)
	id = p["id"].(string)
	_, err = a.DB.Exec(ctx, `UPDATE products SET published_at=now()-interval '1 day' WHERE id=$1`, id)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = a.memberService().DeleteDraft(ctx, u, id, 1); err == nil {
		t.Fatal("historically published draft deleted")
	}
}

func TestR16CleanupExpiryFreshUploadAndProtectedFiles(t *testing.T) {
	a, u, in := r16Fixture(t)
	ctx := context.Background()
	old := r16Draft(t, a, u, in)
	historical := r16Draft(t, a, u, in)
	recent := r16Draft(t, a, u, in)
	for _, p := range []map[string]any{old, historical, recent} {
		if _, err := a.DB.Exec(ctx, `UPDATE products SET created_at=now()-interval '4 days',draft_expires_at=now()-interval '1 hour' WHERE id=$1`, p["id"]); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := a.DB.Exec(ctx, `UPDATE products SET published_at=now()-interval '3 days' WHERE id=$1`, historical["id"]); err != nil {
		t.Fatal(err)
	}
	// Text edits do not refresh the deadline, while a successfully uploaded image does.
	in.ExpectedVersion = 1
	if _, err := a.memberService().Edit(ctx, u, old["id"].(string), in); err != nil {
		t.Fatal(err)
	}
	_, err := a.DB.Exec(ctx, `INSERT INTO media_files(id,owner_id,status,managed_upload,storage_key,ready_at,created_at) VALUES('r16-ready','r16-member','ready',true,'images/r16-ready.jpg',now(),now()-interval '4 days'),('r16-legacy','r16-member','ready',false,'legacy/keep',NULL,now()-interval '4 days')`)
	if err == nil {
		_, err = a.DB.Exec(ctx, `INSERT INTO product_images(product_id,media_id,sort_order) VALUES($1,'r16-legacy',1)`, old["id"])
	}
	if err != nil {
		t.Fatal(err)
	}
	if _, err = a.memberService().AppendImages(ctx, u, recent["id"].(string), member.AppendImagesInput{ExpectedVersion: 1, RequestID: newID(), MediaIDs: []string{"r16-ready"}}); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(a.Config.MediaDir, "legacy", "keep")
	if err = os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(path, []byte("original"), 0600); err != nil {
		t.Fatal(err)
	}
	svc := maintenance.Service{DB: a.DB, MediaDir: a.Config.MediaDir}
	result, err := svc.CleanupDrafts(ctx, 100)
	if err != nil {
		t.Fatal(err)
	}
	if result.Expired != 1 {
		t.Fatalf("wrong expiry count %+v", result)
	}
	if _, err = os.Stat(path); err != nil {
		t.Fatal("legacy original removed")
	}
	for _, p := range []map[string]any{historical, recent} {
		var alive bool
		if err = a.DB.QueryRow(ctx, "SELECT deleted_at IS NULL FROM products WHERE id=$1", p["id"]).Scan(&alive); err != nil || !alive {
			t.Fatal("protected draft removed")
		}
	}
}

func TestR16UploadQuotaIncludesAttachedDraftsAndReservations(t *testing.T) {
	a, u, in := r16Fixture(t)
	ctx := context.Background()
	p := r16Draft(t, a, u, in)
	_, err := a.DB.Exec(ctx, `INSERT INTO media_files(id,owner_id,status,managed_upload,byte_size,storage_key) VALUES('quota-old','r16-member','ready',true,$1,'images/quota-old.jpg')`, postgres.UploadByteLimit-100)
	if err == nil {
		_, err = a.DB.Exec(ctx, `INSERT INTO product_images(product_id,media_id,sort_order) VALUES($1,'quota-old',1)`, p["id"])
	}
	if err != nil {
		t.Fatal(err)
	}
	store := postgres.MediaStore{DB: a.DB}
	actor := media.Actor{ID: u.ID, Role: "member", Surface: "public"}
	start := make(chan struct{})
	result := make(chan error, 2)
	for _, id := range []string{"quota-a", "quota-b"} {
		go func(id string) {
			<-start
			result <- store.Create(ctx, actor, media.CreateInput{Filename: "test.jpg", MIME: "image/jpeg", Size: 80}, id, "uploads/"+id+".raw", "unused")
		}(id)
	}
	close(start)
	ok, busy := 0, 0
	for i := 0; i < 2; i++ {
		e := <-result
		if e == nil {
			ok++
		} else if f, yes := fault.As(e); yes && f.Code == "UPLOAD_QUOTA_EXCEEDED" {
			busy++
		} else {
			t.Fatal(e)
		}
	}
	if ok != 1 || busy != 1 {
		t.Fatalf("quota over-reserved: %d/%d", ok, busy)
	}
}

func TestR16GCRechecksReferencesAfterWaitingForMediaLock(t *testing.T) {
	a, u, in := r16Fixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	p := r16Draft(t, a, u, in)
	_, err := a.DB.Exec(ctx, `INSERT INTO media_files(id,owner_id,status,managed_upload,storage_key) VALUES('r16-race','r16-member','ready',true,'images/r16-race.jpg')`)
	if err != nil {
		t.Fatal(err)
	}
	blocker, err := a.DB.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer blocker.Rollback(context.Background())
	if _, err = blocker.Exec(ctx, `SELECT id FROM media_files WHERE id='r16-race' FOR UPDATE`); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		tx, e := a.DB.Begin(ctx)
		if e != nil {
			done <- e
			return
		}
		defer tx.Rollback(ctx)
		e = postgres.QueueManagedMediaGC(ctx, tx, "r16-race")
		if e == nil {
			e = tx.Commit(ctx)
		}
		done <- e
	}()
	time.Sleep(100 * time.Millisecond)
	if _, err = blocker.Exec(ctx, `INSERT INTO product_images(product_id,media_id,sort_order) VALUES($1,'r16-race',1)`, p["id"]); err != nil {
		t.Fatal(err)
	}
	if err = blocker.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err = <-done; err != nil {
		t.Fatal(err)
	}
	var alive bool
	if err = a.DB.QueryRow(ctx, `SELECT deleted_at IS NULL AND NOT EXISTS(SELECT 1 FROM media_gc_queue WHERE media_id='r16-race') FROM media_files WHERE id='r16-race'`).Scan(&alive); err != nil || !alive {
		t.Fatalf("new reference lost: %v", err)
	}
}

func TestR16MigrationMergesAllRegionsAndPreservesRecentDraft(t *testing.T) {
	a, u, in := r16Fixture(t)
	ctx := context.Background()
	p := r16Draft(t, a, u, in)
	_, err := a.DB.Exec(ctx, `DROP TABLE media_gc_queue,product_dictionary_options,product_dictionary_groups;
 DROP INDEX product_reviews_published_history_idx;
 ALTER TABLE products DROP COLUMN deleted_at CASCADE,DROP COLUMN draft_expires_at CASCADE,DROP COLUMN last_media_upload_at;
 ALTER TABLE media_files DROP COLUMN managed_upload CASCADE,DROP COLUMN ready_at,DROP COLUMN deleted_at CASCADE;
 DELETE FROM schema_migrations WHERE version='0007_shared_dictionaries_and_drafts.sql';
 INSERT INTO categories(id,name,filter_config) VALUES('r16-region-a','出售区服','[{"key":"server","options":[{"value":"region-a","label":"大区甲"}]}]'),('r16-region-b','出租区服','[{"key":"server","options":[{"value":"region-b","label":"大区乙"}]}]');`)
	if err != nil {
		t.Fatal(err)
	}
	_, err = a.DB.Exec(ctx, `UPDATE products SET attributes='{"server":"region-observed","gender":"female"}',created_at=now()-interval '5 days' WHERE id=$1`, p["id"])
	if err != nil {
		t.Fatal(err)
	}
	if _, err = a.DB.Exec(ctx, `INSERT INTO media_files(id,status) VALUES('migration-recent','ready')`); err != nil {
		t.Fatal(err)
	}
	if _, err = a.DB.Exec(ctx, `INSERT INTO product_images(product_id,media_id,sort_order) VALUES($1,'migration-recent',1)`, p["id"]); err != nil {
		t.Fatal(err)
	}
	if err = a.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	var count int
	var label string
	var fresh bool
	if err = a.DB.QueryRow(ctx, `SELECT count(*) FROM product_dictionary_options WHERE dictionary_key='server' AND value IN ('region-a','region-b','region-observed')`).Scan(&count); err != nil || count != 3 {
		t.Fatalf("incomplete region merge %d: %v", count, err)
	}
	if err = a.DB.QueryRow(ctx, `SELECT label FROM product_dictionary_options WHERE dictionary_key='gender' AND value='female'`).Scan(&label); err != nil || label != "女角色" {
		t.Fatalf("raw value replaced explicit label: %s %v", label, err)
	}
	if err = a.DB.QueryRow(ctx, `SELECT draft_expires_at>now()+interval '71 hours' FROM products WHERE id=$1`, p["id"]).Scan(&fresh); err != nil || !fresh {
		t.Fatal("recent upload draft immediately expired")
	}
}

func TestR16FailedGCDoesNotBlockOtherFiles(t *testing.T) {
	a, _, _ := r16Fixture(t)
	ctx := context.Background()
	_, err := a.DB.Exec(ctx, `INSERT INTO media_files(id,managed_upload,status,storage_key,deleted_at) VALUES('bad-key',true,'ready','legacy/do-not-touch',now()),('good-key',true,'ready','images/good-key.jpg',now());INSERT INTO media_gc_queue(media_id,storage_key) VALUES('bad-key','legacy/do-not-touch'),('good-key','images/good-key.jpg')`)
	if err != nil {
		t.Fatal(err)
	}
	svc := maintenance.Service{DB: a.DB, MediaDir: a.Config.MediaDir}
	out, err := svc.CleanupDrafts(ctx, 10)
	if err == nil || out.Removed != 1 {
		t.Fatalf("bad queue row blocked valid cleanup: %+v %v", out, err)
	}
	var completed bool
	if err = a.DB.QueryRow(ctx, `SELECT completed_at IS NOT NULL FROM media_gc_queue WHERE media_id='good-key'`).Scan(&completed); err != nil || !completed {
		t.Fatal("valid file not completed")
	}
}

func TestR16RequiredDraftFieldsLabelsAndDeletedReplay(t *testing.T) {
	a, u, in := r16Fixture(t)
	ctx := context.Background()
	in.Attributes["server"] = ""
	in.Attributes["gender"] = ""
	price := "100"
	in.PriceAmount = &price
	created, err := a.memberService().Create(ctx, u, in)
	if err != nil {
		t.Fatal(err)
	}
	p := created["data"].(map[string]any)
	id := p["id"].(string)
	if _, err = a.memberService().Transition(ctx, u, id, member.TransitionInput{ExpectedVersion: 1}, true); err == nil {
		t.Fatal("empty saved draft bypassed required choices")
	} else if f, ok := fault.As(err); !ok || f.Code != "INCOMPLETE_PRODUCT" {
		t.Fatalf("wrong required field error: %v", err)
	}
	if _, err = a.memberService().DeleteDraft(ctx, u, id, 1); err != nil {
		t.Fatal(err)
	}
	if _, err = a.memberService().Create(ctx, u, in); err == nil {
		t.Fatal("deleted create replay rebuilt draft")
	} else if f, ok := fault.As(err); !ok || f.Kind != fault.NotFound {
		t.Fatalf("wrong deleted replay error: %v", err)
	}
	in.RequestID = newID()
	in.Attributes["server"] = "r16-east"
	in.Attributes["gender"] = "female"
	p = r16Draft(t, a, u, in)
	labels, ok := p["attribute_labels"].(map[string]any)
	if !ok || labels["server"] != "东区" || labels["gender"] != "女角色" {
		t.Fatalf("display labels missing: %#v", p["attribute_labels"])
	}
	if p["attributes"].(map[string]any)["server"] != "r16-east" {
		t.Fatal("display label overwrote code")
	}
	if err := catalog.ValidateDictionary("server", catalog.DictionaryInput{ExpectedVersion: 1, Options: []catalog.DictionaryOption{{Value: "disabled", Label: "停用", Enabled: false}}}); err == nil {
		t.Fatal("all mandatory choices disabled")
	}
}
