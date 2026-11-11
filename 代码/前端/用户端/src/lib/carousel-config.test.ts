import { describe, expect, it } from 'vitest';
import { readHomePresentation, resolveHomePresentation } from '../../../共享/home-content';

const slide = { productId: 'p', title: '手动精选', layout: 'overlay', placement: 'free', textPositionX: 100, textPositionY: 0, textWidth: 45, titleSize: 48, descriptionSize: 16, fontFamily: 'serif', titleColor: '#ffffff' };

describe('轮播可视化配置', () => {
  it('与Go按Unicode字符计数一致，合法emoji文案不会使首页报错', () => {
    const title = '🌿'.repeat(100), description = '中文🌿'.repeat(100);
    expect(readHomePresentation({ slides: [{ ...slide, title, description }] }).slides?.[0]).toMatchObject({ title, description });
    expect(() => readHomePresentation({ slides: [{ ...slide, title: title + '多' }] })).toThrow();
  });
  it('旧配置兼容，数量上限允许调整且不静默裁去超额已保存配置', () => {
    expect(readHomePresentation().maxSlides).toBe(12);
    const slides = Array.from({ length: 20 }, (_, i) => ({ ...slide, productId: `p-${i}` }));
    expect(readHomePresentation({ maxSlides: 20, slides }).slides).toHaveLength(20);
    expect(() => readHomePresentation({ maxSlides: 12, slides })).toThrow();
    for (const maxSlides of [0, 31, 1.5, '20']) expect(() => readHomePresentation({ maxSlides })).toThrow();
  });
  it('过滤外部字体/CSS注入以及越界拖动参数', () => {
    expect(readHomePresentation({ slides: [slide] }).slides?.[0]).toMatchObject(slide);
    for (const patch of [{ titleColor: 'red;position:fixed' }, { fontFamily: 'url(https://example.com)' }, { titleSize: 73 }, { descriptionSize: 11 }, { textPositionX: 101 }, { textPositionY: -1 }, { textWidth: 90 }, { layout: 'image-right' }]) {
      expect(() => readHomePresentation({ slides: [{ ...slide, ...patch }] })).toThrow();
    }
  });
  it('缺失字段使用安全默认值且不写入源对象', () => {
    const before = structuredClone(slide);
    const normalized = resolveHomePresentation({ slides: [slide] });
    expect(normalized.slides?.[0].descriptionColor).toBe('');
    expect(normalized.slides?.[0].imagePositionX).toBe(50);
    expect(slide).toEqual(before);
  });
});
