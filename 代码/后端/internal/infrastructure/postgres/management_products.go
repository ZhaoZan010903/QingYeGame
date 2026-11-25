package postgres

import (
	"context"
	"errors"
	"github.com/jackc/pgx/v5"
	"qingye/backend/internal/domain/catalog"
	"qingye/backend/internal/domain/fault"
	"qingye/backend/internal/domain/management"
	"strconv"
	"strings"
	"unicode/utf8"
)

func (s *ManagementStore) SaveProduct(ctx context.Context, actor management.Actor, id string, in catalog.ProductInput) (map[string]any, error) {
	catalog.NormalizeInput(&in)
	tx, e := s.DB.Begin(ctx)
	if e != nil {
		return nil, managementStoreError(e)
	}
	defer tx.Rollback(ctx)
	validateInput := func() error {
		check := in
		if id != "" && utf8.RuneCountInString(in.Title) > 1000 {
			var original, source string
			if tx.QueryRow(ctx, "SELECT title,source FROM products WHERE id=$1", id).Scan(&original, &source) == nil && source == "legacy" && original == in.Title {
				check.Title = string([]rune(original)[:1000])
			}
		}
		if e := ValidateProductInput(ctx, tx, check, false); e != nil {
			return fault.New(fault.Invalid, "INVALID_PRODUCT", e.Error())
		}
		return nil
	}
	if id == "" {
		needsDefaultCategory := in.TradeType == "sale" && in.CategoryID == ""
		if !needsDefaultCategory {
			if e = validateInput(); e != nil {
				return nil, e
			}
		}
		// Hash the supplied request before resolving a default category. A retry
		// remains the same request if administrators later reorder categories.
		replay, hash, replayErr := replayProduct(ctx, tx, actor.ID, "admin-create", in.RequestID, in, s.CredentialKey)
		if replayErr != nil {
			return nil, productReplayError(replayErr)
		}
		if replay != "" {
			tx.Rollback(ctx)
			p, err := (&CatalogStore{DB: s.DB}).AdminProductView(ctx, replay)
			if err != nil {
				return nil, managementStoreError(err)
			}
			return map[string]any{"data": p}, nil
		}
		if needsDefaultCategory {
			e = tx.QueryRow(ctx, `SELECT id FROM categories WHERE enabled AND trade_type IN ('sale','all') AND (price_units='[]'::jsonb OR price_units ? 'account') ORDER BY sort_order,id LIMIT 1 FOR SHARE`).Scan(&in.CategoryID)
			if errors.Is(e, pgx.ErrNoRows) {
				return nil, fault.New(fault.Invalid, "SALE_CATEGORY_UNAVAILABLE", "暂无可用的出售分类，请先启用支持账号出售的分类")
			}
			if e != nil {
				return nil, managementStoreError(e)
			}
			if e = validateInput(); e != nil {
				return nil, e
			}
		}
		id = s.NewID()
		code, err := nextProductCode(ctx, tx, in.TradeType)
		if err != nil {
			return nil, managementStoreError(err)
		}
		_, e = tx.Exec(ctx, `INSERT INTO products(id,product_code,category_id,title,trade_type,price_unit,price_amount,attributes,description,source) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,'platform')`, id, code, in.CategoryID, strings.TrimSpace(in.Title), in.TradeType, in.PriceUnit, in.PriceAmount, in.Attributes, in.Description)
		if e == nil {
			e = recordProductReplay(ctx, tx, actor.ID, "admin-create", in.RequestID, hash, id)
		}
	} else {
		var version int
		var originalTrade, originalStatus string
		e = tx.QueryRow(ctx, "SELECT version,trade_type,status,attributes,source='legacy' OR published_at IS NOT NULL FROM products WHERE id=$1 AND deleted_at IS NULL FOR UPDATE", id).Scan(&version, &originalTrade, &originalStatus, &in.PreviousAttributes, &in.LegacyAttributes)
		if errors.Is(e, pgx.ErrNoRows) {
			return nil, fault.New(fault.NotFound, "NOT_FOUND", "商品不存在")
		}
		if e == nil && originalStatus == "rejected_final" {
			return nil, fault.New(fault.Conflict, "PRODUCT_FINALLY_REJECTED", "此商品已被最终驳回，不能通过编辑恢复")
		}
		if e == nil && version != in.ExpectedVersion {
			return nil, fault.New(fault.Conflict, "VERSION_CONFLICT", "商品已更新，请重新加载")
		}
		if e == nil && originalTrade != in.TradeType {
			return nil, fault.New(fault.Invalid, "TRADE_TYPE_IMMUTABLE", "已保存商品的租售类型不可更改，如需更换请新建资产")
		}
		if e == nil {
			catalog.PreserveHistoricalKind(&in)
			e = validateInput()
		}
		if e == nil {
			_, e = tx.Exec(ctx, `UPDATE products SET category_id=$2,title=$3,trade_type=$4,price_unit=$5,price_amount=$6,attributes=$7,description=$8,status='draft',version=version+1,approved_version=NULL,review_note='',updated_at=now() WHERE id=$1`, id, in.CategoryID, strings.TrimSpace(in.Title), in.TradeType, in.PriceUnit, in.PriceAmount, in.Attributes, in.Description)
		}
	}
	if e != nil {
		return nil, managementStoreError(e)
	}
	if e = saveProductExtras(ctx, tx, id, s.CredentialKey, in); e != nil {
		return nil, managementStoreError(e)
	}
	_, e = tx.Exec(ctx, "INSERT INTO admin_logs(id,user_id,action,resource_id) VALUES($1,$2,'product.save',$3)", s.NewID(), actor.ID, id)
	if e == nil {
		e = tx.Commit(ctx)
	}
	if e != nil {
		return nil, managementStoreError(e)
	}
	p, e := (&CatalogStore{DB: s.DB}).AdminProductView(ctx, id)
	if e != nil {
		return nil, managementStoreError(e)
	}
	return map[string]any{"data": p}, nil
}
func (s *ManagementStore) TransitionProduct(ctx context.Context, actor management.Actor, id, operation string, in management.TransitionInput) (map[string]any, error) {
	tx, e := s.DB.Begin(ctx)
	if e != nil {
		return nil, managementStoreError(e)
	}
	defer tx.Rollback(ctx)
	var status, source, title, description, tradeType, rentalCategory string
	var version int
	e = tx.QueryRow(ctx, "SELECT status,source,title,description,version,trade_type,rental_category FROM products WHERE id=$1 AND deleted_at IS NULL FOR UPDATE", id).Scan(&status, &source, &title, &description, &version, &tradeType, &rentalCategory)
	if errors.Is(e, pgx.ErrNoRows) {
		return nil, fault.New(fault.NotFound, "NOT_FOUND", "商品不存在")
	}
	if e != nil {
		return nil, managementStoreError(e)
	}
	if version != in.ExpectedVersion {
		return nil, fault.New(fault.Conflict, "VERSION_CONFLICT", "商品版本已变化")
	}
	// Review snapshots now lock their media references. Acquire quota and file
	// rows before objects, matching purge/worker order even when another product's
	// historical review still refers to a file that has been reassociated here.
	var mediaIDs, owners []string
	e = tx.QueryRow(ctx, `WITH media AS (
 SELECT media_id FROM product_images WHERE product_id=$1
 UNION SELECT media_id FROM media_objects WHERE product_id=$1
 ) SELECT coalesce(array_agg(DISTINCT m.id ORDER BY m.id),'{}'),
 coalesce(array_agg(DISTINCT m.owner_id ORDER BY m.owner_id) FILTER(WHERE m.owner_id IS NOT NULL),'{}')
 FROM media_files m JOIN media ON media.media_id=m.id`, id).Scan(&mediaIDs, &owners)
	if e != nil {
		return nil, managementStoreError(e)
	}
	for _, owner := range owners {
		if e = lockMemberQuota(ctx, tx, owner); e != nil {
			return nil, managementStoreError(e)
		}
	}
	if _, e = tx.Exec(ctx, `SELECT id FROM media_files WHERE id=ANY($1::text[]) ORDER BY id FOR UPDATE`, mediaIDs); e != nil {
		return nil, managementStoreError(e)
	}
	if _, e = tx.Exec(ctx, `SELECT media_id FROM media_objects WHERE product_id=$1 ORDER BY media_id,variant FOR UPDATE`, id); e != nil {
		return nil, managementStoreError(e)
	}
	target, action, e := management.Transition(status, source, operation, in)
	if e != nil {
		return nil, e
	}
	previousRentalCategory := rentalCategory
	if in.RentalCategory != nil {
		rentalCategory = *in.RentalCategory
	}
	if !catalog.ValidRentalCategory(rentalCategory) || tradeType != "rent" && rentalCategory != "" {
		return nil, fault.New(fault.Invalid, "INVALID_RENTAL_CATEGORY", "出租分类无效或不适用于出售商品")
	}
	if target == "published" && tradeType == "rent" && rentalCategory == "" {
		return nil, fault.New(fault.Invalid, "RENTAL_CATEGORY_REQUIRED", "请先选择时/日租号或周/月租号分类")
	}
	if target == "sold" && tradeType != "sale" || target == "rented" && tradeType != "rent" {
		return nil, fault.New(fault.Invalid, "INVALID_STATE", "成交状态与交易类型不符")
	}
	if target == "pending" || target == "published" {
		var input catalog.ProductInput
		e = tx.QueryRow(ctx, "SELECT category_id,title,trade_type,price_unit,price_amount::text,attributes,description,rental_quotes,EXISTS(SELECT 1 FROM product_private_credentials pc WHERE pc.product_id=products.id) FROM products WHERE id=$1", id).Scan(&input.CategoryID, &input.Title, &input.TradeType, &input.PriceUnit, &input.PriceAmount, &input.Attributes, &input.Description, &input.RentalQuotes, &input.HasPrivateCredentials)
		if e != nil {
			return nil, managementStoreError(e)
		}
		input.PreviousAttributes = input.Attributes
		if e = tx.QueryRow(ctx, `SELECT source='legacy' OR published_at IS NOT NULL FROM products WHERE id=$1`, id).Scan(&input.LegacyAttributes); e != nil {
			return nil, managementStoreError(e)
		}
		if source == "legacy" && utf8.RuneCountInString(input.Title) > 1000 {
			input.Title = string([]rune(input.Title)[:1000])
		}
		if e = ValidateProductInput(ctx, tx, input, true); e != nil {
			return nil, fault.New(fault.Invalid, "INVALID_PRODUCT", e.Error())
		}
		var ready, total int
		var enabled bool
		e = tx.QueryRow(ctx, "SELECT c.enabled FROM products p JOIN categories c ON c.id=p.category_id WHERE p.id=$1", id).Scan(&enabled)
		if e == nil {
			e = tx.QueryRow(ctx, "SELECT count(*) FILTER(WHERE m.status='ready' AND m.deleted_at IS NULL AND NOT EXISTS(SELECT 1 FROM media_objects o WHERE o.media_id=m.id AND (o.force_delete OR o.deleted_at IS NOT NULL))),count(*) FROM product_images pi JOIN media_files m ON m.id=pi.media_id WHERE pi.product_id=$1", id).Scan(&ready, &total)
		}
		if e != nil {
			return nil, managementStoreError(e)
		}
		if !enabled || strings.TrimSpace(title) == "" || strings.TrimSpace(description) == "" || ready == 0 || ready != total {
			return nil, fault.New(fault.Invalid, "INCOMPLETE_PRODUCT", "请补全资料并等待全部图片就绪")
		}
	}
	_, e = tx.Exec(ctx, `UPDATE products SET status=$2,version=version+1,approved_version=CASE WHEN $2='published' THEN version+1 ELSE NULL END,review_note=$3,published_at=CASE WHEN $2='published' THEN COALESCE(published_at,now()) ELSE published_at END,rental_category=$4,updated_at=now() WHERE id=$1`, id, target, strings.TrimSpace(in.Reason), rentalCategory)
	if e == nil {
		_, e = tx.Exec(ctx, `INSERT INTO product_reviews(id,product_id,reviewer_id,decision,reason,version,snapshot)
SELECT $1,id,$2,$3,$4,version,(to_jsonb(products)-'legacy_payload'-'recommendation_boost'-'recommendation_version')||jsonb_build_object('images',
COALESCE((SELECT jsonb_agg(jsonb_build_object('id',m.id,'sort_order',pi.sort_order,'alt',m.filename) ORDER BY pi.sort_order,m.id) FROM product_images pi JOIN media_files m ON m.id=pi.media_id WHERE pi.product_id=products.id),'[]'::jsonb))
FROM products WHERE id=$5`, s.NewID(), actor.ID, target, in.Reason, id)
	}
	if e == nil {
		_, e = tx.Exec(ctx, "INSERT INTO admin_logs(id,user_id,action,resource_id,detail) VALUES($1,$2,$3,$4,$5)", s.NewID(), actor.ID, "product."+action, id, map[string]any{"rental_category": rentalCategory, "previous_rental_category": previousRentalCategory})
	}
	if e == nil {
		e = tx.Commit(ctx)
	}
	if e != nil {
		return nil, managementStoreError(e)
	}
	p, e := (&CatalogStore{DB: s.DB}).AdminProductView(ctx, id)
	if e != nil {
		return nil, managementStoreError(e)
	}
	return map[string]any{"data": p}, nil
}
func (s *ManagementStore) FeatureProduct(ctx context.Context, actor management.Actor, id string, rank *int) (map[string]any, error) {
	if rank != nil && (*rank < 0 || *rank > 10000) {
		return nil, fault.New(fault.Invalid, "INVALID_RANK", "精选顺序无效")
	}
	tx, e := s.DB.Begin(ctx)
	if e != nil {
		return nil, managementStoreError(e)
	}
	defer tx.Rollback(ctx)
	tag, e := tx.Exec(ctx, "UPDATE products SET manual_rank=$2 WHERE id=$1 AND status='published' AND approved_version=version", id, rank)
	if e != nil {
		return nil, managementStoreError(e)
	}
	if tag.RowsAffected() == 0 {
		return nil, fault.New(fault.Conflict, "INVALID_STATE", "仅已展示商品可以设置精选")
	}
	_, e = tx.Exec(ctx, "INSERT INTO admin_logs(id,user_id,action,resource_id,detail) VALUES($1,$2,'product.featured',$3,$4)", s.NewID(), actor.ID, id, map[string]any{"manual_rank": rank})
	if e == nil {
		e = tx.Commit(ctx)
	}
	if e != nil {
		return nil, managementStoreError(e)
	}
	p, e := (&CatalogStore{DB: s.DB}).AdminProductView(ctx, id)
	if e != nil {
		return nil, managementStoreError(e)
	}
	return map[string]any{"data": p}, nil
}

