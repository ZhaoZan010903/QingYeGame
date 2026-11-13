import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import {
  accessSnapshot,
  closeMediaVerification,
  ensureMediaSession,
  inspectMediaFailure,
  isProtectedMedia,
  mediaVerificationComplete,
  queueMediaRetry,
  resetMediaIdentity,
  safetyRequest,
} from '../../../共享/media-access-client';

describe('R20-1 图片访问恢复', () => {
  beforeEach(() => {
    resetMediaIdentity();
    vi.stubGlobal('window', {
      location: { href: 'https://qingye.test/accounts/a', origin: 'https://qingye.test' },
    });
  });
  afterEach(() => {
    vi.useRealTimers();
    vi.unstubAllGlobals();
    resetMediaIdentity();
  });
  const respond = (state = 'allowed') => ({
    ok: true,
    json: async () => ({ enabled: true, state, retry_after: 2 }),
  });
  it('只处理同源媒体，保留blob、本地图、第三方图原行为', () => {
    for (const src of [
      '/api/v1/media/a/file',
      '/api/v1/media/b/preview?variant=thumb',
      '/api/v1/media/abc-123',
    ])
      expect(isProtectedMedia(src)).toBe(true);
    for (const src of [
      'blob:preview',
      '/logo.svg',
      'https://other.test/api/v1/media/a/file',
      '/api/v1/media/a/file/evil',
    ])
      expect(isProtectedMedia(src)).toBe(false);
  });
  it('并发图片共用一次初始化并保留凭据', async () => {
    const fetcher = vi.fn(async () => respond());
    vi.stubGlobal('fetch', fetcher);
    await Promise.all(Array.from({ length: 100 }, () => ensureMediaSession()));
    expect(fetcher).toHaveBeenCalledTimes(1);
    expect(fetcher.mock.calls[0]).toEqual([
      '/api/v1/security/media-access',
      expect.objectContaining({ credentials: 'same-origin', cache: 'no-store' }),
    ]);
  });
  it('并发失败合并探测，关闭后不重复弹窗', async () => {
    const fetcher = vi.fn(async () => respond('challenge'));
    vi.stubGlobal('fetch', fetcher);
    await Promise.all(Array.from({ length: 50 }, () => inspectMediaFailure()));
    expect(fetcher).toHaveBeenCalledTimes(1);
    expect(accessSnapshot().open).toBe(true);
    closeMediaVerification();
    await inspectMediaFailure();
    expect(accessSnapshot().open).toBe(false);
  });
  it('普通图片失败不自动要求验证码', async () => {
    vi.stubGlobal('fetch', async () => respond());
    await inspectMediaFailure();
    expect(accessSnapshot().status).toBeNull();
  });
  it('身份变化后拒绝旧响应回填', async () => {
    let finish!: (value: ReturnType<typeof respond>) => void;
    vi.stubGlobal(
      'fetch',
      () =>
        new Promise((resolve) => {
          finish = resolve;
        }),
    );
    const old = inspectMediaFailure();
    resetMediaIdentity();
    finish(respond('challenge'));
    await expect(old).rejects.toThrow('会话已变化');
    expect(accessSnapshot().status).toBeNull();
  });
  it('验证通过只发恢复事件，不重载页面', () => {
    const before = accessSnapshot().revision;
    mediaVerificationComplete();
    expect(accessSnapshot()).toEqual({ status: null, open: false, revision: before + 1 });
  });
  it('恢复队列最多同时启动4张，重复释放无影响', () => {
    vi.useFakeTimers();
    const releases: Array<() => void> = [];
    for (let i = 0; i < 30; i++) queueMediaRetry((release) => releases.push(release));
    expect(releases).toHaveLength(4);
    releases[0]();
    releases[0]();
    expect(releases).toHaveLength(5);
    resetMediaIdentity();
    vi.runAllTimers();
    expect(releases).toHaveLength(5);
  });
  it('验证失败显示服务端业务错误，不当作通过', async () => {
    vi.stubGlobal('fetch', async () => ({
      ok: false,
      json: async () => ({ message: '验证已过期' }),
    }));
    await expect(safetyRequest('challenges/verify', { token: 'test-token' })).rejects.toThrow(
      '验证已过期',
    );
  });
});
