package postgres

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	syncapp "qingye/backend/internal/application/legacysync"
	"qingye/backend/internal/domain/catalog"
	"qingye/backend/internal/domain/fault"
	"qingye/backend/internal/domain/legacycatalog"
)

const syncProductColumns = `p.id,p.legacy_id,p.product_code,p.category_id,coalesce(p.legacy_category,''),
 p.title,p.description,p.trade_type,p.rental_category,p.attributes,p.price_amount::text,p.price_unit,p.rental_quotes,
 p.status,coalesce(p.approved_version=p.version,false),p.version,p.created_at,p.published_at,
 p.deleted_at IS NOT NULL OR p.purged_at IS NOT NULL,p.source='legacy',p.legacy_payload,
 coalesce(p.legacy_sync_hidden_version=p.version,false)`

func productDest(p *syncapp.Current) []any {
	return []any{&p.ID, &p.LegacyID, &p.Code, &p.Category, &p.SourceCategory, &p.Title, &p.Description, &p.Trade,
		&p.RentalCategory, &p.Attributes, &p.Amount, &p.Unit, &p.Quotes, &p.Status, &p.Approved,
		&p.Version, &p.CreatedAt, &p.PublishedAt, &p.Deleted, &p.Legacy, &p.Baseline, &p.SyncHidden}
}

func normalizeSyncSnapshot(p *syncapp.Product) {
	p.CreatedAt = p.CreatedAt.UTC()
	if p.PublishedAt != nil {
		t := p.PublishedAt.UTC()
		p.PublishedAt = &t
	}
	if p.Images == nil {
		p.Images = []string{}
	}
	if p.Quotes == nil {
		p.Quotes = []catalog.RentalQuote{}
	}
}

func equalSyncSnapshot(a, b syncapp.Product) bool {
	normalizeSyncSnapshot(&a)
	normalizeSyncSnapshot(&b)
	// Version protects the marker. Historical snapshots predate that field.
	a.SyncHidden, b.SyncHidden = false, false
	left, first := json.Marshal(a)
	right, second := json.Marshal(b)
	return first == nil && second == nil && bytes.Equal(left, right)
}

// Only verified existing OSS objects are reused by the production import.
const syncMediaReady = `m.status='ready' AND m.deleted_at IS NULL AND m.purpose='product' AND
 (EXISTS(SELECT 1 FROM media_objects o WHERE o.media_id=m.id AND o.variant='original'
     AND o.store='oss' AND o.deleted_at IS NULL AND NOT o.force_delete
     AND NOT EXISTS(SELECT 1 FROM media_object_moves mv WHERE mv.media_id=m.id AND NOT mv.completed))
  OR NOT m.lifecycle_managed AND EXISTS(SELECT 1 FROM media_legacy_replicas r WHERE r.media_id=m.id
     AND r.deleted_at IS NULL AND r.storage_key=m.storage_key AND r.byte_size=m.byte_size
     AND r.sha256=m.sha256 AND NOT EXISTS(SELECT 1 FROM media_retired_keys k WHERE k.storage_key=m.storage_key)))`

