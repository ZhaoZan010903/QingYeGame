package postgres

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"qingye/backend/internal/domain/catalog"
	"qingye/backend/internal/domain/fault"
	"qingye/backend/internal/domain/member"
	"strconv"
)

type MemberStore struct {
	DB            *pgxpool.Pool
	NewID         func() string
	CredentialKey string
}

func memberStoreError(err error) error {
	if f, ok := fault.As(err); ok {
		return f
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return fault.New(fault.NotFound, "NOT_FOUND", "未找到资料")
	}
	return fault.Wrap(fault.Unavailable, "DATABASE_UNAVAILABLE", "服务暂不可用，请稍后重试", err)
}
func (s *MemberStore) productResult(ctx context.Context, id string) (map[string]any, error) {
	p, err := (&CatalogStore{DB: s.DB}).ProductView(ctx, id, true)
	if err != nil {
		return nil, memberStoreError(err)
	}
	return map[string]any{"data": p}, nil
}
func pageCursor(offset, limit, total int) any {
	if offset+limit < total {
		return strconv.Itoa(offset + limit)
	}
	return nil
}

// 商品行锁串行化资料修改、审核与图片重排。
func lockedOwner(ctx context.Context, tx pgx.Tx, id, user string) (int, string, error) {
	var version int
	var status string
	err := tx.QueryRow(ctx, `SELECT version,status FROM products WHERE id=$1 AND owner_id=$2 AND deleted_at IS NULL FOR UPDATE`, id, user).Scan(&version, &status)
	return version, status, err
}
func lockedImageOwner(ctx context.Context, tx pgx.Tx, u member.Actor, id string) (int, string, error) {
	if !u.Admin {
		return lockedOwner(ctx, tx, id, u.ID)
	}
	var version int
	var status string
	err := tx.QueryRow(ctx, `SELECT version,status FROM products WHERE id=$1 AND deleted_at IS NULL FOR UPDATE`, id).Scan(&version, &status)
	return version, status, err
}
func (s *MemberStore) imageAudit(ctx context.Context, tx pgx.Tx, u member.Actor, id, action string) error {
	if !u.Admin {
		return nil
	}
	_, err := tx.Exec(ctx, `INSERT INTO admin_logs(id,user_id,action,resource_id) VALUES($1,$2,$3,$4)`, s.NewID(), u.ID, action, id)
	return err
}

