package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"qingye/backend/internal/domain/catalog"
	"qingye/backend/internal/domain/fault"
	"strconv"
	"strings"
)

type CatalogStore struct{ DB *pgxpool.Pool }
type RowQuerier interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}
type RowsQuerier interface {
	RowQuerier
	Query(context.Context, string, ...any) (pgx.Rows, error)
}

func ValidateProductInput(ctx context.Context, q RowQuerier, p catalog.ProductInput, complete bool) error {
	if err := catalog.ValidateBasicInput(p, complete); err != nil {
		return err
	}
	var c catalog.CategoryRules
	// 写用例传入同一事务，分类规则在商品提交前不可被并发修改。
	if err := q.QueryRow(ctx, "SELECT enabled,trade_type,price_units,filter_config FROM categories WHERE id=$1 FOR SHARE", p.CategoryID).Scan(&c.Enabled, &c.TradeType, &c.PriceUnits, &c.Filters); err != nil {
		return errors.New("分类不存在或已停用")
	}
	return catalog.ValidateInput(p, complete, c)
}

const VisibleSQL = "p.status='published' AND p.approved_version=p.version AND c.enabled=true"

// Explicit projection avoids materializing the large private legacy payload for
// each card, and prevents future internal columns from silently becoming API fields.
const ProductJSON = `jsonb_build_object(
 'id',p.id,'product_code',p.product_code,'category_id',p.category_id,'title',p.title,
 'trade_type',p.trade_type,'price_unit',p.price_unit,'attributes',p.attributes,'description',p.description,
 'status',p.status,'source',p.source,'version',p.version,'approved_version',p.approved_version,
 'manual_rank',p.manual_rank,'review_note',p.review_note,'legacy_price',p.legacy_price,
 'legacy_category',p.legacy_category,'verification_status',p.verification_status,
 'created_at',p.created_at,'updated_at',p.updated_at,'published_at',p.published_at,
 'price_amount',p.price_amount::text,'price_note',CASE WHEN p.price_unit='unknown' THEN '原站报价，单位待核对' ELSE '' END,
 'image_total',(SELECT count(*) FROM product_images pi WHERE pi.product_id=p.id),
 'images',COALESCE((SELECT jsonb_agg(i.obj ORDER BY i.sort_order,i.media_id) FROM (
 SELECT pi.sort_order,pi.media_id,jsonb_build_object('id',m.id,'alt',m.filename,'status',m.status,
 'url',CASE WHEN m.status='ready' THEN '/api/v1/media/'||m.id||'/file' ELSE NULL END,
 'thumbnail_url',CASE WHEN m.status='ready' THEN '/api/v1/media/'||m.id||'/file?variant=thumb' ELSE NULL END) obj
 FROM product_images pi JOIN media_files m ON m.id=pi.media_id WHERE pi.product_id=p.id ORDER BY pi.sort_order,pi.media_id LIMIT %d) i),'[]'::jsonb))`

func (a *CatalogStore) ProductView(ctx context.Context, id string, private bool) (map[string]any, error) {
	where := "p.id=$1"
	if !private {
		where += " AND " + VisibleSQL
	}
	var data []byte
	e := a.DB.QueryRow(ctx, "SELECT "+fmt.Sprintf(ProductJSON, 20)+" FROM products p JOIN categories c ON c.id=p.category_id WHERE "+where, id).Scan(&data)
	if e != nil {
		return nil, e
	}
	var out map[string]any
	e = json.Unmarshal(data, &out)
	if !private {
		catalog.PublicFields(out)
	}
	return out, e
}
func (a *CatalogStore) ProductRows(ctx context.Context, where string, args []any, order string, limit, offset int) ([]map[string]any, int, error) {
	return a.productRows(ctx, where, args, order, limit, offset, false, true)
}

