package postgres_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"qingye/backend/internal/application/legacysync"
	"qingye/backend/internal/application/medialifecycle"
	"qingye/backend/internal/domain/catalog"
	"qingye/backend/internal/domain/legacycatalog"
	"qingye/backend/internal/infrastructure/mediafiles"
	"qingye/backend/internal/infrastructure/postgres"
	"qingye/backend/internal/infrastructure/security"
	"qingye/backend/internal/maintenance"
	"qingye/backend/internal/transport/httpapi"
)

type testLegacySource struct {
	inventory legacysync.Inventory
	images    map[string][]byte
	fetches   int
	reads     int
	second    *legacysync.Inventory
	err       error
	onImage   func(string) error
}

func (s *testLegacySource) Catalog(_ context.Context, progress func(int, int) error) (legacysync.Inventory, error) {
	s.reads++
	if progress != nil {
		if err := progress(1, 1); err != nil {
			return legacysync.Inventory{}, err
		}
	}
	if s.second != nil && s.reads == 2 {
		return *s.second, nil
	}
	return s.inventory, s.err
}

func (s *testLegacySource) Image(_ context.Context, url string, w io.Writer) (int64, error) {
	s.fetches++
	if s.onImage != nil {
		if err := s.onImage(url); err != nil {
			return 0, err
		}
	}
	data, ok := s.images[url]
	if !ok {
		return 0, errors.New("no source image")
	}
	return io.Copy(w, bytes.NewReader(data))
}

type testLegacyObjects map[string][]byte

func (objects testLegacyObjects) Put(ctx context.Context, key string, r io.Reader, size int64, mime, sha string) error {
	if _, exists := objects[key]; exists || mime != "image/png" {
		return errors.New("unexpected object overwrite or format")
	}
	data, err := io.ReadAll(r)
	if err != nil {
		return err
	}
	objects[key] = data
	return objects.Verify(ctx, key, size, sha)
}

func (objects testLegacyObjects) Verify(_ context.Context, key string, size int64, sha string) error {
	if data := objects[key]; int64(len(data)) != size || fmt.Sprintf("%x", sha256.Sum256(data)) != sha {
		return errors.New("object checksum mismatch")
	}
	return nil
}

func legacySyncDB(t *testing.T, ctx context.Context) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("TEST_LEGACY_SYNC_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_LEGACY_SYNC_DATABASE_URL is not set")
	}
	server, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(server.Close)
	schema := "legacy_sync_test_" + strings.ReplaceAll(security.NewID(), "-", "")
	name := pgx.Identifier{schema}.Sanitize()
	if _, err = server.Exec(ctx, "CREATE SCHEMA "+name); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if _, err := server.Exec(ctx, "DROP SCHEMA "+name+" CASCADE"); err != nil {
			t.Errorf("drop isolated test schema: %v", err)
		}
	})
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	cfg.ConnConfig.RuntimeParams["search_path"] = schema
	db, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	if err = (&maintenance.Service{DB: db}).Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	return db
}

func fakeListing(id, code, title, category, price, picture string, now time.Time) legacysync.Record {
	return legacysync.Record{LegacyRecord: legacycatalog.LegacyRecord{
		ID: id, Code: code, Title: title, Category: category, Price: json.Number(price),
		Content: picture, Status: "出售中", Created: now.Add(-time.Hour), Updated: now,
	}}
}

type gatedLegacySource struct {
	*testLegacySource
	entered chan struct{}
	release chan struct{}
}

func (s gatedLegacySource) Catalog(ctx context.Context, progress func(int, int) error) (legacysync.Inventory, error) {
	if err := progress(1, 2); err != nil {
		return legacysync.Inventory{}, err
	}
	close(s.entered)
	select {
	case <-s.release:
		return s.testLegacySource.Catalog(ctx, nil)
	case <-ctx.Done():
		return legacysync.Inventory{}, ctx.Err()
	}
}

func waitLegacyBatch(t *testing.T, ctx context.Context, store postgres.LegacySyncStore, id, status string) legacysync.Batch {
	t.Helper()
	for {
		batch, _, _, err := store.Details(ctx, id, "", 1, 0)
		if err != nil {
			t.Fatal(err)
		}
		if batch.Status == status {
			return batch
		}
		if batch.Status != "running" {
			t.Fatalf("batch %s is %s, want %s: %+v", id, batch.Status, status, batch)
		}
		select {
		case <-ctx.Done():
			t.Fatalf("batch %s is %s, want %s", id, batch.Status, status)
		case <-time.After(20 * time.Millisecond):
		}
	}
}

