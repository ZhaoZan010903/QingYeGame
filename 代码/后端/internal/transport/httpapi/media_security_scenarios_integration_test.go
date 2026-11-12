package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
	mediaapp "qingye/backend/internal/application/media"
	"qingye/backend/internal/application/medialifecycle"
	"qingye/backend/internal/domain/fault"
	"qingye/backend/internal/domain/media"
	dsecurity "qingye/backend/internal/domain/security"
	"qingye/backend/internal/infrastructure/cache"
	"qingye/backend/internal/infrastructure/mediafiles"
	"qingye/backend/internal/infrastructure/mediaobject"
	"qingye/backend/internal/infrastructure/postgres"
	"qingye/backend/internal/maintenance"
)

// These scenarios use real isolated PostgreSQL/Redis and synthetic 1–10 KB images.
// Object transport is in memory: the measurements are not OSS/server capacity claims.
type r20SecurityFixture struct {
	*chatFixture
	stage, published *lifecycleMemoryStore
	image            []byte
}

func newR20SecurityFixture(t *testing.T) *r20SecurityFixture {
	t.Helper()
	f := &r20SecurityFixture{chatFixture: newChatFixture(t), stage: &lifecycleMemoryStore{files: map[string][]byte{}}, published: &lifecycleMemoryStore{files: map[string][]byte{}}, image: r20SmallPNG(t)}
	files := &mediaobject.Files{Local: &mediafiles.Store{Root: f.a.Config.MediaDir}, Staging: f.stage, Published: f.published,
		Repository: &postgres.MediaLifecycleStore{DB: f.a.DB, NewID: newID}, Environment: "test", TempDir: t.TempDir(), NewID: newID}
	f.a.Config.MediaR20.Enabled, f.admin.Config.MediaR20.Enabled = true, true
	f.a.MediaFiles, f.admin.MediaFiles = files, files
	t.Cleanup(func() {
		for _, actor := range []string{f.member, f.other, f.first, f.second} {
			keys := []string{}
			for _, prefix := range []string{"upload:product:", "upload:chat:", "upload:avatar:", "upload:contact:", "upload-chunk:public:", "upload-chunk:admin:", "upload-status:"} {
				keys = append(keys, "qingye:limit:"+prefix+actor)
			}
			_ = f.a.Redis.Del(context.Background(), keys...).Err()
		}
	})
	return f
}

func (f *r20SecurityFixture) ticket(t *testing.T, actor, purpose, conversation string) map[string]any {
	t.Helper()
	return memberData(t, f.mediaCall(t, actor, "POST", "/api/v1/uploads", map[string]any{
		"filename": "security-synthetic.png", "mime_type": "image/png", "byte_size": len(f.image),
		"purpose": purpose, "conversation_id": conversation, "resumable": purpose == "product",
	}, http.StatusCreated))
}

func (f *r20SecurityFixture) finish(t *testing.T, actor, purpose string, ticket map[string]any, retry bool) string {
	t.Helper()
	id, url := ticket["media_id"].(string), ticket["upload_url"].(string)
	if purpose == "product" {
		header := fmt.Sprintf("bytes 0-%d/%d", len(f.image)-1, len(f.image))
		r20ChunkHTTP(f.chatFixture, t, actor, url, header, f.image, 200)
		if retry {
			// A lost response retries bytes, not POST /uploads. Both must keep one ID.
			r20ChunkHTTP(f.chatFixture, t, actor, url, header, f.image, 200)
		}
	} else {
		f.mediaCall(t, actor, "PUT", url, f.image, 200)
	}
	f.mediaCall(t, actor, "POST", "/api/v1/uploads/"+id+"/complete", nil, 200)
	if retry {
		f.mediaCall(t, actor, "POST", "/api/v1/uploads/"+id+"/complete", nil, 200)
	}
	return id
}

func (f *r20SecurityFixture) unblocked(t *testing.T, actor string) {
	t.Helper()
	r, err := (&postgres.UploadSecurityStore{DB: f.a.DB, NewID: newID}).Restriction(context.Background(), actor)
	if err != nil || r.Blocked || r.Version != 0 {
		t.Fatal("ordinary traffic changed persistent upload restriction", r, err)
	}
}

