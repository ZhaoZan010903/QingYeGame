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
)

type testLegacySource struct {
	inventory legacysync.Inventory
	images    map[string][]byte
	fetches   int
	err       error
}

func (s *testLegacySource) Catalog(context.Context) (legacysync.Inventory, error) {
	return s.inventory, s.err
}

func (s *testLegacySource) Image(_ context.Context, url string, w io.Writer) (int64, error) {
	s.fetches++
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

func TestLegacySyncTransactions(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	db := legacySyncDB(t, ctx)
	store := postgres.LegacySyncStore{DB: db}
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := db.Exec(ctx, sql, args...); err != nil {
			t.Fatal(err)
		}
	}
	clock := time.Now().UTC().Truncate(time.Second)
	sha := strings.Repeat("a", 64)
	exec(`INSERT INTO users(id,phone,nickname,password_hash,role)
 VALUES('sync-admin','100001','合成管理员','fixture','admin'),('sync-member','100002','合成会员','fixture','member')`)
	exec(`INSERT INTO categories(id,name,trade_type,price_units) VALUES
 ('legacy-sale','QQ炫舞买号','sale','["account"]'),
 ('legacy-short-rent','时日租号','rent','["hour","day","period"]')`)
	const oldImage = "https://cdn.xuansiwei.com/test-original.png"
	const replacement = "https://cdn.xuansiwei.com/test-replacement.png"
	const otherPicture = "https://cdn.xuansiwei.com/test-other.png"
	before := fakeListing("rent-100", "QYZH100", "旧站标题", "时日租号", "90", oldImage, clock)
	missing := fakeListing("rent-101", "QYZH101", "旧站列表缺失商品", "时日租号", "90", oldImage, clock)
	quotes := []catalog.RentalQuote{
		{Unit: "period", Amount: "25.00", StartTime: "08:00", EndTime: "18:00", Label: "白时", BillingUnit: "period"},
		{Unit: "day", Amount: "120.00"},
	}
	for _, original := range []legacysync.Record{before, missing} {
		key := "legacy/" + fmt.Sprintf("%x", sha256.Sum256([]byte(original.ID)))
		exec(`INSERT INTO products(id,legacy_id,product_code,category_id,title,description,trade_type,rental_category,
 price_amount,price_unit,rental_quotes,attributes,status,source,version,approved_version,legacy_price,legacy_category,legacy_payload,published_at)
 VALUES($1,$1,$2,'legacy-short-rent',$3,'新站人工整理的介绍','rent','hour_day',25,'period',$4,
 '{"game":"QQ炫舞","custom_tags":["这是一条历史长标签"]}','published','legacy',3,3,'90','时日租号',$5,$6)`,
			original.ID, original.Code, original.Title, quotes, original, clock)
		media := "media-" + original.ID
		exec(`INSERT INTO media_files(id,storage_key,mime_type,byte_size,sha256,status,purpose,source_url)
 VALUES($1,$2,'image/png',32,$3,'ready','product',$4)`, media, key, sha, oldImage)
		exec(`INSERT INTO media_legacy_replicas(media_id,storage_key,bucket,object_key,byte_size,sha256,crc64,verified_at,manifest_sha256)
 VALUES($1,$2,'test-bucket',$2,32,$3,'42',now(),$3)`, media, key, sha)
		exec(`INSERT INTO product_images(product_id,media_id,sort_order) VALUES($1,$2,1)`, original.ID, media)
	}
	picture := func(r byte) []byte {
		t.Helper()
		img := image.NewRGBA(image.Rect(0, 0, 8, 8))
		img.Set(0, 0, color.RGBA{R: r, A: 255})
		var data bytes.Buffer
		if err := png.Encode(&data, img); err != nil {
			t.Fatal(err)
		}
		return data.Bytes()
	}
	updated := fakeListing("rent-100", "QYZH100", "更新标题 日租110", "时日租号", "110",
		oldImage+","+replacement+","+replacement, clock)
	sale := fakeListing("sale-100", "QYCH100", "新到账号", "QQ炫舞买号", "2500", otherPicture, clock)
	ambiguous := fakeListing("rent-102", "QYZH102", "仅有数字的租号", "时日租号", "120", otherPicture, clock)
	badImage := fakeListing("sale-101", "QYCH101", "不完整图集", "QQ炫舞买号", "200", oldImage, clock)
	source := &testLegacySource{
		inventory: legacysync.Inventory{Records: []legacysync.Record{updated, sale, ambiguous, badImage}, Latest: &clock},
		images:    map[string][]byte{replacement: picture(100), otherPicture: picture(150)},
	}
	objects := testLegacyObjects{}
	service := legacysync.Service{
		Source: source, Store: store, Objects: objects, Environment: "test", NewID: security.NewID,
		Images: mediafiles.LegacyImages{Directory: t.TempDir()},
	}
	readCatalog := func() map[string]legacysync.Current {
		t.Helper()
		result, _, _, err := store.LoadCatalog(ctx)
		if err != nil {
			t.Fatal(err)
		}
		return result
	}
	check := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}

	initial := readCatalog()
	preview, err := service.Run(ctx, nil)
	check(err)
	if preview.Status != "ready" || preview.Summary.Created != 1 || preview.Summary.Updated != 1 ||
		preview.Summary.Missing != 1 || preview.Summary.BadImage != 1 || len(objects) != 2 || source.fetches != 3 {
		t.Fatalf("unexpected preview: %+v, puts=%d, downloads=%d", preview, len(objects), source.fetches)
	}
	saved, items, total, err := store.Details(ctx, preview.ID, 20, 0)
	check(err)
	if saved.FinishedAt == nil || saved.SourceUpdated == nil || total != 5 || len(items) != total {
		t.Fatalf("preview audit is incomplete: %+v, %d rows", saved, total)
	}
	for _, item := range items {
		if item.LegacyID == ambiguous.ID && (item.Action != "hold" || item.After != nil) {
			t.Fatalf("ambiguous rent was assigned a price: %+v", item)
		}
	}
	if current := readCatalog(); len(current) != 2 || current[before.ID].Title != initial[before.ID].Title {
		t.Fatal("preview changed the storefront")
	}
	if _, err = store.SetMode(ctx, "sync-admin", legacysync.Automatic, 1); err == nil {
		t.Fatal("automatic publishing was allowed before a manual review")
	}
	if err = store.ApplyBatch(ctx, preview.ID, "sync-member"); err == nil {
		t.Fatal("member could publish a sync batch")
	}
	check(store.ApplyBatch(ctx, preview.ID, "sync-admin"))
	applied := readCatalog()
	rent := applied[before.ID]
	if rent.Title != updated.Title || rent.Description != initial[before.ID].Description || !slices.Equal(rent.Quotes, quotes) ||
		rent.Unit != "period" || len(rent.Images) != 2 || rent.Images[0] != initial[before.ID].Images[0] ||
		applied[sale.ID].Status != "published" || applied[missing.ID].Status != "published" {
		t.Fatalf("catalog mapping did not preserve manual terms, media order or missing rows: %+v", rent)
	}
	saved, _, _, err = store.Details(ctx, preview.ID, 20, 0)
	check(err)
	if saved.Status != "applied" || saved.RollbackUntil == nil || saved.AppliedAt == nil {
		t.Fatalf("publish audit is incomplete: %+v", saved)
	}
	_, err = store.SetMode(ctx, "sync-admin", legacysync.Automatic, 1)
	check(err)

	// This scheduled batch removes a managed image and updates a second product.
	source.inventory.Records[0].Content = oldImage
	source.inventory.Records[1].Title = "第二批更新"
	planned := legacysync.ScheduledNoon(clock)
	scheduled, err := service.Run(ctx, &planned)
	check(err)
	if scheduled.Status != "applied" || scheduled.Summary.Updated != 2 || len(objects) != 2 || source.fetches != 4 {
		t.Fatalf("scheduled job failed to reuse known URLs: %+v, %d downloads", scheduled, source.fetches)
	}
	deferredImage := rent.Images[1]
	var held bool
	check(db.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM legacy_sync_media_holds WHERE batch_id=$1 AND media_id=$2 AND expires_at>now())`,
		scheduled.ID, deferredImage).Scan(&held))
	if !held {
		t.Fatal("detached media was not retained for rollback")
	}
	exec(`UPDATE media_objects SET retain_until=now()-interval '1 sec', retry_at=now()-interval '1 sec' WHERE media_id=$1`, deferredImage)
	lifecycle := postgres.MediaLifecycleStore{DB: db, NewID: security.NewID}
	didWork, err := lifecycle.Step(ctx, func(context.Context, medialifecycle.Object, string, string) error {
		t.Fatal("media under a rollback hold was moved")
		return nil
	}, func(context.Context, string, string) error {
		t.Fatal("media under a rollback hold was removed")
		return nil
	})
	check(err)
	if !didWork {
		t.Fatal("media worker did not inspect the due object")
	}
	exec(`INSERT INTO favorites(user_id,product_id) VALUES('sync-member',$1)`, applied[sale.ID].ID)
	duplicate, err := service.Run(ctx, &planned)
	check(err)
	if duplicate.ID != "" || source.fetches != 4 {
		t.Fatal("a duplicate scheduled job reached the source")
	}
	check(store.RollbackBatch(ctx, scheduled.ID, "sync-admin"))
	restored := readCatalog()
	if !slices.Equal(restored[before.ID].Images, rent.Images) || restored[sale.ID].Title != sale.Title ||
		restored[before.ID].Version <= rent.Version {
		t.Fatal("rollback did not restore content with a new version")
	}
	_, mode, _, err := store.Batches(ctx, 10, 0)
	check(err)
	if mode.Mode != legacysync.Paused {
		t.Fatal("rollback did not pause automatic sync")
	}
	nextNoon := planned.AddDate(0, 0, 1)
	paused, err := service.Run(ctx, &nextNoon)
	check(err)
	if paused.ID != "" || source.fetches != 4 {
		t.Fatal("paused scheduled job reached the source")
	}
	if err = store.RollbackBatch(ctx, preview.ID, "sync-admin"); err == nil {
		t.Fatal("older conflicting batch was rolled back")
	}

	_, err = store.SetMode(ctx, "sync-admin", legacysync.Preview, mode.Version)
	check(err)
	source.inventory.Records = []legacysync.Record{updated, sale, ambiguous}
	source.inventory.Records[0].Title = "待发布的下一批"
	source.inventory.Records[1].Title = "待发布的第二条"
	stale, err := service.Run(ctx, nil)
	check(err)
	exec(`UPDATE products SET title='人工修改',version=version+1,approved_version=version+1 WHERE id=$1`, before.ID)
	if err = store.ApplyBatch(ctx, stale.ID, "sync-admin"); err == nil {
		t.Fatal("publication overwrote a post-preview edit")
	}
	checkUnaffected := readCatalog()
	if checkUnaffected[sale.ID].Title != sale.Title || checkUnaffected[before.ID].Title != "人工修改" {
		t.Fatal("a conflicting batch was partially published")
	}
	source.err = errors.New("模拟旧站读到不完整目录")
	rejected, err := service.Run(ctx, nil)
	if err == nil || rejected.Status != "failed" {
		t.Fatalf("incomplete source was accepted: %+v, %v", rejected, err)
	}
	saved, _, _, err = store.Details(ctx, rejected.ID, 10, 0)
	check(err)
	if saved.Status != "failed" || saved.FinishedAt == nil {
		t.Fatal("failed source did not leave a completed audit entry")
	}

	source.err = nil
	newRental := fakeListing("rent-200", "QYZH200", "新账号 日租90", "时日租号", "90", replacement, clock)
	source.inventory.Records = []legacysync.Record{newRental}
	proposal, err := service.Run(ctx, nil)
	check(err)
	check(store.ApplyBatch(ctx, proposal.ID, "sync-admin"))
	newProduct := readCatalog()[newRental.ID]
	if newProduct.Status != "published" || newProduct.Unit != "day" || amountOf(newProduct.Amount) != "90.00" {
		t.Fatalf("confirmed rental price was not published: %+v", newProduct)
	}
	exec(`INSERT INTO favorites(user_id,product_id) VALUES('sync-member',$1)`, newProduct.ID)
	check(store.RollbackBatch(ctx, proposal.ID, "sync-admin"))
	draft := readCatalog()[newRental.ID]
	check(db.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM favorites WHERE user_id='sync-member' AND product_id=$1)`,
		newProduct.ID).Scan(&held))
	if draft.Status != "draft" || draft.ID != newProduct.ID || draft.Approved || draft.PublishedAt != nil || !held {
		t.Fatal("rollback removed a newly added product's identity or referenced data")
	}
}

func amountOf(amount *string) string {
	if amount == nil {
		return ""
	}
	return *amount
}