func TestManualLegacySyncRunner(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	db := legacySyncDB(t, ctx)
	store := postgres.LegacySyncStore{DB: db}
	const actor = "sync-operator"
	if _, err := db.Exec(ctx, `INSERT INTO users(id,phone,nickname,password_hash,role)
 VALUES('sync-operator','20001','管理员','fixture','admin'),('sync-member','20002','会员','fixture','member');
 INSERT INTO categories(id,name,trade_type,price_units) VALUES('legacy-sale','QQ炫舞买号','sale','["account"]')`); err != nil {
		t.Fatal(err)
	}
	const pictureURL = "https://cdn.xuansiwei.com/manual-fixture.png"
	record := fakeListing("manual-sale", "QYCH190", "首次同步", "QQ炫舞买号", "1500", pictureURL, time.Now())
	source := &testLegacySource{
		inventory: legacysync.Inventory{Records: []legacysync.Record{record}},
		images:    map[string][]byte{pictureURL: legacyImage(t, 10)},
	}
	temp := t.TempDir()
	objects := testLegacyObjects{}
	newRunner := func(task context.Context, source legacysync.Source) *legacysync.ManualRunner {
		return legacysync.NewManualRunner(task, &legacysync.Service{
			Source: source, Store: store, Objects: objects,
			Images: mediafiles.LegacyImages{Directory: temp}, Environment: "test", NewID: security.NewID,
		})
	}
	gate := gatedLegacySource{source, make(chan struct{}), make(chan struct{})}
	task, stop := context.WithCancel(ctx)
	runner := newRunner(task, gate)
	defer func() { stop(); runner.Wait() }()
	requestID := security.NewID()
	if _, err := runner.Start("sync-member", requestID); err == nil {
		t.Fatal("member started a manual sync")
	}

	const origin = "http://127.0.0.1:5181"
	token := security.RandomToken()
	if _, err := db.Exec(ctx, `INSERT INTO sessions(token_hash,user_id,expires_at,audience)
 VALUES($1,$2,now()+interval '1 hour','admin')`, security.TokenHash(token), actor); err != nil {
		t.Fatal(err)
	}
	router := (&httpapi.App{DB: db, LegacySync: runner, Config: httpapi.Config{
		Surface: "admin", AllowedOrigins: []string{origin},
	}}).Routes()
	request := func(method, path string, body []byte) *httptest.ResponseRecorder {
		req := httptest.NewRequestWithContext(ctx, method, path, bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Origin", origin)
		req.AddCookie(&http.Cookie{Name: "qingye_admin_session", Value: token})
		out := httptest.NewRecorder()
		router.ServeHTTP(out, req)
		return out
	}
	body, err := json.Marshal(map[string]any{"request_id": requestID})
	if err != nil {
		t.Fatal(err)
	}
	const batches = "/api/v1/admin/legacy-sync/batches"
	recorder := request(http.MethodPost, batches, body)
	var response struct{ Data legacysync.Batch }
	if err = json.Unmarshal(recorder.Body.Bytes(), &response); err != nil || recorder.Code != http.StatusAccepted ||
		response.Data.ID != requestID || response.Data.Status != "running" {
		t.Fatalf("manual request was not accepted: %d %s, %v", recorder.Code, recorder.Body.String(), err)
	}
	select {
	case <-gate.entered:
	case <-ctx.Done():
		t.Fatal("catalog worker did not run")
	}
	replay, err := runner.Start(actor, requestID)
	if err != nil || replay.ID != requestID {
		t.Fatalf("retry lost the active batch: %+v, %v", replay, err)
	}
	if _, err := newRunner(ctx, source).Start(actor, security.NewID()); !errors.Is(err, legacysync.ErrBusy) {
		t.Fatalf("concurrent runner was not rejected: %v", err)
	}
	recorder = request(http.MethodGet, batches, nil)
	var observed struct {
		Data struct {
			Latest   legacysync.Batch    `json:"latest_batch"`
			Controls legacysync.Controls `json:"controls"`
		}
	}
	if err = json.Unmarshal(recorder.Body.Bytes(), &observed); err != nil || recorder.Code != http.StatusOK ||
		observed.Data.Latest.ID != requestID || observed.Data.Latest.Progress != (legacysync.Progress{Stage: legacysync.ReadSource, Completed: 1, Total: 2}) ||
		observed.Data.Controls.ScheduleEnabled || recorder.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("actual progress not returned by API: %d %s, %v", recorder.Code, recorder.Body.String(), err)
	}
	close(gate.release)
	published := waitLegacyBatch(t, ctx, store, requestID, "applied")
	runner.Wait()
	if published.Progress.Stage != legacysync.Done || published.PlannedAt != nil || published.Summary.Created != 1 || published.AppliedAt == nil {
		t.Fatalf("manual button did not commit: %+v", published)
	}
	var started, publishedByOperator int
	if err = db.QueryRow(ctx, `SELECT
 (SELECT count(*) FROM admin_logs WHERE action='legacy.sync.start' AND resource_id=$1 AND user_id=$2),
 (SELECT count(*) FROM legacy_sync_batches WHERE id=$1 AND applied_by=$2)`,
		requestID, actor).Scan(&started, &publishedByOperator); err != nil || started != 1 || publishedByOperator != 1 {
		t.Fatalf("operator audit was not retained: %d, %d, %v", started, publishedByOperator, err)
	}
	recorder = request(http.MethodGet, batches+"/"+requestID+"?filter=offline", nil)
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), `"items":[]`) {
		t.Fatalf("result filter returned unrelated changes: %d %s", recorder.Code, recorder.Body.String())
	}
	if stale := request(http.MethodPost, batches+"/"+requestID+"/apply", []byte(`{"confirm":true}`)); stale.Code != http.StatusNotFound {
		t.Fatalf("retired preview route is still reachable: %d", stale.Code)
	}

	expiredID := security.NewID()
	if _, started, err := store.BeginManualBatch(ctx, expiredID, actor); err != nil || !started {
		t.Fatalf("reserve interrupted job: %v", err)
	}
	if _, err := db.Exec(ctx, `UPDATE legacy_sync_batches SET heartbeat_at=now()-interval '91 seconds' WHERE id=$1`, expiredID); err != nil {
		t.Fatal(err)
	}
	if err := store.Heartbeat(ctx, expiredID); !errors.Is(err, legacysync.ErrLeaseLost) {
		t.Fatalf("stale worker revived a lease: %v", err)
	}
	waitLegacyBatch(t, ctx, store, expiredID, "failed")
	shutdown, stopWorker := context.WithCancel(ctx)
	interrupted := newRunner(shutdown, gatedLegacySource{source, make(chan struct{}), make(chan struct{})})
	next, err := interrupted.Start(actor, security.NewID())
	stopWorker()
	if err != nil {
		t.Fatal(err)
	}
	interrupted.Wait()
	waitLegacyBatch(t, ctx, store, next.ID, "failed")
}