// 幂等键先持有事务锁，首次并发请求也只创建一次。
func replayProduct(ctx context.Context, tx pgx.Tx, user, scope, key string, body any, privateKey ...string) (string, string, error) {
	if len(key) < 8 || len(key) > 128 {
		return "", "", fault.New(fault.Conflict, "IDEMPOTENCY_CONFLICT", "request_id must be 8–128 characters")
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return "", "", err
	}
	hash := productReplayHash(raw)
	if p, ok := body.(catalog.ProductInput); ok && p.PrivateCredentials != nil {
		if len(privateKey) == 0 || privateKey[0] == "" {
			return "", "", fault.New(fault.Unavailable, "CREDENTIAL_STORAGE_UNAVAILABLE", "私密资料存储尚未配置")
		}
		mac := hmac.New(sha256.New, []byte(privateKey[0]))
		mac.Write(raw)
		hash = hex.EncodeToString(mac.Sum(nil))
	}
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, user+":"+scope+":"+key); err != nil {
		return "", "", err
	}
	var oldHash string
	var result map[string]any
	err = tx.QueryRow(ctx, `SELECT body_hash,response FROM idempotency WHERE user_id=$1 AND scope=$2 AND request_id=$3`, user, scope, key).Scan(&oldHash, &result)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", hash, nil
	}
	if err != nil {
		return "", "", err
	}
	if oldHash != hash {
		return "", "", fault.New(fault.Conflict, "IDEMPOTENCY_CONFLICT", "idempotency key reused with another payload")
	}
	id, _ := result["product_id"].(string)
	return id, hash, nil
}
func recordProductReplay(ctx context.Context, tx pgx.Tx, user, scope, key, hash, id string) error {
	_, err := tx.Exec(ctx, `INSERT INTO idempotency(user_id,scope,request_id,body_hash,response) VALUES($1,$2,$3,$4,$5)`, user, scope, key, hash, map[string]any{"product_id": id})
	return err
}
func productReplayHash(raw []byte) string {
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}
func (s *MemberStore) List(ctx context.Context, u member.Actor, in member.ListInput) (map[string]any, error) {
	offset, limit := in.Offset, in.Limit
	favorites := in.Favorites
	var count int
	var rows pgx.Rows
	var err error
	if favorites {
		err = s.DB.QueryRow(ctx, `SELECT count(*) FROM favorites WHERE user_id=$1 AND active`, u.ID).Scan(&count)
		if err == nil {
			rows, err = s.DB.Query(ctx, `SELECT product_id FROM favorites WHERE user_id=$1 AND active ORDER BY created_at DESC,product_id DESC LIMIT $2 OFFSET $3`, u.ID, limit, offset)
		}
	} else {
		status := in.Status
		err = s.DB.QueryRow(ctx, `SELECT count(*) FROM products WHERE owner_id=$1 AND deleted_at IS NULL AND ($2='' OR status=$2)`, u.ID, status).Scan(&count)
		if err == nil {
			rows, err = s.DB.Query(ctx, `SELECT id FROM products WHERE owner_id=$1 AND deleted_at IS NULL AND ($2='' OR status=$2) ORDER BY updated_at DESC,id DESC LIMIT $3 OFFSET $4`, u.ID, status, limit, offset)
		}
	}
	if err != nil {
		return nil, memberStoreError(err)
	}
	ids := []string{}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			break
		}
		ids = append(ids, id)
	}
	rowErr := rows.Err()
	rows.Close()
	if err != nil || rowErr != nil {
		return nil, memberStoreError(fmt.Errorf("list read failed"))
	}
	// 整页批量读取且每件商品仅取封面，查询次数不随图集张数增长。
	products := map[string]map[string]any{}
	if len(ids) > 0 {
		where := "p.id=ANY($1::text[])"
		args := []any{ids}
		if favorites {
			where += " AND " + VisibleSQL
		} else {
			where += " AND p.owner_id=$2"
			args = append(args, u.ID)
		}
		batch, e := s.DB.Query(ctx, "SELECT "+fmt.Sprintf(ProductJSON, 1)+" FROM products p JOIN categories c ON c.id=p.category_id WHERE "+where, args...)
		if e != nil {
			return nil, memberStoreError(e)
		}
		for batch.Next() {
			var raw []byte
			if e = batch.Scan(&raw); e != nil {
				break
			}
			var p map[string]any
			if e = json.Unmarshal(raw, &p); e != nil {
				break
			}
			products[p["id"].(string)] = p
		}
		if e == nil {
			e = batch.Err()
		}
		batch.Close()
		if e != nil {
			return nil, memberStoreError(e)
		}
	}
	result := []map[string]any{}
	for _, id := range ids {
		p := products[id]
		if p == nil {
			if !favorites {
				continue
			}
			p = map[string]any{"id": id, "title": "该账号已停止公开", "status": "offline", "price_amount": nil, "images": []any{}, "image_total": 0}
		}
		if favorites {
			catalog.PublicFields(p)
			p["favorite"] = true
		}
		result = append(result, p)
	}
	return map[string]any{"data": result, "total": count, "next_cursor": pageCursor(offset, limit, count)}, nil
}

func (s *MemberStore) Favorite(ctx context.Context, u member.Actor, id string, add bool) (map[string]any, error) {
	if add {
		tag, err := s.DB.Exec(ctx, `INSERT INTO favorites(user_id,product_id) SELECT $1,p.id FROM products p JOIN categories c ON c.id=p.category_id WHERE p.id=$2 AND p.status='published' AND p.approved_version=p.version AND c.enabled ON CONFLICT(user_id,product_id) DO UPDATE SET active=true,updated_at=now()`, u.ID, id)
		if err != nil {
			return nil, memberStoreError(err)
		}
		if tag.RowsAffected() == 0 {
			return nil, fault.New(fault.NotFound, "NOT_FOUND", "该商品当前不可收藏")
		}
	} else {
		if _, err := s.DB.Exec(ctx, `UPDATE favorites SET active=false,updated_at=now() WHERE user_id=$1 AND product_id=$2`, u.ID, id); err != nil {
			return nil, memberStoreError(err)
		}
	}
	return map[string]any{"data": map[string]any{"ok": true}}, nil
}

