package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"qingye/backend/internal/domain/catalog"
	"qingye/backend/internal/domain/fault"
	"qingye/backend/internal/domain/management"
	"reflect"
	"strings"
)

func (s *CatalogStore) Dictionaries(ctx context.Context, private bool) ([]map[string]any, error) {
	return s.JSONRows(ctx, `SELECT jsonb_build_object('key',g.key,'label',g.label,'version',g.version,'options',COALESCE((SELECT jsonb_agg(jsonb_build_object('value',o.value,'label',o.label,'enabled',o.enabled,'sort_order',o.sort_order) ORDER BY o.sort_order,o.value) FROM product_dictionary_options o WHERE o.dictionary_key=g.key AND ($1 OR o.enabled)),'[]'::jsonb)) FROM product_dictionary_groups g ORDER BY g.sort_order,g.key`, private)
}
func (s *ManagementStore) SaveDictionary(ctx context.Context, actor management.Actor, key string, in catalog.DictionaryInput) (map[string]any, error) {
	if err := management.ValidateActor(actor); err != nil {
		return nil, err
	}
	if err := catalog.ValidateDictionary(key, in); err != nil {
		return nil, err
	}
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return nil, managementStoreError(err)
	}
	defer tx.Rollback(ctx)
	var version int
	if err = tx.QueryRow(ctx, `SELECT version FROM product_dictionary_groups WHERE key=$1 FOR UPDATE`, key).Scan(&version); err != nil {
		return nil, managementStoreError(err)
	}
	if version != in.ExpectedVersion {
		return nil, fault.New(fault.Conflict, "VERSION_CONFLICT", "字典已更新，请重新加载")
	}
	values := make([]string, 0, len(in.Options))
	for _, o := range in.Options {
		values = append(values, o.Value)
	}
	var used bool
	err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM product_dictionary_options o JOIN products p ON (p.attributes->>$1=o.value OR (jsonb_typeof(p.attributes->$1)='array' AND (p.attributes->$1) ? o.value)) WHERE o.dictionary_key=$1 AND NOT(o.value=ANY($2::text[])) AND p.deleted_at IS NULL)`, key, values).Scan(&used)
	if err != nil {
		return nil, managementStoreError(err)
	}
	if used {
		return nil, fault.New(fault.Conflict, "DICTIONARY_IN_USE", "已有商品引用被删除选项，请停用并保留编码")
	}
	if _, err = tx.Exec(ctx, `DELETE FROM product_dictionary_options WHERE dictionary_key=$1 AND NOT(value=ANY($2::text[]))`, key, values); err != nil {
		return nil, managementStoreError(err)
	}
	for _, o := range in.Options {
		if _, err = tx.Exec(ctx, `INSERT INTO product_dictionary_options(dictionary_key,value,label,enabled,sort_order) VALUES($1,$2,$3,$4,$5) ON CONFLICT(dictionary_key,value) DO UPDATE SET label=EXCLUDED.label,enabled=EXCLUDED.enabled,sort_order=EXCLUDED.sort_order`, key, o.Value, o.Label, o.Enabled, o.SortOrder); err != nil {
			return nil, managementStoreError(err)
		}
	}
	_, err = tx.Exec(ctx, `UPDATE product_dictionary_groups SET version=version+1 WHERE key=$1`, key)
	if err == nil {
		_, err = tx.Exec(ctx, `INSERT INTO admin_logs(id,user_id,action,resource_id) VALUES($1,$2,'dictionary.save',$3)`, s.NewID(), actor.ID, key)
	}
	if err == nil {
		err = tx.Commit(ctx)
	}
	if err != nil {
		return nil, managementStoreError(err)
	}
	groups, err := (&CatalogStore{DB: s.DB}).Dictionaries(ctx, true)
	if err != nil {
		return nil, managementStoreError(err)
	}
	for _, g := range groups {
		if g["key"] == key {
			return map[string]any{"data": g}, nil
		}
	}
	return nil, fault.New(fault.NotFound, "NOT_FOUND", "字典不存在")
}

// All product writes hold group locks in the same order as dictionary updates.
func validateDictionaryAttributes(ctx context.Context, q RowQuerier, p catalog.ProductInput, complete bool) error {
	for _, key := range []string{"gender", "level", "server"} {
		var label string
		if err := q.QueryRow(ctx, `SELECT label FROM product_dictionary_groups WHERE key=$1 FOR SHARE`, key).Scan(&label); err != nil {
			return errors.New("发布字典暂不可用")
		}
		value := p.Attributes[key]
		previous, hadPrevious := p.PreviousAttributes[key]
		text, isText := value.(string)
		empty := value == nil || isText && strings.TrimSpace(text) == ""
		if empty {
			if complete && (key == "server" || key == "gender") {
				if p.LegacyAttributes && hadPrevious && sameAttribute(value, previous) {
					continue
				}
				return fmt.Errorf("请填写%s", label)
			}
			continue
		}
		if hadPrevious && sameAttribute(value, previous) && (isText || p.LegacyAttributes) {
			continue
		}
		if !isText {
			return fmt.Errorf("%s只能选择一项", label)
		}
		var allowed bool
		if err := q.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM product_dictionary_options WHERE dictionary_key=$1 AND value=$2 AND enabled)`, key, text).Scan(&allowed); err != nil {
			return errors.New("发布字典暂不可用")
		}
		if !allowed {
			return fmt.Errorf("%s选项不存在或已停用，请重新选择", label)
		}
	}
	return nil
}
func sameAttribute(a, b any) bool {
	x, e1 := json.Marshal(a)
	y, e2 := json.Marshal(b)
	return e1 == nil && e2 == nil && reflect.DeepEqual(x, y)
}
