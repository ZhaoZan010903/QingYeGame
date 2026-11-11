import { describe, expect, it, vi } from 'vitest';
import { GalleryPager } from './gallery-pager';
import { ApiError } from './query';
const makeImages = (start: number, count: number, prefix = '') =>
  Array.from({ length: count }, (_, index) => ({
    id: `${prefix}${start + index}`,
    url: `/original/${start + index}`,
    thumbnailUrl: `/thumb/${start + index}`,
    alt: String(start + index),
  }));
const seed = { items: [], total: 202, version: 1 };
describe('详情全图串行分页', () => {
  it.each([202, 567, 1001])('继续加载图集可按顺序到达全部 %i 张，没有总量截断', async (total) => {
    let active = 0,
      peak = 0;
    const fetch = vi.fn(async (cursor?: string | null) => {
      active++;
      peak = Math.max(active, peak);
      await Promise.resolve();
      const start = Number(cursor) || 0,
        count = Math.min(20, total - start);
      active--;
      return {
        items: makeImages(start, count),
        total,
        version: 1,
        nextCursor: start + count < total ? String(start + count) : null,
      };
    });
    const pager = new GalleryPager(fetch, { ...seed, total });
    // Lightbox controls may request a page together; these calls must coalesce.
    do {
      await Promise.all([pager.load(), pager.load(), pager.load()]);
    } while (pager.getSnapshot().nextCursor);
    expect(pager.getSnapshot().items.map((image) => image.id)).toEqual(
      Array.from({ length: total }, (_, i) => String(i)),
    );
    expect(fetch).toHaveBeenCalledTimes(Math.ceil(total / 20));
    expect(peak).toBe(1);
    await pager.load();
    expect(fetch).toHaveBeenCalledTimes(Math.ceil(total / 20));
  });
  it('网络失败保留已加载页和游标，重试当前页后继续完成', async () => {
    const fetch = vi
      .fn()
      .mockResolvedValueOnce({ items: makeImages(0, 20), total: 30, version: 1, nextCursor: '20' })
      .mockRejectedValueOnce(new ApiError('网络中断', 0, 'NETWORK_ERROR'))
      .mockResolvedValueOnce({
        items: makeImages(20, 10),
        total: 30,
        version: 1,
        nextCursor: null,
      });
    const pager = new GalleryPager(fetch, seed);
    await pager.load();
    await pager.load();
    expect(pager.getSnapshot().items).toHaveLength(20);
    expect(pager.getSnapshot().nextCursor).toBe('20');
    expect(pager.getSnapshot().error).toBeInstanceOf(ApiError);
    await pager.load();
    expect(pager.getSnapshot().items).toHaveLength(30);
    expect(pager.getSnapshot().error).toBeUndefined();
    expect(fetch.mock.calls[1]).toEqual(fetch.mock.calls[2]);
  });
  it('409后显式重新加载新版本，不混入旧版图片', async () => {
    const fetch = vi
      .fn()
      .mockResolvedValueOnce({ items: makeImages(0, 20), total: 40, version: 1, nextCursor: '20' })
      .mockRejectedValueOnce(new ApiError('版本变化', 409, 'VERSION_CONFLICT'))
      .mockResolvedValueOnce({
        items: makeImages(0, 10, 'new-'),
        total: 10,
        version: 2,
        nextCursor: null,
      });
    const pager = new GalleryPager(fetch, seed);
    await pager.load();
    await pager.load();
    expect(pager.getSnapshot().version).toBe(1);
    expect(pager.getSnapshot().items).toHaveLength(20);
    await pager.load(true);
    expect(fetch.mock.calls[2]).toEqual([undefined, undefined]);
    expect(pager.getSnapshot().version).toBe(2);
    expect(pager.getSnapshot().items.every((image) => image.id.startsWith('new-'))).toBe(true);
  });
  it('重复分页游标和不完整末页明确报错，防止无限加载或误报全图', async () => {
    const pager = new GalleryPager(
      async () => ({ items: makeImages(0, 20), total: 40, version: 1, nextCursor: '20' }),
      seed,
    );
    await pager.load();
    await pager.load();
    expect(pager.getSnapshot().error).toMatchObject({ code: 'INVALID_CURSOR' });
    const incomplete = new GalleryPager(
      async () => ({ items: makeImages(0, 20), total: 40, version: 1, nextCursor: null }),
      seed,
    );
    await incomplete.load();
    expect(incomplete.getSnapshot().error).toMatchObject({ code: 'INCOMPLETE_GALLERY' });
  });
});
