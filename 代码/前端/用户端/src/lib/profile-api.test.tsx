import { afterEach, describe, expect, it, vi } from 'vitest';
import { renderToStaticMarkup } from 'react-dom/server';
import { NicknameStatus, nicknameValidation } from '../components/ProfileNickname';
import { ProfileAvatarEditor } from '../components/ProfileAvatar';
import type { MemberProfile } from '../../../共享/profile-types';

afterEach(() => {
  vi.unstubAllGlobals();
  vi.unstubAllEnvs();
  vi.resetModules();
});
const response = (data: unknown, status = 200) =>
  new Response(JSON.stringify(data), { status, headers: { 'Content-Type': 'application/json' } });
const profile: MemberProfile = {
  nickname: '现有昵称',
  nickname_request: null,
  next_nickname_change_at: null,
  can_request_nickname: true,
};

describe('昵称申请与头像库', () => {
  it('昵称按 Unicode 字符计数，与后端保持30字边界', () => {
    expect(nicknameValidation('叶'.repeat(30), '原昵称')).toBe('');
    expect(nicknameValidation('🌿'.repeat(30), '原昵称')).toBe('');
    expect(nicknameValidation('🌿'.repeat(31), '原昵称')).toContain('30');
    expect(nicknameValidation('原昵称', '原昵称')).toContain('不同');
  });
  it('提交昵称使用已登录的会员接口，服务端冲突不会伪装成保存成功', async () => {
    vi.stubEnv('VITE_DATA_MODE', 'api');
    const fetch = vi
      .fn()
      .mockResolvedValue(response({ code: 'NICKNAME_PENDING', message: '已有待审核昵称' }, 409));
    vi.stubGlobal('fetch', fetch);
    const { profileApi } = await import('./profile-api');
    await expect(profileApi.requestNickname('新昵称')).rejects.toMatchObject({
      status: 409,
      message: '已有待审核昵称',
    });
    expect(fetch.mock.calls[0][0]).toBe('/api/v1/me');
    expect(fetch.mock.calls[0][1]).toMatchObject({
      method: 'PATCH',
      credentials: 'include',
      body: JSON.stringify({ nickname: '新昵称' }),
    });
  });
  it('保留后端审核状态与下次申请日期，不自行视为立即改名', async () => {
    const pending = {
      ...profile,
      can_request_nickname: false,
      nickname_request: {
        id: 'request',
        nickname: '新昵称',
        status: 'pending',
        review_note: '',
        created_at: '2026-11-13T10:00:00Z',
        reviewed_at: null,
      },
    };
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(response({ data: pending })));
    const { profileApi } = await import('./profile-api');
    expect(await profileApi.profile()).toEqual(pending);
    const html = renderToStaticMarkup(<NicknameStatus profile={pending as MemberProfile} />);
    expect(html).toContain('等待管理员审核');
    expect(html).toContain('通过前仍展示当前昵称');
    expect(html).not.toContain('已通过审核');
  });
  it('驳回原因按文本展示，冷却时间明确为北京时间', () => {
    const rejected = {
      ...profile,
      nickname_request: {
        id: 'request',
        nickname: '新昵称',
        status: 'rejected' as const,
        review_note: '<script>调整昵称</script>',
        created_at: '',
        reviewed_at: null,
      },
    };
    const rejectedHTML = renderToStaticMarkup(<NicknameStatus profile={rejected} />);
    expect(rejectedHTML).toContain('未通过审核');
    expect(rejectedHTML).toContain('&lt;script&gt;');
    expect(rejectedHTML).not.toContain('<script>');
    const cooldown = renderToStaticMarkup(
      <NicknameStatus
        profile={{
          ...profile,
          can_request_nickname: false,
          next_nickname_change_at: '2026-12-13T01:00:00Z',
        }}
      />,
    );
    expect(cooldown).toContain('09:00');
    expect(cooldown).toContain('北京时间');
  });
  it('头像编辑器只提供库内按钮，失败保留重试入口，没有任意文件上传入口', () => {
    const html = renderToStaticMarkup(
      <ProfileAvatarEditor
        name="会员"
        disabled={false}
        demo={false}
        avatar={{
          url: '/api/v1/media/one',
          items: [
            {
              id: 'a',
              media_id: 'one',
              name: '卡通猫',
              url: '/api/v1/media/one',
              sort_order: 0,
              enabled: true,
              version: 1,
            },
          ],
          loading: false,
          busy: false,
          error: '头像已停用，请重新选择',
          saved: '',
          change: async () => {},
          reload: () => {},
        }}
      />,
    );
    expect(html).toContain('卡通猫');
    expect(html).toContain('aria-pressed="true"');
    expect(html).toContain('重新加载头像库');
    expect(html).not.toContain('type="file"');
  });
});
