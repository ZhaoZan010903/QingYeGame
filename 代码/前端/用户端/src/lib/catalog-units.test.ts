import { describe, expect, it } from 'vitest';
import type { Category } from '../types';
import { catalogPriceUnits } from './catalog-units';

const category = (
  tradeType?: Category['tradeType'],
  priceUnits: Category['priceUnits'] = [],
): Category => ({
  id: tradeType ?? 'all',
  name: '交易模板',
  tradeType,
  priceUnits,
  filters: [],
});

describe('集市模板报价单位', () => {
  it('空出售与出租模板分别使用业务默认，交易页签不混入其他单位', () => {
    const categories = [category('sale'), category('rent')];
    expect(catalogPriceUnits(categories, 'sale')).toEqual(['account']);
    expect(catalogPriceUnits(categories, 'rent')).toEqual(['hour', 'day', 'week', 'month']);
    expect(catalogPriceUnits(categories)).toEqual(['account', 'hour', 'day', 'week', 'month']);
  });
  it('通用空模板跟随当前页签，全部账号页合并默认单位', () => {
    expect(catalogPriceUnits([category()], 'sale')).toEqual(['account']);
    expect(catalogPriceUnits([category()], 'rent')).toEqual(['hour', 'day', 'week', 'month']);
    expect(catalogPriceUnits([category()])).toEqual(['account', 'hour', 'day', 'week', 'month']);
  });
  it('非空配置不扩充，合并去重且待核对单位不参与价格筛选', () => {
    expect(
      catalogPriceUnits([category('rent', ['day']), category('rent', ['month', 'day'])], 'rent'),
    ).toEqual(['day', 'month']);
    expect(catalogPriceUnits([category(undefined, ['account', 'month'])], 'rent')).toEqual([
      'month',
    ]);
    expect(catalogPriceUnits([category(undefined, ['unknown'])])).toEqual([]);
    expect(catalogPriceUnits([])).toEqual([]);
  });
});