func (s *MemberStore) Create(ctx context.Context, u member.Actor, input catalog.ProductInput) (map[string]any, error) {
	catalog.NormalizeInput(&input)
	if input.Attributes == nil {
		input.Attributes = map[string]any{}
	}
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return nil, memberStoreError(err)
	}
	defer tx.Rollback(ctx)
	if err := ValidateProductInput(ctx, tx, input, false); err != nil {
		return nil, fault.New(fault.Invalid, "INVALID_PRODUCT", err.Error())
	}
	replay, hash, err := replayProduct(ctx, tx, u.ID, "create-product", input.RequestID, input, s.CredentialKey)
	if err != nil {
		return nil, productReplayError(err)
	}
	if replay != "" {
		tx.Rollback(ctx)
		return s.productResult(ctx, replay)
	}
	if err = checkDraftQuota(ctx, tx, u.ID); err != nil {
		return nil, err
	}
	id := s.NewID()
	code, err := nextProductCode(ctx, tx, input.TradeType)
	if err != nil {
		return nil, memberStoreError(err)
	}
	_, err = tx.Exec(ctx, `INSERT INTO products(id,product_code,owner_id,category_id,title,trade_type,price_amount,price_unit,attributes,description,source,draft_expires_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,'member',now()+interval '72 hours')`, id, code, u.ID, input.CategoryID, input.Title, input.TradeType, input.PriceAmount, input.PriceUnit, input.Attributes, input.Description)
	if err == nil {
		err = saveProductExtras(ctx, tx, id, s.CredentialKey, input)
	}
	if err == nil {
		err = recordProductReplay(ctx, tx, u.ID, "create-product", input.RequestID, hash, id)
	}
	if err == nil {
		err = tx.Commit(ctx)
	}
	if err != nil {
		return nil, memberStoreError(err)
	}
	return s.productResult(ctx, id)
}

func (s *MemberStore) Edit(ctx context.Context, u member.Actor, id string, input catalog.ProductInput) (map[string]any, error) {
	catalog.NormalizeInput(&input)
	if input.Attributes == nil {
		input.Attributes = map[string]any{}
	}
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return nil, memberStoreError(err)
	}
	defer tx.Rollback(ctx)
	version, _, err := lockedOwner(ctx, tx, id, u.ID)
	if err != nil {
		return nil, memberStoreError(err)
	}
	if err := member.CheckVersion(version, input.ExpectedVersion); err != nil {
		return nil, err
	}
	var originalTrade string
	if err = tx.QueryRow(ctx, "SELECT trade_type,attributes,source='legacy' OR published_at IS NOT NULL FROM products WHERE id=$1", id).Scan(&originalTrade, &input.PreviousAttributes, &input.LegacyAttributes); err != nil {
		return nil, memberStoreError(err)
	}
	if originalTrade != input.TradeType {
		return nil, fault.New(fault.Invalid, "TRADE_TYPE_IMMUTABLE", "已保存商品的租售类型不可更改，如需更换请新建资产")
	}
	if err := ValidateProductInput(ctx, tx, input, false); err != nil {
		return nil, fault.New(fault.Invalid, "INVALID_PRODUCT", err.Error())
	}
	_, err = tx.Exec(ctx, `UPDATE products SET category_id=$1,title=$2,trade_type=$3,price_amount=$4,price_unit=$5,attributes=$6,description=$7,status='draft',version=version+1,approved_version=NULL,review_note='',updated_at=now() WHERE id=$8`, input.CategoryID, input.Title, input.TradeType, input.PriceAmount, input.PriceUnit, input.Attributes, input.Description, id)
	if err == nil {
		err = saveProductExtras(ctx, tx, id, s.CredentialKey, input)
	}
	if err == nil {
		err = tx.Commit(ctx)
	}
	if err != nil {
		return nil, memberStoreError(err)
	}
	return s.productResult(ctx, id)
}

