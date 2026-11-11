import { describe, expect, it } from 'vitest';
import type { Product } from '../types';
import {
  defaultHomePresentation,
  resolveHomePresentation,
  selectHomeSections,
} from './home-content';

const product = (id: string) => ({ id }) as Product;

describe('首页可配置展示边界', () => {
  it('旧接口不提供配置时保留现有布局，返回配置不会污染默认值', () => {
    const value = resolveHomePresentation();
    expect(value.sections.map(({ source, limit }) => [source, limit])).toEqual([
      ['hot', 6],
      ['recommended', 6],
      ['newest', 6],
    ]);
    value.sections[0].title = '修改';
    value.shortcuts[0].title = '修改';
    expect(defaultHomePresentation.sections[0].title).toBe('热门账号');
    expect(defaultHomePresentation.shortcuts[0].title).toBe('账号出售');
  });

  it('支持局部配置，拒绝非法数量和过快轮播，保持服务端配置的分区顺序', () => {
    const value = resolveHomePresentation({
      editorial: { title: '每周精选', intervalMs: 100 },
      sections: [
        { source: 'newest', title: '刚刚上新', limit: 3 },
        { source: 'hot', limit: 999 },
        { source: 'newest', limit: 8 },
        { source: 'unknown', limit: 3 },
      ],
      publishing: { action: '提交账号' },
    });
    expect(value.editorial.title).toBe('每周精选');
    expect(value.editorial.intervalMs).toBe(7500);
    expect(value.sections.map(({ source, limit }) => [source, limit])).toEqual([
      ['newest', 3],
      ['hot', 6],
    ]);
    expect(value.publishing.action).toBe('提交账号');
    expect(value.publishing.title).toBe(defaultHomePresentation.publishing.title);
  });

  it('显式空数组隐藏模块；缺少或错误类型的数组使用默认配置', () => {
    expect(resolveHomePresentation({ sections: [], shortcuts: [] }).sections).toEqual([]);
    expect(resolveHomePresentation({ sections: [], shortcuts: [] }).shortcuts).toEqual([]);
    expect(resolveHomePresentation({ sections: null, shortcuts: 'invalid' }).sections).toHaveLength(
      3,
    );
    expect(
      resolveHomePresentation({ sections: null, shortcuts: 'invalid' }).shortcuts,
    ).toHaveLength(4);
  });

  it('快捷入口只接受既有站内入口，不接受外部链接、脚本或未知页面', () => {
    const value = resolveHomePresentation({
      shortcuts: [
        { href: '//evil.example', title: '外部' },
        { href: 'javascript:alert(1)', title: '脚本' },
        { href: '/admin', title: '未知' },
        { href: '/accounts?sort=newest', title: '上新', icon: 'plus' },
      ],
    });
    expect(value.shortcuts).toEqual([
      { href: '/accounts?sort=newest', title: '上新', description: '', icon: 'plus' },
    ]);
  });

  it('各分区独立去重和限量，不重排后端结果，不填入样例库存', () => {
    const data = {
      featured: [product('featured')],
      newest: ['featured', 'b', 'a', 'b'].map(product),
      hot: ['b', 'featured', 'c', 'd', 'a'].map(product),
    };
    const before = structuredClone(data);
    const presentation = resolveHomePresentation({
      sections: [
        { source: 'newest', limit: 3 },
        { source: 'hot', limit: 1 },
      ],
    });
    const sections = selectHomeSections(data, presentation.sections);
    expect(sections.map(({ products }) => products.map(({ id }) => id))).toEqual([
      ['featured', 'b', 'a'],
      ['b'],
    ]);
    expect(data).toEqual(before);
    expect(
      selectHomeSections({ featured: [], hot: [], newest: [] }, presentation.sections).every(
        ({ products }) => products.length === 0,
      ),
    ).toBe(true);
  });
});
