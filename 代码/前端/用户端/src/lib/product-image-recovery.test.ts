import { describe, expect, it, vi } from 'vitest';
import {
  locateProductImage,
  removePendingProductImage,
  type ImageRecoveryTransport,
} from '../../../共享/product-image-recovery';
import { UploadSelectionOrder } from '../../../共享/ordered-uploads';
import type { Product } from '../types';

const product = (version: number, ids: string[] = []) =>
  ({ id: 'p', version, images: ids.map((id) => ({ id, alt: id })) }) as Product;
const page = (ids: string[], version: number, nextCursor: string | null = null) => ({
  items: ids.map((id) => ({ id, alt: id })),
  total: ids.length,
  version,
  nextCursor,
});
const transport = (): ImageRecoveryTransport => ({
  myProduct: vi.fn().mockResolvedValue(product(7)),
  myImages: vi.fn().mockResolvedValue(page([], 7)),
  removeImage: vi.fn().mockResolvedValue(product(8)),
});

describe('上传成功但图库写入结果未知的恢复', () => {
  it('响应丢失后从分页图库查找已关联图片，使用最新版本', async () => {
    const api = transport();
    vi.mocked(api.myImages)
      .mockResolvedValueOnce(page(['other'], 7, 'page-2'))
      .mockResolvedValueOnce(page(['uploaded'], 7));
    const located = await locateProductImage(api, 'p', 'uploaded', () => {});
    expect(located.attached).toBe(true);
    expect(located.product.version).toBe(7);
    expect(api.myImages).toHaveBeenNthCalledWith(2, 'p', 'page-2', 7);
  });
  it('关联成功但响应丢失，移除走图库删除而不是再次取消ready上传', async () => {
    const api = transport();
    vi.mocked(api.myProduct).mockResolvedValue(product(7, ['uploaded']));
    const cancel = vi.fn();
    await expect(
      removePendingProductImage(api, 'p', 'uploaded', cancel, () => {}),
    ).resolves.toMatchObject({ version: 8 });
    expect(api.removeImage).toHaveBeenCalledWith('p', 'uploaded', 7);
    expect(cancel).not.toHaveBeenCalled();
  });
  it('未关联的ready图片取消上传，避免无限保留失败队列', async () => {
    const api = transport();
    const cancel = vi.fn().mockResolvedValue(undefined);
    await removePendingProductImage(api, 'p', 'uploaded', cancel, () => {});
    expect(cancel).toHaveBeenCalledOnce();
    expect(api.removeImage).not.toHaveBeenCalled();
  });
  it('取消时关联刚好完成，重新核对后删除；不盲目循环请求', async () => {
    const api = transport();
    vi.mocked(api.myProduct)
      .mockResolvedValueOnce(product(7))
      .mockResolvedValueOnce(product(8, ['uploaded']));
    const cancel = vi.fn().mockRejectedValue({ status: 409 });
    await removePendingProductImage(api, 'p', 'uploaded', cancel, () => {});
    expect(api.removeImage).toHaveBeenCalledWith('p', 'uploaded', 8);
    expect(cancel).toHaveBeenCalledOnce();
  });
  it('数据库或版本错误继续向界面报告失败，不能忘记待处理项', async () => {
    const api = transport();
    vi.mocked(api.myProduct).mockResolvedValue(product(7, ['uploaded']));
    vi.mocked(api.removeImage).mockRejectedValue({ status: 409 });
    await expect(
      removePendingProductImage(api, 'p', 'uploaded', vi.fn(), () => {}),
    ).rejects.toMatchObject({ status: 409 });
    const missing = transport();
    vi.mocked(missing.myImages).mockRejectedValue({ status: 503 });
    const cancel = vi.fn();
    await expect(
      removePendingProductImage(missing, 'p', 'uploaded', cancel, () => {}),
    ).rejects.toMatchObject({ status: 503 });
    expect(cancel).not.toHaveBeenCalled();
  });
  it('图库版本变化或重复游标停止核对，不根据残缺数据删除', async () => {
    const api = transport();
    vi.mocked(api.myImages).mockResolvedValue(page([], 8));
    await expect(locateProductImage(api, 'p', 'uploaded', () => {})).rejects.toThrow('图集已更新');
    vi.mocked(api.myImages).mockResolvedValue(page([], 7, 'same'));
    await expect(locateProductImage(api, 'p', 'uploaded', () => {})).rejects.toThrow('分页异常');
  });
  it('切换商品后旧读取完成不能继续写入', async () => {
    const api = transport();
    vi.mocked(api.myProduct).mockResolvedValue(product(7, ['uploaded']));
    let calls = 0;
    const check = () => {
      if (++calls > 1) throw new DOMException('changed', 'AbortError');
    };
    await expect(
      removePendingProductImage(api, 'p', 'uploaded', vi.fn(), check),
    ).rejects.toMatchObject({ name: 'AbortError' });
    expect(api.removeImage).not.toHaveBeenCalled();
  });
  it('服务器确认图片已被移除后清除关联标记但保留原排序锚点', () => {
    const order = new UploadSelectionOrder();
    order.register(['a', 'b']);
    order.markAttached('a', 'image-a');
    order.markAttached('b', 'image-b');
    order.unmarkAttached('a');
    expect(order.attached('a')).toBeUndefined();
    expect(order.before('a')).toBe('image-b');
  });
});