func (s *MemberStore) Transition(ctx context.Context, u member.Actor, id string, input member.TransitionInput, submit bool) (map[string]any, error) {
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return nil, memberStoreError(err)
	}
	defer tx.Rollback(ctx)
	version, status, err := lockedOwner(ctx, tx, id, u.ID)
	if err != nil {
		return nil, memberStoreError(err)
	}
	if err := member.CheckVersion(version, input.ExpectedVersion); err != nil {
		return nil, err
	}
	next, err := member.NextStatus(status, submit)
	if err != nil {
		return nil, err
	}
	if submit {
		var p catalog.ProductInput
		err = tx.QueryRow(ctx, `SELECT category_id,title,trade_type,price_amount::text,price_unit,attributes,description,rental_quotes,EXISTS(SELECT 1 FROM product_private_credentials pc WHERE pc.product_id=products.id) FROM products WHERE id=$1`, id).Scan(&p.CategoryID, &p.Title, &p.TradeType, &p.PriceAmount, &p.PriceUnit, &p.Attributes, &p.Description, &p.RentalQuotes, &p.HasPrivateCredentials)
		if err != nil {
			return nil, memberStoreError(err)
		}
		p.PreviousAttributes = p.Attributes
		if err = tx.QueryRow(ctx, `SELECT source='legacy' OR published_at IS NOT NULL FROM products WHERE id=$1`, id).Scan(&p.LegacyAttributes); err != nil {
			return nil, memberStoreError(err)
		}
		if err = ValidateProductInput(ctx, tx, p, true); err != nil {
			return nil, fault.New(fault.Invalid, "INCOMPLETE_PRODUCT", err.Error())
		}
		var count int
		err = tx.QueryRow(ctx, `SELECT count(*) FROM product_images pi JOIN media_files m ON m.id=pi.media_id WHERE pi.product_id=$1 AND m.status='ready'`, id).Scan(&count)
		if err != nil {
			return nil, memberStoreError(err)
		}
		if count == 0 {
			return nil, fault.New(fault.Invalid, "IMAGE_REQUIRED", "请至少添加一张有效图片")
		}
	}
	_, err = tx.Exec(ctx, `UPDATE products SET status=$1,version=version+1,approved_version=NULL,review_note='',updated_at=now() WHERE id=$2`, next, id)
	if err == nil {
		err = tx.Commit(ctx)
	}
	if err != nil {
		return nil, memberStoreError(err)
	}
	return s.productResult(ctx, id)
}

func (s *MemberStore) Gallery(ctx context.Context, u member.Actor, id string, in member.GalleryInput) (map[string]any, error) {
	if in.ReviewID != "" {
		if !u.Admin {
			return nil, fault.New(fault.Forbidden, "FORBIDDEN", "审核记录仅管理员可查看")
		}
		return s.ReviewGallery(ctx, id, in)
	}
	offset, limit := in.Offset, in.Limit
	tx, err := s.DB.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return nil, memberStoreError(err)
	}
	defer tx.Rollback(ctx)
	var version, total int
	err = tx.QueryRow(ctx, `SELECT version FROM products WHERE id=$1 AND deleted_at IS NULL AND (owner_id=$2 OR $3)`, id, u.ID, u.Admin).Scan(&version)
	if err != nil {
		return nil, memberStoreError(err)
	}
	if in.ExpectedVersion != nil {
		wanted := *in.ExpectedVersion
		if err := member.CheckVersion(version, wanted); err != nil {
			return nil, err
		}
	}
	if err = tx.QueryRow(ctx, `SELECT count(*) FROM product_images WHERE product_id=$1`, id).Scan(&total); err != nil {
		return nil, memberStoreError(err)
	}
	rows, err := tx.Query(ctx, `SELECT m.id,m.filename,m.status FROM product_images pi JOIN media_files m ON m.id=pi.media_id WHERE pi.product_id=$1 ORDER BY pi.sort_order,m.id LIMIT $2 OFFSET $3`, id, limit, offset)
	if err != nil {
		return nil, memberStoreError(err)
	}
	data := []map[string]any{}
	for rows.Next() {
		var mid, name, state string
		if err = rows.Scan(&mid, &name, &state); err != nil {
			break
		}
		item := map[string]any{"id": mid, "filename": name, "status": state}
		if state == "ready" {
			item["url"] = "/api/v1/media/" + mid + "/file"
			item["thumbnail_url"] = "/api/v1/media/" + mid + "/file?variant=thumb"
		}
		data = append(data, item)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return nil, memberStoreError(err)
	}
	return map[string]any{"data": data, "total": total, "version": version, "next_cursor": pageCursor(offset, limit, total)}, nil
}