func legacyImage(t *testing.T, red byte) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 8, 8))
	img.Set(0, 0, color.RGBA{R: red, A: 255})
	var data bytes.Buffer
	if err := png.Encode(&data, img); err != nil {
		t.Fatal(err)
	}
	return data.Bytes()
}

// Start from an applied catalog, replace one listing with another, then roll
// that batch back after an unchanged run. Counts alone cannot distinguish
// these catalogs. Both the product IDs and the ordered galleries matter.
func TestLegacySyncMixedBatchRepeatAndRollback(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	db := legacySyncDB(t, ctx)
	store := postgres.LegacySyncStore{DB: db}
	const actor = "catalog-operator"
	if _, err := db.Exec(ctx, `INSERT INTO users(id,phone,nickname,password_hash,role)
 VALUES('catalog-operator','21001','管理员','fixture','admin');
 INSERT INTO categories(id,name,trade_type,price_units) VALUES('legacy-sale','QQ炫舞买号','sale','["account"]')`); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	const cdn = "https://cdn.xuansiwei.com/"
	leaving := fakeListing("leaving", "QYCH11", "原有商品", "QQ炫舞买号", "200", cdn+"leaving.png", now)
	staying := fakeListing("staying", "QYCH12", "保留商品", "QQ炫舞买号", "300", cdn+"staying.png", now)
	entering := fakeListing("entering", "QYCH13", "新上架商品", "QQ炫舞买号", "400", cdn+"entering.png", now)
	source := &testLegacySource{
		inventory: legacysync.Inventory{Records: []legacysync.Record{leaving, staying}, Latest: &now},
		images: map[string][]byte{
			leaving.Content: legacyImage(t, 10), staying.Content: legacyImage(t, 20), entering.Content: legacyImage(t, 30),
		},
	}
	objects := testLegacyObjects{}
	service := &legacysync.Service{Source: source, Store: store, Objects: objects, Images: mediafiles.LegacyImages{Directory: t.TempDir()},
		Environment: "test", NewID: security.NewID}
	manual := func(id, status string) legacysync.Batch {
		t.Helper()
		runner := legacysync.NewManualRunner(ctx, service)
		if _, err := runner.Start(actor, id); err != nil {
			t.Fatal(err)
		}
		runner.Wait()
		return waitLegacyBatch(t, ctx, store, id, status)
	}
	catalogState := func() map[string]legacysync.Current {
		t.Helper()
		rows, _, _, err := store.LoadCatalog(ctx)
		if err != nil {
			t.Fatal(err)
		}
		return rows
	}
	assertImages := func(want int) {
		t.Helper()
		var mediaRows int
		if err := db.QueryRow(ctx, `SELECT count(*) FROM media_files`).Scan(&mediaRows); err != nil {
			t.Fatal(err)
		}
		if source.fetches != want || len(objects) != want || mediaRows != want {
			t.Fatalf("image downloads/objects/media rows = %d/%d/%d, want %d each", source.fetches, len(objects), mediaRows, want)
		}
	}
	manual(security.NewID(), "applied")
	assertImages(2)

	staying.Title = "商品改名，图片未变"
	source.inventory.Records = []legacysync.Record{staying, entering}
	mixed := manual(security.NewID(), "applied")
	if mixed.Summary.Created != 1 || mixed.Summary.Updated != 1 || mixed.Summary.Removed != 1 || mixed.RollbackUntil == nil {
		t.Fatalf("expected a single batch containing the replacement and rename: %+v", mixed)
	}
	after := catalogState()
	if after[leaving.ID].Status != "offline" || after[entering.ID].Status != "published" || after[staying.ID].Title != staying.Title {
		t.Fatal("the public catalog does not reflect the mixed batch")
	}
	assertImages(3)

	source.inventory.Records = []legacysync.Record{entering, staying}
	unchanged := manual(security.NewID(), "no_change")
	_, _, detailCount, err := store.Details(ctx, unchanged.ID, "", 1, 0)
	if err != nil || detailCount != 0 || unchanged.RollbackUntil != nil || unchanged.AppliedAt != nil ||
		unchanged.Progress.Stage != legacysync.Done || len(mixed.SourceDigest) != 64 || unchanged.SourceDigest != mixed.SourceDigest {
		t.Fatalf("unexpected unchanged batch details: count=%d batch=%+v err=%v", detailCount, unchanged, err)
	}
	reads := source.reads
	manual(unchanged.ID, "no_change")
	if reads != source.reads {
		t.Fatal("retrying the same manual request ran a second worker")
	}
	for id, current := range catalogState() {
		if current.Version != after[id].Version || !slices.Equal(current.Images, after[id].Images) {
			t.Fatalf("unchanged sync modified product %s", id)
		}
	}
	assertImages(3)

	if err := store.RollbackBatch(ctx, mixed.ID, actor); err != nil {
		t.Fatal(err)
	}
	rolled := catalogState()
	if rolled[leaving.ID].Status != "published" || rolled[entering.ID].Status != "draft" || rolled[staying.ID].Title == staying.Title {
		t.Fatal("rollback after an unchanged run failed to restore all three listings")
	}
	reapplied := manual(security.NewID(), "applied")
	if reapplied.Summary.Created != 0 || reapplied.Summary.Restored != 1 || reapplied.Summary.Removed != 1 || reapplied.Summary.Updated != 1 {
		t.Fatalf("replay should restore the same listing and remove its predecessor: %+v", reapplied)
	}
	replayed := catalogState()
	if len(replayed) != len(after) {
		t.Fatalf("replay changed product count: %d vs %d", len(replayed), len(after))
	}
	for id, before := range after {
		current := replayed[id]
		if current.ID != before.ID || current.Title != before.Title || current.Status != before.Status ||
			!slices.Equal(current.Images, before.Images) {
			t.Fatalf("replay changed the identity, data or image references of %s", id)
		}
	}
	assertImages(3)

	// The new title can be applied using the existing gallery while the image
	// host is unavailable. Identical catalog metadata on the next run must
	// still retry that image and replace the old reference when it recovers.
	entering.Title = "更换图片的商品"
	entering.Content = cdn + "recovered.png"
	source.inventory.Records[0] = entering
	withOldGallery := manual(security.NewID(), "applied")
	held := catalogState()[entering.ID]
	if withOldGallery.Summary.Review == 0 || held.Title != entering.Title ||
		!slices.Equal(held.Images, replayed[entering.ID].Images) || source.fetches != 4 || len(objects) != 3 {
		t.Fatalf("transient image failure did not retain the old gallery: %+v", withOldGallery)
	}
	source.images[entering.Content] = legacyImage(t, 40)
	recovered := manual(security.NewID(), "applied")
	ready := catalogState()[entering.ID]
	if recovered.Summary.Imaged != 1 || len(ready.Images) != 1 || ready.Images[0] == held.Images[0] ||
		ready.ImagesByURL[entering.Content] != ready.Images[0] || source.fetches != 5 || len(objects) != 4 {
		t.Fatalf("identical source catalog suppressed a pending image retry: %+v", recovered)
	}
}

