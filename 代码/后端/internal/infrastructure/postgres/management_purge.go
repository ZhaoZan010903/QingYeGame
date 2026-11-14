package postgres

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"github.com/jackc/pgx/v5"
	"qingye/backend/internal/domain/fault"
	"qingye/backend/internal/domain/management"
	"sort"
	"strings"
	"time"
)

// All changes to the trash set acquire this before product rows. FOR UPDATE
// alone cannot protect a bulk confirmation from newly trashed rows.
func lockProductTrash(ctx context.Context, tx pgx.Tx) error {
	_, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('admin-product-trash',0))`)
	return err
}

type purgeProductRow struct {
	ID, Code  string
	Version   int
	DeletedAt time.Time
}

func purgeLimitError() error {
	return fault.New(fault.Invalid, "TRASH_PURGE_LIMIT_EXCEEDED", "本次最多清理1000件商品、100000条图片引用；请先单条清理部分商品后重新预览")
}

func readTrashForPurge(ctx context.Context, tx pgx.Tx, lock bool) ([]purgeProductRow, error) {
	query := `SELECT id,product_code,version,deleted_at FROM products WHERE deleted_at IS NOT NULL AND deleted_by_admin AND purged_at IS NULL ORDER BY id LIMIT $1`
	if lock {
		query += ` FOR UPDATE`
	}
	rows, err := tx.Query(ctx, query, management.PurgeProductLimit+1)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []purgeProductRow{}
	for rows.Next() {
		var item purgeProductRow
		if err = rows.Scan(&item.ID, &item.Code, &item.Version, &item.DeletedAt); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	if len(items) > management.PurgeProductLimit {
		return nil, purgeLimitError()
	}
	return items, nil
}

func trashSnapshot(items []purgeProductRow) string {
	type identity struct {
		ID        string
		Version   int
		DeletedAt string
	}
	identities := make([]identity, 0, len(items))
	for _, item := range items {
		identities = append(identities, identity{item.ID, item.Version, item.DeletedAt.UTC().Format(time.RFC3339Nano)})
	}
	raw, _ := json.Marshal(identities)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

// Historical review-only images must be considered before those snapshots are
// erased. The media queue independently protects references outside this batch.
func purgeMediaCandidates(ctx context.Context, tx pgx.Tx, items []purgeProductRow) (map[string][]string, []string, error) {
	ids := make([]string, 0, len(items))
	for _, item := range items {
		ids = append(ids, item.ID)
	}
	rows, err := tx.Query(ctx, `SELECT product_id,media_id FROM (
 SELECT product_id,media_id FROM product_images WHERE product_id=ANY($1::text[])
 UNION SELECT r.product_id,image->>'id' FROM product_reviews r
 CROSS JOIN LATERAL jsonb_array_elements(CASE WHEN jsonb_typeof(r.snapshot->'images')='array' THEN r.snapshot->'images' ELSE '[]'::jsonb END) image
 WHERE r.product_id=ANY($1::text[]) AND jsonb_typeof(image->'id')='string'
 ) refs WHERE media_id<>'' ORDER BY product_id,media_id LIMIT $2`, ids, management.PurgeImageLimit+1)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	byProduct := make(map[string][]string)
	seen := make(map[string]bool)
	count := 0
	for rows.Next() {
		var productID, mediaID string
		if err = rows.Scan(&productID, &mediaID); err != nil {
			return nil, nil, err
		}
		count++
		if count > management.PurgeImageLimit {
			return nil, nil, purgeLimitError()
		}
		byProduct[productID] = append(byProduct[productID], mediaID)
		seen[mediaID] = true
	}
	if err = rows.Err(); err != nil {
		return nil, nil, err
	}
	mediaIDs := make([]string, 0, len(seen))
	for id := range seen {
		mediaIDs = append(mediaIDs, id)
	}
	sort.Strings(mediaIDs)
	return byProduct, mediaIDs, nil
}

func (s *ManagementStore) TrashPreview(ctx context.Context) (map[string]any, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	tx, err := s.DB.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return nil, managementStoreError(err)
	}
	defer tx.Rollback(ctx)
	items, err := readTrashForPurge(ctx, tx, false)
	if err != nil {
		return nil, managementStoreError(err)
	}
	_, mediaIDs, err := purgeMediaCandidates(ctx, tx, items)
	if err != nil {
		return nil, managementStoreError(err)
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, managementStoreError(err)
	}
	return map[string]any{"data": map[string]any{"snapshot": trashSnapshot(items), "total": len(items), "image_count": len(mediaIDs)}}, nil
}

// Purge has a dedicated idempotency scope: unlike create-product, its stored
// response is complete and remains replayable after the product is tombstoned.
func purgeReplay(ctx context.Context, tx pgx.Tx, actor, requestID, operation, id string, input any) (map[string]any, string, error) {
	raw, err := json.Marshal(struct {
		Operation string
		ProductID string
		Input     any
	}{operation, id, input})
	if err != nil {
		return nil, "", err
	}
	sum := sha256.Sum256(raw)
	hash := hex.EncodeToString(sum[:])
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, actor+":admin-product-purge:"+requestID); err != nil {
		return nil, "", err
	}
	var oldHash string
	var response map[string]any
	err = tx.QueryRow(ctx, `SELECT body_hash,response FROM idempotency WHERE user_id=$1 AND scope='admin-product-purge' AND request_id=$2`, actor, requestID).Scan(&oldHash, &response)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, hash, nil
	}
	if err != nil {
		return nil, "", err
	}
	if oldHash != hash {
		return nil, "", fault.New(fault.Conflict, "IDEMPOTENCY_CONFLICT", "此请求标识已用于其他删除内容，请刷新后重新确认")
	}
	return response, hash, nil
}

func (s *ManagementStore) PurgeProduct(ctx context.Context, actor management.Actor, id string, in management.PurgeProductInput) (map[string]any, error) {
	return s.purge(ctx, actor, id, in.RequestID, "single", in, in.ExpectedVersion, "", in.Reason)
}

func (s *ManagementStore) PurgeTrash(ctx context.Context, actor management.Actor, in management.PurgeTrashInput) (map[string]any, error) {
	return s.purge(ctx, actor, "", in.RequestID, "all", in, 0, in.Snapshot, in.Reason)
}

func (s *ManagementStore) purge(ctx context.Context, actor management.Actor, id, requestID, operation string, input any, version int, snapshot, reason string) (map[string]any, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return nil, managementStoreError(err)
	}
	defer tx.Rollback(ctx)
	response, hash, err := purgeReplay(ctx, tx, actor.ID, requestID, operation, id, input)
	if err != nil {
		return nil, managementStoreError(err)
	}
	if response != nil {
		return response, nil
	}
	if err = lockProductTrash(ctx, tx); err != nil {
		return nil, managementStoreError(err)
	}
	var items []purgeProductRow
	if operation == "all" {
		items, err = readTrashForPurge(ctx, tx, true)
		if err != nil {
			return nil, managementStoreError(err)
		}
		if trashSnapshot(items) != snapshot {
			return nil, fault.New(fault.Conflict, "TRASH_SNAPSHOT_CHANGED", "回收站已变化，未删除任何商品，请重新预览并确认")
		}
	} else {
		var item purgeProductRow
		var deleted, adminDeleted, purged bool
		err = tx.QueryRow(ctx, `SELECT id,product_code,version,coalesce(deleted_at,'epoch'::timestamptz),deleted_at IS NOT NULL,deleted_by_admin,purged_at IS NOT NULL FROM products WHERE id=$1 FOR UPDATE`, id).Scan(&item.ID, &item.Code, &item.Version, &item.DeletedAt, &deleted, &adminDeleted, &purged)
		if errors.Is(err, pgx.ErrNoRows) || err == nil && (purged || deleted && !adminDeleted) {
			return nil, fault.New(fault.NotFound, "NOT_FOUND", "商品不存在或已被永久清理")
		}
		if err != nil {
			return nil, managementStoreError(err)
		}
		if item.Version != version {
			return nil, fault.New(fault.Conflict, "VERSION_CONFLICT", "商品已更新，请刷新后重新确认")
		}
		if !deleted || !adminDeleted {
			return nil, fault.New(fault.Conflict, "INVALID_STATE", "只有管理员回收站中的商品可以永久删除")
		}
		items = []purgeProductRow{item}
	}
	byProduct, mediaIDs, err := purgeMediaCandidates(ctx, tx, items)
	if err != nil {
		return nil, managementStoreError(err)
	}
	if err = s.purgeProductsTx(ctx, tx, actor, items, byProduct, mediaIDs, requestID, reason); err != nil {
		return nil, managementStoreError(err)
	}
	response = map[string]any{"data": map[string]any{"purged_count": len(items), "cleanup_pending": true, "image_count": len(mediaIDs)}}
	if _, err = tx.Exec(ctx, `INSERT INTO idempotency(user_id,scope,request_id,body_hash,response) VALUES($1,'admin-product-purge',$2,$3,$4)`, actor.ID, requestID, hash, response); err != nil {
		return nil, managementStoreError(err)
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, managementStoreError(err)
	}
	return response, nil
}

func (s *ManagementStore) purgeProductsTx(ctx context.Context, tx pgx.Tx, actor management.Actor, items []purgeProductRow, byProduct map[string][]string, mediaIDs []string, requestID, reason string) error {
	ids := make([]string, 0, len(items))
	for _, item := range items {
		ids = append(ids, item.ID)
	}
	if len(ids) == 0 {
		return nil
	}
	// Match the lifecycle worker's product -> owner quota -> media -> object
	// order before DELETE product_images invokes its object-locking trigger.
	rows, err := tx.Query(ctx, `SELECT DISTINCT owner_id FROM media_files WHERE id=ANY($1::text[]) AND owner_id IS NOT NULL ORDER BY owner_id`, mediaIDs)
	if err != nil {
		return err
	}
	owners := []string{}
	for rows.Next() {
		var owner string
		if err = rows.Scan(&owner); err != nil {
			rows.Close()
			return err
		}
		owners = append(owners, owner)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, owner := range owners {
		if err = lockMemberQuota(ctx, tx, owner); err != nil {
			return err
		}
	}
	if _, err = tx.Exec(ctx, `SELECT id FROM media_files WHERE id=ANY($1::text[]) ORDER BY id FOR UPDATE`, mediaIDs); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `SELECT media_id FROM media_objects WHERE media_id=ANY($1::text[]) ORDER BY media_id,variant FOR UPDATE`, mediaIDs); err != nil {
		return err
	}
	// Preserve conversation/message identities, text, attachments and unread
	// positions; only the deleted product's copied card data is redacted.
	card := `jsonb_build_object('id',p.id,'code',p.product_code,'title','商品已永久删除','price',NULL,'unit','unknown','cover_url','','rental_quotes','[]'::jsonb)`
	if _, err = tx.Exec(ctx, `UPDATE chat_messages m SET product_snapshot=`+card+` FROM products p WHERE p.id=ANY($1::text[]) AND m.kind='product' AND m.product_snapshot->>'id'=p.id`, ids); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `UPDATE chat_conversations c SET product_snapshot=`+card+` FROM products p WHERE p.id=ANY($1::text[]) AND c.product_snapshot->>'id'=p.id`, ids); err != nil {
		return err
	}
	statements := []string{
		`UPDATE product_reviews SET snapshot='{}'::jsonb,reason='' WHERE product_id=ANY($1::text[])`,
		`DELETE FROM product_images WHERE product_id=ANY($1::text[])`,
		`DELETE FROM product_private_credentials WHERE product_id=ANY($1::text[])`,
		`DELETE FROM favorites WHERE product_id=ANY($1::text[])`,
		`DELETE FROM product_views_daily WHERE product_id=ANY($1::text[])`,
		`DELETE FROM product_hot_scores WHERE product_id=ANY($1::text[])`,
		`DELETE FROM product_consultations WHERE product_id=ANY($1::text[])`,
		`DELETE FROM product_item_similarities WHERE source_id=ANY($1::text[]) OR target_id=ANY($1::text[])`,
		`UPDATE site_settings SET value=jsonb_set(value,'{slides}',(SELECT coalesce(jsonb_agg(slide ORDER BY ord),'[]'::jsonb) FROM jsonb_array_elements(value->'slides') WITH ORDINALITY AS x(slide,ord) WHERE (slide->>'productId'=ANY($1::text[])) IS DISTINCT FROM TRUE)),updated_at=now() WHERE key='home_presentation' AND jsonb_typeof(value->'slides')='array' AND EXISTS(SELECT 1 FROM jsonb_array_elements(value->'slides') slide WHERE slide->>'productId'=ANY($1::text[]))`,
		// Structural columns and identity remain to prevent FK breaks, old
		// import resurrection, number reuse and create-request replay duplication.
		`UPDATE products SET purged_at=now(),deleted_by_admin=false,version=version+1,approved_version=NULL,owner_id=NULL,title='',description='',attributes='{}',rental_quotes='[]',rental_category='',price_amount=NULL,price_unit='unknown',manual_rank=NULL,recommendation_boost=0,recommendation_version=recommendation_version+1,review_note='',legacy_price=NULL,legacy_category=NULL,legacy_payload=NULL,verification_status='unprovided',published_at=NULL,draft_expires_at=NULL,last_media_upload_at=NULL,updated_at=now() WHERE id=ANY($1::text[])`,
	}
	for _, statement := range statements {
		if _, err = tx.Exec(ctx, statement, ids); err != nil {
			return err
		}
	}
	for _, item := range items {
		if err = QueueProductPurgeMediaTx(ctx, tx, item.ID, byProduct[item.ID]); err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, `INSERT INTO admin_logs(id,user_id,action,resource_id,detail) VALUES($1,$2,'product.purge',$3,$4)`, s.NewID(), actor.ID, item.ID, map[string]any{"product_code": item.Code, "previous_version": item.Version, "request_id": requestID, "reason": strings.TrimSpace(reason), "image_count": len(byProduct[item.ID])}); err != nil {
			return err
		}
	}
	return nil
}
