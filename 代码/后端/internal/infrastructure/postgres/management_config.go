package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"qingye/backend/internal/domain/catalog"
	"qingye/backend/internal/domain/fault"
	"qingye/backend/internal/domain/management"
)

type ManagementStore struct {
	DB    *pgxpool.Pool
	NewID func() string
}

func managementStoreError(err error) error {
	if f, ok := fault.As(err); ok {
		return f
	}
	return fault.Wrap(fault.Unavailable, "DATABASE_UNAVAILABLE", "数据暂时不可用，请稍后重试", err)
}
func managementCursor(offset, limit, total int) any { return catalog.NextPage(offset, limit, total) }
func (s *ManagementStore) jsonRows(ctx context.Context, sql string, args ...any) ([]map[string]any, error) {
	rows, err := s.DB.Query(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []map[string]any{}
	for rows.Next() {
		var raw []byte
		if err = rows.Scan(&raw); err != nil {
			return nil, err
		}
		var item map[string]any
		if err = json.Unmarshal(raw, &item); err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

// 分类锁覆盖旧规则检查与保存；商品写事务持有同一分类的共享锁。
func (s *ManagementStore) SaveCategory(ctx context.Context, actor management.Actor, id string, create bool, in management.CategoryInput) (map[string]any, error) {
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return nil, managementStoreError(err)
	}
	defer tx.Rollback(ctx)
	// 新分类还没有可锁的行，事务锁同时保护并发创建时的冲突契约。
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "category:"+id); err != nil {
		return nil, managementStoreError(err)
	}
	var existingType string
	var raw []byte
	err = tx.QueryRow(ctx, `SELECT trade_type,filter_config FROM categories WHERE id=$1 FOR UPDATE`, id).Scan(&existingType, &raw)
	if create {
		if err == nil {
			return nil, fault.New(fault.Conflict, "CATEGORY_EXISTS", "分类编码已存在")
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return nil, managementStoreError(err)
		}
	} else {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fault.New(fault.NotFound, "NOT_FOUND", "分类不存在")
		}
		if err != nil {
			return nil, managementStoreError(err)
		}
		if in.TradeType != "" && in.TradeType != existingType {
			return nil, fault.New(fault.Invalid, "IMMUTABLE_TRADE_TYPE", "已有分类的交易类型不可修改，请新建分类")
		}
		in.TradeType = existingType
	}
	if err = management.ValidateCategoryRules(in); err != nil {
		return nil, err
	}
	if !create {
		var old []catalog.FilterGroup
		if err = json.Unmarshal(raw, &old); err != nil {
			return nil, managementStoreError(err)
		}
		removed := management.RemovedOptions(old, in.Filters)
		if len(removed) > 0 {
			// 一次查询验证全部移除选项，避免每个选项一次数据库往返。
			encoded, _ := json.Marshal(removed)
			var used bool
			err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM products p
    CROSS JOIN jsonb_to_recordset($2::jsonb) AS option(key text,value text)
    WHERE p.category_id=$1 AND (p.attributes->>option.key=option.value OR (p.attributes->option.key) ? option.value))`, id, encoded).Scan(&used)
			if err != nil {
				return nil, managementStoreError(err)
			}
			if used {
				return nil, fault.New(fault.Conflict, "FILTER_IN_USE", "已有商品引用此选项，请设置 enabled:false 停用并保留编码")
			}
		}
	}
	units, _ := json.Marshal(in.PriceUnits)
	filters, _ := json.Marshal(in.Filters)
	if in.PriceUnits == nil {
		units = []byte("[]")
	}
	if in.Filters == nil {
		filters = []byte("[]")
	}
	var out map[string]any
	if create {
		out, err = s.configWriteTx(ctx, tx, actor, "category", id, `INSERT INTO categories(id,name,trade_type,price_units,filter_config,sort_order,enabled) VALUES($1,$2,$3,$4,$5,$6,$7) RETURNING to_jsonb(categories)`, id, in.Name, in.TradeType, units, filters, in.SortOrder, in.Enabled)
	} else {
		out, err = s.configWriteTx(ctx, tx, actor, "category", id, `UPDATE categories SET name=$2,price_units=$3,filter_config=$4,sort_order=$5,enabled=$6 WHERE id=$1 RETURNING to_jsonb(categories)`, id, in.Name, units, filters, in.SortOrder, in.Enabled)
	}
	if err != nil {
		return nil, err
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, managementStoreError(err)
	}
	return out, nil
}

// 二维码权限检查、客服修改及审计必须作为一个事务提交。
func (s *ManagementStore) SaveContact(ctx context.Context, actor management.Actor, id string, in management.ContactInput) (map[string]any, error) {
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return nil, managementStoreError(err)
	}
	defer tx.Rollback(ctx)
	create := id == ""
	if !create {
		var existing string
		err = tx.QueryRow(ctx, `SELECT id FROM contacts WHERE id=$1 FOR UPDATE`, id).Scan(&existing)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fault.New(fault.NotFound, "NOT_FOUND", "配置不存在")
		}
		if err != nil {
			return nil, managementStoreError(err)
		}
	}
	if in.QRMediaID != nil && *in.QRMediaID != "" {
		var mediaID string
		err = tx.QueryRow(ctx, `SELECT m.id FROM media_files m WHERE m.id=$1 AND m.status='ready'
   AND (m.owner_id=$2 OR EXISTS(SELECT 1 FROM contacts c WHERE c.id=$3 AND c.qr_media_id=m.id)) FOR SHARE OF m`, *in.QRMediaID, actor.ID, id).Scan(&mediaID)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fault.New(fault.Invalid, "INVALID_MEDIA", "请选择本人已上传的二维码素材")
		}
		if err != nil {
			return nil, managementStoreError(err)
		}
	} else {
		in.QRMediaID = nil
	}
	purpose, _ := json.Marshal(in.Purpose)
	if in.Purpose == nil {
		purpose = []byte("[]")
	}
	var out map[string]any
	if create {
		id = s.NewID()
		out, err = s.configWriteTx(ctx, tx, actor, "contact", id, `INSERT INTO contacts(id,name,purpose,channel,contact_value,qr_media_id,hours,enabled) VALUES($1,$2,$3,$4,$5,$6,$7,$8) RETURNING to_jsonb(contacts)`, id, in.Name, purpose, in.Channel, in.Value, in.QRMediaID, in.Hours, in.Enabled)
	} else {
		out, err = s.configWriteTx(ctx, tx, actor, "contact", id, `UPDATE contacts SET name=$2,purpose=$3,channel=$4,contact_value=$5,qr_media_id=$6,hours=$7,enabled=$8 WHERE id=$1 RETURNING to_jsonb(contacts)`, id, in.Name, purpose, in.Channel, in.Value, in.QRMediaID, in.Hours, in.Enabled)
	}
	if err != nil {
		return nil, err
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, managementStoreError(err)
	}
	return out, nil
}
func (s *ManagementStore) SavePage(ctx context.Context, actor management.Actor, slug string, in management.PageInput) (map[string]any, error) {
	return s.configWrite(ctx, actor, "page", slug, `INSERT INTO content_pages(slug,title,body,published) VALUES($1,$2,$3,$4) ON CONFLICT(slug) DO UPDATE SET title=EXCLUDED.title,body=EXCLUDED.body,published=EXCLUDED.published,version=content_pages.version+1 RETURNING to_jsonb(content_pages)`, slug, in.Title, in.Body, in.Published)
}
func (s *ManagementStore) SaveSetting(ctx context.Context, actor management.Actor, key string, in management.SettingInput) (map[string]any, error) {
	return s.configWrite(ctx, actor, "setting", key, `INSERT INTO site_settings(key,value) VALUES($1,$2) ON CONFLICT(key) DO UPDATE SET value=EXCLUDED.value,updated_at=now() RETURNING to_jsonb(site_settings)`, key, in.Value)
}
func (s *ManagementStore) Users(ctx context.Context, actor management.Actor, q string, limit, offset int) (map[string]any, error) {
	var total int
	e := s.DB.QueryRow(ctx, "SELECT count(*) FROM users WHERE nickname ILIKE '%'||$1||'%' OR phone=$1", q).Scan(&total)
	if e != nil {
		return nil, managementStoreError(e)
	}
	rows, e := s.jsonRows(ctx, `SELECT jsonb_build_object('id',id,'nickname',nickname,'phone_masked',left(phone,3)||'****'||right(phone,4),'role',role,'enabled',enabled,'created_at',created_at) FROM users WHERE nickname ILIKE '%'||$1||'%' OR phone=$1 ORDER BY created_at DESC,id LIMIT $2 OFFSET $3`, q, limit, offset)
	if e != nil {
		return nil, managementStoreError(e)
	}
	return map[string]any{"data": rows, "total": total, "next_cursor": managementCursor(offset, limit, total)}, nil
}
func (s *ManagementStore) UpdateUser(ctx context.Context, actor management.Actor, id string, in management.UserInput) (map[string]any, error) {
	tx, e := s.DB.Begin(ctx)
	if e != nil {
		return nil, managementStoreError(e)
	}
	defer tx.Rollback(ctx)
	_, e = tx.Exec(ctx, "SELECT pg_advisory_xact_lock(1834181)")
	if e != nil {
		return nil, managementStoreError(e)
	}
	var role string
	e = tx.QueryRow(ctx, "SELECT role FROM users WHERE id=$1 FOR UPDATE", id).Scan(&role)
	if errors.Is(e, pgx.ErrNoRows) {
		return nil, fault.New(fault.NotFound, "NOT_FOUND", "用户不存在")
	}
	if e != nil {
		return nil, managementStoreError(e)
	}
	if role == "admin" && !*in.Enabled {
		var other int
		e = tx.QueryRow(ctx, "SELECT count(*) FROM users WHERE role='admin' AND enabled AND id<>$1", id).Scan(&other)
		if e != nil {
			return nil, managementStoreError(e)
		}
		if other == 0 {
			return nil, fault.New(fault.Conflict, "LAST_ADMIN", "需保留至少一位可用管理员")
		}
	}
	_, e = tx.Exec(ctx, "UPDATE users SET enabled=$2,updated_at=now() WHERE id=$1", id, *in.Enabled)
	if e == nil && !*in.Enabled {
		_, e = tx.Exec(ctx, "DELETE FROM sessions WHERE user_id=$1", id)
	}
	if e == nil {
		_, e = tx.Exec(ctx, "INSERT INTO admin_logs(id,user_id,action,resource_id,detail) VALUES($1,$2,'user.enabled',$3,$4)", s.NewID(), actor.ID, id, map[string]bool{"enabled": *in.Enabled})
	}
	if e == nil {
		e = tx.Commit(ctx)
	}
	if e != nil {
		return nil, managementStoreError(e)
	}
	return map[string]any{"data": map[string]any{"id": id, "enabled": *in.Enabled}}, nil
}
func (s *ManagementStore) configWrite(ctx context.Context, actor management.Actor, resource, id, sql string, args ...any) (map[string]any, error) {
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return nil, managementStoreError(err)
	}
	defer tx.Rollback(ctx)
	out, err := s.configWriteTx(ctx, tx, actor, resource, id, sql, args...)
	if err != nil {
		return nil, err
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, managementStoreError(err)
	}
	return out, nil
}
func (s *ManagementStore) configWriteTx(ctx context.Context, tx pgx.Tx, actor management.Actor, resource, id, sql string, args ...any) (map[string]any, error) {
	var raw []byte
	err := tx.QueryRow(ctx, sql, args...).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, fault.New(fault.NotFound, "NOT_FOUND", "配置不存在")
	}
	if err != nil {
		return nil, managementStoreError(err)
	}
	_, err = tx.Exec(ctx, `INSERT INTO admin_logs(id,user_id,action,resource_id) VALUES($1,$2,$3,$4)`, s.NewID(), actor.ID, resource+".save", id)
	if err != nil {
		return nil, managementStoreError(err)
	}
	return map[string]any{"data": json.RawMessage(raw)}, nil
}
func (s *ManagementStore) Categories(ctx context.Context) ([]map[string]any, error) {
	return s.jsonRows(ctx, `SELECT to_jsonb(c) FROM categories c ORDER BY sort_order,id`)
}
func (s *ManagementStore) Contacts(ctx context.Context) ([]map[string]any, error) {
	return s.jsonRows(ctx, `SELECT to_jsonb(c)||jsonb_build_object('qr_url',CASE WHEN qr_media_id IS NOT NULL THEN '/api/v1/media/'||qr_media_id||'/file' ELSE qr_url END) FROM contacts c ORDER BY id`)
}
func (s *ManagementStore) Pages(ctx context.Context) ([]map[string]any, error) {
	return s.jsonRows(ctx, `SELECT to_jsonb(p) FROM content_pages p ORDER BY slug`)
}
func (s *ManagementStore) Settings(ctx context.Context) ([]map[string]any, error) {
	return s.jsonRows(ctx, `SELECT to_jsonb(s) FROM site_settings s WHERE key IN ('home_presentation','recommendation') ORDER BY key`)
}
