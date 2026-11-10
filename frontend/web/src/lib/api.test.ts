import { afterEach, describe, expect, it, vi } from 'vitest';
afterEach(() => {
  vi.unstubAllEnvs();
  vi.unstubAllGlobals();
  vi.resetModules();
});
const response = (data: unknown, status = 200) =>
  new Response(JSON.stringify(data), { status, headers: { 'Content-Type': 'application/json' } });
describe('真实接口与演示边界', () => {
  it('真实服务失败不会回退到样例商品', async () => {
    vi.stubEnv('VITE_DATA_MODE', 'api');
    vi.stubGlobal(
      'fetch',
      vi.fn().mockResolvedValue(response({ code: 'UNAVAILABLE', message: '服务维护中' }, 503)),
    );
    const { api, isDemo } = await import('./api');
    expect(isDemo).toBe(false);
    await expect(api.products()).rejects.toMatchObject({ status: 503, message: '服务维护中' });
  });
  it('保留服务端推荐顺序与真实核验状态', async () => {
    vi.stubEnv('VITE_DATA_MODE', 'api');
    vi.stubGlobal(
      'fetch',
      vi.fn().mockResolvedValue(
        response({
          data: {
            hot: [
              { id: 'server-first', price_amount: '25.00' },
              { id: 'server-second', price_amount: '2.00' },
            ],
          },
        }),
      ),
    );
    const { api } = await import('./api');
    const home = await api.home();
    expect(home.hot.map((p) => p.id)).toEqual(['server-first', 'server-second']);
    expect(home.hot[0].verification).toBe('unprovided');
    expect(home.hot[0].art).toBe(-1);
  });
  it('会话失效返回游客，服务器异常保留为错误', async () => {
    vi.stubEnv('VITE_DATA_MODE', 'api');
    const fetch = vi
      .fn()
      .mockResolvedValueOnce(response({ message: '请登录' }, 401))
      .mockResolvedValueOnce(response({ message: '数据库维护' }, 503));
    vi.stubGlobal('fetch', fetch);
    const { api } = await import('./api');
    await expect(api.me()).resolves.toBeNull();
    await expect(api.me()).rejects.toMatchObject({ status: 503 });
    expect(fetch.mock.calls[0][1].credentials).toBe('include');
  });
  it('短信演示不会发送请求或伪造成功', async () => {
    vi.stubEnv('VITE_DATA_MODE', 'demo');
    const fetch = vi.fn();
    vi.stubGlobal('fetch', fetch);
    const { api } = await import('./api');
    await expect(api.sms('13800000000', 'login')).rejects.toMatchObject({ code: 'DEMO_ONLY' });
    expect(fetch).not.toHaveBeenCalled();
  });
  it('客服入口按接口约定查询买号、上架、租号及通用联系方式', async () => {
    vi.stubEnv('VITE_DATA_MODE', 'api');
    const fetch = vi.fn().mockImplementation(() => Promise.resolve(response({ data: [] })));
    vi.stubGlobal('fetch', fetch);
    const { api } = await import('./api');
    for (const purpose of ['buy', 'publish', 'sale', 'rent', 'general', undefined]) {
      await api.contacts(purpose);
    }
    expect(fetch.mock.calls.map((call) => call[0])).toEqual([
      '/api/v1/contacts?purpose=sale',
      '/api/v1/contacts?purpose=listing',
      '/api/v1/contacts?purpose=sale',
      '/api/v1/contacts?purpose=rent',
      '/api/v1/contacts?purpose=general',
      '/api/v1/contacts',
    ]);
  });
  it('收藏读取继续获取游标后的全部记录', async () => {
    vi.stubEnv('VITE_DATA_MODE', 'api');
    const fetch = vi
      .fn()
      .mockResolvedValueOnce(response({ data: [{ id: 'one' }], next_cursor: 'cursor-two' }))
      .mockResolvedValueOnce(response({ data: [{ id: 'two' }], next_cursor: null }));
    vi.stubGlobal('fetch', fetch);
    const { api } = await import('./api');
    expect((await api.favorites()).map((p) => p.id)).toEqual(['one', 'two']);
    expect(fetch.mock.calls[1][0]).toContain('cursor=cursor-two');
  });
  it('HTTP预览没有randomUUID时仍能生成请求标识', async () => {
    vi.stubGlobal('crypto', {
      getRandomValues: (bytes: Uint8Array) => {
        bytes.fill(15);
        return bytes;
      },
    });
    const { createRequestId } = await import('./api');
    expect(createRequestId()).toMatch(
      /^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/,
    );
  });
  it('草稿创建响应丢失后重试复用请求编号，成功后下一次创建使用新编号', async () => {
    vi.stubEnv('VITE_DATA_MODE', 'api');
    const fetch = vi
      .fn()
      .mockRejectedValueOnce(new TypeError('network'))
      .mockResolvedValueOnce(response({ data: { id: 'saved-one' } }))
      .mockResolvedValueOnce(response({ data: { id: 'saved-two' } }));
    vi.stubGlobal('fetch', fetch);
    const { api } = await import('./api');
    const { fixtureProducts } = await import('./fixtures');
    await expect(api.saveProduct(fixtureProducts[0])).rejects.toMatchObject({
      code: 'NETWORK_ERROR',
    });
    await api.saveProduct(fixtureProducts[0]);
    await api.saveProduct(fixtureProducts[0]);
    const ids = fetch.mock.calls.map((call) => JSON.parse(call[1].body).request_id);
    expect(ids[0]).toBe(ids[1]);
    expect(ids[2]).not.toBe(ids[1]);
  });
  it('公开详情接口错误地返回下架资料时仍不向页面展示内容', async () => {
    vi.stubEnv('VITE_DATA_MODE', 'api');
    vi.stubGlobal(
      'fetch',
      vi
        .fn()
        .mockResolvedValue(
          response({ data: { id: 'offline', status: 'offline', description: '不应展示的资料' } }),
        ),
    );
    const { api } = await import('./api');
    await expect(api.product('offline')).rejects.toMatchObject({ status: 410 });
  });
  it('文件传输已完成但完成通知失败时，重试只发完成通知', async () => {
    vi.stubEnv('VITE_DATA_MODE', 'api');
    let sends = 0;
    class UploadRequest {
      upload = {};
      status = 200;
      onload?: () => void;
      open() {}
      setRequestHeader() {}
      send() {
        sends++;
        queueMicrotask(() => this.onload?.());
      }
      abort() {}
    }
    vi.stubGlobal('XMLHttpRequest', UploadRequest);
    const fetch = vi
      .fn()
      .mockResolvedValueOnce(
        response({ data: { media_id: 'media-1', upload_url: 'https://storage.example/upload' } }),
      )
      .mockResolvedValueOnce(response({ message: '稍后重试' }, 503))
      .mockResolvedValueOnce(
        response({ data: { id: 'media-1', state: 'ready', url: 'https://storage.example/image' } }),
      );
    vi.stubGlobal('fetch', fetch);
    const { api } = await import('./api');
    const file = new File(['image'], 'image.png', { type: 'image/png' });
    await expect(api.upload(file)).rejects.toMatchObject({ status: 503 });
    await expect(api.upload(file)).resolves.toMatchObject({ id: 'media-1' });
    expect(sends).toBe(1);
    expect(fetch.mock.calls.map((c) => c[0])).toEqual([
      '/api/v1/uploads',
      '/api/v1/uploads/media-1/complete',
      '/api/v1/uploads/media-1/complete',
    ]);
  });
  it('演示图集超过200张仍能持续分页，版本不一致时拒绝混合图集', async () => {
    vi.stubEnv('VITE_DATA_MODE', 'demo');
    const { fixtureProducts } = await import('./fixtures');
    const product = {
      ...fixtureProducts[0],
      status: 'draft',
      imageTotal: 225,
      images: Array.from({ length: 225 }, (_, i) => ({
        id: `art-${i}`,
        art: i % 4,
        alt: `概念图${i}`,
      })),
    };
    vi.stubGlobal('localStorage', {
      getItem: () => JSON.stringify({ user: { id: 'demo' }, favorites: [], products: [product] }),
    });
    const { api } = await import('./api');
    let cursor: string | null = null;
    let count = 0;
    do {
      const page = await api.myImages(product.id, cursor, 1);
      count += page.items.length;
      cursor = page.nextCursor;
    } while (cursor);
    expect(count).toBe(225);
    expect((await api.myProduct(product.id)).images).toHaveLength(20);
    await expect(api.myImages(product.id, null, 2)).rejects.toMatchObject({ status: 409 });
  });
  it('图集写入只返回新版本时重新读取完整资料', async () => {
    vi.stubEnv('VITE_DATA_MODE', 'api');
    const fetch = vi
      .fn()
      .mockResolvedValueOnce(response({ data: { version: 2 } }))
      .mockResolvedValueOnce(
        response({ data: { id: 'product-1', title: '当前资料', version: 2 } }),
      );
    vi.stubGlobal('fetch', fetch);
    const { api } = await import('./api');
    await expect(api.appendImages('product-1', ['image-1'], 1)).resolves.toMatchObject({
      id: 'product-1',
      title: '当前资料',
      version: 2,
    });
    expect(fetch.mock.calls[1][0]).toBe('/api/v1/me/products/product-1');
  });
});