func (s LegacySyncStore) LoadCatalog(ctx context.Context) (map[string]syncapp.Current, map[string]catalog.CategoryRules, map[string]string, error) {
	tx, err := s.DB.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return nil, nil, nil, err
	}
	defer tx.Rollback(ctx)
	products := map[string]syncapp.Current{}
	rows, err := tx.Query(ctx, `SELECT `+syncProductColumns+`,last.after_state,coalesce(last.ownership,'{}'::jsonb)
 FROM products p LEFT JOIN LATERAL (
 SELECT i.after_state,i.ownership FROM legacy_sync_items i JOIN legacy_sync_batches b ON b.id=i.batch_id
 WHERE i.legacy_id=p.legacy_id AND i.after_state IS NOT NULL AND b.status='applied'
 ORDER BY b.applied_at DESC,b.id DESC LIMIT 1
 ) last ON true WHERE p.legacy_id IS NOT NULL`)
	if err != nil {
		return nil, nil, nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var p syncapp.Current
		var previous []byte
		if err = rows.Scan(append(productDest(&p), &previous, &p.Ownership)...); err != nil {
			return nil, nil, nil, err
		}
		if len(previous) > 0 {
			if err = json.Unmarshal(previous, &p.LastApplied); err != nil {
				return nil, nil, nil, err
			}
		}
		normalizeSyncSnapshot(&p.Product)
		p.ImagesByURL = make(map[string]string)
		products[p.LegacyID] = p
	}
	if err = rows.Err(); err != nil {
		return nil, nil, nil, err
	}
	rows, err = tx.Query(ctx, `SELECT p.legacy_id,pi.media_id,coalesce(nullif(m.source_url,''),a.source_url,''),`+syncMediaReady+`,u.urls
 FROM product_images pi JOIN products p ON p.id=pi.product_id JOIN media_files m ON m.id=pi.media_id
 LEFT JOIN legacy_sync_assets a ON a.media_id=m.id AND a.legacy_id=p.legacy_id
 LEFT JOIN (SELECT media_id,array_agg(source_url) AS urls FROM legacy_sync_asset_urls GROUP BY media_id) u ON u.media_id=a.media_id
 WHERE p.legacy_id IS NOT NULL
 ORDER BY p.legacy_id,pi.sort_order,pi.media_id`)
	if err != nil {
		return nil, nil, nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var legacyID, mediaID, original string
		var ready bool
		var aliases []string
		if err = rows.Scan(&legacyID, &mediaID, &original, &ready, &aliases); err != nil {
			return nil, nil, nil, err
		}
		p := products[legacyID]
		p.Images = append(p.Images, mediaID)
		if ready {
			p.ReadyImages++
			for _, alias := range aliases {
				p.ImagesByURL[alias] = mediaID
			}
		}
		if source, parseErr := legacycatalog.CanonicalImageURL(original); ready && parseErr == nil {
			p.ImagesByURL[source] = mediaID
		}
		products[legacyID] = p
	}
	if err = rows.Err(); err != nil {
		return nil, nil, nil, err
	}
	codes := map[string]string{}
	rows, err = tx.Query(ctx, `SELECT upper(product_code),min(id),count(*) FROM products GROUP BY upper(product_code)`)
	if err != nil {
		return nil, nil, nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var code, id string
		var count int
		if err = rows.Scan(&code, &id, &count); err != nil {
			return nil, nil, nil, err
		}
		if count > 1 {
			id = "\x00" // No PostgreSQL text ID can match a duplicate.
		}
		codes[code] = id
	}
	if err = rows.Err(); err != nil {
		return nil, nil, nil, err
	}
	categories := map[string]catalog.CategoryRules{}
	rows, err = tx.Query(ctx, `SELECT id,enabled,trade_type,price_units,filter_config FROM categories`)
	if err != nil {
		return nil, nil, nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		var rule catalog.CategoryRules
		if err = rows.Scan(&id, &rule.Enabled, &rule.TradeType, &rule.PriceUnits, &rule.Filters); err != nil {
			return nil, nil, nil, err
		}
		categories[id] = rule
	}
	if err = rows.Err(); err != nil {
		return nil, nil, nil, err
	}
	return products, categories, codes, tx.Commit(ctx)
}

func lockedSyncProduct(ctx context.Context, tx pgx.Tx, id string) (syncapp.Current, error) {
	var p syncapp.Current
	err := tx.QueryRow(ctx, `SELECT `+syncProductColumns+` FROM products p WHERE id=$1 AND legacy_id IS NOT NULL FOR UPDATE`, id).
		Scan(productDest(&p)...)
	if err != nil {
		return p, err
	}
	err = tx.QueryRow(ctx, `SELECT ARRAY(SELECT media_id FROM product_images WHERE product_id=$1 ORDER BY sort_order,media_id)`, id).Scan(&p.Images)
	normalizeSyncSnapshot(&p.Product)
	return p, err
}

