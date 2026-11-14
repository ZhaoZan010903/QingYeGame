import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
vi.unmock('../../../共享/response-crypto');
import {
  RESPONSE_PROTOCOL,
  ResponseCryptoClient,
  responseCrypto,
} from '../../../共享/response-crypto';
import {
  fixtureInit,
  fixtureEncrypted,
  fixtureChat,
  fixtureLocalImage,
} from '../../../共享/response-crypto-fixture';

beforeEach(() => {
  vi.stubGlobal('navigator', {});
});
afterEach(() => {
  responseCrypto.reset();
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});
function clientWith(handler?: (input: string, init: RequestInit) => Promise<Response>) {
  const fetcher = vi.fn(async (input: RequestInfo | URL, init: RequestInit = {}) => {
    if (String(input).includes('response-crypto/init')) return fixtureInit();
    return handler
      ? handler(String(input), init)
      : fixtureEncrypted(String(input), init, { data: { title: '中文商品' } });
  });
  return {
    client: new ResponseCryptoClient({ origin: 'https://qingye.test', fetch: fetcher }),
    fetcher,
  };
}
async function gzip(value: string) {
  const stream = new Response(value).body!.pipeThrough(new CompressionStream('gzip'));
  return new Uint8Array(await new Response(stream).arrayBuffer());
}
describe('R24 公开JSON压缩', () => {
  it('有解压能力时明确声明gzip，先验证密文再限量解压', async () => {
    const data = {
      data: Array.from({ length: 80 }, () => ({
        title: '公开商品详情',
        description: '重复的公开说明',
      })),
    };
    const compressed = await gzip(JSON.stringify(data));
    const { client, fetcher } = clientWith((input, init) =>
      fixtureEncrypted(input, init, null, { contentEncoding: 'gzip', bytes: compressed }),
    );
    expect(await (await client.fetch('/api/v1/products')).json()).toEqual(data);
    expect(new Headers(fetcher.mock.calls[1][1]!.headers).get('X-QY-Accept-Encoding')).toBe('gzip');
  });
  it('无DecompressionStream时不声明gzip，仍用AES identity且拒绝意外压缩', async () => {
    vi.stubGlobal('DecompressionStream', undefined);
    const { client, fetcher } = clientWith();
    expect((await (await client.fetch('/api/v1/products')).json()).data.title).toBe('中文商品');
    expect(new Headers(fetcher.mock.calls[1][1]!.headers).has('X-QY-Accept-Encoding')).toBe(false);
    const invalid = clientWith((input, init) =>
      fixtureEncrypted(input, init, null, {
        contentEncoding: 'gzip',
        bytes: new Uint8Array([1, 2]),
      }),
    );
    await expect(invalid.client.fetch('/api/v1/products')).rejects.toThrow();
  });
  it('编码是AAD的一部分，identity改为gzip也不能通过验证', async () => {
    const { client } = clientWith(async (input, init) => {
      const response = await fixtureEncrypted(input, init, { data: 'public' });
      response.headers.set('X-QY-Content-Encoding', 'gzip');
      return response;
    });
    await expect(client.fetch('/api/v1/home')).rejects.toThrow();
  });
  it('压缩炸弹解压超过4MiB即中止，不处理超额JSON', async () => {
    const compressed = await gzip('a'.repeat(4 * 1024 * 1024 + 1));
    const { client } = clientWith((input, init) =>
      fixtureEncrypted(input, init, null, { contentEncoding: 'gzip', bytes: compressed }),
    );
    await expect(client.fetch('/api/v1/home')).rejects.toThrow('解压失败');
  });
  it.each([
    { path: '/api/v1/me', type: 'application/json', status: 200 },
    { path: '/api/v1/products/not-a-uuid', type: 'application/json', status: 200 },
    { path: '/api/v1/media/image-1/file', type: 'image/png', status: 200 },
    { path: '/api/v1/products', type: 'application/json', status: 409 },
  ])('私密/图片/错误响应不能进入公开压缩分支 %j', async ({ path, type, status }) => {
    const { client } = clientWith((input, init) =>
      fixtureEncrypted(input, init, null, {
        contentEncoding: 'gzip',
        contentType: type,
        status,
        bytes: new Uint8Array([1, 2]),
      }),
    );
    await expect(client.fetch(path)).rejects.toThrow();
  });
});
describe('R24 实际 WebCrypto 与 HTTP 传输', () => {
  it('仅明确标记并匹配请求的本地媒体GET可免AES，仍受字节和身份检查', async () => {
    const { client } = clientWith(async (_input, init) => fixtureLocalImage(init));
    const response = await client.fetch('/api/v1/media/local-1/file?grant=short');
    expect(response.headers.get('Content-Type')).toBe('image/png');
    expect(new Uint8Array(await response.arrayBuffer())).toEqual(
      new Uint8Array([137, 80, 78, 71, 0, 255]),
    );
    const old = await client.fetch('/api/v1/media/local-1/file');
    client.reset();
    await expect(old.arrayBuffer()).rejects.toMatchObject({ name: 'AbortError' });
  });
  it.each(['missing-marker', 'json', 'wrong-path', 'wrong-binding', 'post', 'gzip'])(
    '本地图片豁免拒绝 %s，不能放开明文业务接口',
    async (kind) => {
      const { client } = clientWith(async (_input, init) => {
        const response = fixtureLocalImage(
          init,
          kind === 'json' ? 'application/json' : 'image/png',
        );
        if (kind === 'missing-marker') response.headers.delete('X-QY-Response-Mode');
        if (kind === 'gzip') response.headers.set('X-QY-Content-Encoding', 'gzip');
        if (kind === 'wrong-binding') response.headers.set('X-QY-Context', 'another-context');
        return response;
      });
      await expect(
        client.fetch(kind === 'wrong-path' ? '/api/v1/me' : '/api/v1/media/local-1/file', {
          method: kind === 'post' ? 'POST' : 'GET',
        }),
      ).rejects.toThrow();
    },
  );
  it('并发合并初始化、每请求独立标识且业务正文可解密', async () => {
    const { client, fetcher } = clientWith();
    const results = await Promise.all(
      Array.from({ length: 20 }, () => client.fetch('/api/v1/products').then((r) => r.json())),
    );
    expect(results.every((value) => value.data.title === '中文商品')).toBe(true);
    expect(fetcher.mock.calls.filter(([url]) => String(url).includes('/init'))).toHaveLength(1);
    const requests = fetcher.mock.calls.filter(([url]) => !String(url).includes('/init'));
    expect(
      new Set(requests.map(([, init]) => new Headers(init!.headers).get('X-QY-Request-Id'))).size,
    ).toBe(20);
  });
  it('仅明确禁用能力允许旧正文，404与不完整能力不能降级', async () => {
    for (const response of [
      new Response('missing', { status: 404 }),
      new Response(JSON.stringify({ data: { enabled: false } })),
    ]) {
      const fetcher = vi.fn(async () => response);
      const client = new ResponseCryptoClient({ fetch: fetcher });
      await expect(client.fetch('/api/v1/products')).rejects.toThrow();
      expect(fetcher).toHaveBeenCalledTimes(1);
    }
    const fetcher = vi.fn(async (url) =>
      String(url).includes('/init')
        ? fixtureInit('public', { enabled: false, media_access_plan: false })
        : new Response('{"data":1}'),
    );
    const client = new ResponseCryptoClient({ fetch: fetcher });
    expect(await (await client.fetch('/api/v1/products')).json()).toEqual({ data: 1 });
  });
  it('启用后拒绝明文业务响应，不自动重放提交', async () => {
    const { client, fetcher } = clientWith(async () => new Response('{"data":{"saved":true}}'));
    await expect(
      client.fetch('/api/v1/me/products', { method: 'POST', body: '{"request_id":"stable"}' }),
    ).rejects.toThrow();
    expect(fetcher.mock.calls.filter(([url]) => String(url).includes('/me/products'))).toHaveLength(
      1,
    );
  });
  it.each([
    'X-QY-Context',
    'X-QY-Key-Id',
    'X-QY-Request-Id',
    'X-QY-Nonce',
    'X-QY-Content-Type',
    'X-QY-Protocol',
    'X-QY-Content-Encoding',
  ])('拒绝响应头篡改 %s', async (header) => {
    const { client } = clientWith(async (input, init) => {
      const response = await fixtureEncrypted(input, init, { secret: 'private' });
      response.headers.set(header, 'tampered');
      return response;
    });
    await expect(client.fetch('/api/v1/me')).rejects.toThrow();
  });
  it('拒绝密文改动和请求路径替换', async () => {
    const { client } = clientWith();
    const context = await client.prepare('/api/v1/me?view=one');
    const good = await fixtureEncrypted(
      '/api/v1/me?view=one',
      { headers: context.headers },
      { data: 1 },
    );
    const damaged = new Uint8Array(await good.arrayBuffer());
    damaged[0] ^= 1;
    await expect(
      client.decode(new Response(damaged, { headers: good.headers }), context),
    ).rejects.toThrow();
    const other = await fixtureEncrypted(
      '/api/v1/me?view=two',
      { headers: context.headers },
      { data: 1 },
    );
    await expect(client.decode(other, context)).rejects.toThrow();
  });
  it('保留真实错误HTTP状态和字段，二进制不经过Base64', async () => {
    const { client } = clientWith((input, init) =>
      fixtureEncrypted(
        input,
        init,
        { code: 'VERSION_CONFLICT', fields: { version: '已变更' } },
        { status: 409 },
      ),
    );
    const error = await client.fetch('/api/v1/products/a', { method: 'PUT' });
    expect(error.status).toBe(409);
    expect((await error.json()).fields.version).toBe('已变更');
    const binary = clientWith((input, init) =>
      fixtureEncrypted(input, init, null, {
        contentType: 'image/png',
        bytes: new Uint8Array([0, 255, 1, 2, 0]),
      }),
    );
    const image = await binary.client.fetch('/api/v1/media/a/file');
    expect(image.headers.get('Content-Type')).toBe('image/png');
    expect(new Uint8Array(await image.arrayBuffer())).toEqual(new Uint8Array([0, 255, 1, 2, 0]));
  });
  it('小时轮换合并刷新，在途旧key响应仍能校验', async () => {
    let now = 0,
      hour = 1;
    const fetcher = vi.fn(async () =>
      fixtureInit('public', {
        key_id: `hour-${hour}`,
        server_time: 1800000000 + (hour - 1) * 3600,
        rotate_at: 1800000000 + hour * 3600,
        retain_until: 1800000120 + hour * 3600,
      }),
    );
    const client = new ResponseCryptoClient({ now: () => now, fetch: fetcher });
    const old = await client.prepare('/api/v1/products');
    now = 3600000;
    hour = 2;
    await Promise.all(Array.from({ length: 30 }, () => client.ensure()));
    expect(fetcher).toHaveBeenCalledTimes(2);
    const response = await fixtureEncrypted(
      '/api/v1/products',
      { headers: old.headers },
      { data: 'old-flight' },
    );
    expect(await (await client.decode(response, old)).json()).toEqual({ data: 'old-flight' });
  });
  it('身份重置后旧初始化和旧响应不得回填', async () => {
    const { client } = clientWith();
    const context = await client.prepare('/api/v1/me');
    const response = await fixtureEncrypted(
      '/api/v1/me',
      { headers: context.headers },
      { data: 'old-user' },
    );
    client.reset();
    await expect(client.decode(response, context)).rejects.toMatchObject({ name: 'AbortError' });
    let finish!: (response: Response) => void;
    const late = new ResponseCryptoClient({
      fetch: () =>
        new Promise((resolve) => {
          finish = resolve;
        }),
    });
    const request = late.ensure();
    late.reset();
    finish(fixtureInit());
    await expect(request).rejects.toMatchObject({ name: 'AbortError' });
  });
  it('旧初始化结束不能清除新身份正在进行的初始化', async () => {
    const completions: Array<(response: Response) => void> = [];
    const fetcher = vi.fn(() => new Promise<Response>((resolve) => completions.push(resolve)));
    const client = new ResponseCryptoClient({ fetch: fetcher });
    const first = client.ensure();
    const rejected = expect(first).rejects.toMatchObject({ name: 'AbortError' });
    client.reset();
    const second = client.ensure();
    completions[0](fixtureInit());
    await rejected;
    const merged = client.ensure();
    expect(fetcher).toHaveBeenCalledTimes(2);
    completions[1](fixtureInit('public', { context_id: 'context-2' }));
    expect((await second).context_id).toBe('context-2');
    expect((await merged).context_id).toBe('context-2');
  });
  it('声明超出64MiB上限时拒绝读取响应流', async () => {
    const { client } = clientWith();
    const context = await client.prepare('/api/v1/media/a/file');
    const response = await fixtureEncrypted(
      '/api/v1/media/a/file',
      { headers: context.headers },
      { data: 1 },
    );
    response.headers.set('Content-Length', String(64 * 1024 * 1024 + 17));
    await expect(client.decode(response, context)).rejects.toThrow('响应过大');
  });
  it('协议拒绝只接受最小错误且不重发原写请求', async () => {
    const { client, fetcher } = clientWith(
      async () =>
        new Response('{"code":"CONTEXT_EXPIRED","message":"请重试"}', {
          status: 409,
          headers: { 'X-QY-Protocol-Error': '1' },
        }),
    );
    expect((await client.fetch('/api/v1/write', { method: 'POST' })).status).toBe(409);
    expect(fetcher).toHaveBeenCalledTimes(2);
    const unsafe = clientWith(
      async () =>
        new Response('{"code":"x","message":"x","data":{"private":true}}', {
          status: 409,
          headers: { 'X-QY-Protocol-Error': '1' },
        }),
    );
    await expect(unsafe.client.fetch('/api/v1/write', { method: 'POST' })).rejects.toThrow();
  });
  it('不会把解密请求头和Cookie发送到第三方', async () => {
    const { client, fetcher } = clientWith();
    await expect(client.fetch('https://other.test/api/v1/me')).rejects.toThrow();
    expect(fetcher).not.toHaveBeenCalled();
  });
  it('XHR非空错误响应同样解密，不能只凭状态丢掉密文', async () => {
    const { client } = clientWith();
    const context = await client.prepare('/api/v1/uploads/a/file', 'PUT');
    const encrypted = await fixtureEncrypted(
      '/api/v1/uploads/a/file',
      { method: 'PUT', headers: context.headers },
      { message: '上传被限制' },
      { status: 429 },
    );
    const xhr = {
      status: 429,
      response: await encrypted.arrayBuffer(),
      getAllResponseHeaders: () =>
        [...encrypted.headers.entries()].map(([k, v]) => `${k}: ${v}`).join('\r\n'),
    } as XMLHttpRequest;
    const response = await client.decodeXHR(xhr, context);
    expect(response.status).toBe(429);
    expect((await response.json()).message).toBe('上传被限制');
  });
});
describe('R24 跨标签身份变化', () => {
  it('首次匿名初始化使用同源锁串行，不在标签之间交换密钥', async () => {
    let chain = Promise.resolve();
    const lock = vi.fn((_name: string, _options: unknown, run: () => Promise<Response>) => {
      const result = chain.then(run);
      chain = result.then(() => {});
      return result;
    });
    vi.stubGlobal('navigator', { locks: { request: lock } });
    let active = 0,
      maximum = 0;
    const fetcher = async () => {
      active++;
      maximum = Math.max(maximum, active);
      await new Promise((resolve) => setTimeout(resolve, 1));
      active--;
      return fixtureInit();
    };
    const clients = Array.from({ length: 4 }, () => new ResponseCryptoClient({ fetch: fetcher }));
    await Promise.all(clients.map((client) => client.ensure()));
    expect(lock).toHaveBeenCalledTimes(4);
    expect(maximum).toBe(1);
    expect(lock.mock.calls.every(([name]) => name === 'qingye-response-init-v1')).toBe(true);
  });
  it('通知只携带身份变化信号，旧请求失效且业务订阅立即清理视图', async () => {
    const channels: Channel[] = [];
    const messages: unknown[] = [];
    class Channel {
      onmessage: ((event: { data: unknown }) => void) | null = null;
      constructor(_name: string) {
        channels.push(this);
      }
      postMessage(value: unknown) {
        messages.push(value);
        for (const channel of channels) if (channel !== this) channel.onmessage?.({ data: value });
      }
    }
    vi.stubGlobal('window', { location: new URL('https://qingye.test') });
    vi.stubGlobal('BroadcastChannel', Channel);
    const first = clientWith().client,
      second = clientWith().client;
    await first.ensure();
    const context = await second.prepare('/api/v1/me');
    const old = await fixtureEncrypted(
      '/api/v1/me',
      { headers: context.headers },
      { data: 'old-user' },
    );
    const cleanup = vi.fn();
    const unsubscribe = second.subscribeIdentity(cleanup);
    first.reset(true);
    expect(cleanup).toHaveBeenCalledTimes(1);
    expect(messages).toEqual([{ type: 'identity-changed' }]);
    await expect(second.decode(old, context)).rejects.toMatchObject({ name: 'AbortError' });
    unsubscribe();
    first.reset(true);
    expect(cleanup).toHaveBeenCalledTimes(1);
  });
  it('密码结果不明时可通知其他标签而不触发本页自动重新登录', async () => {
    const client = clientWith().client;
    const local = vi.fn();
    client.subscribeIdentity(local);
    client.reset(true, false);
    expect(local).not.toHaveBeenCalled();
  });
});

