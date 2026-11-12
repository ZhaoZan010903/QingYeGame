import { describe, expect, it, vi } from 'vitest';
import { createProductUploader, validateProductBatch } from '../../../共享/product-upload';
import {
  runOrderedUploads,
  UploadSelectionOrder,
  UploadScope,
} from '../../../共享/ordered-uploads';

// Small binary fixtures exercise protocol/order correctness, not production throughput.
const file = (name = 'test.png', size = 4096) =>
  new File([new Uint8Array(size)], name, { type: 'image/png' });

describe('R20 商品批次与续传', () => {
  it('服务端已确认的offset立即恢复进度，完成校验前最多显示99%', async () => {
    const progress: number[] = [];
    const uploader = createProductUploader(
      async (path) => ({
        data: path.endsWith('/complete')
          ? { status: 'ready' }
          : {
              media_id: 'id',
              offset: 1024,
              chunk_size: 1024,
              upload_url: '/api/v1/uploads/id/content',
            },
      }),
      (record) => record,
      async () => {},
    );
    await uploader.upload(file('small.png', 4096), (value) => progress.push(value));
    expect(progress).toEqual([25, 50, 75, 99, 100]);
  });
  it('换商品使旧回调失效，A到B再回A也不能恢复旧回调', () => {
    const scope = new UploadScope('a');
    const old = scope.capture();
    scope.select('b');
    expect(old).toThrow();
    scope.select('a');
    expect(old).toThrow();
    expect(scope.capture()).not.toThrow();
    const draft = new UploadScope();
    const creating = draft.capture();
    draft.select('new-draft', true);
    expect(creating).not.toThrow();
  });
  it('上传后重新排列图集，早先失败项的重试锚点使用新顺序', () => {
    const order = new UploadSelectionOrder();
    order.register(['a', 'b', 'c']);
    order.markAttached('b', 'image-b');
    order.markAttached('c', 'image-c');
    order.reorderImages(['image-c', 'image-b']);
    expect(order.before('a')).toBe('image-c');
    expect(order.before('not-registered')).toBeNull();
  });
  it('身份切换后迟到的上传申请不能启动传输或写入新会话', async () => {
    let finish!: (response: { data: Record<string, unknown> }) => void;
    const request = vi.fn(
      () =>
        new Promise<{ data: Record<string, unknown> }>((resolve) => {
          finish = resolve;
        }),
    );
    const transfer = vi.fn();
    const uploader = createProductUploader(request, (record) => record, transfer);
    const pending = uploader.upload(file());
    uploader.reset();
    finish({
      data: {
        media_id: 'old-identity',
        offset: 0,
        chunk_size: 1024,
        upload_url: '/api/v1/uploads/old/content',
      },
    });
    await expect(pending).rejects.toMatchObject({ name: 'AbortError' });
    expect(transfer).not.toHaveBeenCalled();
  });

  it('已暂停的批次不传输任何字节', async () => {
    const controller = new AbortController();
    controller.abort();
    const transfer = vi.fn();
    const uploader = createProductUploader(
      async () => ({ data: { media_id: 'id', offset: 0, chunk_size: 1024 } }),
      (record) => record,
      transfer,
    );
    await expect(uploader.upload(file(), undefined, controller.signal)).rejects.toMatchObject({
      name: 'AbortError',
    });
    expect(transfer).not.toHaveBeenCalled();
  });
  it('99张允许，100张整批拒绝，空文件与不支持格式拒绝', () => {
    expect(
      validateProductBatch(
        Array.from({ length: 99 }, () => file()),
        10240,
      ),
    ).toBe('');
    expect(
      validateProductBatch(
        Array.from({ length: 100 }, () => file()),
        10240,
      ),
    ).toContain('1—99');
    expect(validateProductBatch([], 10240)).toContain('1—99');
    expect(validateProductBatch([file('empty.png', 0)], 10240)).toContain('不能为空');
    expect(
      validateProductBatch([{ name: 'a.svg', type: 'image/svg+xml', size: 2000 }], 10240),
    ).toContain('JPG');
  });

  it('断网后的重试从服务器offset继续，并复用上传任务', async () => {
    const uploaded: string[] = [];
    let offset = 0,
      failed = false;
    const request = vi.fn(async (path: string) => ({
      data: path.endsWith('/complete')
        ? { status: 'ready', url: '/preview' }
        : {
            media_id: 'media-1',
            offset,
            chunk_size: 1024,
            upload_url: '/api/v1/uploads/media-1/content',
            status: 'pending',
          },
    }));
    const uploader = createProductUploader(
      request,
      (r) => r,
      async (_url, blob, headers) => {
        if (offset === 1024 && !failed) {
          failed = true;
          throw new Error('network');
        }
        uploaded.push(headers['Content-Range']);
        offset += blob.size;
      },
    );
    const selected = file();
    await expect(uploader.upload(selected)).rejects.toThrow('network');
    const result = await uploader.upload(selected);
    expect(result.id).toBe('media-1');
    expect(uploaded).toEqual([
      'bytes 0-1023/4096',
      'bytes 1024-2047/4096',
      'bytes 2048-3071/4096',
      'bytes 3072-4095/4096',
    ]);
    expect(request.mock.calls.filter(([path]) => path === '/uploads')).toHaveLength(1);
    expect(request.mock.calls.some(([path]) => path.endsWith('/status'))).toBe(true);
  });

  it('服务端已收块但响应丢失，恢复时跳过已收字节', async () => {
    let offset = 0;
    const sent: string[] = [];
    const request = vi.fn(async (path: string) => ({
      data: path.endsWith('/complete')
        ? { status: 'ready' }
        : { media_id: 'id', offset, chunk_size: 1024, upload_url: '/api/v1/uploads/id/content' },
    }));
    const uploader = createProductUploader(
      request,
      (r) => r,
      async (_url, blob, headers) => {
        sent.push(headers['Content-Range']);
        offset += blob.size;
        if (offset === 1024) throw new Error('lost response');
      },
    );
    const selected = file('test.png', 2048);
    await expect(uploader.upload(selected)).rejects.toThrow('lost response');
    await uploader.upload(selected);
    expect(sent).toEqual(['bytes 0-1023/2048', 'bytes 1024-2047/2048']);
  });

  it('取消删除已创建任务；reset使下个身份无法复用旧任务', async () => {
    let calls = 0;
    const request = vi.fn(async (path: string, method?: string) => ({
      data:
        method === 'DELETE'
          ? {}
          : path.endsWith('/complete')
            ? { status: 'ready' }
            : {
                media_id: `id-${++calls}`,
                offset: 0,
                chunk_size: 1024,
                upload_url: '/api/v1/uploads/id/content',
              },
    }));
    const uploader = createProductUploader(
      request,
      (r) => r,
      async () => {},
    );
    const selected = file('test.png', 1024);
    await uploader.upload(selected);
    await uploader.cancel(selected);
    expect(request.mock.calls).toContainEqual(['/uploads/id-1', 'DELETE']);
    await uploader.upload(selected);
    uploader.reset();
    await uploader.upload(selected);
    expect(request.mock.calls.filter(([path]) => path === '/uploads')).toHaveLength(3);
  });

  it('恶意offset/chunk响应不进入字节上传', async () => {
    const transfer = vi.fn();
    const uploader = createProductUploader(
      async () => ({ data: { media_id: 'id', offset: -1, chunk_size: 1024 } }),
      (r) => r,
      transfer,
    );
    await expect(uploader.upload(file())).rejects.toThrow('进度无效');
    expect(transfer).not.toHaveBeenCalled();
  });

  it('300张按99+99+99+3追加，异步完成仍按全局顺序关联且并发不超过3', async () => {
    const order = new UploadSelectionOrder();
    const committed: string[] = [];
    let active = 0,
      peak = 0,
      index = 0;
    for (const size of [99, 99, 99, 3]) {
      const batch = Array.from({ length: size }, () => ({ key: String(index++), value: file() }));
      expect(
        validateProductBatch(
          batch.map((item) => item.value),
          10240,
        ),
      ).toBe('');
      order.register(batch.map((item) => item.key));
      await runOrderedUploads(
        batch,
        3,
        async (item) => {
          peak = Math.max(peak, ++active);
          await new Promise((resolve) => setTimeout(resolve, Number(item.key) % 3));
          active--;
          return item.key;
        },
        async (item, id) => {
          order.markAttached(item.key, id);
          committed.push(id);
        },
        () => {
          throw new Error('unexpected failure');
        },
      );
    }
    expect(committed).toEqual(Array.from({ length: 300 }, (_, i) => String(i)));
    expect(peak).toBe(3);
  });

  it('上传前跨批排序保留已关联项作为重试锚点', () => {
    const order = new UploadSelectionOrder();
    order.register(['a', 'b']);
    order.register(['c', 'd']);
    order.markAttached('b', 'saved-b');
    order.reorderPending(['d', 'c', 'a']);
    expect(order.before('d')).toBe('saved-b');
    expect(order.before('a')).toBeNull();
  });
});
