import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
vi.unmock('../../../共享/response-crypto');
import { responseCrypto } from '../../../共享/response-crypto';
import { acquireMediaSlot, mediaReference, resolveMediaImage } from '../../../共享/media-delivery';
import {
  fixtureInit,
  fixtureEncrypted,
  fixtureLocalImage,
} from '../../../共享/response-crypto-fixture';
import { safetyRequest } from '../../../共享/media-access-client';

beforeEach(() => {
  const location = new URL('https://qingye.test/accounts/a');
  vi.stubGlobal('location', location);
  vi.stubGlobal('window', { location });
  // Browser-only cross-tab delivery is exercised by the browser acceptance run.
  vi.stubGlobal('BroadcastChannel', undefined);
});
afterEach(() => {
  responseCrypto.reset();
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
});
function source(mode = 'oss', changes: Record<string, unknown> = {}) {
  return {
    id: 'image-1',
    variant: 'original',
    mode,
    url:
      mode === 'oss'
        ? 'https://qingye-images-20261.oss-cn-beijing.aliyuncs.com/test-image?signature=short'
        : '/api/v1/media/image-1/file?grant=opaque',
    expires_at: '2026-11-15T00:00:05Z',
    mime_type: 'image/png',
    ...changes,
  };
}
function server(item: ReturnType<typeof source>, bytes = new Uint8Array([137, 80, 78, 71])) {
  const fetcher = vi.fn(async (input: string, init: RequestInit = {}) => {
    if (input.includes('/response-crypto/init')) return fixtureInit();
    if (input.includes('/access-plan'))
      return fixtureEncrypted(input, init, {
        data: { server_time: '2026-11-15T00:00:00Z', items: [item] },
      });
    return fixtureEncrypted(input, init, null, { contentType: 'image/png', bytes });
  });
  vi.stubGlobal('fetch', fetcher);
  return fetcher;
}
describe('R24 图片访问计划', () => {
  it('本地图片仍走鉴权计划和Blob，只有计划解密而不对原始图片做AES', async () => {
    vi.stubGlobal('fetch', async (input: string, init: RequestInit = {}) => {
      if (input.includes('/response-crypto/init')) return fixtureInit();
      if (input.includes('/access-plan'))
        return fixtureEncrypted(input, init, {
          data: {
            server_time: '2026-11-15T00:00:00Z',
            items: [source('go', { expires_at: '2026-11-15T00:00:30Z' })],
          },
        });
      return fixtureLocalImage(init);
    });
    const decrypt = vi.spyOn(crypto.subtle, 'decrypt');
    const image = await resolveMediaImage(
      '/api/v1/media/image-1/file',
      new AbortController().signal,
    );
    expect(image.mode).toBe('go');
    expect(image.url.startsWith('blob:')).toBe(true);
    expect(decrypt).toHaveBeenCalledTimes(1);
    image.dispose();
  });
  it('只解析本站媒体编号，preview与thumb查询沿用原规格', () => {
    expect(mediaReference('/api/v1/media/image-1/preview')).toEqual({
      id: 'image-1',
      variant: 'thumb',
    });
    expect(mediaReference('/api/v1/media/image-1/file?variant=thumb').variant).toBe('thumb');
    expect(() => mediaReference('https://other.test/api/v1/media/image-1/file')).toThrow();
    expect(mediaReference('/api/v1/media/image-1/file?product_id=p&version=7')).toMatchObject({
      product_id: 'p',
      product_version: 7,
    });
    expect(() => mediaReference('/api/v1/media/image-1/file?version=7')).toThrow();
    expect(() =>
      mediaReference('/api/v1/media/image-1/file?product_id=p&version=7', {
        productId: 'p',
        productVersion: 8,
      }),
    ).toThrow();
  });
  it('OSS分支仅请求当前图的计划，浏览器img直连且不发送Go头到OSS', async () => {
    const fetcher = server(source());
    const result = await resolveMediaImage(
      '/api/v1/media/image-1/file',
      new AbortController().signal,
    );
    expect(result.mode).toBe('oss');
    expect(result.url).toContain('qingye-images-20261.oss-cn-beijing.aliyuncs.com');
    expect(fetcher).toHaveBeenCalledTimes(2);
    const call = fetcher.mock.calls.find(([url]) => url.includes('access-plan'))!;
    expect(JSON.parse(String(call[1]?.body))).toEqual({
      items: [{ id: 'image-1', variant: 'original' }],
    });
    result.dispose();
  });
  it('Go分支解密原始字节后创建Blob，释放必须幂等', async () => {
    const bytes = new Uint8Array([137, 80, 78, 71, 0, 255]);
    server(source('go', { expires_at: '2026-11-15T00:00:30Z' }), bytes);
    const create = vi.spyOn(URL, 'createObjectURL'),
      revoke = vi.spyOn(URL, 'revokeObjectURL');
    const result = await resolveMediaImage(
      '/api/v1/media/image-1/file',
      new AbortController().signal,
    );
    expect(result.mode).toBe('go');
    expect(result.url.startsWith('blob:')).toBe(true);
    const blob = create.mock.calls[0][0] as Blob;
    expect(new Uint8Array(await blob.arrayBuffer())).toEqual(bytes);
    result.dispose();
    result.dispose();
    expect(revoke).toHaveBeenCalledTimes(1);
  });
  it('商品展示使用所见版本，不为冲突补取最新版本或自动重发', async () => {
    const fetcher = vi.fn(async (input: string, init: RequestInit = {}) =>
      input.includes('/response-crypto/init')
        ? fixtureInit()
        : fixtureEncrypted(
            input,
            init,
            { code: 'VERSION_CONFLICT', message: '图集已更新' },
            { status: 409 },
          ),
    );
    vi.stubGlobal('fetch', fetcher);
    await expect(
      resolveMediaImage('/api/v1/media/image-1/file', new AbortController().signal, {
        productId: 'product-1',
        productVersion: 7,
      }),
    ).rejects.toMatchObject({ status: 409 });
    expect(JSON.parse(String(fetcher.mock.calls[1][1]?.body)).items[0]).toMatchObject({
      product_id: 'product-1',
      product_version: 7,
    });
    expect(fetcher).toHaveBeenCalledTimes(2);
  });
  it('计划有效期减去请求等待，慢网不会把收到响应当作5秒起点', async () => {
    let now = 0;
    vi.spyOn(performance, 'now').mockImplementation(() => now);
    vi.stubGlobal('fetch', async (input: string, init: RequestInit = {}) => {
      if (input.includes('/response-crypto/init')) return fixtureInit();
      now = 8000;
      return fixtureEncrypted(input, init, {
        data: { server_time: '2026-11-15T00:00:00Z', items: [source()] },
      });
    });
    const image = await resolveMediaImage(
      '/api/v1/media/image-1/file',
      new AbortController().signal,
    );
    expect(image.expiresAt).toBe(5000);
    expect(image.expiresAt! < performance.now()).toBe(true);
  });
  it.each([
    { url: 'https://other.test/photo.png' },
    { id: 'wrong-image' },
    { expires_at: '2026-11-15T01:00:00Z' },
    { mime_type: 'image/svg+xml' },
  ])('拒绝不匹配或过宽的访问计划 %j', async (changes) => {
    server(source('oss', changes));
    await expect(
      resolveMediaImage('/api/v1/media/image-1/file', new AbortController().signal),
    ).rejects.toThrow();
  });
  it('被取消的请求不创建Blob，也不自动补签', async () => {
    const fetcher = server(source());
    const controller = new AbortController();
    controller.abort();
    await expect(
      resolveMediaImage('/api/v1/media/image-1/file', controller.signal),
    ).rejects.toThrow();
    expect(fetcher.mock.calls.filter(([url]) => url.includes('access-plan'))).toHaveLength(0);
  });
  it('图片读取并发最多4个，取消排队和重复释放不会漏放或超额', async () => {
    const controllers = Array.from({ length: 8 }, () => new AbortController());
    const releases: Array<() => void> = [];
    const tasks = controllers.map((controller) =>
      acquireMediaSlot(controller.signal)
        .then((release) => {
          releases.push(release);
        })
        .catch(() => {}),
    );
    await Promise.resolve();
    expect(releases).toHaveLength(4);
    controllers[4].abort();
    releases[0]();
    releases[0]();
    await Promise.resolve();
    expect(releases).toHaveLength(5);
    for (const release of releases) release();
    await Promise.resolve();
    for (const release of releases) release();
    await Promise.all(tasks);
    expect(releases).toHaveLength(7);
  });
  it('安全验证接口也解密业务拒绝，不把错误当成验证通过', async () => {
    vi.stubGlobal('fetch', async (input: string, init: RequestInit = {}) =>
      input.includes('/response-crypto/init')
        ? fixtureInit()
        : fixtureEncrypted(
            input,
            init,
            { code: 'CHALLENGE_EXPIRED', message: '验证已过期' },
            { status: 403 },
          ),
    );
    await expect(safetyRequest('challenges/verify', { token: 'synthetic' })).rejects.toThrow(
      '验证已过期',
    );
  });
});
