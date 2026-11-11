import { useState } from 'react';
import { useSession } from '../context';
import { request } from '../lib/api';
import { useLoad } from '../lib-use';
import './recommendation-preferences.css';

interface Profile {
  enabled: boolean;
  sample_count: number;
  categories: { id: string; name: string; weight: number }[];
}
export function RecommendationPreferences({
  compact = false,
  onChange,
}: {
  compact?: boolean;
  onChange?: () => void;
}) {
  const { user } = useSession();
  const result = useLoad(
    async () => (await request('/recommendations/profile')).data as Profile,
    `interests:${user?.id ?? 'visitor'}`,
  );
  const [busy, setBusy] = useState(false),
    [error, setError] = useState(''),
    [notice, setNotice] = useState('');
  async function update(body: { enabled?: boolean; reset?: boolean }) {
    setBusy(true);
    setError('');
    setNotice('');
    try {
      await request('/recommendations/profile', 'PATCH', body);
      result.retry();
      onChange?.();
      setNotice(
        body.reset
          ? '推荐偏好已重置，将从后续浏览重新整理。'
          : body.enabled
            ? '已开启个性化推荐。'
            : '已关闭个性化推荐。',
      );
    } catch (e) {
      setError(e instanceof Error ? e.message : '保存失败，请重试。');
    } finally {
      setBusy(false);
    }
  }
  return (
    <details
      className={`recommendation-preferences ${compact ? '' : 'panel'}`}
      open={compact ? undefined : true}
    >
      <summary>{compact ? '推荐偏好' : '我的推荐偏好'}</summary>
      <div className="recommendation-preferences-body">
        <p>仅根据本站最近 30 天的账号浏览整理；登录后跟随账号，未登录时保存在当前浏览器身份下。</p>
        {result.loading ? (
          <p>正在读取偏好…</p>
        ) : result.error ? (
          <p role="alert">
            偏好读取失败。<button onClick={result.retry}>重试</button>
          </p>
        ) : (
          result.data && (
            <>
              <p>
                {result.data.enabled
                  ? result.data.categories.length
                    ? `常看的分类：${result.data.categories
                        .slice(0, 3)
                        .map((c) => c.name)
                        .join('、')}`
                    : '还没有形成浏览偏好。'
                  : '个性化推荐已关闭，推荐区将展示热门账号。'}
              </p>
              <div className="recommendation-preferences-actions">
                <button
                  className="button-outline"
                  disabled={busy}
                  onClick={() => void update({ enabled: !result.data!.enabled })}
                >
                  {result.data.enabled ? '关闭个性化推荐' : '开启个性化推荐'}
                </button>
                <button
                  className="text-button"
                  disabled={busy || !result.data.sample_count}
                  onClick={() => void update({ reset: true })}
                >
                  重置推荐偏好
                </button>
              </div>
              <small>重置只影响推荐，不会删除收藏或最近浏览列表；热门榜的去重统计仍保留。</small>
            </>
          )
        )}
        {error && <p role="alert">{error}</p>}
        {notice && <p role="status">{notice}</p>}
      </div>
    </details>
  );
}
