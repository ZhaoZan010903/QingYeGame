import { describe, expect, it } from 'vitest';
import { renderToStaticMarkup } from 'react-dom/server';
import { MemoryRouter } from 'react-router-dom';
import { AssetCard, Price } from '../components/shared';
import { FilterForm, RentalCategoryTabs } from '../pages/CatalogPage';
import { ProductTradeMode } from '../pages/DetailPage';
import { productFrom, productToWire } from './api';
import { catalogRequest } from './catalog-filters';
import { catalogCursor, catalogPageNumbers } from './catalog-pagination';
import { filterProducts } from './demo-catalog';
import { toApiQuery } from './query';
import { rentalPriceRows } from './rental-prices';

const rental = productFrom({
  id: 'multiple-quotes',
  trade_type: 'rent',
  rental_category: 'hour_day',
  price_unit: 'hour',
  price_amount: '10.00',
  rental_quotes: [
    { unit: 'month', amount: '600.00' },
    { unit: 'hour', amount: '10.00' },
    { unit: 'week', amount: '200.00' },
    { unit: 'day', amount: '40.00' },
    { unit: 'period', label: '包夜', amount: '20.00', billing_unit: 'period' },
    {
      unit: 'period',
      label: '晚间',
      amount: '30.00',
      billing_unit: 'period',
      start_time: '20:00',
      end_time: '08:00',
    },
    {
      unit: 'period',
      label: '白天',
      amount: '8.00',
      billing_unit: 'hour',
      start_time: '08:00',
      end_time: '20:00',
    },
    { unit: 'period', label: '午间', amount: '9.00', billing_unit: 'hour' },
  ],
});

describe('R21-2 多报价展示与明确租号分类', () => {
  it('详情不把缺失账号结构的新商品误标为独立账号，历史编码与标签仍展示', () => {
    const missing = productFrom({ id: 'new-listing', trade_type: 'rent' });
    expect(missing.kind).toBe('single');
    expect(renderToStaticMarkup(<ProductTradeMode product={missing} />)).toBe('账号租赁');
    const linked = productFrom({
      id: 'old-listing',
      trade_type: 'rent',
      attributes: { kind: 'linked' },
    });
    expect(renderToStaticMarkup(<ProductTradeMode product={linked} />)).toContain('双号连体');
    const single = productFrom({ id: 'old-single', attributes: { kind: 'single' } });
    expect(renderToStaticMarkup(<ProductTradeMode product={single} />)).toContain('独立账号');
    expect(
      renderToStaticMarkup(
        <ProductTradeMode product={{ ...missing, attributeLabels: { kind: '历史组合账号' } }} />,
      ),
    ).toContain('历史组合账号');
  });

  it('卡片保留全部普通单位，只在相同计价方式内汇总时段最低价', () => {
    const rows = rentalPriceRows(rental.rentalQuotes!);
    expect(rows.slice(0, 4).map((row) => row.amount)).toEqual(['10', '40', '200', '600']);
    expect(rows.slice(4)).toMatchObject([
      { label: '包夜', amount: '20', unit: '元/时段', starting: true },
      { label: '白天', amount: '8', unit: '元/小时', starting: true },
    ]);
    const html = renderToStaticMarkup(<Price product={rental} compact />);
    for (const unit of ['元/小时', '元/日', '元/周', '元/月', '元/时段起'])
      expect(html).toContain(unit);
    expect(html).toContain('共 4 档时段报价');
    expect(html).not.toContain('>8</b><small>元/时段');
  });

  it('详情显示所有时段名称、可选起止时间与各自计价方式', () => {
    const html = renderToStaticMarkup(<Price product={rental} allPeriods />);
    for (const value of ['包夜', '晚间', '白天', '午间', '20:00–次日08:00', '08:00–20:00'])
      expect(html).toContain(value);
    expect(html).toContain('>30</b><small>元/时段');
    expect(html).toContain('>9</b><small>元/小时');
    expect(html).not.toContain('元/时段起');
    expect(html).not.toContain('undefined');
  });

  it('时日周月标签只依赖显式分类，旧单位和历史分类不能代替选择', () => {
    const card = (product = rental) =>
      renderToStaticMarkup(
        <MemoryRouter>
          <AssetCard product={product} />
        </MemoryRouter>,
      );
    expect(card()).toContain('时日租号');
    expect(card({ ...rental, rentalCategory: 'week_month' })).toContain('周月租号');
    const generic = card({
      ...rental,
      rentalCategory: undefined,
      unit: 'month',
      legacyCategory: '周月租号',
    });
    expect(generic).toContain('账号租赁');
    expect(generic).not.toContain('周月租号');
    expect(generic).not.toContain('时日租号');
  });

  it('单价历史租号继续显示已核实的原单位', () => {
    const html = renderToStaticMarkup(
      <Price product={{ ...rental, rentalQuotes: [], unit: 'day', price: 45 }} compact />,
    );
    expect(html).toContain('45');
    expect(html).toContain('/ 日');
  });

  it('读取管理员分类，会员保存保留时段资料但不写入分类，出售清除报价', () => {
    const wire = productToWire(rental);
    expect(rental.rentalCategory).toBe('hour_day');
    expect(wire).not.toHaveProperty('rental_category');
    expect(wire.attributes).not.toHaveProperty('kind');
    expect(productToWire({ ...rental, attributes: { kind: 'linked' } }).attributes.kind).toBe(
      'linked',
    );
    expect(wire.rental_quotes).toEqual(rental.rentalQuotes);
    expect(productToWire({ ...rental, mode: 'sale' }).rental_quotes).toEqual([]);
  });
});