func TestLegacySyncTransactions(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	db := legacySyncDB(t, ctx)
	store := postgres.LegacySyncStore{DB: db}
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := db.Exec(ctx, sql, args...); err != nil {
			t.Fatal(err)
		}
	}
	check := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	const actor = "sync-admin"
	now := time.Now().UTC().Truncate(time.Second)
	sha := strings.Repeat("a", 64)
	exec(`INSERT INTO users(id,phone,nickname,password_hash,role)
 VALUES('sync-admin','100001','合成管理员','fixture','admin'),('sync-member','100002','合成会员','fixture','member');
 INSERT INTO categories(id,name,trade_type,price_units) VALUES
 ('legacy-sale','QQ炫舞买号','sale','["account"]'),
 ('legacy-short-rent','时日租号','rent','["hour","day","period"]');
 INSERT INTO products(id,product_code,category_id,trade_type,price_unit,status,source,approved_version)
 VALUES('native','QYCH500','legacy-sale','sale','account','published','platform',1)`)
	const oldImage = "https://cdn.xuansiwei.com/test-original.png"
	const replacement = "https://cdn.xuansiwei.com/test-replacement.png"
	const otherPicture = "https://cdn.xuansiwei.com/test-other.png"
	before := fakeListing("rent-100", "QYZH100", "旧站标题", "时日租号", "90", oldImage, now)
	missing := fakeListing("rent-101", "QYZH101", "旧站列表缺失商品", "时日租号", "90", oldImage, now)
	alsoMissing := fakeListing("rent-103", "QYZH103", "第二个缺失商品", "时日租号", "90", oldImage, now)
	manual := fakeListing("rent-104", "QYZH104", "人工下架", "时日租号", "90", oldImage, now)
	quotes := []catalog.RentalQuote{
		{Unit: "period", Amount: "25.00", StartTime: "08:00", EndTime: "18:00", Label: "白时", BillingUnit: "period"},
		{Unit: "day", Amount: "120.00"},
	}
	for _, original := range []legacysync.Record{before, missing, alsoMissing, manual} {
		key := "legacy/" + fmt.Sprintf("%x", sha256.Sum256([]byte(original.ID)))
		exec(`INSERT INTO products(id,legacy_id,product_code,category_id,title,description,trade_type,rental_category,
 price_amount,price_unit,rental_quotes,attributes,status,source,version,approved_version,legacy_price,legacy_category,legacy_payload,published_at)
 VALUES($1,$1,$2,'legacy-short-rent',$3,'新站人工整理的介绍','rent','hour_day',25,'period',$4,
 '{"game":"QQ炫舞","custom_tags":["这是一条历史长标签"]}','published','legacy',3,3,'90','时日租号',$5,$6)`,
			original.ID, original.Code, original.Title, quotes, original, now)
		media := "media-" + original.ID
		exec(`INSERT INTO media_files(id,storage_key,mime_type,byte_size,sha256,status,purpose,source_url)
 VALUES($1,$2,'image/png',32,$3,'ready','product',$4)`, media, key, sha, oldImage)
		exec(`INSERT INTO media_legacy_replicas(media_id,storage_key,bucket,object_key,byte_size,sha256,crc64,verified_at,manifest_sha256)
 VALUES($1,$2,'test-bucket',$2,32,$3,'42',now(),$3)`, media, key, sha)
		exec(`INSERT INTO product_images(product_id,media_id,sort_order) VALUES($1,$2,1)`, original.ID, media)
	}
	exec(`UPDATE products SET status='offline',approved_version=NULL WHERE id=$1`, manual.ID)
	updated := fakeListing("rent-100", "QYZH100", "更新标题 日租110", "时日租号", "110", oldImage+","+replacement+","+replacement, now)
	sale := fakeListing("sale-100", "QYCH100", "新到账号", "QQ炫舞买号", "2500", otherPicture, now)
	ambiguous := fakeListing("rent-102", "QYZH102", "仅有数字的租号", "时日租号", "120", otherPicture, now)
	badImage := fakeListing("sale-101", "QYCH101", "不完整图集", "QQ炫舞买号", "200", oldImage, now)
	source := &testLegacySource{
		inventory: legacysync.Inventory{Records: []legacysync.Record{updated, sale, ambiguous, manual}, Latest: &now},
		images:    map[string][]byte{replacement: legacyImage(t, 100), otherPicture: legacyImage(t, 150)},
	}
	objects := testLegacyObjects{}
	service := &legacysync.Service{
		Source: source, Store: store, Objects: objects, Environment: "test", NewID: security.NewID,
		Images: mediafiles.LegacyImages{Directory: t.TempDir()},
	}
	readCatalog := func() map[string]legacysync.Current {
		t.Helper()
		catalog, _, _, err := store.LoadCatalog(ctx)
		check(err)
		return catalog
	}
	publicCount := func(want int) {
		t.Helper()
		var visible int
		check(db.QueryRow(ctx, `SELECT count(*) FROM products p JOIN categories c ON c.id=p.category_id WHERE `+postgres.VisibleSQL).Scan(&visible))
		if visible != want {
			t.Fatalf("public catalog count: got %d, want %d", visible, want)
		}
	}
	run := func(status string) legacysync.Batch {
		t.Helper()
		runner := legacysync.NewManualRunner(ctx, service)
		batch, err := runner.Start(actor, security.NewID())
		check(err)
		runner.Wait()
		return waitLegacyBatch(t, ctx, store, batch.ID, status)
	}
	pristine := func() {
		t.Helper()
		got := readCatalog()
		if len(got) != 4 || got[before.ID].Title != before.Title || got[missing.ID].Status != "published" ||
			got[alsoMissing.ID].Status != "published" || got[manual.ID].Status != "offline" || len(got[before.ID].Images) != 1 {
			t.Fatal("a stopped batch changed products or image references")
		}
		publicCount(4)
	}
	initial := readCatalog()
	pristine()

	// Re-reading the same count must verify identities and the complete source record.
	unstable := slices.Clone(source.inventory.Records)
	unstable[1].ID = "same-count-other-id"
	source.second = &legacysync.Inventory{Records: unstable}
	blocked := run("blocked")
	if blocked.Progress.Stage != legacysync.VerifySource || blocked.AppliedAt != nil {
		t.Fatalf("an unstable catalog reached the write stage: %+v", blocked)
	}
	pristine()
	source.reads = 0
	unstable = slices.Clone(source.inventory.Records)
	unstable[1].Title += "（复核期间编辑）"
	source.second = &legacysync.Inventory{Records: unstable}
	changedDuringRead := run("blocked")
	if changedDuringRead.Progress.Stage != legacysync.VerifySource || source.fetches != 0 {
		t.Fatal("a title change during removal verification was missed")
	}
	pristine()
	source.second = nil

	completeDirectory := source.inventory.Records
	source.inventory.Records = completeDirectory[:1]
	blocked = run("blocked")
	if !strings.Contains(blocked.Reason, "批量下架保护") || blocked.Summary.Missing != 2 {
		t.Fatalf("a narrowed catalog passed the safety threshold: %+v", blocked)
	}
	pristine()
	source.inventory.Records = completeDirectory

	collision := fakeListing("foreign", "qych500", "编号冲突", "QQ炫舞买号", "550", otherPicture, now)
	source.inventory.Records = append(source.inventory.Records, collision)
	blocked = run("blocked")
	_, issues, total, err := store.Details(ctx, blocked.ID, "issues", 10, 0)
	check(err)
	if !slices.ContainsFunc(issues, func(item legacysync.Item) bool { return item.LegacyID == collision.ID && item.Blocking }) || total == 0 {
		t.Fatalf("missing identification conflict: %+v", issues)
	}
	pristine()

	source.inventory.Records[len(source.inventory.Records)-1] = badImage
	blocked = run("blocked")
	if blocked.Progress.Stage != legacysync.PrepareMedia || blocked.RollbackUntil != nil {
		t.Fatalf("a missing new image was ignored: %+v", blocked)
	}
	pristine()
	source.inventory.Records = source.inventory.Records[:4]

	const concurrentPicture = "https://cdn.xuansiwei.com/concurrent.png"
	source.images[concurrentPicture] = legacyImage(t, 30)
	source.inventory.Records[0].Content = oldImage + "," + concurrentPicture
	source.onImage = func(url string) error {
		if url != concurrentPicture {
			return nil
		}
		_, err := db.Exec(ctx, `UPDATE products SET version=version+1,approved_version=version+1 WHERE id=$1`, before.ID)
		return err
	}
	blocked = run("blocked")
	if blocked.Progress.Stage != legacysync.Commit || !strings.Contains(blocked.Reason, before.Code) ||
		readCatalog()[before.ID].Version != initial[before.ID].Version+1 {
		t.Fatalf("the synchronization overwrote a concurrent edit: %+v", blocked)
	}
	pristine()
	source.onImage = nil
	source.inventory.Records[0] = updated

	// Force the last audit insert to fail after product/image writes and batch status update.
	exec(`CREATE FUNCTION reject_sync_commit() RETURNS trigger LANGUAGE plpgsql AS $$
 BEGIN IF NEW.action='legacy.sync.apply' THEN RAISE EXCEPTION 'test commit failure'; END IF; RETURN NEW; END $$;
 CREATE TRIGGER reject_sync_commit BEFORE INSERT ON admin_logs FOR EACH ROW EXECUTE FUNCTION reject_sync_commit()`)
	failed := run("failed")
	if failed.Progress.Stage != legacysync.Commit || failed.AppliedAt != nil {
		t.Fatalf("late transaction failure was reported as success: %+v", failed)
	}
	pristine()
	exec(`DROP TRIGGER reject_sync_commit ON admin_logs`)

	batch := run("applied")
	if batch.Summary.Created != 2 || batch.Summary.Updated != 1 || batch.Summary.Removed != 2 ||
		batch.Summary.Missing != 2 || batch.Progress.Stage != legacysync.Done || batch.FinishedAt == nil || batch.RollbackUntil == nil {
		t.Fatalf("same-count additions and removals did not commit together: %+v", batch)
	}
	publicCount(4)
	result := readCatalog()
	rent := result[before.ID]
	if rent.Title != updated.Title || rent.Description != initial[before.ID].Description || !slices.Equal(rent.Quotes, quotes) ||
		rent.Unit != "period" || len(rent.Images) != 2 || rent.Images[0] != initial[before.ID].Images[0] ||
		result[sale.ID].Status != "published" || result[missing.ID].Status != "offline" || !result[missing.ID].SyncHidden ||
		result[alsoMissing.ID].Status != "offline" || result[manual.ID].Status != "offline" || result[manual.ID].SyncHidden {
		t.Fatalf("catalog mapping did not preserve terms, media, or manual status: %+v", rent)
	}
	view, err := (&postgres.CatalogStore{DB: db}).ProductView(ctx, result[ambiguous.ID].ID, false)
	check(err)
	if view["price_amount"] != nil || view["legacy_price"] != nil || view["price_unit"] != "unknown" {
		t.Fatalf("unverified old price was exposed: %+v", view)
	}
	_, issues, _, err = store.Details(ctx, batch.ID, "create", 10, 0)
	check(err)
	if !slices.ContainsFunc(issues, func(item legacysync.Item) bool { return item.LegacyID == ambiguous.ID && item.SourcePrice == "120" }) {
		t.Fatal("original unknown price was not retained for the admin audit")
	}
	exec(`INSERT INTO favorites(user_id,product_id) VALUES('sync-member',$1)`, result[sale.ID].ID)
	check(store.RollbackBatch(ctx, batch.ID, actor))
	rolled := readCatalog()
	var retained bool
	check(db.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM favorites WHERE user_id='sync-member' AND product_id=$1)`, result[sale.ID].ID).Scan(&retained))
	if rolled[sale.ID].Status != "draft" || rolled[sale.ID].ID != result[sale.ID].ID || !retained ||
		rolled[missing.ID].Status != "published" || !slices.Equal(rolled[before.ID].Images, initial[before.ID].Images) {
		t.Fatal("rollback did not restore the whole batch while retaining product identity")
	}
	publicCount(4)

	// A repeated pull restores the rolled-back insertions with the same identifiers.
	retry := run("applied")
	if retry.Summary.Restored != 2 || retry.Summary.Removed != 2 || retry.Summary.Created != 0 {
		t.Fatalf("retry did not preserve product identity: %+v", retry)
	}
	publicCount(4)
	unchanged := run("no_change")
	if unchanged.RollbackUntil != nil || unchanged.Summary.Created+unchanged.Summary.Updated+
		unchanged.Summary.Restored+unchanged.Summary.Removed != 0 {
		t.Fatalf("unchanged job produced writes: %+v", unchanged)
	}
	if err = store.RollbackBatch(ctx, unchanged.ID, actor); err == nil {
		t.Fatal("accepted rollback for a no-change batch")
	}

	source.inventory.Records[0].Content = oldImage
	source.inventory.Records = append(source.inventory.Records, missing)
	page, err := store.Batches(ctx, 10, 0)
	check(err)
	_, err = store.SetSchedule(ctx, actor, true, page.Controls.Version)
	check(err)
	noon := legacysync.ScheduledNoon(now)
	scheduled, err := service.RunScheduled(ctx, noon)
	check(err)
	if scheduled.Status != "applied" || scheduled.Summary.Restored != 1 || scheduled.Summary.Imaged != 1 ||
		readCatalog()[missing.ID].ID != initial[missing.ID].ID {
		t.Fatalf("scheduled job used a different pipeline: %+v", scheduled)
	}
	reads := source.reads
	skipped, err := service.RunScheduled(ctx, noon)
	check(err)
	if skipped.ID != "" || source.reads != reads {
		t.Fatal("a duplicate occurrence reached the source")
	}
	detached := rent.Images[1]
	var held bool
	check(db.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM legacy_sync_media_holds WHERE batch_id=$1 AND media_id=$2 AND expires_at>now())`,
		scheduled.ID, detached).Scan(&held))
	if !held {
		t.Fatal("detached media has no rollback hold")
	}
	exec(`UPDATE media_objects SET retain_until=now()-interval '1 sec', retry_at=now()-interval '1 sec' WHERE media_id=$1`, detached)
	lifecycle := postgres.MediaLifecycleStore{DB: db, NewID: security.NewID}
	worked, err := lifecycle.Step(ctx, func(context.Context, medialifecycle.Object, string, string) error {
		t.Fatal("media under rollback hold was moved")
		return nil
	}, func(context.Context, string, string) error {
		t.Fatal("media under rollback hold was removed")
		return nil
	})
	check(err)
	if !worked {
		t.Fatal("media worker did not inspect the due object")
	}
	check(store.RollbackBatch(ctx, scheduled.ID, actor))
	afterRollback := readCatalog()
	if !afterRollback[missing.ID].SyncHidden || afterRollback[missing.ID].Status != "offline" ||
		!slices.Equal(afterRollback[before.ID].Images, rent.Images) {
		t.Fatal("rollback lost the prior sync visibility or album")
	}
	page, err = store.Batches(ctx, 10, 0)
	check(err)
	if page.Controls.ScheduleEnabled {
		t.Fatal("rollback did not pause the noon rule")
	}
	skipped, err = service.RunScheduled(ctx, noon.AddDate(0, 0, 1))
	check(err)
	if skipped.ID != "" || source.reads != reads {
		t.Fatal("paused scheduled task reached the source")
	}
	if err = store.RollbackBatch(ctx, retry.ID, actor); err == nil {
		t.Fatal("rollback accepted after the confirmed versions changed")
	}
}
