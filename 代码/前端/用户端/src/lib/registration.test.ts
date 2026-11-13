import { afterEach, expect, it, vi } from 'vitest';
afterEach(() => {
  vi.unstubAllEnvs();
  vi.unstubAllGlobals();
  vi.resetModules();
});
it('注册能力由服务端返回，未启用的短信场景保持关闭', async () => {
  vi.stubEnv('VITE_DATA_MODE', 'api');
  vi.stubEnv('VITE_SMS_ENABLED', 'true');
  const fetch = vi
    .fn()
    .mockResolvedValue(
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
