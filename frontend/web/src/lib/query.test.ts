import { describe, expect, it } from 'vitest';
import { fixtureProducts } from './fixtures';
import { demoHot, filterProducts, paginate, toApiQuery } from './query';
import { productFrom, productToWire } from './api';
describe('商品浏览业务规则', () => {
  it('不混排不同报价单位，即使筛选为出售也要求明确计价单位', () => {
    expect(() => filterProducts(fixtureProducts, new URLSearchParams('sort=price-up'))).toThrow(
      '报价单位',
    );
    expect(() =>
      filterProducts(fixtureProducts, new URLSearchParams('trade_type=sale&min_price=100')),
    ).toThrow('报价单位');
    const list = filterProducts(
      fixtureProducts,
      new URLSearchParams('price_unit=account&sort=price-up'),
    );
    expect(list.map((p) => p.price)).toEqual([2680, 3280, 4280, 6880, 8200]);
    expect(new Set(list.map((p) => p.unit))).toEqual(new Set(['account']));
  });
  it('同组筛选取并集，不同组取交集', () => {
    const list = filterProducts(
      fixtureProducts,
      new URLSearchParams(
        'server=华东一区&server=华北一区&gender=female&trade_type=sale&level=7&level=10',
      ),
    );
    expect(list.map((p) => p.id)).toEqual(['QY-02418', 'QY-02439', 'QY-02466']);
    expect(
      filterProducts(fixtureProducts, new URLSearchParams('server=华南一区&gender=female')),
    ).toHaveLength(0);
  });
  it('筛选先于排序，热门并列以首次公开时间及编号稳定排序', () => {
    const list = filterProducts(fixtureProducts, new URLSearchParams('trade_type=rent&sort=hot'));
    expect(list.map((p) => p.id)).toEqual(['QY-02452', 'QY-02473', 'QY-02445']);
    const a = { ...fixtureProducts[0], id: 'X-B' },
      b = { ...fixtureProducts[0], id: 'X-A' };
    expect([a, b].sort(demoHot).map((p) => p.id)).toEqual(['X-A', 'X-B']);
  });
  it('缺少价格的商品排在后面且不进入价格范围', () => {
    const list = [...fixtureProducts, { ...fixtureProducts[0], id: 'no-price', price: null }];
    expect(
      filterProducts(list, new URLSearchParams('price_unit=account&sort=price-down')).at(-1)?.id,
    ).toBe('no-price');
    expect(
      filterProducts(list, new URLSearchParams('price_unit=account&min_price=0')).some(
        (p) => p.id === 'no-price',
      ),
    ).toBe(false);
  });
  it('游标可以读到全部记录，不丢失排序', () => {
    const first = paginate(fixtureProducts, new URLSearchParams('limit=3'));
    const second = paginate(
      fixtureProducts,
      new URLSearchParams(`limit=3&cursor=${first.nextCursor}`),
    );
    const third = paginate(
      fixtureProducts,
      new URLSearchParams(`limit=3&cursor=${second.nextCursor}`),
    );
    expect([...first.items, ...second.items, ...third.items].map((p) => p.id)).toEqual(
      fixtureProducts.map((p) => p.id),
    );
    expect(third.nextCursor).toBeNull();
  });
  it('仅返回公开商品，支持编号搜索', () => {
    expect(
      filterProducts([{ ...fixtureProducts[0], status: 'draft' }], new URLSearchParams()),
    ).toEqual([]);
    expect(
      filterProducts(fixtureProducts, new URLSearchParams('q=QY-02418')).map((p) => p.id),
    ).toEqual(['QY-02418']);
  });
});
describe('Go 接口适配', () => {
  it('写入采用白名单和十进制金额，不传入审核与所有者字段', () => {
    const wire = productToWire({ ...fixtureProducts[0], price: 123.4 });
    expect(wire).toMatchObject({
      category_id: 'qq-dance',
      trade_type: 'sale',
      price_amount: '123.40',
      price_unit: 'account',
      attributes: { server: '华东一区' },
    });
    for (const key of ['id', 'status', 'verification', 'owner_id', 'images', 'art'])
      expect(wire).not.toHaveProperty(key);
    expect(productToWire({ ...fixtureProducts[0], price: null }).price_amount).toBeNull();
  });
  it('筛选组合以结构化 JSON 传输', () => {
    const p = toApiQuery(
      new URLSearchParams('server=华东一区&server=华北一区&unit=account&min_price=20&mode=sale'),
    );
    expect(p.get('trade_type')).toBe('sale');
    expect(p.get('price_unit')).toBe('account');
    expect(JSON.parse(p.get('filters')!)).toEqual({ server: ['华东一区', '华北一区'] });
    expect(p.has('server')).toBe(false);
  });
  it('发布审核状态与核验状态独立，不把通过审核当作已核验', () => {
    const p = productFrom({
      id: 'public-1',
      status: 'published',
      price_amount: '0.00',
      attributes: { server: '华东一区' },
    });
    expect(p.status).toBe('published');
    expect(p.verification).toBe('unprovided');
    expect(p.price).toBe(0);
  });
});
