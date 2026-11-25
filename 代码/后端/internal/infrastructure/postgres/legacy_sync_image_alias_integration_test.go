package postgres_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"qingye/backend/internal/application/legacysync"
	"qingye/backend/internal/infrastructure/mediafiles"
	"qingye/backend/internal/infrastructure/postgres"
	"qingye/backend/internal/infrastructure/security"
)

// Simulate a committed alias insert whose response did not reach the worker.
type lostAliasAck struct {
	postgres.LegacySyncStore
	loseNext bool
	lookups  int
}

func (s *lostAliasAck) FindAsset(ctx context.Context, legacyID, url, sha string) (legacysync.Asset, bool, error) {
	s.lookups++
	return s.LegacySyncStore.FindAsset(ctx, legacyID, url, sha)
}

func (s *lostAliasAck) RememberAssetURL(ctx context.Context, id, url string) error {
	if err := s.LegacySyncStore.RememberAssetURL(ctx, id, url); err != nil {
		return err
	}
	if s.loseNext {
		s.loseNext = false
		return errors.New("lost database response")
	}
	return nil
}

func TestLegacySyncImageURLAlias(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	db := legacySyncDB(t, ctx)
	store := &lostAliasAck{LegacySyncStore: postgres.LegacySyncStore{DB: db}, loseNext: true}
	if _, err := db.Exec(ctx, `INSERT INTO users(id,phone,nickname,password_hash,role)
 VALUES('alias-admin','30001','管理员','fixture','admin');
 INSERT INTO categories(id,name,trade_type,price_units) VALUES('legacy-sale','QQ炫舞买号','sale','["account"]')`); err != nil {
		t.Fatal(err)
	}
	const first = "https://cdn.xuansiwei.com/original.png"
	const alias = "https://cdn.xuansiwei.com/moved.png"
	now := time.Now().UTC().Truncate(time.Second)
	listing := fakeListing("asset-owner", "QYCH10", "同图旧地址", "QQ炫舞买号", "100", first, now)
	data := legacyImage(t, 60)
	src := &testLegacySource{inventory: legacysync.Inventory{Records: []legacysync.Record{listing}, Latest: &now},
		images: map[string][]byte{first: data, alias: data}}
	objects := testLegacyObjects{}
	svc := &legacysync.Service{Source: src, Store: store, Objects: objects, Images: mediafiles.LegacyImages{Directory: t.TempDir()},
		Environment: "test", NewID: security.NewID}
	run := func() legacysync.Batch {
		t.Helper()
		id := security.NewID()
		runner := legacysync.NewManualRunner(ctx, svc)
		if _, err := runner.Start("alias-admin", id); err != nil {
			t.Fatal(err)
		}
		runner.Wait()
		return waitLegacyBatch(t, ctx, store.LegacySyncStore, id, "applied")
	}
	run()
	src.inventory.Records[0].Content = alias
	src.inventory.Records[0].Title = "同图新地址"
	if b := run(); b.Summary.Review != 1 || store.loseNext || src.fetches != 2 || len(objects) != 1 {
		t.Fatalf("lost alias acknowledgement: batch=%+v fetches=%d objects=%d", b, src.fetches, len(objects))
	}
	// An attached ready alias is resolved in the gallery query even during a title update.
	src.inventory.Records[0].Title = "再次修改标题"
	lookups := store.lookups
	if b := run(); b.Summary.Updated != 1 || b.Summary.Review != 0 || src.fetches != 2 || len(objects) != 1 || store.lookups != lookups {
		t.Fatalf("alias was not reused on full comparison: batch=%+v fetches=%d objects=%d extra lookups=%d", b, src.fetches, len(objects), store.lookups-lookups)
	}
	a, found, err := store.FindAsset(ctx, listing.ID, first, "")
	if err != nil || !found {
		t.Fatalf("original URL lost: %v", err)
	}
	b, found, err := store.FindAsset(ctx, listing.ID, alias, "")
	if err != nil || !found || b.ID != a.ID {
		t.Fatalf("alias does not point to original: %v %v", found, err)
	}
	if err := store.RememberAssetURL(ctx, a.ID, alias); err != nil {
		t.Fatal(err)
	}
	var media, aliases, links int
	if err := db.QueryRow(ctx, `SELECT (SELECT count(*) FROM media_files),
 (SELECT count(*) FROM legacy_sync_asset_urls),(SELECT count(*) FROM product_images WHERE media_id=$1)`, a.ID).Scan(&media, &aliases, &links); err != nil ||
		media != 1 || aliases != 1 || links != 1 {
		t.Fatalf("unexpected media/aliases/links: %d/%d/%d %v", media, aliases, links, err)
	}
	if _, found, err := store.FindAsset(ctx, "unrelated-listing", alias, ""); err != nil || found {
		t.Fatalf("alias escaped owner scope: found=%v err=%v", found, err)
	}
	if _, err := db.Exec(ctx, `UPDATE media_objects SET force_delete=true WHERE media_id=$1`, a.ID); err != nil {
		t.Fatal(err)
	}
	if _, found, err := store.FindAsset(ctx, listing.ID, alias, ""); err != nil || found {
		t.Fatalf("alias revived retired object: found=%v err=%v", found, err)
	}
	current, _, _, err := store.LoadCatalog(ctx)
	if err != nil || current[listing.ID].ImagesByURL[alias] != "" {
		t.Fatalf("gallery lookup returned retired alias: %v", err)
	}
}
