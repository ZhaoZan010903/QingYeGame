import { describe, expect, it } from 'vitest';
import {
  summarizeUploadProgress,
  publicationInterrupted,
  publicationProgress,
} from '../../../共享/upload-progress';

describe('R20 上传与上架的真实进度', () => {
  it('大小不同的图片按字节加权，不把已完成小图当成一半流量', () => {
    const value = summarizeUploadProgress({ count: 1, bytes: 1024 }, [
      { file: { size: 9216 }, progress: 50, status: 'uploading' },
    ]);
    expect(value).toMatchObject({ total: 2, completed: 1, percent: 55 });
  });
  it('网络传完但仍在处理或关联时不显示整批完成', () => {
    expect(
      summarizeUploadProgress({ count: 0, bytes: 0 }, [
        { file: { size: 4096 }, progress: 100, status: 'saving' },
      ]),
    ).toMatchObject({ completed: 0, percent: 99, saving: 1 });
  });
  it('失败和暂停保持真实已上传比例，不随时间自行增加', () => {
    const pending = [
      { file: { size: 4096 }, progress: 50, status: 'paused' },
      { file: { size: 4096 }, progress: 25, status: 'failed' },
    ];
    expect(summarizeUploadProgress({ count: 0, bytes: 0 }, pending)).toMatchObject({
      percent: 37,
      paused: 1,
      failed: 1,
    });
    expect(summarizeUploadProgress({ count: 0, bytes: 0 }, pending)).toEqual(
      summarizeUploadProgress({ count: 0, bytes: 0 }, pending),
    );
  });
  it('已完成任务释放预览后仍保留完成数，追加批次使用新的总数', () => {
    const completed = { count: 99, bytes: 99 * 4096 };
    expect(summarizeUploadProgress(completed, [])).toMatchObject({
      completed: 99,
      total: 99,
      percent: 100,
    });
    expect(
      summarizeUploadProgress(completed, [{ file: { size: 4096 }, progress: 0, status: 'queued' }]),
    ).toMatchObject({ total: 100, percent: 99 });
  });
  it('同步进度无图片时为0，只有已验证图片计入完成', () => {
    expect(publicationProgress(0, 0)).toBe(0);
    expect(publicationProgress(3, 8)).toBe(37);
    expect(publicationProgress(8, 8)).toBe(100);
    expect(publicationProgress(99, 8)).toBe(100);
    expect(publicationProgress(NaN, 8)).toBe(0);
  });
  it('仅上架中的中止标记改变提示，历史标记不覆盖其他商品状态', () => {
    expect(publicationInterrupted('publishing', true)).toBe(true);
    expect(publicationInterrupted('publishing', false)).toBe(false);
    expect(publicationInterrupted('offline', true)).toBe(false);
  });
});
