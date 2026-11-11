import { describe, expect, it, vi } from 'vitest';
import { runOrderedUploads, UploadSelectionOrder } from '../../../共享/ordered-uploads';

const deferred = <T>() => {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>((done) => {
    resolve = done;
  });
  return { promise, resolve };
};
describe('图片选择顺序与并发上传', () => {
  it('后选文件先传完，关联仍按选择顺序并保持并发上限', async () => {
    const first = deferred<string>(),
      second = deferred<string>();
    const uploaded: string[] = [],
      committed: string[] = [];
    let active = 0,
      maximum = 0;
    const run = runOrderedUploads(
      ['1', '2', '3'],
      2,
      async (key) => {
        maximum = Math.max(maximum, ++active);
        const value =
          key === '1' ? await first.promise : key === '2' ? await second.promise : 'image-3';
        active--;
        uploaded.push(key);
        return value;
      },
      async (key) => {
        committed.push(key);
      },
      () => {
        throw new Error('unexpected failure');
      },
    );
    second.resolve('image-2');
    await vi.waitFor(() => expect(uploaded).toEqual(['2', '3']));
    expect(committed).toEqual([]);
    first.resolve('image-1');
    await run;
    expect(committed).toEqual(['1', '2', '3']);
    expect(maximum).toBe(2);
  });
  it('前项失败或取消不会阻塞后续成功文件，关联失败也可继续处理', async () => {
    const failed: string[] = [],
      committed: string[] = [];
    await runOrderedUploads(
      ['fail', 'cancel', 'save-fail', 'ok'],
      3,
      async (key) => {
        if (key === 'fail') throw new Error('network');
        if (key === 'cancel') throw new DOMException('cancelled', 'AbortError');
        return key;
      },
      async (key) => {
        if (key === 'save-fail') throw new Error('version conflict');
        committed.push(key);
      },
      (key) => {
        failed.push(key);
      },
    );
    expect(committed).toEqual(['ok']);
    expect(failed).toEqual(['fail', 'cancel', 'save-fail']);
  });
  it('失败文件稍后重试插回后选成功项之前，后续批次不打乱原顺序', () => {
    const order = new UploadSelectionOrder();
    order.register(['1', '2', '3']);
    order.markAttached('2', 'image-2');
    order.markAttached('3', 'image-3');
    order.register(['4', '5']);
    order.markAttached('4', 'image-4');
    expect(order.before('1')).toBe('image-2');
    expect(order.before('5')).toBeNull();
    order.markAttached('1', 'image-1');
    expect(order.attached('1')).toBe('image-1');
    expect(order.before('2')).toBe('image-3');
  });
  it('关联成功但排序失败时记录关联ID，重试不重复追加', async () => {
    const order = new UploadSelectionOrder();
    order.register(['1', '2']);
    order.markAttached('2', 'image-2');
    const appended: string[] = [],
      failed: string[] = [];
    let failMove = true;
    const commit = async (key: string, id: string) => {
      if (!order.attached(key)) {
        appended.push(id);
        order.markAttached(key, id);
      }
      if (order.before(key) && failMove) throw new Error('move response lost');
    };
    await runOrderedUploads(
      ['1'],
      3,
      async () => 'image-1',
      commit,
      (key) => {
        failed.push(key);
      },
    );
    failMove = false;
    await runOrderedUploads(
      ['1'],
      3,
      async () => 'image-1',
      commit,
      () => {
        throw new Error('unexpected failure');
      },
    );
    expect(appended).toEqual(['image-1']);
    expect(failed).toEqual(['1']);
  });
  it('移除失败项或已保存图片后不会再使用失效排序锚点', () => {
    const order = new UploadSelectionOrder();
    order.register(['1', '2', '3']);
    order.markAttached('2', 'image-2');
    order.markAttached('3', 'image-3');
    order.removeImage('image-2');
    expect(order.before('1')).toBe('image-3');
    order.forget('3');
    expect(order.before('1')).toBeNull();
  });
});
