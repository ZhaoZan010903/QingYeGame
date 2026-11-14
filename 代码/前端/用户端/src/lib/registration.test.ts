import { afterEach, expect, it, vi } from 'vitest';
afterEach(() => {
  vi.unstubAllEnvs();
  vi.unstubAllGlobals();
  vi.resetModules();
});
it('注册能力由服务端返回，未启用的短信场景保持关闭', async () => {
  vi.stubEnv('VITE_DATA_MODE', 'api');
  const fetch = vi.fn().mockResolvedValue(
    new Response(
      JSON.stringify({
        data: { registration_sms: true, sms_login: false, password_reset: false },
      }),
    ),
  );
  vi.stubGlobal('fetch', fetch);
  const { api } = await import('./api');
  expect(await api.authCapabilities()).toEqual({
    registration_sms: true,
    sms_login: false,
    password_reset: false,
  });
});
it('读取登录能力失败时保留错误，不把服务异常当作功能关闭', async () => {
  vi.stubEnv('VITE_DATA_MODE', 'api');
  const fetch = vi.fn().mockResolvedValue(
    new Response(
      JSON.stringify({
        code: 'SERVICE_UNAVAILABLE',
        message: '登录服务暂时不可用',
      }),
      { status: 503 },
    ),
  );
  vi.stubGlobal('fetch', fetch);
  const { api } = await import('./api');
  await expect(api.authCapabilities()).rejects.toMatchObject({
    status: 503,
    code: 'SERVICE_UNAVAILABLE',
    message: '登录服务暂时不可用',
  });
  expect(fetch).toHaveBeenCalledTimes(1);
});
it('短信发送结果未知时不自动重试，也不返回伪成功凭据', async () => {
  vi.stubEnv('VITE_DATA_MODE', 'api');
  const fetch = vi.fn().mockRejectedValue(new TypeError('connection lost'));
  vi.stubGlobal('fetch', fetch);
  const { api } = await import('./api');
  await expect(
    api.sms('13800000000', 'register', 'challenge', 'verified-token'),
  ).rejects.toMatchObject({ code: 'NETWORK_ERROR' });
  expect(fetch).toHaveBeenCalledTimes(1);
  expect(JSON.parse(fetch.mock.calls[0][1].body)).toEqual({
    phone: '13800000000',
    purpose: 'register',
    challenge_id: 'challenge',
    token: 'verified-token',
  });
});
it('演示模式不会调用真实短信接口', async () => {
  vi.stubEnv('VITE_DATA_MODE', 'demo');
  const fetch = vi.fn();
  vi.stubGlobal('fetch', fetch);
  const { api } = await import('./api');
  expect((await api.authCapabilities()).registration_sms).toBe(false);
  await expect(api.smsChallenge('13800000000')).rejects.toMatchObject({ code: 'DEMO_ONLY' });
  expect(fetch).not.toHaveBeenCalled();
});
it('短信登录使用独立用途，新手机号返回待注册状态', async () => {
  vi.stubEnv('VITE_DATA_MODE', 'api');
  const fetch = vi
    .fn()
    .mockResolvedValueOnce(
      new Response(
        JSON.stringify({
          data: { challenge_id: 'login-proof', site_key: 'test', action: 'sms_send' },
        }),
      ),
    )
    .mockResolvedValueOnce(
      new Response(
        JSON.stringify({ data: { status: 'registration_required', challenge_id: 'login-proof' } }),
      ),
    );
  vi.stubGlobal('fetch', fetch);
  const { api } = await import('./api');
  await api.smsChallenge('13800000000', 'login');
  expect(JSON.parse(fetch.mock.calls[0][1].body).purpose).toBe('login');
  expect(
    await api.smsLogin({ phone: '13800000000', code: '123456', challenge_id: 'login-proof' }),
  ).toEqual({ status: 'registration_required', challenge_id: 'login-proof' });
  expect(JSON.parse(fetch.mock.calls[1][1].body).method).toBe('sms');
});
it('短信登录成功才转换会员信息，不能把待注册响应当作会员', async () => {
  vi.stubEnv('VITE_DATA_MODE', 'api');
  const fetch = vi.fn().mockResolvedValue(
    new Response(
      JSON.stringify({
        data: {
          status: 'authenticated',
          user: { id: 'member', nickname: '清叶会员', role: 'member', enabled: true },
        },
      }),
    ),
  );
  vi.stubGlobal('fetch', fetch);
  const { api } = await import('./api');
  const result = await api.smsLogin({
    phone: '13800000000',
    code: '123456',
    challenge_id: 'proof',
  });
  expect(result.status).toBe('authenticated');
  if (result.status === 'authenticated') expect(result.user.id).toBe('member');
});
it('待注册凭据不匹配时拒绝异常响应，不自动重发验证码', async () => {
  vi.stubEnv('VITE_DATA_MODE', 'api');
  const fetch = vi
    .fn()
    .mockResolvedValue(
      new Response(
        JSON.stringify({ data: { status: 'registration_required', challenge_id: 'other-proof' } }),
      ),
    );
  vi.stubGlobal('fetch', fetch);
  const { api } = await import('./api');
  await expect(
    api.smsLogin({ phone: '13800000000', code: '123456', challenge_id: 'proof' }),
  ).rejects.toMatchObject({ code: 'INVALID_AUTH_RESPONSE' });
  expect(fetch).toHaveBeenCalledTimes(1);
});
