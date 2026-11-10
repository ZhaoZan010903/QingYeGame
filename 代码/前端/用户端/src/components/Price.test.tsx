import { describe, expect, it } from 'vitest';
import { renderToStaticMarkup } from 'react-dom/server';
import { Price, genderLabel } from './shared';
import { productFrom, productToWire } from '../lib/api';
import { toApiQuery } from '../lib/query';

describe('旧站报价与未知属性', () => {
  it('映射缩略图和原图，并在保存时保留分类自定义属性', () => {
    const product = productFrom({
      id: 'custom-1',
      images: [{ id: 'image-1', url: '/original', thumbnail_url: '/thumbnail' }],
      attributes: { custom: ['a', 'b'], server: 'east' },
    });
    expect(product.images[0]).toMatchObject({ url: '/original', thumbnailUrl: '/thumbnail' });
    const payload = productToWire({ ...product, categoryId: 'all-categories' });
    expect(payload.attributes.custom).toEqual(['a', 'b']);
    expect(payload.category_id).toBe('all-categories');
  });
  const legacy = productFrom({
    id: 'old-1',
    title: '迁移商品',
    price_unit: 'unknown',
    price_amount: null,
    legacy_price: '160.00',
    legacy_category: '周月租号',
    price_note: '原站报价，单位待核对',
    attributes: { original: '保留' },
  });
  it('原样展示旧报价，不添加货币或推断日/月单位', () => {
    const html = renderToStaticMarkup(<Price product={legacy} />);
    expect(html).toContain('160.00');
    expect(html).toContain('原站报价，单位待核对');
    expect(html).not.toContain('¥');
    expect(html).not.toContain('元/月');
    expect(legacy.price).toBeNull();
    expect(legacy.attributes).toEqual({ original: '保留' });
    expect(genderLabel(legacy.gender)).toBe('角色未注明');
  });
  it('未知单位可以浏览，但不能用于预算筛选或价格排序', () => {
    expect(toApiQuery(new URLSearchParams('price_unit=unknown')).get('price_unit')).toBe('unknown');
    for (const filter of ['min_price=1', 'max_price=100', 'sort=price-up', 'sort=price_desc']) {
      expect(() => toApiQuery(new URLSearchParams(`price_unit=unknown&${filter}`))).toThrow(
        '报价单位尚未核对',
      );
    }
  });
  it.each([
    ['account', '元'],
    ['day', '元/日'],
    ['month', '元/月'],
  ] as const)('核对后的%s报价明确展示人民币和单位，忽略遗留待核对说明', (unit, label) => {
    const confirmed = productFrom({
      id: 'confirmed-1',
      price_unit: unit,
      price_amount: '168.50',
      legacy_price: '160.00',
      price_note: '原站报价，单位待核对',
    });
    const html = renderToStaticMarkup(<Price product={confirmed} />);
    expect(html).toContain('¥');
    expect(html).toContain('168.5');
    expect(html).toContain(label);
    expect(html).not.toContain('160.00');
    expect(html).not.toContain('单位待核对');
    expect(
      toApiQuery(new URLSearchParams(`price_unit=${unit}&min_price=10&sort=price_asc`)).get(
        'price_unit',
      ),
    ).toBe(unit);
  });
});
