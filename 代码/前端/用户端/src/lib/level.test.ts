import { describe, expect, it } from 'vitest';
import { readLevel, levelLabel } from '../../../共享/level';
import { productFrom, productToWire } from './api';
describe('真实等级文本与数字兼容', () => {
  it.each(['彩钻', 'XVVIP 皇冠', '王钻', '9'])('保留文本等级 %s 并原样写回会员属性', (value) => {
    const product = productFrom({ id: 'legacy', attributes: { level: value } });
    expect(product.level).toBe(value);
    expect(productToWire(product).attributes.level).toBe(value);
    expect(levelLabel(product.level)).not.toContain('NaN');
  });
  it('数字等级延续VIP展示，空值或损坏数值不伪造等级', () => {
    expect(levelLabel(readLevel(9))).toBe('VIP 9');
    expect(levelLabel(readLevel('彩钻'))).toBe('彩钻');
    expect(levelLabel(readLevel(undefined))).toBe('未填写');
    expect(levelLabel(readLevel(Number.NaN))).toBe('未填写');
  });
});
