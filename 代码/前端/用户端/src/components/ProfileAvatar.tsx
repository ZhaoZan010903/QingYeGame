import { useEffect, useRef, useState } from 'react';
import { Camera } from 'lucide-react';
import { ProtectedImage } from '../../../共享/MediaSecurity';
import { uploadChatImage, validateChatImage } from '../../../共享/chat-upload';
import { request } from '../lib/api';
import { chatApi } from '../lib/chat-api';

export function useProfileAvatar(identity: string, enabled: boolean) {
  const [url, setURL] = useState('');
  const [loading, setLoading] = useState(enabled);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  const [progress, setProgress] = useState(0);
  const [revision, setRevision] = useState(0);
  const [saved, setSaved] = useState('');
  const generation = useRef(0);
  const operation = useRef<AbortController | null>(null);

  useEffect(() => {
    const current = ++generation.current;
    let active = true;
    setURL('');
    setBusy(false);
    setError('');
    setSaved('');
    setLoading(enabled && !!identity);
    if (enabled && identity) {
      chatApi
        .profile()
        .then((profile) => {
          if (active) setURL(profile.avatar_url);
        })
        .catch(() => {
          if (active) setError('头像资料加载失败，请重试。');
        })
        .finally(() => {
          if (active) setLoading(false);
        });
    }
    return () => {
      active = false;
      if (generation.current === current) generation.current++;
      operation.current?.abort();
      operation.current = null;
    };
  }, [identity, enabled, revision]);

  async function change(file?: File) {
    if (!enabled || !identity || loading || operation.current) return;
    setSaved('');
    if (file) {
      const invalid = validateChatImage(file, true);
      if (invalid) {
        setError(invalid);
        return;
      }
    }
    const current = generation.current;
    const controller = new AbortController();
    operation.current = controller;
    setBusy(true);
    setError('');
    setProgress(0);
    try {
      const id = file
        ? await uploadChatImage(
            request,
            file,
            { purpose: 'avatar' },
            controller.signal,
            (value) => {
              if (generation.current === current) setProgress(value);
            },
          )
        : '';
      if (generation.current !== current) return;
      const profile = await chatApi.profile(id);
      if (generation.current !== current) return;
      setURL(profile.avatar_url);
      setSaved(file ? '头像已更新。' : '已恢复默认头像。');
    } catch (e) {
      if (generation.current === current)
        setError(e instanceof Error ? e.message : '头像更新失败，请重试。');
    } finally {
      if (generation.current === current) {
        operation.current = null;
        setBusy(false);
      }
    }
  }
  return {
    url,
    loading,
    busy,
    error,
    progress,
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
  const input = useRef<HTMLInputElement>(null);
  const unavailable = demo || disabled || avatar.loading || avatar.busy;
  return (
    <div
      className="profile-avatar-editor"
      aria-label="修改头像"
      aria-busy={avatar.busy || avatar.loading}
    >
      <ProfileAvatar src={avatar.url} name={name} />
      <div className="profile-avatar-description">
        <strong>我的头像</strong>
        <p>{demo ? '登录真实账户后可更换头像。' : '用于个人中心和聊天展示，修改后自动保存。'}</p>
        <small>支持 JPG、PNG、WebP，大小不超过 2 MB。</small>
        <div className="profile-avatar-actions">
          <button
            className="button-outline"
            type="button"
            disabled={unavailable}
            onClick={() => input.current?.click()}
          >
            <Camera size={15} aria-hidden="true" />
            {avatar.loading
              ? '正在加载…'
              : avatar.busy
                ? avatar.progress > 0 && avatar.progress < 100
                  ? `正在上传 ${avatar.progress}%`
                  : '正在保存…'
                : '更换头像'}
          </button>
          {avatar.url && (
            <button
              className="profile-avatar-reset"
              type="button"
              disabled={unavailable}
              onClick={() => void avatar.change()}
            >
              恢复默认
            </button>
          )}
        </div>
        {avatar.error && (
          <div className="profile-avatar-feedback">
            <p role="alert">{avatar.error}</p>
            <button type="button" disabled={unavailable} onClick={avatar.reload}>
              重新加载头像
            </button>
          </div>
        )}
        <p className="profile-avatar-saved" role="status">
          {avatar.saved}
        </p>
      </div>
      <input
        ref={input}
        type="file"
        accept="image/jpeg,image/png,image/webp"
        aria-label="选择头像图片"
        hidden
        disabled={unavailable}
        onChange={(event) => {
          const file = event.target.files?.[0];
          event.target.value = '';
          if (file) void avatar.change(file);
        }}
      />
    </div>
  );
}