func (s *ManagementStore) Products(ctx context.Context, in management.ProductQuery) (map[string]any, error) {
	where := "p.deleted_at IS NULL"
	if in.Trash {
		where = "p.deleted_at IS NOT NULL AND p.deleted_by_admin"
	}
	if in.Visibility == "public" {
		where += " AND " + VisibleSQL
	}
	args := []any{}
	// 列名固定，用户输入只作为绑定参数。
	for _, item := range []struct{ column, value string }{{"status", in.Status}, {"source", in.Source}, {"trade_type", in.TradeType}, {"rental_category", in.RentalCategory}} {
		if item.value != "" && item.value != "all" {
			args = append(args, item.value)
			where += " AND p." + item.column + "=$" + strconv.Itoa(len(args))
		}
	}
	if in.Query != "" {
		args = append(args, in.Query)
		n := strconv.Itoa(len(args))
		where += " AND (p.title ILIKE '%'||$" + n + "||'%' OR p.product_code ILIKE '%'||$" + n + "||'%')"
	}
	order := "p.updated_at DESC,p.id"
	if in.Sort == "published" {
		order = "p.published_at DESC NULLS LAST,p.id"
	}
	if in.Sort == "created" {
		order = "p.created_at DESC,p.id"
	}
	items, total, err := (&CatalogStore{DB: s.DB}).projectedProductRows(ctx, where, args, order, in.Limit, in.Offset, true, true, AdminProductJSON)
	if err != nil {
		return nil, managementStoreError(err)
	}
	return map[string]any{"data": items, "total": total, "next_cursor": catalog.NextPage(in.Offset, in.Limit, total), "page": in.Offset/in.Limit + 1, "page_size": in.Limit}, nil
}
func (s *ManagementStore) Product(ctx context.Context, id string) (map[string]any, error) {
	p, err := (&CatalogStore{DB: s.DB}).AdminProductView(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, fault.New(fault.NotFound, "NOT_FOUND", "商品不存在")
	}
	if err != nil {
		return nil, managementStoreError(err)
	}
	return map[string]any{"data": p}, nil
}