func (s *MemberStore) ReviewGallery(ctx context.Context, id string, in member.GalleryInput) (map[string]any, error) {
	offset, limit := in.Offset, in.Limit
	review := in.ReviewID
	var total, version int
	err := s.DB.QueryRow(ctx, `SELECT version,CASE WHEN jsonb_typeof(snapshot->'images')='array' THEN jsonb_array_length(snapshot->'images') ELSE -1 END FROM product_reviews WHERE id=$1 AND product_id=$2`, review, id).Scan(&version, &total)
	if err != nil {
		return nil, memberStoreError(err)
	}
	if total < 0 {
		return nil, fault.New(fault.Invalid, "REVIEW_GALLERY_UNAVAILABLE", "该历史审核记录未保存图集快照，无法还原历史图片")
	}
	if in.ExpectedVersion != nil {
		wanted := *in.ExpectedVersion
		if err := member.CheckVersion(version, wanted); err != nil {
			return nil, err
		}
	}
	rows, err := s.DB.Query(ctx, `SELECT item->>'id',COALESCE(item->>'alt',''),COALESCE(m.status,'missing')
 FROM product_reviews pr CROSS JOIN LATERAL jsonb_array_elements(pr.snapshot->'images') WITH ORDINALITY AS images(item,position)
 LEFT JOIN media_files m ON m.id=item->>'id' WHERE pr.id=$1 AND pr.product_id=$2 ORDER BY position LIMIT $3 OFFSET $4`, review, id, limit, offset)
	if err != nil {
		return nil, memberStoreError(err)
	}
	data := []map[string]any{}
	for rows.Next() {
		var mid, alt, status string
		if err = rows.Scan(&mid, &alt, &status); err != nil {
			break
		}
		item := map[string]any{"id": mid, "alt": alt, "status": status}
		if status == "ready" {
			item["url"] = "/api/v1/media/" + mid + "/file"
			item["thumbnail_url"] = "/api/v1/media/" + mid + "/file?variant=thumb"
		}
		data = append(data, item)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return nil, memberStoreError(err)
	}
	return map[string]any{"data": data, "total": total, "version": version, "review_id": review, "next_cursor": pageCursor(offset, limit, total)}, nil
}

func (s *MemberStore) AppendImages(ctx context.Context, u member.Actor, id string, input member.AppendImagesInput) (map[string]any, error) {
	if len(input.MediaIDs) < 1 || len(input.MediaIDs) > 20 {
		return nil, fault.New(fault.Invalid, "BATCH_SIZE", "每批追加1至20张，可继续分批添加")
	}
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return nil, memberStoreError(err)
	}
	defer tx.Rollback(ctx)
	replay, hash, err := replayProduct(ctx, tx, u.ID, "append:"+id, input.RequestID, input)
	if err != nil {
		return nil, productReplayError(err)
	}
	if replay != "" {
		tx.Rollback(ctx)
		return s.productResult(ctx, replay)
	}
	version, _, err := lockedImageOwner(ctx, tx, u, id)
	if err != nil {
		return nil, memberStoreError(err)
	}
	if err := member.CheckVersion(version, input.ExpectedVersion); err != nil {
		return nil, err
	}
	var sort int
	err = tx.QueryRow(ctx, `SELECT COALESCE(max(sort_order),0) FROM product_images WHERE product_id=$1`, id).Scan(&sort)
	if err != nil {
		return nil, memberStoreError(err)
	}
	// Lock shared media in one stable order even when clients request opposite display orders.
	mediaRows, err := tx.Query(ctx, `SELECT id,COALESCE(owner_id,''),status FROM media_files WHERE id=ANY($1::text[]) AND purpose='product' AND deleted_at IS NULL ORDER BY id FOR UPDATE`, input.MediaIDs)
	if err != nil {
		return nil, memberStoreError(err)
	}
	ready := map[string]bool{}
	for mediaRows.Next() {
		var mid, owner, state string
		if err = mediaRows.Scan(&mid, &owner, &state); err != nil {
			break
		}
		ready[mid] = owner == u.ID && state == "ready"
	}
	if err == nil {
		err = mediaRows.Err()
	}
	mediaRows.Close()
	if err != nil {
		return nil, memberStoreError(err)
	}
	seen := map[string]bool{}
	for _, mid := range input.MediaIDs {
		if seen[mid] {
			continue
		}
		seen[mid] = true
		if !ready[mid] {
			return nil, fault.New(fault.Invalid, "MEDIA_NOT_READY", "只能使用本人已完成校验的图片")
		}
		var linked string
		err = tx.QueryRow(ctx, `SELECT product_id FROM product_images WHERE media_id=$1`, mid).Scan(&linked)
		if err == nil {
			if linked == id {
				continue
			}
			return nil, fault.New(fault.Invalid, "MEDIA_ALREADY_LINKED", "图片已关联其他商品")
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return nil, memberStoreError(err)
		}
		sort++
		if _, err = tx.Exec(ctx, `INSERT INTO product_images(product_id,media_id,sort_order) VALUES($1,$2,$3)`, id, mid, sort); err != nil {
			return nil, memberStoreError(err)
		}
	}
	_, err = tx.Exec(ctx, `UPDATE products SET version=version+1,status='draft',approved_version=NULL,review_note='',updated_at=now(),
 last_media_upload_at=(SELECT max(COALESCE(ready_at,created_at)) FROM media_files WHERE id=ANY($2::text[])),
 draft_expires_at=CASE WHEN source='member' AND published_at IS NULL AND legacy_id IS NULL THEN GREATEST(COALESCE(draft_expires_at,created_at+interval '72 hours'),(SELECT max(COALESCE(ready_at,created_at))+interval '72 hours' FROM media_files WHERE id=ANY($2::text[]))) ELSE NULL END WHERE id=$1`, id, input.MediaIDs)
	if err == nil {
		err = recordProductReplay(ctx, tx, u.ID, "append:"+id, input.RequestID, hash, id)
	}
	if err == nil {
		err = s.imageAudit(ctx, tx, u, id, "images.append")
	}
	if err == nil {
		err = tx.Commit(ctx)
	}
	if err != nil {
		return nil, memberStoreError(err)
	}
	return s.productResult(ctx, id)
}

