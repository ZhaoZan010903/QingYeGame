import { describe, expect, it } from 'vitest';
import { formFromProduct, galleryFormDecision } from './product-form-snapshot';
import type { Product } from '../types';

const product = (version: number) =>
  ({
    id: 'p',
    version,
    title: '新标题',
    mode: 'rent',
    unit: 'day',
    price: 8,
    categoryId: 'rent',
    server: '新服',
    gender: 'female',
    level: 9,
    kind: 'single',
    description: '新描述',
    attributes: { custom_tags: ['新标签'] },
    rentalQuotes: [{ unit: 'day', amount: '8' }],
  }) as unknown as Product;
describe('图集更新与用户编辑快照', () => {
  it('有未保存编辑时拒绝新版快照，不能只升级写入版本', () => {
    const old = product(3);
    expect(galleryFormDecision(old, product(4), true)).toBe('conflict');
    expect(old.version).toBe(3);
  });
  it('同版后台读取不覆盖脏表单；干净表单完整刷新可编辑字段', () => {
    expect(galleryFormDecision(product(3), product(3), true)).toBe('preserve');
    expect(galleryFormDecision(product(3), product(4), false)).toBe('replace');
    expect(formFromProduct(product(4))).toMatchObject({
      title: '新标题',
      server: '新服',
      price: 8,
      description: '新描述',
      tags: ['新标签'],
      rentalQuotes: [{ unit: 'day', amount: '8' }],
    });
  });
});
