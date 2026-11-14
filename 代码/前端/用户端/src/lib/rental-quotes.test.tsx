import { describe, expect, it } from 'vitest';
import { renderToStaticMarkup } from 'react-dom/server';
import { RentalQuoteEditor } from '../../../共享/RentalQuoteEditor';
import { periodLabel, quotesFromProduct, validateRentalQuotes } from '../../../共享/rental-quotes';
import type { RentalQuote } from '../types';
import { formFromProduct } from './product-form-snapshot';
import { productFrom } from './api';

const period = (patch: Partial<RentalQuote> = {}): RentalQuote => ({
  unit: 'period',
  label: '包夜',
  amount: '20',
  billing_unit: 'period',
  ...patch,
});
describe('R21-2 时段报价', () => {
  it('保留同一时段不同计价单位，时间可省略，金额不换算', () => {
    const quotes = validateRentalQuotes([
      period(),
      period({ billing_unit: 'hour', amount: '8' }),
      { unit: 'day', amount: '100' },
    ]);
    expect(quotes).toEqual([
      period({ amount: '20.00' }),
      period({ billing_unit: 'hour', amount: '8.00' }),
      { unit: 'day', amount: '100.00' },
    ]);
    expect(quotes[0]).not.toHaveProperty('start_time');
  });
  it('跨午夜标记次日，并拒绝缺失、无效或相同起止时间', () => {
    const q = period({ start_time: '20:00', end_time: '08:00' });
    expect(periodLabel(q)).toBe('20:00–次日08:00');
    expect(validateRentalQuotes([q])).toHaveLength(1);
    for (const patch of [
      { start_time: '20:00' },
      { start_time: '24:00', end_time: '08:00' },
      { start_time: '20:00', end_time: '20:00' },
    ]) {
      expect(() => validateRentalQuotes([period(patch)])).toThrow();
    }
    expect(periodLabel(period())).toBe('');
  });
  it('名称、单位、重复和数量边界校验，不会丢弃旧报价', () => {
    for (const patch of [
      { label: '' },
      { label: '字'.repeat(31) },
      { label: '白天\n包价' },
      { amount: '3.1415' },
    ])
      expect(() => validateRentalQuotes([period(patch)])).toThrow();
    expect(() => validateRentalQuotes([period(), period()])).toThrow('不能重复');
    const max = Array.from({ length: 12 }, (_, i) => period({ label: `时段${i}` }));
    expect(validateRentalQuotes(max)).toHaveLength(12);
    expect(() => validateRentalQuotes([...max, period({ label: '额外' })])).toThrow('12');
    expect(() => validateRentalQuotes([period()], ['day'])).toThrow('不支持');
    expect(() => validateRentalQuotes([{ unit: 'day', amount: '8', label: '包夜' }])).toThrow(
      '时段信息',
    );
  });
  it('旧账号结构保留于属性，但不新增发布字段；多条时段在编辑快照中完整保留', () => {
    const product = productFrom({
      id: 'p',
      trade_type: 'rent',
      attributes: { kind: 'linked' },
      rental_quotes: [period(), period({ label: '白天', billing_unit: 'hour' })],
    });
    const form = formFromProduct(product);
    expect(form).not.toHaveProperty('kind');
    expect(form.attributes?.kind).toBe('linked');
    expect(quotesFromProduct(product)).toEqual(product.rentalQuotes);
    expect(quotesFromProduct(product)).toHaveLength(2);
  });
  it('发布报价编辑器支持名称、可选时间和五种计价单位，不展示管理员分类', () => {
    const html = renderToStaticMarkup(
      <RentalQuoteEditor quotes={[period()]} onChange={() => {}} />,
    );
    expect(html).toContain('时段 1 名称');
    expect(html).toContain('时段 1 计价单位');
    expect(html).toContain('时间可留空');
    expect(html).toContain('value="week"');
    expect(html).not.toContain('租赁分类');
  });
});