describe('R24 聊天下行加密', () => {
  it('校验通道、拒绝明文、重复帧和跨通道密文', async () => {
    const { client } = clientWith();
    await client.ensure();
    const config = await client.validateChat({
      enabled: true,
      context_id: 'context-1',
      channel_id: 'channel-1',
    });
    const seen = new Set<string>(),
      frame = await fixtureChat({ type: 'ready' });
    expect(await client.decodeChat(frame, config, seen)).toEqual({ type: 'ready' });
    await expect(client.decodeChat(frame, config, seen)).rejects.toThrow();
    await expect(client.decodeChat({ type: 'ready' }, config, seen)).rejects.toThrow();
    await expect(
      client.decodeChat(await fixtureChat({ type: 'ready' }, { channel: 'other' }), config, seen),
    ).rejects.toThrow();
    await expect(client.validateChat(undefined)).rejects.toThrow();
  });
  it('新小时chat key合并取回，不泄漏到另一上下文', async () => {
    const fetcher = vi.fn(async (url) =>
      fixtureInit('public', {
        key_id: String(url).includes('key_id=hour-2') ? 'hour-2' : 'hour-1',
      }),
    );
    const client = new ResponseCryptoClient({ fetch: fetcher });
    await client.ensure();
    const config = { enabled: true, context_id: 'context-1', channel_id: 'channel-1' };
    const frames = await Promise.all(
      ['b', 'c'].map((id) =>
        fixtureChat({ type: 'update' }, { keyId: 'hour-2', frameId: id.repeat(32) }),
      ),
    );
    await Promise.all(frames.map((frame) => client.decodeChat(frame, config, new Set())));
    expect(fetcher.mock.calls.filter(([url]) => String(url).includes('key_id='))).toHaveLength(1);
  });
});
describe('R24 双端业务API接线', () => {
  it('短信待补注册保留当前会话与表单proof，不发送身份变更事件', async () => {
    vi.stubGlobal('location', new URL('https://qingye.test'));
    const changed = vi.fn(),
      unsubscribe = responseCrypto.subscribeIdentity(changed);
    const fetcher = vi.fn(async (input: string, init: RequestInit = {}) =>
      input.includes('/response-crypto/init')
        ? fixtureInit()
        : fixtureEncrypted(input, init, {
            data: { status: 'registration_required', challenge_id: 'proof-1' },
          }),
    );
    vi.stubGlobal('fetch', fetcher);
    const { api } = await import('./api');
    const epoch = responseCrypto.epoch();
    expect(
      await api.smsLogin({ phone: '13800000000', code: '123456', challenge_id: 'proof-1' }),
    ).toEqual({ status: 'registration_required', challenge_id: 'proof-1' });
    expect(responseCrypto.epoch()).toBe(epoch);
    expect(changed).not.toHaveBeenCalled();
    await responseCrypto.ensure();
    expect(
      fetcher.mock.calls.filter(([url]) => url.includes('/response-crypto/init')),
    ).toHaveLength(1);
    unsubscribe();
  });
  it('确认登录后更新传输身份但不重挂本地表单，原refresh和跳转能够完成', async () => {
    vi.stubGlobal('location', new URL('https://qingye.test'));
    const changed = vi.fn(),
      unsubscribe = responseCrypto.subscribeIdentity(changed);
    vi.stubGlobal('fetch', async (input: string, init: RequestInit = {}) =>
      input.includes('/response-crypto/init')
        ? fixtureInit()
        : fixtureEncrypted(input, init, {
            data: {
              status: 'authenticated',
              user: { id: 'member-1', nickname: '会员', role: 'member' },
            },
          }),
    );
    const { api } = await import('./api');
    const epoch = responseCrypto.epoch();
    expect(
      (await api.smsLogin({ phone: '13800000000', code: '123456', challenge_id: 'proof-1' }))
        .status,
    ).toBe('authenticated');
    expect(responseCrypto.epoch()).toBe(epoch + 1);
    expect(changed).not.toHaveBeenCalled();
    unsubscribe();
  });
  it('实际用户API在解密失败后保留原业务幂等编号', async () => {
    vi.stubGlobal('location', new URL('https://qingye.test'));
    const writes: string[] = [];
    let fail = true;
    vi.stubGlobal('fetch', async (input: string, init: RequestInit = {}) => {
      if (input.includes('response-crypto/init')) return fixtureInit();
      writes.push(String(init.body));
      if (fail) {
        fail = false;
        return new Response('bad-response');
      }
      return fixtureEncrypted(input, init, { data: { id: 'saved' } });
    });
    const { idempotentRequest } = await import('./api');
    await expect(idempotentRequest('/me/products', { title: 'same-intent' })).rejects.toMatchObject(
      { code: 'RESPONSE_SECURITY' },
    );
    expect(writes).toHaveLength(1);
    await idempotentRequest('/me/products', { title: 'same-intent' });
    expect(writes[0]).toBe(writes[1]);
    expect(JSON.parse(writes[0]).request_id).toBeTruthy();
    expect(RESPONSE_PROTOCOL).toBe('qy-r24-aesgcm-v1');
  });
});