func r20LogSecuritySamples(t *testing.T, name string, samples []time.Duration, elapsed time.Duration, f *r20SecurityFixture) {
	t.Helper()
	ordered := append([]time.Duration(nil), samples...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i] < ordered[j] })
	percentile := func(percent int) float64 {
		if len(ordered) == 0 {
			return 0
		}
		index := (len(ordered)*percent+99)/100 - 1
		return float64(ordered[index]) / float64(time.Millisecond)
	}
	stats := f.a.DB.Stat()
	report, err := json.Marshal(map[string]any{
		"scenario": name, "samples": len(samples), "elapsed_ms": float64(elapsed) / float64(time.Millisecond),
		"p50_ms": percentile(50), "p95_ms": percentile(95), "max_ms": percentile(100),
		"db_total_connections": stats.TotalConns(), "db_acquired_connections": stats.AcquiredConns(),
		"db_empty_acquires": stats.EmptyAcquireCount(), "db_acquire_duration_ms": float64(stats.AcquireDuration()) / float64(time.Millisecond),
		"scope": "isolated-schema; real-PG-Redis; synthetic-small-images; in-memory-object-transport",
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Log("R20_SECURITY_MEASURE " + string(report))
}

func TestR20SecurityEverydayBatchesRetriesAndSharedIP(t *testing.T) {
	f := newR20SecurityFixture(t)
	ctx := context.Background()
	started := time.Now()
	samples := make([]time.Duration, 0, 324)
	// The browser batch boundary is 99; the per-file HTTP API does not infer it.
	// This models four accepted browser batches and actual ordered DB associations.
	product := newID()
	if _, err := f.a.DB.Exec(ctx, `INSERT INTO products(id,product_code,owner_id,category_id,title,trade_type,price_unit,status) VALUES($1,$1,$2,'chat-test','安全正常使用样本','sale','account','draft')`, product, f.member); err != nil {
		t.Fatal(err)
	}
	version, sequence, repeats := 1, 0, 0
	for _, batch := range []int{99, 99, 99, 3} {
		ids := make([]string, 0, batch)
		for i := 0; i < batch; i++ {
			begin := time.Now()
			retry := sequence%25 == 0
			if retry {
				repeats++
			}
			ids = append(ids, f.finish(t, f.member, "product", f.ticket(t, f.member, "product", ""), retry))
			samples = append(samples, time.Since(begin))
			sequence++
		}
		for start := 0; start < len(ids); start += 20 {
			end := min(start+20, len(ids))
			out := memberData(t, f.mediaCall(t, f.member, "POST", "/api/v1/me/products/"+product+"/images", map[string]any{"media_ids": ids[start:end], "expected_version": version, "request_id": newID()}, 200))
			version = int(out["version"].(float64))
		}
	}
	var total, unique, ready int
	if err := f.a.DB.QueryRow(ctx, `SELECT count(*),count(DISTINCT pi.media_id),count(*) FILTER(WHERE m.status='ready') FROM product_images pi JOIN media_files m ON m.id=pi.media_id WHERE pi.product_id=$1`, product).Scan(&total, &unique, &ready); err != nil || total != 300 || unique != 300 || ready != 300 {
		t.Fatal("retries duplicated/lost image associations", total, unique, ready, err)
	}
	createKey, chunkKey := "qingye:limit:upload:product:"+f.member, "qingye:limit:upload-chunk:public:"+f.member
	creates, err := f.a.Redis.Get(ctx, createKey).Int()
	if err != nil || creates != 300 {
		t.Fatal("retry charged new-create counter", creates, err)
	}
	chunks, err := f.a.Redis.Get(ctx, chunkKey).Int()
	if err != nil || chunks != 300+repeats {
		t.Fatal("chunk retry accounting", chunks, err)
	}
	for _, key := range []string{createKey, chunkKey} {
		ttl, err := f.a.Redis.TTL(ctx, key).Result()
		if err != nil || ttl <= 0 || ttl > 10*time.Minute {
			t.Fatal("rate key has no bounded TTL", ttl, err)
		}
	}
	// Continue on a second product, then another member behind the identical IP.
	for _, actor := range []string{f.member, f.other} {
		second := newID()
		if _, err := f.a.DB.Exec(ctx, `INSERT INTO products(id,product_code,owner_id,category_id,title,trade_type,price_unit,status) VALUES($1,$1,$2,'chat-test','第二商品','sale','account','draft')`, second, actor); err != nil {
			t.Fatal(err)
		}
		ids := []string{}
		for i := 0; i < 12; i++ {
			begin := time.Now()
			ids = append(ids, f.finish(t, actor, "product", f.ticket(t, actor, "product", ""), false))
			samples = append(samples, time.Since(begin))
		}
		f.mediaCall(t, actor, "POST", "/api/v1/me/products/"+second+"/images", map[string]any{"media_ids": ids, "expected_version": 1, "request_id": newID()}, 200)
		f.unblocked(t, actor)
	}
	var count, audit int
	if err := f.a.DB.QueryRow(ctx, `SELECT (SELECT count(*) FROM media_files WHERE purpose='product' AND lifecycle_managed),(SELECT count(*) FROM product_upload_restrictions WHERE blocked)`).Scan(&count, &audit); err != nil || count != 324 || audit != 0 {
		t.Fatal("normal activity changed security state", count, audit, err)
	}
	r20LogSecuritySamples(t, "324_full_uploads_with_300_in_four_batches", samples, time.Since(started), f)
}

func TestR20SecurityRedisFailureDoesNotBanOrCommit(t *testing.T) {
	f := newR20SecurityFixture(t)
	ctx := context.Background()
	ticket := f.ticket(t, f.member, "product", "")
	id := ticket["media_id"].(string)
	original := f.a.Redis
	failed := redis.NewClient(original.Options())
	_ = failed.Close() // Deterministic connection failure; never stops the shared Redis server.
	f.a.Redis = failed
	defer func() { f.a.Redis = original }()
	for i := 0; i < 3; i++ {
		f.mediaCall(t, f.member, "POST", "/api/v1/uploads", map[string]any{"filename": "unavailable.png", "mime_type": "image/png", "byte_size": len(f.image), "purpose": "product", "resumable": true}, 503)
		f.mediaCall(t, f.member, "GET", "/api/v1/uploads/"+id+"/status", nil, 503)
		r20ChunkHTTP(f.chatFixture, t, f.member, ticket["upload_url"].(string), fmt.Sprintf("bytes 0-%d/%d", len(f.image)-1, len(f.image)), f.image, 503)
	}
	var count, restrictions int
	var offset int64
	if err := f.a.DB.QueryRow(ctx, `SELECT (SELECT count(*) FROM media_files),(SELECT count(*) FROM product_upload_restrictions),upload_offset FROM media_files WHERE id=$1`, id).Scan(&count, &restrictions, &offset); err != nil || count != 1 || restrictions != 0 || offset != 0 {
		t.Fatal("Redis failure committed upload/ban", count, restrictions, offset, err)
	}
	f.a.Redis = original
	f.finish(t, f.member, "product", ticket, false)
	f.finish(t, f.member, "product", f.ticket(t, f.member, "product", ""), false)
	f.unblocked(t, f.member)
}

func TestR20SecurityConfirmedBanRevokesOnlyProductStaging(t *testing.T) {
	f := newR20SecurityFixture(t)
	ctx := context.Background()
	conversation := f.start(t, f.member, f.product)
	stageID := f.finish(t, f.member, "product", f.ticket(t, f.member, "product", ""), false)
	otherID := f.finish(t, f.other, "product", f.ticket(t, f.other, "product", ""), false)
	ossID := f.finish(t, f.member, "product", f.ticket(t, f.member, "product", ""), false)
	// A seeded, already-published location isolates the cleanup authorization rule.
	// Actual cloud migration is verified by the separate storage/lifecycle suite.
	if _, err := f.a.DB.Exec(ctx, `UPDATE media_objects SET store='oss',retain_until=NULL WHERE media_id=$1`, ossID); err != nil {
		t.Fatal(err)
	}
	for key, data := range f.stage.files {
		if bytes.Contains([]byte(key), []byte("/"+ossID+"/")) {
			f.published.files[key] = data
			delete(f.stage.files, key)
		}
	}
	legacyID := newID()
	legacyKey := "legacy/" + legacyID + ".png"
	if err := os.MkdirAll(filepath.Join(f.a.Config.MediaDir, "legacy"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(f.a.Config.MediaDir, filepath.FromSlash(legacyKey)), f.image, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := f.a.DB.Exec(ctx, `INSERT INTO media_files(id,owner_id,status,storage_key,mime_type,byte_size) VALUES($1,$2,'ready',$3,'image/png',$4)`, legacyID, f.member, legacyKey, len(f.image)); err != nil {
		t.Fatal(err)
	}
	old := f.ticket(t, f.member, "product", "")
	path := "/api/v1/admin/users/" + f.member + "/upload-restriction"
	body := map[string]any{"blocked": true, "expected_version": 0, "reason": "isolated confirmed-abuse fixture", "purge_staging": true}
	f.mediaCall(t, f.member, "PUT", path, body, 404)
	blocked := memberData(t, f.mediaCall(t, f.first, "PUT", path, body, 200))
	f.mediaCall(t, f.member, "GET", "/api/v1/uploads/"+old["media_id"].(string)+"/status", nil, 403)
	r20ChunkHTTP(f.chatFixture, t, f.member, old["upload_url"].(string), fmt.Sprintf("bytes 0-%d/%d", len(f.image)-1, len(f.image)), f.image, 403)
	worker := medialifecycle.Worker{Repository: &postgres.MediaLifecycleStore{DB: f.a.DB, NewID: newID}, Staging: f.stage, Published: f.published}
	for i := 0; i < 20; i++ {
		done, err := worker.RunOnce(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if !done {
			break
		}
		if i == 19 {
			t.Fatal("bounded cleanup did not settle")
		}
	}
	var deleted bool
	if err := f.a.DB.QueryRow(ctx, `SELECT deleted_at IS NOT NULL FROM media_files WHERE id=$1`, stageID).Scan(&deleted); err != nil || !deleted {
		t.Fatal("confirmed staging purge missing", err)
	}
	for _, id := range []string{otherID, ossID, legacyID} {
		if err := f.a.DB.QueryRow(ctx, `SELECT deleted_at IS NOT NULL FROM media_files WHERE id=$1`, id).Scan(&deleted); err != nil || deleted {
			t.Fatal("purge exceeded account/location boundary", id, err)
		}
	}
	if len(f.published.files) != 1 {
		t.Fatal("published display object was purged")
	}
	if raw, err := os.ReadFile(filepath.Join(f.a.Config.MediaDir, filepath.FromSlash(legacyKey))); err != nil || !bytes.Equal(raw, f.image) {
		t.Fatal("legacy file changed", err)
	}
	// Product-only restriction must not become a blanket login or attachment ban.
	for _, item := range []struct{ actor, purpose, conversation string }{{f.member, "chat", conversation}, {f.member, "avatar", ""}, {f.first, "contact", ""}} {
		id := f.finish(t, item.actor, item.purpose, f.ticket(t, item.actor, item.purpose, item.conversation), false)
		f.mediaCall(t, item.actor, "GET", "/api/v1/media/"+id+"/preview", nil, 200)
		var managed bool
		if err := f.a.DB.QueryRow(ctx, `SELECT lifecycle_managed FROM media_files WHERE id=$1`, id).Scan(&managed); err != nil || managed {
			t.Fatal("nonproduct purpose enrolled into product cleanup", err)
		}
	}
	f.mediaCall(t, f.member, "GET", "/api/v1/me/products", nil, 200)
	f.mediaCall(t, f.first, "PUT", path, map[string]any{"blocked": false, "expected_version": blocked["version"], "reason": "isolated recovery"}, 200)
	f.mediaCall(t, f.member, "GET", "/api/v1/uploads/"+old["media_id"].(string)+"/status", nil, 403)
	f.finish(t, f.member, "product", f.ticket(t, f.member, "product", ""), false)
}

func TestR20SecurityPerformanceLayerSamples(t *testing.T) {
	f := newR20SecurityFixture(t)
	ctx := context.Background()
	const iterations = 80
	measure := func(name string, fn func()) {
		t.Helper()
		fn() // One untimed warm-up; reported calls and cumulative DB stats are explicit.
		start := time.Now()
		samples := make([]time.Duration, 0, iterations)
		for i := 0; i < iterations; i++ {
			one := time.Now()
			fn()
			samples = append(samples, time.Since(one))
		}
		r20LogSecuritySamples(t, name, samples, time.Since(start), f)
		if f.a.DB.Stat().AcquiredConns() != 0 {
			t.Fatal("completed scenario retained DB connection")
		}
	}
	repository := &postgres.UploadSecurityStore{DB: f.a.DB, NewID: newID}
	measure("PG_security_restriction_read", func() {
		if _, err := repository.Restriction(ctx, f.member); err != nil {
			t.Fatal(err)
		}
	})
	key := "security-measure:" + newID()
	t.Cleanup(func() { _ = f.a.Redis.Del(ctx, "qingye:limit:"+key).Err() })
	measure("Redis_atomic_limit_script", func() {
		if err := (cache.Limiter{Client: f.a.Redis}).Allow(ctx, key, 1000, 10*time.Minute); err != nil {
			t.Fatal(err)
		}
	})
	ticket := f.ticket(t, f.member, "product", "")
	measure("HTTP_status_auth_Redis_PG_without_image_processing", func() {
		f.mediaCall(t, f.member, "GET", "/api/v1/uploads/"+ticket["media_id"].(string)+"/status", nil, 200)
	})
	local := &mediafiles.Store{Root: t.TempDir()}
	if err := os.MkdirAll(filepath.Join(local.Root, "uploads"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(local.Root, "uploads", "measure.raw"), f.image, 0600); err != nil {
		t.Fatal(err)
	}
	measure("local_PNG_decode_encode_disk_without_DB_Redis", func() {
		out, err := local.Sanitize(ctx, media.Upload{ID: "measure", Key: "uploads/measure.raw", MIME: "image/png", Size: int64(len(f.image)), Purpose: "product"})
		if err != nil {
			t.Fatal(err)
		}
		local.RemoveRaw(out.Key)
	})
	f.unblocked(t, f.member)
}

// A microbenchmark for the pure policy only; excludes DB, Redis, HTTP and files.
func BenchmarkR20SecurityRestrictionPolicy(b *testing.B) {
	in := dsecurity.RestrictInput{Blocked: true, Reason: "confirmed test", ExpectedVersion: 3, PurgeStaging: true}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if err := dsecurity.ValidateRestriction("admin", in); err != nil {
			b.Fatal(err)
		}
	}
}

type r20FailureFiles struct {
	mediaapp.Files
	capacity, receive, sanitize error
}

func (f *r20FailureFiles) CheckCapacity(ctx context.Context, size int64) error {
	if f.capacity != nil {
		return f.capacity
	}
	if checker, ok := f.Files.(mediaapp.CapacityChecker); ok {
		return checker.CheckCapacity(ctx, size)
	}
	return nil
}
func (f *r20FailureFiles) CheckProductCapacity(ctx context.Context, size int64) error {
	if f.capacity != nil {
		return f.capacity
	}
	if checker, ok := f.Files.(mediaapp.ProductCapacityChecker); ok {
		return checker.CheckProductCapacity(ctx, size)
	}
	return f.CheckCapacity(ctx, size)
}
func (f *r20FailureFiles) ReceiveChunk(ctx context.Context, key string, offset, size int64, reader io.Reader) (mediaapp.Chunk, error) {
	if f.receive != nil {
		return nil, f.receive
	}
	return f.Files.(mediaapp.ChunkFiles).ReceiveChunk(ctx, key, offset, size, reader)
}
func (f *r20FailureFiles) Receive(ctx context.Context, key string, size int64, reader io.Reader) (media.Staged, error) {
	if f.receive != nil {
		return nil, f.receive
	}
	return f.Files.Receive(ctx, key, size, reader)
}
func (f *r20FailureFiles) Sanitize(ctx context.Context, upload media.Upload) (media.Processed, error) {
	if f.sanitize != nil {
		return media.Processed{}, f.sanitize
	}
	return f.Files.Sanitize(ctx, upload)
}

type r20ObjectFailureStore struct {
	*lifecycleMemoryStore
	fail     bool
	capacity error
}

func (s *r20ObjectFailureStore) CheckCapacity(context.Context, int64) error { return s.capacity }

func (s *r20ObjectFailureStore) Put(ctx context.Context, key string, r io.Reader, size int64, mime, sha string) error {
	if s.fail && strings.HasSuffix(key, "/original") {
		return fault.New(fault.Unavailable, "STORAGE_FULL", "injected object storage exhaustion")
	}
	return s.lifecycleMemoryStore.Put(ctx, key, r, size, mime, sha)
}

func (f *r20SecurityFixture) textDraft(t *testing.T) string {
	t.Helper()
	if _, err := f.a.DB.Exec(context.Background(), `INSERT INTO product_dictionary_options(dictionary_key,value,label) VALUES('server','security-fixture','隔离区') ON CONFLICT DO NOTHING`); err != nil {
		t.Fatal(err)
	}
	out := memberData(t, f.mediaCall(t, f.member, "POST", "/api/v1/me/products", map[string]any{
		"category_id": "chat-test", "title": "存储失败仍保留文字", "description": "独立测试的完整文字资料", "trade_type": "sale", "price_unit": "account", "request_id": newID(),
		"attributes": map[string]any{"server": "security-fixture", "gender": "female", "level": "", "kind": "", "custom_tags": []string{}},
	}, 200))
	return out["id"].(string)
}

func (f *r20SecurityFixture) drainSecurityObjects(t *testing.T) {
	t.Helper()
	worker := medialifecycle.Worker{Repository: &postgres.MediaLifecycleStore{DB: f.a.DB, NewID: newID}, Staging: f.stage, Published: f.published}
	for i := 0; i < 100; i++ {
		done, err := worker.RunOnce(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if !done {
			return
		}
	}
	t.Fatal("fixture object cleanup did not settle")
}

func TestR20SecurityStorageFailurePreservesDraftAndNeverPromotesPartialMedia(t *testing.T) {
	for _, scenario := range []string{"preflight", "receive_after_preflight", "sanitize", "original_write", "ready_statement", "ready_commit"} {
		t.Run(scenario, func(t *testing.T) {
			f := newR20SecurityFixture(t)
			ctx := context.Background()
			product := f.textDraft(t)
			originalFiles := f.a.MediaFiles.(*mediaobject.Files)
			injected := &r20FailureFiles{Files: originalFiles}
			f.a.MediaFiles = injected
			full := fault.New(fault.Unavailable, "STORAGE_FULL", "injected disk capacity failure")
			if scenario == "preflight" {
				injected.capacity = full
				f.mediaCall(t, f.member, "POST", "/api/v1/uploads", map[string]any{"filename": "small.png", "mime_type": "image/png", "byte_size": len(f.image), "purpose": "product", "resumable": true}, 503)
				var reserved int
				if err := f.a.DB.QueryRow(ctx, `SELECT count(*) FROM media_files`).Scan(&reserved); err != nil || reserved != 0 {
					t.Fatal("capacity preflight reserved DB ticket", reserved, err)
				}
				injected.capacity = nil
			}
			ticket := f.ticket(t, f.member, "product", "")
			id := ticket["media_id"].(string)
			header := fmt.Sprintf("bytes 0-%d/%d", len(f.image)-1, len(f.image))
			if scenario == "receive_after_preflight" {
				injected.receive = full
				r20ChunkHTTP(f.chatFixture, t, f.member, ticket["upload_url"].(string), header, f.image, 503)
			} else {
				r20ChunkHTTP(f.chatFixture, t, f.member, ticket["upload_url"].(string), header, f.image, 200)
			}
			objectStore := &r20ObjectFailureStore{lifecycleMemoryStore: f.stage, fail: scenario == "original_write"}
			originalFiles.Staging = objectStore
			if scenario == "sanitize" {
				injected.sanitize = full
			}
			if scenario == "ready_statement" || scenario == "ready_commit" {
				if _, err := f.a.DB.Exec(ctx, `CREATE TABLE r20_security_ready_failure(media_id text PRIMARY KEY);
 CREATE FUNCTION r20_security_fail_ready() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN
 IF NEW.status='ready' AND EXISTS(SELECT 1 FROM r20_security_ready_failure WHERE media_id=NEW.id) THEN
 RAISE EXCEPTION 'injected ready transaction failure' USING ERRCODE='40001'; END IF; RETURN NEW; END $$;`); err != nil {
					t.Fatal(err)
				}
				trigger := `CREATE TRIGGER r20_security_ready_failure BEFORE UPDATE ON media_files FOR EACH ROW EXECUTE FUNCTION r20_security_fail_ready()`
				if scenario == "ready_commit" {
					trigger = `CREATE CONSTRAINT TRIGGER r20_security_ready_failure AFTER UPDATE ON media_files DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION r20_security_fail_ready()`
				}
				if _, err := f.a.DB.Exec(ctx, trigger); err != nil {
					t.Fatal(err)
				}
				if _, err := f.a.DB.Exec(ctx, `INSERT INTO r20_security_ready_failure VALUES($1)`, id); err != nil {
					t.Fatal(err)
				}
			}
			if scenario != "preflight" && scenario != "receive_after_preflight" {
				f.mediaCall(t, f.member, "POST", "/api/v1/uploads/"+id+"/complete", nil, 503)
			}
			var state string
			var readyObjects, associations int
			var processing bool
			if err := f.a.DB.QueryRow(ctx, `SELECT status,processing_token IS NOT NULL,(SELECT count(*) FROM media_objects WHERE media_id=$1),(SELECT count(*) FROM product_images WHERE media_id=$1) FROM media_files WHERE id=$1`, id).Scan(&state, &processing, &readyObjects, &associations); err != nil || state == "ready" || processing || readyObjects != 0 || associations != 0 {
				t.Fatal("failed upload became ready/attached or kept live processing", state, processing, readyObjects, associations, err)
			}
			f.mediaCall(t, f.member, "POST", "/api/v1/me/products/"+product+"/images", map[string]any{"media_ids": []string{id}, "expected_version": 1, "request_id": newID()}, 422)
			f.mediaCall(t, f.member, "POST", "/api/v1/me/products/"+product+"/submit", map[string]any{"expected_version": 1}, 422)
			var preserved bool
			if err := f.a.DB.QueryRow(ctx, `SELECT status='draft' AND deleted_at IS NULL AND title='存储失败仍保留文字' AND description='独立测试的完整文字资料' FROM products WHERE id=$1`, product).Scan(&preserved); err != nil || !preserved {
				t.Fatal("storage failure deleted valid text draft", err)
			}
			if scenario != "receive_after_preflight" {
				if raw, err := os.ReadFile(filepath.Join(f.a.Config.MediaDir, "uploads", id+".raw")); err != nil || !bytes.Equal(raw, f.image) {
					t.Fatal("retry source lost after failed completion", err)
				}
			}
			if scenario == "original_write" || strings.HasPrefix(scenario, "ready_") {
				var candidates int
				if err := f.a.DB.QueryRow(ctx, `SELECT count(*) FROM media_stage_candidates WHERE media_id=$1 AND expires_at<=now()`, id).Scan(&candidates); err != nil || candidates != 1 {
					t.Fatal("failed-attempt objects not tracked/due", candidates, err)
				}
			}
			injected.receive, injected.sanitize = nil, nil
			objectStore.fail = false
			if strings.HasPrefix(scenario, "ready_") {
				if _, err := f.a.DB.Exec(ctx, `DELETE FROM r20_security_ready_failure WHERE media_id=$1`, id); err != nil {
					t.Fatal(err)
				}
			}
			if scenario == "receive_after_preflight" {
				r20ChunkHTTP(f.chatFixture, t, f.member, ticket["upload_url"].(string), header, f.image, 200)
			}
			f.mediaCall(t, f.member, "POST", "/api/v1/uploads/"+id+"/complete", nil, 200)
			f.mediaCall(t, f.member, "POST", "/api/v1/uploads/"+id+"/complete", nil, 200)
			f.mediaCall(t, f.member, "POST", "/api/v1/me/products/"+product+"/images", map[string]any{"media_ids": []string{id}, "expected_version": 1, "request_id": newID()}, 200)
			f.drainSecurityObjects(t)
			var mediaRows, imageRows, objects int
			if err := f.a.DB.QueryRow(ctx, `SELECT (SELECT count(*) FROM media_files),(SELECT count(*) FROM product_images WHERE product_id=$1),(SELECT count(*) FROM media_objects WHERE media_id=$2 AND deleted_at IS NULL)`, product, id).Scan(&mediaRows, &imageRows, &objects); err != nil || mediaRows != 1 || imageRows != 1 || objects != 1 || len(f.stage.files) != 1 {
				t.Fatal("retry duplicated records or leaked failed generation", mediaRows, imageRows, objects, len(f.stage.files), err)
			}
		})
	}
}

func TestR20SecurityReadyCancellationAndAppendRollbackAreReferenceSafe(t *testing.T) {
	f := newR20SecurityFixture(t)
	ctx := context.Background()
	product := f.textDraft(t)
	unattached := f.finish(t, f.member, "product", f.ticket(t, f.member, "product", ""), false)
	attached := f.finish(t, f.member, "product", f.ticket(t, f.member, "product", ""), false)
	// Fail the append after its association statement, inside the same real PG transaction.
	if _, err := f.a.DB.Exec(ctx, `CREATE FUNCTION r20_security_fail_append() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'injected append failure' USING ERRCODE='40001'; END $$;
 CREATE TRIGGER r20_security_fail_append AFTER INSERT ON product_images FOR EACH ROW EXECUTE FUNCTION r20_security_fail_append()`); err != nil {
		t.Fatal(err)
	}
	f.mediaCall(t, f.member, "POST", "/api/v1/me/products/"+product+"/images", map[string]any{"media_ids": []string{unattached}, "expected_version": 1, "request_id": newID()}, 503)
	var intact bool
	if err := f.a.DB.QueryRow(ctx, `SELECT status='ready' AND NOT EXISTS(SELECT 1 FROM product_images WHERE media_id=$1) AND (SELECT count(*)=1 FROM media_objects WHERE media_id=$1 AND product_id IS NULL AND deleted_at IS NULL AND NOT force_delete) FROM media_files WHERE id=$1`, unattached).Scan(&intact); err != nil || !intact || len(f.stage.files) != 2 {
		t.Fatal("append rollback damaged source images", err)
	}
	if _, err := f.a.DB.Exec(ctx, `DROP TRIGGER r20_security_fail_append ON product_images`); err != nil {
		t.Fatal(err)
	}
	f.mediaCall(t, f.other, "DELETE", "/api/v1/uploads/"+unattached, nil, 404)
	f.mediaCall(t, f.member, "DELETE", "/api/v1/uploads/"+unattached, nil, 200)
	f.mediaCall(t, f.member, "DELETE", "/api/v1/uploads/"+unattached, nil, 200)
	if len(f.stage.files) != 2 {
		t.Fatal("Cancel performed unsafe inline object deletion")
	}
	f.mediaCall(t, f.member, "POST", "/api/v1/me/products/"+product+"/images", map[string]any{"media_ids": []string{attached}, "expected_version": 1, "request_id": newID()}, 200)
	f.mediaCall(t, f.member, "DELETE", "/api/v1/uploads/"+attached, nil, 409)
	f.drainSecurityObjects(t)
	if len(f.stage.files) != 1 {
		t.Fatal("ready cancellation deleted attached media or retained unused media", len(f.stage.files))
	}
	var charged int64
	if err := f.a.DB.QueryRow(ctx, `SELECT coalesce(sum(byte_size),0) FROM media_files WHERE owner_id=$1 AND status<>'failed' AND deleted_at IS NULL`, f.member).Scan(&charged); err != nil {
		t.Fatal(err)
	}
	var expected int64
	if err := f.a.DB.QueryRow(ctx, `SELECT byte_size FROM media_files WHERE id=$1 AND status='ready'`, attached).Scan(&expected); err != nil || charged != expected {
		t.Fatal("canceled upload still consumes logical quota", charged, expected, err)
	}
	if _, err := (&maintenance.Service{DB: f.a.DB, MediaDir: f.a.Config.MediaDir}).CleanupDrafts(ctx, 100); err != nil {
		t.Fatal(err)
	}
	f.mediaCall(t, f.member, "GET", "/api/v1/media/"+attached+"/preview", nil, 200)
}

func TestR20SecurityInvalidImageReleasesReservationAndPreservesText(t *testing.T) {
	f := newR20SecurityFixture(t)
	ctx := context.Background()
	product := f.textDraft(t)
	valid := append([]byte(nil), f.image...)
	f.image = f.image[:len(f.image)/2] // A bounded, truncated PNG; still within the synthetic 1–10 KB range.
	if len(f.image) < 1024 {
		t.Fatal("malformed fixture unexpectedly small")
	}
	ticket := f.ticket(t, f.member, "product", "")
	id := ticket["media_id"].(string)
	r20ChunkHTTP(f.chatFixture, t, f.member, ticket["upload_url"].(string), fmt.Sprintf("bytes 0-%d/%d", len(f.image)-1, len(f.image)), f.image, 200)
	f.mediaCall(t, f.member, "POST", "/api/v1/uploads/"+id+"/complete", nil, 422)
	var state string
	var charged int64
	var objects, candidates int
	if err := f.a.DB.QueryRow(ctx, `SELECT status,(SELECT coalesce(sum(byte_size),0) FROM media_files WHERE owner_id=$2 AND status<>'failed' AND deleted_at IS NULL),(SELECT count(*) FROM media_objects),(SELECT count(*) FROM media_stage_candidates) FROM media_files WHERE id=$1`, id, f.member).Scan(&state, &charged, &objects, &candidates); err != nil || state != "failed" || charged != 0 || objects != 0 || candidates != 0 || len(f.stage.files) != 0 {
		t.Fatal("invalid image reserved quota or object", state, charged, objects, candidates, err)
	}
	if _, err := os.Stat(filepath.Join(f.a.Config.MediaDir, "uploads", id+".raw")); !os.IsNotExist(err) {
		t.Fatal("invalid raw survived confirmed rejection", err)
	}
	var preserved bool
	if err := f.a.DB.QueryRow(ctx, `SELECT status='draft' AND deleted_at IS NULL AND title='存储失败仍保留文字' FROM products WHERE id=$1`, product).Scan(&preserved); err != nil || !preserved {
		t.Fatal("invalid image removed user's text draft", err)
	}
	f.image = valid
	f.finish(t, f.member, "product", f.ticket(t, f.member, "product", ""), false)
	f.unblocked(t, f.member)
}

func TestR20SecurityReadyCancelRacingAppendCannotDeleteTheWinner(t *testing.T) {
	f := newR20SecurityFixture(t)
	ctx := context.Background()
	product := f.textDraft(t)
	for iteration := 0; iteration < 12; iteration++ {
		id := f.finish(t, f.member, "product", f.ticket(t, f.member, "product", ""), false)
		var version int
		if err := f.a.DB.QueryRow(ctx, `SELECT version FROM products WHERE id=$1`, product).Scan(&version); err != nil {
			t.Fatal(err)
		}
		start := make(chan struct{})
		type outcome struct {
			action string
			status int
		}
		results := make(chan outcome, 2)
		go func() {
			<-start
			w := surfaceRequest(t, f.pub, "POST", "/api/v1/me/products/"+product+"/images", "http://qingye.test", map[string]any{"media_ids": []string{id}, "expected_version": version, "request_id": newID()}, f.cookies[f.member])
			results <- outcome{"append", w.Code}
		}()
		go func() {
			<-start
			w := surfaceRequest(t, f.pub, "DELETE", "/api/v1/uploads/"+id, "http://qingye.test", nil, f.cookies[f.member])
			results <- outcome{"cancel", w.Code}
		}()
		close(start)
		observed := map[string]int{}
		for i := 0; i < 2; i++ {
			select {
			case result := <-results:
				observed[result.action] = result.status
			case <-time.After(10 * time.Second):
				t.Fatal("cancel/append lock order did not settle")
			}
		}
		var linked, ready, forced bool
		if err := f.a.DB.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM product_images WHERE media_id=$1),status='ready',EXISTS(SELECT 1 FROM media_objects WHERE media_id=$1 AND force_delete) FROM media_files WHERE id=$1`, id).Scan(&linked, &ready, &forced); err != nil {
			t.Fatal(err)
		}
		if observed["append"] == 200 {
			if observed["cancel"] != 409 || !linked || !ready || forced {
				t.Fatal("cancel destroyed committed association", observed, linked, ready, forced)
			}
		} else if observed["cancel"] == 200 {
			if observed["append"] != 422 || linked || ready || !forced {
				t.Fatal("append resurrected canceled media", observed, linked, ready, forced)
			}
		} else {
			t.Fatal("both ordinary contenders failed", observed)
		}
	}
	f.drainSecurityObjects(t)
	var preserved int
	if err := f.a.DB.QueryRow(ctx, `SELECT count(*) FROM product_images pi JOIN media_objects o ON o.media_id=pi.media_id WHERE pi.product_id=$1 AND (o.deleted_at IS NOT NULL OR o.force_delete)`, product).Scan(&preserved); err != nil || preserved != 0 {
		t.Fatal("cleanup deleted the associated race winner", preserved, err)
	}
}

func TestR20SecurityProductCapacityDoesNotBlockOtherMediaPurposes(t *testing.T) {
	f := newR20SecurityFixture(t)
	conversation := f.start(t, f.member, f.product)
	f.a.MediaFiles.(*mediaobject.Files).Staging = &r20ObjectFailureStore{lifecycleMemoryStore: f.stage, capacity: fault.New(fault.Unavailable, "STORAGE_FULL", "isolated product store full")}
	f.mediaCall(t, f.member, "POST", "/api/v1/uploads", map[string]any{"filename": "small.png", "mime_type": "image/png", "byte_size": len(f.image), "purpose": "product", "resumable": true}, 503)
	for _, item := range []struct{ actor, purpose, conversation string }{{f.member, "chat", conversation}, {f.member, "avatar", ""}, {f.first, "contact", ""}} {
		f.finish(t, item.actor, item.purpose, f.ticket(t, item.actor, item.purpose, item.conversation), false)
	}
	var products, other int
	if err := f.a.DB.QueryRow(context.Background(), `SELECT count(*) FILTER(WHERE purpose='product'),count(*) FILTER(WHERE purpose IN ('chat','avatar','contact') AND status='ready' AND NOT lifecycle_managed) FROM media_files`).Scan(&products, &other); err != nil || products != 0 || other != 3 {
		t.Fatal("product-store capacity leaked into unrelated purposes", products, other, err)
	}
}