describe('R21-2 租号筛选与页码', () => {
  it('分类切换与时段计价保持顶层参数，离开租赁或时段筛选时移除相应条件', () => {
    const source = new URLSearchParams(
      'trade_type=rent&rental_category=hour_day&price_unit=period&period_basis=hour&min_price=8',
    );
    expect(toApiQuery(catalogRequest(source, [], 9)).get('rental_category')).toBe('hour_day');
    expect(toApiQuery(source).get('period_basis')).toBe('hour');
    source.set('trade_type', 'sale');
    source.set('price_unit', 'account');
    const sale = toApiQuery(catalogRequest(source, [], 9));
    expect(sale.has('rental_category')).toBe(false);
    expect(sale.has('period_basis')).toBe(false);
    expect(
      toApiQuery(new URLSearchParams('rental_category=week_month')).has('rental_category'),
    ).toBe(false);
  });

  it('租号分类按钮与时段计价选择可以读取当前状态', () => {
    const tabs = renderToStaticMarkup(<RentalCategoryTabs value="hour_day" onChange={() => {}} />);
    expect(tabs).toContain('全部租号');
    expect(tabs).toContain('aria-pressed="true" class="active">时日');
    const form = renderToStaticMarkup(
      <FilterForm
        params={new URLSearchParams('price_unit=period&period_basis=hour')}
        onChange={() => {}}
        groups={[]}
      />,
    );
    expect(form).toContain('时段计价方式');
    expect(form).toContain('按所选计价方式的最低时段报价');
  });

  it('多报价按选定普通单位筛选，按时段计价方式分别取最低价且不互相串价', () => {
    const second = {
      ...rental,
      id: 'second',
      rentalCategory: 'week_month' as const,
      price: 1,
      rentalQuotes: [
        { unit: 'day' as const, amount: '50.00' },
        {
          unit: 'period' as const,
          label: '整段',
          amount: '12.00',
          billing_unit: 'period' as const,
        },
        { unit: 'period' as const, label: '逐时', amount: '15.00', billing_unit: 'hour' as const },
      ],
    };
    const products = [rental, second];
    const ids = (query: string) =>
      filterProducts(products, new URLSearchParams(query)).map((p) => p.id);
    expect(ids('trade_type=rent&rental_category=hour_day')).toEqual([rental.id]);
    expect(ids('price_unit=day&min_price=45')).toEqual(['second']);
    expect(ids('price_unit=period&sort=price-up')).toEqual(['second', rental.id]);
    expect(ids('price_unit=period&period_basis=hour&sort=price-up')).toEqual([rental.id, 'second']);
    expect(ids('price_unit=period&period_basis=hour&max_price=10')).toEqual([rental.id]);
    expect(ids('price_unit=period&period_basis=month')).toEqual([]);
  });

  it('API游标推导真实页码，最后一页不显示成第一页', () => {
    expect(catalogPageNumbers(20, null, 9)).toEqual({ current: 1, pages: 3 });
    expect(catalogPageNumbers(20, 'OQ', 9)).toEqual({ current: 2, pages: 3 });
    expect(catalogPageNumbers(20, 'MTg', 9)).toEqual({ current: 3, pages: 3 });
    expect(catalogPageNumbers(13, catalogCursor(6), 6)).toEqual({ current: 2, pages: 3 });
    expect(catalogPageNumbers(0, 'invalid', 9)).toEqual({ current: 1, pages: 1 });
  });
});