func (s *MemberStore) ChangeImage(ctx context.Context, u member.Actor, id, mid string, input member.ChangeImageInput, remove bool) (map[string]any, error) {
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return nil, memberStoreError(err)
	}
	defer tx.Rollback(ctx)
	version, _, err := lockedImageOwner(ctx, tx, u, id)
	if err != nil {
		return nil, memberStoreError(err)
	}
	if err := member.CheckVersion(version, input.ExpectedVersion); err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx, `SELECT media_id FROM product_images WHERE product_id=$1 ORDER BY sort_order,media_id`, id)
	if err != nil {
		return nil, memberStoreError(err)
	}
	ids := []string{}
	found := false
	for rows.Next() {
		var value string
		if err = rows.Scan(&value); err != nil {
			break
		}
		if value == mid {
			found = true
		} else {
			ids = append(ids, value)
		}
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return nil, memberStoreError(err)
	}
	if !found {
		return nil, fault.New(fault.NotFound, "NOT_FOUND", "图片不存在")
	}
	if remove {
		_, err = tx.Exec(ctx, `DELETE FROM product_images WHERE product_id=$1 AND media_id=$2`, id, mid)
	} else {
		at := len(ids)
		if input.BeforeMediaID != nil {
			if *input.BeforeMediaID == mid {
				return map[string]any{"data": map[string]any{"version": version}}, nil
			}
			at = -1
			for i, value := range ids {
				if value == *input.BeforeMediaID {
					at = i
					break
				}
			}
			if at < 0 {
				return nil, fault.New(fault.Conflict, "VERSION_CONFLICT", "目标图片已变化")
			}
		}
		ids = append(ids, "")
		copy(ids[at+1:], ids[at:])
		ids[at] = mid
		// 同一商品的行锁覆盖整个重排，一次批量更新避免逐图数据库往返。
		_, err = tx.Exec(ctx, `UPDATE product_images AS pi SET sort_order=ordered.position::integer
 FROM unnest($2::text[]) WITH ORDINALITY AS ordered(media_id,position)
 WHERE pi.product_id=$1 AND pi.media_id=ordered.media_id`, id, ids)
	}
	if err == nil {
		_, err = tx.Exec(ctx, `UPDATE products SET version=version+1,status='draft',approved_version=NULL,review_note='',updated_at=now() WHERE id=$1`, id)
	}
	if err == nil {
		err = s.imageAudit(ctx, tx, u, id, "images."+map[bool]string{true: "delete", false: "patch"}[remove])
	}
	if err == nil {
		err = tx.Commit(ctx)
	}
	if err != nil {
		return nil, memberStoreError(err)
	}
	return s.productResult(ctx, id)
}

func productReplayError(err error) error {
	if f, ok := fault.As(err); ok {
		return f
	}
	return memberStoreError(err)
}

// 所有权与私有详情在同一查询中约束，不先查权限再读另一份数据。
func (s *MemberStore) Detail(ctx context.Context, u member.Actor, id string) (map[string]any, error) {
	var raw []byte
	err := s.DB.QueryRow(ctx, "SELECT "+fmt.Sprintf(ProductJSON, 20)+" FROM products p JOIN categories c ON c.id=p.category_id WHERE p.id=$1 AND p.owner_id=$2 AND p.deleted_at IS NULL", id, u.ID).Scan(&raw)
	if err != nil {
		return nil, memberStoreError(err)
	}
	var data map[string]any
	if err = json.Unmarshal(raw, &data); err != nil {
		return nil, memberStoreError(err)
	}
	return map[string]any{"data": data}, nil
}
