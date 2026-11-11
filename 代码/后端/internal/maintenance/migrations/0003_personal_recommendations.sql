-- Personal interests use only this site's browsing, independently of aggregate heat.
ALTER TABLE product_views_daily ADD COLUMN IF NOT EXISTS last_seen_at timestamptz;
CREATE INDEX IF NOT EXISTS views_visitor_date_idx ON product_views_daily(visitor_hash,viewed_date DESC);
CREATE TABLE IF NOT EXISTS recommendation_preferences (
 visitor_hash text PRIMARY KEY,
 enabled boolean NOT NULL DEFAULT true,
 reset_at timestamptz NOT NULL DEFAULT '1970-01-01 UTC',
 updated_at timestamptz NOT NULL DEFAULT now()
);
-- New-listing bonus is retired. Keep old keys readable for API compatibility.
UPDATE site_settings SET value=jsonb_set(value,'{new_bonus}','0'),updated_at=now() WHERE key='recommendation';
-- Preserve configured sections/order/copy; add the newly requested section once.
UPDATE site_settings SET value=jsonb_set(value,'{sections}',
 (SELECT jsonb_agg(CASE WHEN item->>'source'='hot' AND item->>'title'='热门推荐' THEN jsonb_set(item,'{title}','"热门账号"') ELSE item END ORDER BY ord)
  FROM jsonb_array_elements(value->'sections') WITH ORDINALITY AS e(item,ord))
 || '[{"source":"recommended","title":"为你推荐","description":"根据你近期浏览的账号整理。","limit":6}]'::jsonb),updated_at=now()
WHERE key='home_presentation' AND jsonb_typeof(value->'sections')='array' AND jsonb_array_length(value->'sections')>0
 AND NOT EXISTS(SELECT 1 FROM jsonb_array_elements(value->'sections') AS s WHERE s->>'source'='recommended');

UPDATE content_pages SET title='热门与推荐规则',body='热门账号：根据统计窗口内的有效浏览和收藏计算热度。默认窗口为7个北京时间自然日，浏览权重为1，收藏权重为5；管理端可调整。相同访客对同一商品每天最多计一次热度浏览，反复取消再收藏不会增加收藏次数，上架时间不加热度分。榜单通常每15分钟更新。

为你推荐：仅使用本站最近30天的浏览记录，最多参考最近50个不同账号。按账号分类、买卖或租赁方式、区服和同一报价单位下的相近价位匹配；近期浏览权重更高，约每7天减半。不推断用户年龄、性别或其他身份属性。没有浏览记录时先展示热门账号。

登录后推荐偏好跟随会员账号；未登录时使用当前浏览器的匿名身份，两者不自动合并。可在首页“推荐偏好”或个人中心关闭个性化推荐、重置偏好。重置后从后续浏览重新整理，收藏和最近浏览列表不受影响；独立的热门去重统计仍保留。

最新上架：按首次公开上架时间倒序排列，时间相同时按商品标识稳定排序；重新上架不重置首次发布时间。热门、为你推荐、最新上架各自独立，同一个商品可以出现在不同分区。下架、未审核通过或停用分类的商品不参与展示。',version=version+1
WHERE slug='recommendation';
