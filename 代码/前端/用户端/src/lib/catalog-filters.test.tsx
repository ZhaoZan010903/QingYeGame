import { describe, expect, it } from 'vitest';
import { renderToStaticMarkup } from 'react-dom/server';
import { FilterForm } from '../pages/CatalogPage';
import { catalogFilters, catalogRequest } from './catalog-filters';
import { toApiQuery } from './query';
import { filterProducts } from './demo-catalog';
import { fixtureProducts } from './fixtures';
import type { FilterGroup } from '../types';

const group = (key: string, labels: string[]): FilterGroup => ({
  key,
  label: key,
  options: labels.map((label, index) => ({ value: `${key}-${index}`, label })),
});

describe('R21-1 集市筛选', () => {
  it('区服和VIP按指定顺序显示，排序与其他改名不改变原始编码', () => {
    const original = [
      group('server', ['其他大区', '十年之约', '一舞时光', '拾忆光阴']),
      group('kind', ['独立账号']),
      group('level', ['彩钻', '王钻', '金钻', '银钻', '星钻']),
      group('keyword', ['永夜头', '包子']),
    ];
    const filters = catalogFilters(original);
    expect(filters.map((g) => g.key)).toEqual(['keyword', 'server', 'level']);
    expect(filters[1].options.map((o) => o.label)).toEqual([
      '一舞时光',
      '拾忆光阴',
      '十年之约',
      '其他',
    ]);
    expect(filters[1].options.map((o) => o.value)).toEqual([
      'server-2',
      'server-3',
      'server-1',
      'server-0',
    ]);
    expect(filters[2].options.map((o) => o.label)).toEqual([
      '星钻',
      '银钻',
      '金钻',
      '彩钻',
      '王钻',
    ]);
    expect(original[0].options[0].label).toBe('其他大区');
  });

  it('热门条件与区服、预算组合传输，旧账号结构链接不继续限制结果', () => {
    const params = new URLSearchParams(
      'keyword=包子&keyword=永夜头&server=一舞时光&kind=linked&price_unit=account&min_price=100&q=珍藏',
    );
    const query = toApiQuery(
      catalogRequest(params, [group('keyword', []), group('server', [])], 9),
    );
    expect(JSON.parse(query.get('filters')!)).toEqual({
      keyword: ['包子', '永夜头'],
      server: ['一舞时光'],
    });
    expect(query.get('min_price')).toBe('100');
    expect(query.get('q')).toBe('珍藏');
    expect(query.has('kind')).toBe(false);
  });

  it('展示管理员排序的热门关键词，并保留多选状态', () => {
    const html = renderToStaticMarkup(
      <FilterForm
        params={new URLSearchParams('keyword=keyword-1')}
        onChange={() => {}}
        groups={[{ ...group('keyword', ['永夜头', '包子']), label: '热门筛选' }]}
      />,
    );
    expect(html.indexOf('热门筛选')).toBeLessThan(html.indexOf('预算范围'));
    expect(html.indexOf('永夜头')).toBeLessThan(html.indexOf('包子'));
    expect(html).toContain('checked=""');
  });

  it('演示筛选同组并集、跨组交集，关键词中的百分号不会匹配所有商品', () => {
    const products = [
      { ...fixtureProducts[0], id: 'keyword-a', tags: ['永夜头'], server: '一舞时光' },
      { ...fixtureProducts[0], id: 'keyword-b', tags: ['包子'], server: '拾忆光阴' },
    ];
    expect(
      filterProducts(products, new URLSearchParams('keyword=永夜头&keyword=包子')).length,
    ).toBe(2);
    expect(
      filterProducts(
        products,
        new URLSearchParams('keyword=永夜头&keyword=包子&server=一舞时光'),
      ).map((p) => p.id),
    ).toEqual(['keyword-a']);
    expect(filterProducts(products, new URLSearchParams('keyword=%25'))).toEqual([]);
  });
});