func syncAppliedItems(ctx context.Context, tx pgx.Tx, id string) ([]syncapp.Item, error) {
	rows, err := tx.Query(ctx, `SELECT `+legacyItemColumns+` FROM legacy_sync_items
 WHERE batch_id=$1 AND action IN ('create','update','offline','restore') AND after_state IS NOT NULL
 ORDER BY after_state->>'id'`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []syncapp.Item
	for rows.Next() {
		var item syncapp.Item
		if err = scanLegacyItem(rows, &item); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func lockSyncImages(ctx context.Context, tx pgx.Tx, ids []string, requireReady bool) error {
	if len(ids) == 0 {
		return nil
	}
	rows, err := tx.Query(ctx, `SELECT m.id FROM media_files m WHERE m.id=ANY($1::text[])
 ORDER BY m.id FOR UPDATE`, ids)
	if err != nil {
		return err
	}
	defer rows.Close()
	count := 0
	for rows.Next() {
		count++
	}
	if err = rows.Err(); err != nil {
		return err
	}
	if count != len(ids) {
		return legacyConflict("商品图片记录已经变化")
	}
	if requireReady {
		var verified int
		err = tx.QueryRow(ctx, `SELECT count(*) FROM media_files m WHERE m.id=ANY($1::text[]) AND (`+syncMediaReady+`)`, ids).Scan(&verified)
		if err != nil {
			return err
		}
		if verified != len(ids) {
			return legacyConflict("有图片未就绪或已回收，请重新同步")
		}
	}
	return nil
}

func setSyncImages(ctx context.Context, tx pgx.Tx, current, next syncapp.Product) error {
	if slices.Equal(current.Images, next.Images) {
		return nil
	}
	if _, err := tx.Exec(ctx, `DELETE FROM product_images WHERE product_id=$1 AND NOT(media_id=ANY($2::text[]))`,
		next.ID, next.Images); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, `INSERT INTO product_images(product_id,media_id,sort_order)
 SELECT $1,media_id,position::integer FROM unnest($2::text[]) WITH ORDINALITY a(media_id,position)
 ON CONFLICT(product_id,media_id) DO UPDATE SET sort_order=excluded.sort_order`, next.ID, next.Images)
	return err
}

func syncHold(ctx context.Context, tx pgx.Tx, batch string, imageIDs []string, until time.Time) error {
	if len(imageIDs) == 0 {
		return nil
	}
	_, err := tx.Exec(ctx, `INSERT INTO legacy_sync_media_holds(batch_id,media_id,expires_at)
 SELECT $1,id,$3 FROM unnest($2::text[]) id ON CONFLICT(batch_id,media_id) DO NOTHING`, batch, imageIDs, until)
	return err
}

func syncSetProduct(ctx context.Context, tx pgx.Tx, current, next syncapp.Product) error {
	if err := setSyncImages(ctx, tx, current, next); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, `UPDATE products SET title=$2,description=$3,attributes=$4,
 price_amount=$5,price_unit=$6,rental_quotes=$7,status=$8,version=version+1,
 approved_version=CASE WHEN $9 THEN version+1 ELSE NULL END,published_at=$10,updated_at=clock_timestamp(),
 legacy_sync_hidden_version=CASE WHEN $11 THEN version+1 ELSE NULL END
 WHERE id=$1`, next.ID, next.Title, next.Description, next.Attributes, next.Amount, next.Unit,
		next.Quotes, next.Status, next.Approved, next.PublishedAt, next.SyncHidden)
	return err
}

func syncNewProduct(ctx context.Context, tx pgx.Tx, next syncapp.Product, price string) error {
	var used bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM products WHERE legacy_id=$1 OR upper(product_code)=upper($2))`,
		next.LegacyID, next.Code).Scan(&used); err != nil {
		return err
	}
	if used {
		return legacyConflict(fmt.Sprintf("商品 %s 的编号或旧站 ID 已被使用", next.Code))
	}
	_, err := tx.Exec(ctx, `INSERT INTO products(id,legacy_id,product_code,category_id,title,description,trade_type,
 rental_category,attributes,price_amount,price_unit,rental_quotes,status,source,version,approved_version,
 legacy_price,legacy_category,created_at,updated_at,published_at)
 VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,'published','legacy',1,1,
 CASE WHEN $11='unknown' THEN NULL ELSE $13 END,$14,$15,clock_timestamp(),$16)`,
		next.ID, next.LegacyID, next.Code, next.Category, next.Title, next.Description, next.Trade, next.RentalCategory,
		next.Attributes, next.Amount, next.Unit, next.Quotes, price, next.SourceCategory, next.CreatedAt, next.PublishedAt)
	if err != nil {
		return err
	}
	return setSyncImages(ctx, tx, syncapp.Product{}, next)
}

func (s LegacySyncStore) CommitBatch(ctx context.Context, batch syncapp.Batch, items []syncapp.Item, actor string) (syncapp.Batch, error) {
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return batch, err
	}
	defer tx.Rollback(ctx)
	controls, err := lockLegacyControls(ctx, tx)
	if err != nil {
		return batch, err
	}
	if actor != "" {
		if err = legacyActor(ctx, tx, actor); err != nil {
			return batch, err
		}
	}
	var reserved syncapp.Batch
	err = scanLegacyBatch(tx.QueryRow(ctx, `SELECT `+legacyBatchColumns+` FROM legacy_sync_batches WHERE id=$1 FOR UPDATE`, batch.ID), &reserved)
	if err != nil {
		return batch, err
	}
	if reserved.Status != "running" || reserved.Progress.Stage != syncapp.Commit ||
		(reserved.PlannedAt == nil) != (actor != "") {
		return batch, syncapp.ErrLeaseLost
	}
	if actor == "" && !controls.ScheduleEnabled {
		return batch, legacyConflict("提交前定时同步已暂停")
	}
	var now time.Time
	if err = tx.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&now); err != nil {
		return batch, err
	}
	// Native and imported products share the existing allocator's prefix locks.
	for _, prefix := range []string{"QYCH", "QYZH"} {
		if slices.ContainsFunc(items, func(item syncapp.Item) bool {
			return item.After != nil && item.Before == nil && strings.HasPrefix(item.After.Code, prefix)
		}) {
			if err = lockProductNumber(ctx, tx, prefix); err != nil {
				return batch, err
			}
		}
	}
	holdUntil := now.Add(7 * 24 * time.Hour)
	written := 0
	savedItems := slices.Clone(items)
	slices.SortFunc(savedItems, func(a, b syncapp.Item) int {
		if a.After == nil {
			if b.After == nil {
				return strings.Compare(a.LegacyID, b.LegacyID)
			}
			return 1
		}
		if b.After == nil {
			return -1
		}
		return strings.Compare(a.After.ID, b.After.ID)
	})
	for i := range savedItems {
		item := &savedItems[i]
		if item.Blocking {
			return batch, legacyConflict("本批商品校验未通过，请重新同步")
		}
		if item.After == nil {
			continue
		}
		next := *item.After
		var current syncapp.Current
		if item.Before != nil {
			current, err = lockedSyncProduct(ctx, tx, item.Before.ID)
			if errors.Is(err, pgx.ErrNoRows) || err == nil &&
				(current.Deleted || !current.Legacy || !equalSyncSnapshot(current.Product, *item.Before)) {
				return batch, legacyConflict(fmt.Sprintf("商品 %s 已被其他操作修改，请重新同步", item.Code))
			}
			if err != nil {
				return batch, err
			}
		}
		if next.Status == "published" || item.Action == "offline" {
			rule, err := lockSyncCategory(ctx, tx, next)
			if err != nil {
				return batch, err
			}
			if next.Status == "published" && (item.Before == nil || !sameSyncQuote(current.Product, next)) {
				if err = catalog.ValidateInput(next.Input(), true, rule); err != nil {
					return batch, legacyConflict(fmt.Sprintf("商品 %s 不再符合分类规则，请重新同步", item.Code))
				}
			}
		}
		if next.Status == "published" && len(next.Images) == 0 {
			return batch, legacyConflict(fmt.Sprintf("商品 %s 需要至少一张已校验图片", item.Code))
		}
		if err = lockSyncImages(ctx, tx, next.Images, next.Status == "published"); err != nil {
			return batch, err
		}
		if item.Before != nil {
			if err = syncHold(ctx, tx, batch.ID, current.Images, holdUntil); err != nil {
				return batch, err
			}
		}
		if next.Status == "published" && next.PublishedAt == nil {
			next.PublishedAt = &now
		}
		if item.Before == nil {
			err = syncNewProduct(ctx, tx, next, item.SourcePrice)
		} else {
			err = syncSetProduct(ctx, tx, current.Product, next)
		}
		if err != nil {
			return batch, err
		}
		saved, err := lockedSyncProduct(ctx, tx, next.ID)
		if err != nil {
			return batch, err
		}
		if saved.Status != next.Status || saved.Approved != next.Approved || saved.SyncHidden != next.SyncHidden {
			return batch, legacyConflict(fmt.Sprintf("商品 %s 未达到预期发布状态", item.Code))
		}
		item.After = &saved.Product
		written++
	}
	if err = saveLegacyItems(ctx, tx, batch.ID, savedItems); err != nil {
		return batch, err
	}
	var completedAt time.Time
	if err = tx.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&completedAt); err != nil {
		return batch, err
	}
	var until *time.Time
	status := "no_change"
	if written > 0 {
		status = "applied"
		end := completedAt.Add(7 * 24 * time.Hour)
		until = &end
		if _, err = tx.Exec(ctx, `UPDATE legacy_sync_media_holds SET expires_at=$2 WHERE batch_id=$1`, batch.ID, end); err != nil {
			return batch, err
		}
	}
	var result syncapp.Batch
	err = scanLegacyBatch(tx.QueryRow(ctx, `UPDATE legacy_sync_batches
 SET status=$9,finished_at=$8,applied_at=CASE WHEN $9='applied' THEN $8::timestamptz END,rollback_until=$2,applied_by=nullif($3,''),
 source_updated_at=$4,source_count=$5,source_digest=$6,summary=$7,progress_stage='done',progress_completed=1,progress_total=1
 WHERE id=$1 RETURNING `+legacyBatchColumns, batch.ID, until, actor, batch.SourceUpdated, batch.SourceCount, batch.SourceDigest, batch.Summary, completedAt, status), &result)
	if err != nil {
		return batch, err
	}
	if err = legacyAudit(ctx, tx, actor, "legacy.sync.apply", batch.ID, batch.Summary); err != nil {
		return batch, err
	}
	return result, tx.Commit(ctx)
}

func lockSyncCategory(ctx context.Context, tx pgx.Tx, product syncapp.Product) (catalog.CategoryRules, error) {
	var rule catalog.CategoryRules
	err := tx.QueryRow(ctx, `SELECT enabled,trade_type,price_units,filter_config FROM categories WHERE id=$1 FOR SHARE`,
		product.Category).Scan(&rule.Enabled, &rule.TradeType, &rule.PriceUnits, &rule.Filters)
	if errors.Is(err, pgx.ErrNoRows) || err == nil && (!rule.Enabled || rule.TradeType != "all" && rule.TradeType != product.Trade) {
		return rule, legacyConflict(fmt.Sprintf("商品 %s 的分类已停用或修改，请先核对分类", product.Code))
	}
	return rule, err
}

func sameSyncQuote(a, b syncapp.Product) bool {
	return a.Unit == b.Unit && slices.Equal(a.Quotes, b.Quotes) &&
		(a.Amount == nil && b.Amount == nil || a.Amount != nil && b.Amount != nil && *a.Amount == *b.Amount)
}

func (s LegacySyncStore) RollbackBatch(ctx context.Context, id, actor string) error {
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err = lockLegacyControls(ctx, tx); err != nil {
		return err
	}
	if err = legacyActor(ctx, tx, actor); err != nil {
		return err
	}
	var batch syncapp.Batch
	err = scanLegacyBatch(tx.QueryRow(ctx, `SELECT `+legacyBatchColumns+` FROM legacy_sync_batches WHERE id=$1 FOR UPDATE`, id), &batch)
	if errors.Is(err, pgx.ErrNoRows) {
		return fault.New(fault.NotFound, "NOT_FOUND", "同步批次不存在")
	}
	if err != nil || batch.Status == "rolled_back" {
		return err
	}
	var now time.Time
	if err = tx.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&now); err != nil {
		return err
	}
	if batch.Status != "applied" || batch.RollbackUntil == nil || !batch.RollbackUntil.After(now) {
		return legacyConflict("只有回滚期内且有实际变更的已完成批次可以回滚")
	}
	var busy bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM legacy_sync_batches WHERE status='running' AND `+freshLegacyBatch+`)`).Scan(&busy); err != nil {
		return err
	}
	if busy {
		return legacyConflict("有同步任务正在执行，请完成后再回滚")
	}
	items, err := syncAppliedItems(ctx, tx, id)
	if err != nil {
		return err
	}
	if len(items) == 0 {
		return legacyConflict("这一批没有商品变更")
	}
	for _, item := range items {
		current, err := lockedSyncProduct(ctx, tx, item.After.ID)
		if errors.Is(err, pgx.ErrNoRows) || err == nil &&
			(current.Deleted || !current.Legacy || !equalSyncSnapshot(current.Product, *item.After)) {
			return legacyConflict(fmt.Sprintf("商品 %s 已被后续修改，本批次未回滚", item.Code))
		}
		if err != nil {
			return err
		}
		restore := current.Product
		if item.Before == nil {
			// Retain the identity for existing favorites and conversations.
			restore.Status, restore.Approved, restore.PublishedAt = "draft", false, nil
			restore.SyncHidden = true
		} else {
			restore = *item.Before
		}
		if restore.Status == "published" {
			if _, err = lockSyncCategory(ctx, tx, restore); err != nil {
				return err
			}
		}
		if err = lockSyncImages(ctx, tx, restore.Images, restore.Status == "published"); err != nil {
			return err
		}
		if err = syncSetProduct(ctx, tx, current.Product, restore); err != nil {
			return err
		}
		saved, err := lockedSyncProduct(ctx, tx, restore.ID)
		if err != nil {
			return err
		}
		if saved.Status != restore.Status || saved.Approved != restore.Approved {
			return legacyConflict(fmt.Sprintf("商品 %s 未达到原有状态，本批次未回滚", item.Code))
		}
	}
	if _, err = tx.Exec(ctx, `UPDATE legacy_sync_batches SET status='rolled_back',
 rolled_back_at=clock_timestamp(),rolled_back_by=$2 WHERE id=$1`, id, actor); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `UPDATE legacy_sync_control SET mode='paused',version=version+1,updated_at=clock_timestamp()`); err != nil {
		return err
	}
	if err = legacyAudit(ctx, tx, actor, "legacy.sync.rollback", id, map[string]int{"products": len(items)}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