func (a *CatalogStore) productRows(ctx context.Context, where string, args []any, order string, limit, offset int, summary, countTotal bool) ([]map[string]any, int, error) {
	var total int
	if countTotal {
		if e := a.DB.QueryRow(ctx, "SELECT count(*) FROM products p JOIN categories c ON c.id=p.category_id WHERE "+where, args...).Scan(&total); e != nil {
			return nil, 0, e
		}
	}
	args = append(args, limit, offset)
	projection := ProductJSON
	// 卡片不读取长说明；首页栏目无需执行总数统计。
	if summary {
		projection = strings.Replace(projection, "'description',p.description,", "", 1)
	}
	query := "SELECT " + fmt.Sprintf(projection, 1) + " FROM products p JOIN categories c ON c.id=p.category_id WHERE " + where + " ORDER BY " + order + fmt.Sprintf(" LIMIT $%d OFFSET $%d", len(args)-1, len(args))
	rows, e := a.DB.Query(ctx, query, args...)
	if e != nil {
		return nil, 0, e
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var b []byte
		if e = rows.Scan(&b); e != nil {
			return nil, 0, e
		}
		var m map[string]any
		if e = json.Unmarshal(b, &m); e != nil {
			return nil, 0, e
		}
		out = append(out, m)
	}
	return out, total, rows.Err()
}
func (a *CatalogStore) FilterOptions(ctx context.Context) (map[string]map[string]bool, error) {
	rows, e := a.DB.Query(ctx, "SELECT filter_config FROM categories WHERE enabled")
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := map[string]map[string]bool{}
	for rows.Next() {
		var b []byte
		if e = rows.Scan(&b); e != nil {
			return nil, e
		}
		var groups []catalog.FilterGroup
		_ = json.Unmarshal(b, &groups)
		for _, g := range groups {
			if g.Enabled != nil && !*g.Enabled {
				continue
			}
			if out[g.Key] == nil {
				out[g.Key] = map[string]bool{}
			}
			for _, o := range g.Options {
				if o.Enabled != nil && !*o.Enabled {
					continue
				}
				out[g.Key][o.Value] = true
			}
		}
	}
	return out, rows.Err()
}
func (a *CatalogStore) ImageRows(ctx context.Context, id string, limit, offset int) ([]map[string]any, int, error) {
	return ImageRowsWith(ctx, a.DB, id, limit, offset)
}

func ImageRowsWith(ctx context.Context, q RowsQuerier, id string, limit, offset int) ([]map[string]any, int, error) {
	var total int
	if e := q.QueryRow(ctx, "SELECT count(*) FROM product_images WHERE product_id=$1", id).Scan(&total); e != nil {
		return nil, 0, e
	}
	rows, e := q.Query(ctx, "SELECT m.id,m.filename,m.status FROM product_images pi JOIN media_files m ON m.id=pi.media_id WHERE pi.product_id=$1 ORDER BY pi.sort_order,pi.media_id LIMIT $2 OFFSET $3", id, limit, offset)
	if e != nil {
		return nil, 0, e
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var id, name, status string
		if e = rows.Scan(&id, &name, &status); e != nil {
			return nil, 0, e
		}
		item := map[string]any{"id": id, "alt": name, "status": status}
		if status == "ready" {
			item["url"] = "/api/v1/media/" + id + "/file"
			item["thumbnail_url"] = "/api/v1/media/" + id + "/file?variant=thumb"
		}
		out = append(out, item)
	}
	return out, total, rows.Err()
}

// 同一个只读快照返回版本和图片，避免编辑期间拼接新旧图集。
func (a *CatalogStore) PublicImages(ctx context.Context, id, expected string, limit, offset int) ([]map[string]any, int, int, error) {
	tx, e := a.DB.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if e != nil {
		return nil, 0, 0, e
	}
	defer tx.Rollback(ctx)
	var version int
	e = tx.QueryRow(ctx, "SELECT p.version FROM products p JOIN categories c ON c.id=p.category_id WHERE p.id=$1 AND "+VisibleSQL, id).Scan(&version)
	if errors.Is(e, pgx.ErrNoRows) {
		return nil, 0, 0, fault.New(fault.NotFound, "NOT_FOUND", "商品不存在或已停止公开")
	}
	if e != nil {
		return nil, 0, 0, e
	}
	if expected != "" && expected != strconv.Itoa(version) {
		return nil, 0, 0, fault.New(fault.Conflict, "VERSION_CONFLICT", "图集已更新，请重新加载")
	}
	items, total, e := ImageRowsWith(ctx, tx, id, limit, offset)
	if e != nil {
		return nil, 0, 0, e
	}
	if e = tx.Commit(ctx); e != nil {
		return nil, 0, 0, e
	}
	return items, total, version, nil
}
func (a *CatalogStore) PublicProduct(ctx context.Context, id string) (map[string]any, error) {
	p, e := a.ProductView(ctx, id, false)
	if errors.Is(e, pgx.ErrNoRows) {
		var status string
		err := a.DB.QueryRow(ctx, "SELECT status FROM products WHERE id=$1", id).Scan(&status)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return nil, err
		}
		if status == "offline" {
			return nil, fault.New(fault.Gone, "OFFLINE", "此账号已下架")
		}
		return nil, fault.New(fault.NotFound, "NOT_FOUND", "商品不存在或尚未公开")
	}
	return p, e
}
