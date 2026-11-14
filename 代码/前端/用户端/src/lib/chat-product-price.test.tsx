import { describe, expect, it } from 'vitest';
import { renderToStaticMarkup } from 'react-dom/server';
import { ChatProductCard } from '../components/ChatPage';
import { chatProductPrice } from '../../../共享/chat-product-price';
import type { ChatProduct } from '../../../共享/chat-types';

const product: ChatProduct = {
  id: 'p',
  code: 'QYZH1',
  title: '咨询商品',
  price: '8',
  unit: 'period',
  cover_url: '',
};
describe('聊天商品时段快照', () => {
  it('首条夜间小时报价展示正确名称、跨午夜与单位，不误写成包价', () => {
    const snapshot: ChatProduct = {
      ...product,
      rental_category: 'hour_day',
      rental_quotes: [
        {
          unit: 'period',
          label: '夜间',
          amount: '8',
          billing_unit: 'hour',
          start_time: '22:00',
          end_time: '07:00',
        },
        { unit: 'period', label: '包夜', amount: '20', billing_unit: 'period' },
      ],
    };
    const html = renderToStaticMarkup(<ChatProductCard product={snapshot} />);
    expect(html).toContain('夜间 · 22:00–次日07:00 · ¥ 8 元/小时');
    expect(html).not.toContain('元/时段');
  });
  it('旧普通快照保留原单位，缺失时段明细不臆测计价方式', () => {
    expect(chatProductPrice({ ...product, unit: 'day', price: '25.00' })).toBe('¥ 25 元/日');
    expect(chatProductPrice({ ...product, unit: 'hour' })).toBe('¥ 8 元/小时');
    expect(chatProductPrice(product)).toBe('联系客服确认时段报价');
    expect(chatProductPrice({ ...product, unit: 'unknown' })).toBe('联系客服报价');
  });
  it('无时刻包价保留名称与整段单位，并使用保存的首条金额', () => {
    expect(
      chatProductPrice({
        ...product,
        price: '999',
        rental_quotes: [{ unit: 'period', label: '包夜', amount: '20' }],
      }),
    ).toBe('包夜 · ¥ 20 元/时段');
  });
});
