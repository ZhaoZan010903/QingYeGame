import { useEffect, useRef, useState } from 'react';
import { ProtectedImage } from '../../../共享/MediaSecurity';
import type { AvatarLibraryItem } from '../../../共享/profile-types';
import { profileApi } from '../lib/profile-api';
import { chatApi } from '../lib/chat-api';

export function useProfileAvatar(identity: string, enabled: boolean) {
  const [url, setURL] = useState('');
  const [items, setItems] = useState<AvatarLibraryItem[]>([]);
  const [loading, setLoading] = useState(enabled);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  const [revision, setRevision] = useState(0);
  const [saved, setSaved] = useState('');
  const generation = useRef(0);
  const operation = useRef(false);
  useEffect(() => {
    const current = ++generation.current;
    setURL('');
    setItems([]);
    setBusy(false);
    operation.current = false;
    setError('');
    setSaved('');
    setLoading(enabled && !!identity);
    if (enabled && identity) {
      Promise.allSettled([chatApi.profile(), profileApi.avatars()])
        .then(([profile, library]) => {
          if (generation.current !== current) return;
          if (profile.status === 'fulfilled') setURL(profile.value.avatar_url);
          if (library.status === 'fulfilled') setItems(library.value);
          const failed = [profile, library].find((result) => result.status === 'rejected');
          if (failed?.status === 'rejected')
            setError(
              failed.reason instanceof Error ? failed.reason.message : '头像库加载失败，请重试。',
            );
        })
        .finally(() => {
          if (generation.current === current) setLoading(false);
        });
    }
    return () => {
      generation.current++;
    };
  }, [identity, enabled, revision]);
  async function change(item: AvatarLibraryItem) {
    if (
      !enabled ||
      !identity ||
      loading ||
      operation.current ||
      !items.some((row) => row.id === item.id)
    )
      return;
    const current = generation.current;
    operation.current = true;
    setBusy(true);
    setError('');
    setSaved('');
    try {
      const profile = await chatApi.profile(item.media_id);
      if (generation.current !== current) return;
      setURL(profile.avatar_url);
      setSaved('头像已更新。');
    } catch (e) {
      if (generation.current === current)
        setError(e instanceof Error ? e.message : '头像更新失败，请重试。');
    } finally {
      if (generation.current === current) {
        operation.current = false;
        setBusy(false);
      }
    }
  }
  return {
    url,
    items,
    loading,
    busy,
    error,
    saved,
    change,
    reload: () => setRevision((v) => v + 1),
  };
}
export function ProfileAvatar({ src, name }: { src: string; name: string }) {
  const [failed, setFailed] = useState('');
  return (
    <div className="profile-avatar" aria-hidden="true">
      {src && failed !== src ? (
        <ProtectedImage src={src} alt="" onError={() => setFailed(src)} />
      ) : (
        Array.from(name || '清叶')[0]
      )}
    </div>
  );
}
export function ProfileAvatarEditor({
  avatar,
  name,
  disabled,
  demo,
}: {
  avatar: ReturnType<typeof useProfileAvatar>;
  name: string;
  disabled: boolean;
  demo: boolean;
}) {
  const unavailable = demo || disabled || avatar.loading || avatar.busy;
  return (
    <div
      className="profile-avatar-library"
      aria-label="修改头像"
      aria-busy={avatar.busy || avatar.loading}
    >
      <div className="profile-avatar-editor">
        <ProfileAvatar src={avatar.url} name={name} />
        <div className="profile-avatar-description">
          <strong>我的头像</strong>
          <p>{demo ? '登录真实账户后可选择头像。' : '从头像库中选取，用于个人中心和聊天展示。'}</p>
          <small>
            {avatar.loading
              ? '正在加载头像库…'
              : avatar.busy
                ? '正在保存…'
                : '点击喜欢的头像即可保存。'}
          </small>
        </div>
      </div>
      {!demo && (
        <div className="profile-avatar-options" role="group" aria-label="头像库">
          {avatar.items.map((item) => (
            <button
              key={item.id}
              type="button"
              disabled={unavailable}
              aria-pressed={avatar.url === item.url}
              onClick={() => void avatar.change(item)}
              title={item.name}
            >
              <ProtectedImage src={item.url} alt="" loading="lazy" />
              <span>{item.name}</span>
            </button>
          ))}
        </div>
      )}
      {!demo && !avatar.loading && !avatar.error && !avatar.items.length && (
        <p className="profile-note">头像库暂时没有可选头像，请稍后再来。</p>
      )}
      {avatar.error && (
        <div className="profile-avatar-feedback">
          <p role="alert">{avatar.error}</p>
          <button type="button" disabled={unavailable} onClick={avatar.reload}>
            重新加载头像库
          </button>
        </div>
      )}
      <p className="profile-avatar-saved" role="status">
        {avatar.saved}
      </p>
    </div>
  );
}
