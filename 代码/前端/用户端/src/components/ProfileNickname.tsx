import { useEffect, useRef, useState, type FormEvent } from 'react';
import { profileApi } from '../lib/profile-api';
import { profileDate, type MemberProfile } from '../../../共享/profile-types';

export function nicknameValidation(next: string, current: string): string {
  if (!next || Array.from(next).length > 30) return '昵称需为 1 至 30 个字符。';
  return next === current ? '请输入与当前昵称不同的新昵称。' : '';
}

export function NicknameStatus({ profile }: { profile: MemberProfile }) {
  const request = profile.nickname_request;
  return (
    <div className="nickname-status" role="status">
      {request?.status === 'pending' ? (
        <p>「{request.nickname}」正在等待管理员审核，通过前仍展示当前昵称。</p>
      ) : request?.status === 'rejected' ? (
        <p>
          「{request.nickname}」未通过审核。{request.review_note || '请调整昵称后重新提交。'}
        </p>
      ) : request?.status === 'approved' ? (
        <p>昵称「{request.nickname}」已通过审核。</p>
      ) : null}
      {!profile.can_request_nickname &&
        request?.status !== 'pending' &&
        profile.next_nickname_change_at && (
          <p>下次可申请修改：{profileDate(profile.next_nickname_change_at)}（北京时间）</p>
        )}
    </div>
  );
}

export function ProfileNickname({
  identity,
  name,
  phone,
  demo,
  disabled,
  refreshSession,
}: {
  identity: string;
  name: string;
  phone: string;
  demo: boolean;
  disabled: boolean;
  refreshSession: () => Promise<void>;
}) {
  const [profile, setProfile] = useState<MemberProfile | null>(null);
  const [nickname, setNickname] = useState(name);
  const [loading, setLoading] = useState(!demo);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  const [notice, setNotice] = useState('');
  const [revision, setRevision] = useState(0);
  const generation = useRef(0);
  const operation = useRef(false);
  useEffect(() => {
    const current = ++generation.current;
    setProfile(null);
    setNickname(name);
    setError('');
    setNotice('');
    setBusy(false);
    operation.current = false;
    setLoading(!demo);
    if (!demo)
      profileApi
        .profile()
        .then((value) => {
          if (generation.current === current) {
            setProfile(value);
            setNickname(value.nickname);
            if (value.nickname !== name) void refreshSession();
          }
        })
        .catch((e) => {
          if (generation.current === current)
            setError(e instanceof Error ? e.message : '昵称资料加载失败。');
        })
        .finally(() => {
          if (generation.current === current) setLoading(false);
        });
    return () => {
      generation.current++;
    };
  }, [identity, name, demo, revision, refreshSession]);
  async function submit(event: FormEvent) {
    event.preventDefault();
    if (operation.current || !profile?.can_request_nickname || demo || disabled) return;
    const next = nickname.trim();
    const invalid = nicknameValidation(next, profile.nickname);
    if (invalid) {
      setError(invalid);
      return;
    }
    operation.current = true;
    setBusy(true);
    setError('');
    setNotice('');
    const current = generation.current;
    try {
      await profileApi.requestNickname(next);
      if (generation.current !== current) return;
      // A successful write stays locked even if the following read fails.
      setProfile((old) => (old ? { ...old, can_request_nickname: false } : old));
      setNotice('昵称申请已提交，等待管理员审核。');
      const value = await profileApi.profile();
      if (generation.current === current) setProfile(value);
    } catch (e) {
      if (generation.current === current)
        setError(e instanceof Error ? e.message : '提交失败，请重试。');
    } finally {
      if (generation.current === current) {
        operation.current = false;
        setBusy(false);
      }
    }
  }
  const unavailable = demo || disabled || loading || busy || !profile?.can_request_nickname;
  return (
    <form className="member-form profile-form" onSubmit={submit} aria-busy={busy || loading}>
      <label className="form-field">
        昵称
        <input
          value={nickname}
          onChange={(e) => setNickname(e.target.value)}
          maxLength={60}
          required
          placeholder="怎样称呼你"
          disabled={unavailable}
        />
        <small>最多 30 个字符。修改需管理员审核，通过后 30 天可再次申请。</small>
      </label>
      <label className="form-field">
        手机号
        <input value={demo ? '演示账户 · 未绑定手机号' : phone || '未绑定手机号'} disabled />
        <small>
          {demo ? '演示体验无需绑定真实手机号。' : '如需更换绑定手机号，请联系平台客服。'}
        </small>
      </label>
      {profile && <NicknameStatus profile={profile} />}
      {profile && (
        <button
          className="text-button nickname-refresh"
          type="button"
          disabled={busy || loading}
          onClick={() => setRevision((value) => value + 1)}
        >
          刷新审核状态
        </button>
      )}
      {loading && <p role="status">正在读取昵称状态…</p>}
      {error && (
        <div className="profile-avatar-feedback">
          <p role="alert">{error}</p>
          <button type="button" disabled={busy} onClick={() => setRevision((v) => v + 1)}>
            重新加载昵称状态
          </button>
        </div>
      )}
      <div className="profile-form-footer">
        <p role="status">
          {notice ||
            (demo
              ? '登录真实账户后可申请修改昵称。'
              : '审核通过后，新昵称将用于个人中心和聊天展示。')}
        </p>
        <button type="submit" className="button-white" disabled={unavailable}>
          {busy ? '正在提交…' : '提交昵称审核'}
        </button>
      </div>
    </form>
  );
}
