import { afterEach, describe, expect, it, vi } from 'vitest';
afterEach(() => {
  vi.unstubAllEnvs();
  vi.unstubAllGlobals();
  vi.resetModules();
});
const response = (data: unknown, status = 200) =>
  new Response(JSON.stringify(data), { status, headers: { 'Content-Type': 'application/json' } });
describe('配置校验', () => {
  it('保留上架中止标记，同时不把未返回标记的历史商品误判为中止', async () => {
    const { productFrom } = await import('./api');
    expect(
      productFrom({ id: 'a', status: 'publishing', publication_interrupted: true })
        .publicationInterrupted,
    ).toBe(true);
    expect(productFrom({ id: 'old', status: 'draft' }).publicationInterrupted).toBe(false);
  });
  it('删除已上传封面后采用服务器返回的新顺序和版本，删除失败不返回虚假结果', async () => {
    vi.stubEnv('VITE_DATA_MODE', 'api');
    const fetch = vi
      .fn()
      .mockResolvedValueOnce(
        response({
          data: {
            id: 'p',
            title: '商品',
            version: 8,
            image_total: 1,
            images: [{ id: 'second', url: '/second' }],
          },
        }),
      )
      .mockResolvedValueOnce(response({ code: 'VERSION_CONFLICT', message: '请刷新图集' }, 409));
    vi.stubGlobal('fetch', fetch);
    const { api } = await import('./api');
    const result = await api.removeImage('p', 'first', 7);
    expect(result.images.map((image) => image.id)).toEqual(['second']);
    expect(result).toMatchObject({ imageTotal: 1, version: 8 });
    expect(fetch.mock.calls[0][1].method).toBe('DELETE');
    expect(JSON.parse(fetch.mock.calls[0][1].body)).toEqual({ expected_version: 7 });
    await expect(api.removeImage('p', 'second', 7)).rejects.toMatchObject({ status: 409 });
    expect(result.images[0].id).toBe('second');
  });
  it('接口环境默认不显示演示提示，只有显式环境标签才展示', async () => {
    vi.stubEnv('VITE_DATA_MODE', 'api');
    vi.stubEnv('VITE_ENVIRONMENT_LABEL', '测试环境 · 旧站商品快照');
    const { environmentLabel } = await import('./api');
    expect(environmentLabel).toBe('测试环境 · 旧站商品快照');
  });
  it('错误的首页配置向页面报错，不静默显示默认配置', async () => {
    vi.stubEnv('VITE_DATA_MODE', 'api');
    vi.stubGlobal(
      'fetch',
      vi
        .fn()
        .mockResolvedValue(response({ data: { presentation: { editorial: { intervalMs: -1 } } } })),
    );
    const { api } = await import('./api');
    await expect(api.home()).rejects.toThrow('首页展示配置格式有误');
  });
  it('公开筛选隐藏停用项，保留服务器配置编码', async () => {
    vi.stubEnv('VITE_DATA_MODE', 'api');
    vi.stubGlobal(
      'fetch',
      vi.fn().mockResolvedValue(
        response({
          data: [
            {
              id: 'sale',
              filter_config: [
                {
                  key: 'server',
                  label: '区服',
                  options: [
                    { value: 'east', label: '华东' },
                    { value: 'retired', label: '旧服', enabled: false },
                  ],
                },
                { key: 'private', enabled: false, options: [] },
              ],
            },
          ],
        }),
      ),
    );
    const { api } = await import('./api');
    const categories = await api.categories();
    expect(categories[0].filters).toEqual([
      { key: 'server', label: '区服', options: [{ value: 'east', label: '华东' }] },
    ]);
  });
});
describe('真实接口与演示边界', () => {
  it('演示首页各区独立排序，最新商品不因入选其他分区而消失', async () => {
    vi.stubEnv('VITE_DATA_MODE', 'demo');
    const fetch = vi.fn();
    vi.stubGlobal('fetch', fetch);
    const { api } = await import('./api');
    const home = await api.home();
    expect(home.featured).toHaveLength(3);
    expect(home.hot).toHaveLength(6);
    expect(home.newest).toHaveLength(6);
    const { fixtureProducts, demoHot, stableNewest } = await import('./demo-catalog');
    expect(home.hot.map((p) => p.id)).toEqual(
      [...fixtureProducts]
        .sort(demoHot)
        .slice(0, 6)
        .map((p) => p.id),
    );
    expect(home.newest.map((p) => p.id)).toEqual(
      [...fixtureProducts]
        .sort(stableNewest)
        .slice(0, 6)
        .map((p) => p.id),
    );
    expect(home.recommended).toEqual(home.hot);
    expect(fetch).not.toHaveBeenCalled();
  });
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
  it('读取可选首页展示配置，不在接口层截断或重排真实库存', async () => {
    vi.stubEnv('VITE_DATA_MODE', 'api');
    vi.stubGlobal(
      'fetch',
      vi.fn().mockResolvedValue(
        response({
          data: {
            hot: [{ id: 'first' }, { id: 'second' }],
            presentation: {
              sections: [{ source: 'hot', title: '本周关注', limit: 1 }],
              editorial: { intervalMs: 6000 },
              shortcuts: [],
            },
          },
        }),
      ),
    );
    const { api } = await import('./api');
    const home = await api.home();
    expect(home.hot.map(({ id }) => id)).toEqual(['first', 'second']);
    expect(home.presentation?.sections).toEqual([
      {
        source: 'hot',
        title: '本周关注',
        description: '大家正在浏览和收藏的账号。',
        limit: 1,
      },
    ]);
    expect(home.presentation?.editorial.intervalMs).toBe(6000);
    expect(home.presentation?.shortcuts).toEqual([]);
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
    vi.stubGlobal('window', { location: { origin: 'https://qingye.test' } });
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
        response({
          data: {
            media_id: 'media-1',
            upload_url: '/api/v1/uploads/media-1/content',
            offset: 0,
            chunk_size: 1024,
          },
        }),
      )
      .mockResolvedValueOnce(response({ message: '稍后重试' }, 503))
      .mockResolvedValueOnce(
        response({ data: { offset: 5, chunk_size: 1024, status: 'uploaded' } }),
      )
      .mockResolvedValueOnce(
        response({
          data: { id: 'media-1', status: 'ready', url: 'https://storage.example/image' },
        }),
      );
    vi.stubGlobal('fetch', fetch);
    const { api } = await import('./api');
    const file = new File(['image'], 'image.png', { type: 'image/png' });
    await expect(api.uploadProduct(file)).rejects.toMatchObject({ status: 503 });
    await expect(api.uploadProduct(file)).resolves.toMatchObject({ id: 'media-1' });
    expect(sends).toBe(1);
    expect(fetch.mock.calls.map((c) => c[0])).toEqual([
      '/api/v1/uploads',
      '/api/v1/uploads/media-1/complete',
      '/api/v1/uploads/media-1/status',
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
