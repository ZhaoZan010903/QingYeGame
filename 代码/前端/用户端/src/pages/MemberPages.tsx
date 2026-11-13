import { ProtectedImage } from '../../../共享/MediaSecurity';
import { canDeleteDraft } from '../lib/publish-dictionaries';
import { publicationInterrupted } from '../../../共享/upload-progress';
import { useCallback, useEffect, useState } from 'react';
import { Link, NavLink, useLocation, useNavigate } from 'react-router-dom';
import {
  ArrowUpRight,
  Heart,
  History,
  LayoutGrid,
  LogOut,
  MessageCircle,
  Plus,
  Settings2,
  ShieldCheck,
} from 'lucide-react';
import { api, isDemo, ApiError } from '../lib/api';
import type { Product } from '../types';
import { useSession, useUi } from '../context';
import { RecommendationPreferences } from '../components/RecommendationPreferences';
import { ProfileAvatar, ProfileAvatarEditor, useProfileAvatar } from '../components/ProfileAvatar';
import { ProfileNickname } from '../components/ProfileNickname';
import '../member.css';
import '../profile-library.css';

export const listingStatus: Record<Product['status'], string> = {
  draft: '草稿',
  pending: '审核中',
  publishing: '上架中',
  published: '已展示',
  rejected: '退回修改',
  rejected_final: '未通过',
  offline: '已下架',
  sold: '已售出',
  rented: '租用中',
};
const message = (e: unknown) => (e instanceof Error ? e.message : '操作失败，请重试。');
const units = { account: ' 元', hour: ' 元/小时', day: ' 元/日', week: ' 元/周', month: ' 元/月' };
const memberLinks = [
  { to: '/me', label: '账户概览', icon: LayoutGrid },
  {
    to: '/me/favorites',
    label: '我的收藏',
    icon: Heart,
    description: '心动的账号，留待下次相见。',
  },
  {
    to: '/me/listings',
    label: '我的发布',
    icon: Plus,
    description: '整理草稿，查看发布与审核进度。',
  },
  {
    to: '/me/history',
    label: '最近浏览',
    icon: History,
    description: '回到看过的账号，继续挑选。',
  },
];
function MemberArt({ product }: { product: Product }) {
  const first = product.images[0];
  const [failed, setFailed] = useState(false);
  return (
    <div
      className={`member-item-art ${!first?.url && isDemo ? `asset-art art-${first?.art ?? product.art}` : ''}`}
    >
      {first?.url && !failed ? (
        <ProtectedImage
          src={first.thumbnailUrl ?? first.url}
          alt={first.alt || product.title}
          loading="lazy"
          onError={() => setFailed(true)}
        />
      ) : (
        (!isDemo || failed) && <span className="image-unavailable">图片暂不可用</span>
      )}
    </div>
  );
}
function Empty({
  title,
  text,
  action = '去浏览账号',
  href = '/accounts',
}: {
  title: string;
  text: string;
  action?: string;
  href?: string;
}) {
  return (
    <div className="member-empty">
      <span className="empty-symbol" aria-hidden="true">
        ◇
      </span>
      <h3>{title}</h3>
      <p>{text}</p>
      <Link className="button-white" to={href}>
        {action} <span aria-hidden="true">↗</span>
      </Link>
    </div>
  );
}

export function MemberPage() {
  const { pathname } = useLocation();
  const navigate = useNavigate();
  const { user, refresh } = useSession();
  const { notify, contact } = useUi();
  const tab = pathname.endsWith('/favorites')
    ? 'favorites'
    : pathname.endsWith('/listings')
      ? 'listings'
      : pathname.endsWith('/history')
        ? 'history'
        : 'profile';
  const [items, setItems] = useState<Product[]>([]);
  const [loading, setLoading] = useState(false);
  const [busy, setBusy] = useState('');
  const avatar = useProfileAvatar(user?.id || '', !isDemo);
  const [error, setError] = useState('');
  const [status, setStatus] = useState('all');
  const [reload, setReload] = useState(0);
  const phone = user?.phone?.replace(/^(\d{3})\d{4}(\d{4})$/, '$1 ···· $2');
  const readHistory = useCallback(async () => {
    let ids: string[] = [];
    try {
      const stored = JSON.parse(localStorage.getItem('qingye-history') || '[]');
      if (Array.isArray(stored)) ids = stored;
    } catch {
      /* An unavailable device store has an empty history. */
    }
    const settled = await Promise.allSettled(
      ids.filter((id) => typeof id === 'string').map((id) => api.product(id)),
    );
    const failed = settled.find(
      (result) =>
        result.status === 'rejected' &&
        !(result.reason instanceof ApiError && [404, 410].includes(result.reason.status)),
    );
    if (failed?.status === 'rejected') throw failed.reason;
    return settled.flatMap((result) => (result.status === 'fulfilled' ? [result.value] : []));
  }, []);
  useEffect(() => {
    let active = true;
    setError('');
    setItems([]);
    if (tab === 'profile') return;
    setLoading(true);
    const request =
      tab === 'favorites'
        ? api.favorites()
        : tab === 'listings'
          ? api.myProducts(status === 'all' ? undefined : status)
          : readHistory();
    request
      .then((data) => {
        if (active) setItems(data);
      })
      .catch((e) => {
        if (active) setError(message(e));
      })
      .finally(() => {
        if (active) setLoading(false);
      });
    return () => {
      active = false;
    };
  }, [tab, status, reload, readHistory]);
  async function logout() {
    setBusy('logout');
    setError('');
    try {
      await api.logout();
      await refresh();
      navigate('/');
      notify('已退出登录。');
    } catch (e) {
      setError(message(e));
    } finally {
      setBusy('');
    }
  }
  async function removeFavorite(id: string) {
    setBusy(id);
    setError('');
    try {
      await api.favorite(id, false);
      setItems((previous) => previous.filter((item) => item.id !== id));
      notify('已取消收藏。');
    } catch (e) {
      setError(message(e));
    } finally {
      setBusy('');
    }
  }
  async function withdraw(product: Product) {
    const verb = product.status === 'pending' ? '撤回审核' : '下架';
    if (
      !window.confirm(
        `确认${verb}「${product.title}」？${product.status === 'published' ? '下架后将不再公开展示。' : '撤回后可继续编辑草稿。'}`,
      )
    )
      return;
    setBusy(product.id);
    setError('');
    try {
      await api.withdrawProduct(product.id, product.version);
      setReload((v) => v + 1);
      notify(isDemo ? `演示记录已${verb}。` : `已${verb}。`);
    } catch (e) {
      setError(message(e));
    } finally {
      setBusy('');
    }
  }
  async function deleteDraft(product: Product) {
    if (!window.confirm('删除这份草稿？删除后无法继续编辑。')) return;
    setBusy(product.id);
    setError('');
    try {
      await api.deleteDraft(product.id, product.version);
      setItems((rows) => rows.filter((row) => row.id !== product.id));
      notify('草稿已删除。');
    } catch (e) {
      setError(message(e));
    } finally {
      setBusy('');
    }
  }
  function clearHistory() {
    try {
      localStorage.removeItem('qingye-history');
      setItems([]);
      notify('已清空此设备的浏览记录。');
    } catch {
      setError('浏览器不允许清除本地存储。');
    }
  }
  return (
    <section className="member-shell page-width mx-auto" aria-label="我的清叶">
      <div className="member-header">
        <div>
          <h1>我的清叶</h1>
          <p>收藏心动，打理你的每一份热爱。</p>
        </div>
      </div>
      <section className="member-identity" aria-label="账户信息">
        <div className="member-identity-person">
          <ProfileAvatar src={avatar.url} name={user?.nickname || '清叶'} />
          <div>
            <h2>{user?.nickname || '清叶会员'}</h2>
            <p>{isDemo ? '演示账户 · 仅在当前设备保存' : phone || '未绑定手机号'}</p>
          </div>
        </div>
        <div className="member-identity-actions">
          <Link className="button-white" to="/publish">
            <Plus size={16} aria-hidden="true" />
            发布账号
          </Link>
          <button className="member-logout" onClick={logout} disabled={!!busy || avatar.busy}>
            <LogOut size={16} aria-hidden="true" />
            {busy === 'logout' ? '正在退出…' : '退出登录'}
          </button>
        </div>
      </section>
      <div className="member-layout">
        <nav className="member-nav" aria-label="个人中心导航">
          {memberLinks.map(({ to, icon: Icon, label }) => (
            <NavLink key={to} to={to} end>
              <Icon size={17} aria-hidden="true" />
              <span>{label}</span>
            </NavLink>
          ))}
        </nav>
        <section
          className="member-content"
          aria-label={
            {
              profile: '账户概览',
              favorites: '我的收藏',
              listings: '我的发布',
              history: '最近浏览',
            }[tab]
          }
        >
          {error && (
            <div className="member-error">
              <p role="alert" className="form-error">
                {error}
              </p>
              {tab !== 'profile' && (
                <button className="button-outline" onClick={() => setReload((v) => v + 1)}>
                  重新加载
                </button>
              )}
            </div>
          )}
          {tab === 'profile' ? (
            <>
              <div className="member-shortcuts" aria-label="我的账号与收藏">
                {memberLinks.slice(1).map(({ to, icon: Icon, label, description }) => (
                  <Link className="member-shortcut" to={to} key={to}>
                    <span className="member-shortcut-icon">
                      <Icon size={21} aria-hidden="true" />
                    </span>
                    <div>
                      <h2>{label}</h2>
                      <p>{description}</p>
                    </div>
                    <ArrowUpRight className="member-shortcut-arrow" size={18} aria-hidden="true" />
                  </Link>
                ))}
              </div>
              <section className="panel profile-panel" aria-labelledby="profile-heading">
                <div className="profile-panel-heading">
                  <div>
                    <h2 id="profile-heading">基本资料</h2>
                    <p>让这里更像你。</p>
                  </div>
                  <Settings2 size={20} aria-hidden="true" />
                </div>
                <ProfileAvatarEditor
                  avatar={avatar}
                  name={user?.nickname || '清叶'}
                  disabled={!!busy}
                  demo={isDemo}
                />
                <ProfileNickname
                  identity={user?.id || ''}
                  name={user?.nickname || ''}
                  phone={phone || ''}
                  demo={isDemo}
                  disabled={!!busy || avatar.busy}
                  refreshSession={refresh}
                />
              </section>
              {!isDemo && <RecommendationPreferences />}
              <div className="member-help-row">
                <Link to="/forgot-password?returnTo=%2Fme">
                  <ShieldCheck size={19} aria-hidden="true" />
                  <span>
                    账户安全<small>验证手机号并重设密码</small>
                  </span>
                  <ArrowUpRight size={16} aria-hidden="true" />
                </Link>
                <button onClick={() => contact(undefined, 'general')}>
                  <MessageCircle size={19} aria-hidden="true" />
                  <span>
                    需要帮助？<small>联系平台客服</small>
                  </span>
                  <ArrowUpRight size={16} aria-hidden="true" />
                </button>
              </div>
            </>
          ) : (
            <>
              <div className="member-toolbar">
                <div className="member-section-heading">
                  <h2>
                    {{ favorites: '我的收藏', listings: '我的发布', history: '最近浏览' }[tab]}
                  </h2>
                  <p>
                    {
                      {
                        favorites: '心动的珍藏，都在这里。',
                        listings: '从一份草稿，到下一次相遇。',
                        history: '上一次心动，值得再看一眼。',
                      }[tab]
                    }
                  </p>
                </div>
                {tab === 'listings' ? (
                  <div className="member-toolbar" style={{ marginBottom: 0 }}>
                    <select
                      className="member-select"
                      aria-label="按发布状态筛选"
                      value={status}
                      onChange={(e) => setStatus(e.target.value)}
                    >
                      <option value="all">全部状态</option>
                      {Object.entries(listingStatus).map(([value, name]) => (
                        <option key={value} value={value}>
                          {name}
                        </option>
                      ))}
                    </select>
                    <Link className="text-button" to="/publish">
                      ＋ 发布账号
                    </Link>
                  </div>
                ) : (
                  tab === 'history' && (
                    <button className="text-button" disabled={!items.length} onClick={clearHistory}>
                      清空记录
                    </button>
                  )
                )}
              </div>
              {tab === 'history' && (
                <p className="profile-note" style={{ margin: '0 0 20px' }}>
                  仅记录此设备访问过的公开商品，失效商品不再展示。
                </p>
              )}
              {loading ? (
                <div className="member-loading" role="status">
                  正在读取你的
                  {tab === 'listings' ? '发布' : tab === 'favorites' ? '收藏' : '浏览记录'}…
                </div>
              ) : !error && !items.length ? (
                <Empty
                  title={
                    tab === 'favorites'
                      ? '还没有收藏的账号'
                      : tab === 'listings'
                        ? '这里还没有发布记录'
                        : '还没有浏览记录'
                  }
                  text={
                    tab === 'favorites'
                      ? '遇到喜欢的账号，点击收藏，方便下次回来查看。'
                      : tab === 'listings'
                        ? '把账号资料整理成草稿，再提交审核。'
                        : '去探索账号，最近看过的珍藏会出现在这里。'
                  }
                  href={tab === 'listings' ? '/publish' : '/accounts'}
                  action={tab === 'listings' ? '发布第一个账号' : '去浏览账号'}
                />
              ) : (
                <div className="member-list">
                  {items.map((product) => (
                    <article className="member-item" key={product.id}>
                      {product.status === 'published' ? (
                        <Link to={`/accounts/${product.id}`} aria-label={`查看${product.title}`}>
                          <MemberArt product={product} />
                        </Link>
                      ) : (
                        <MemberArt product={product} />
                      )}
                      <div className="member-item-body">
                        <span className="member-item-id mono">{product.code || product.id}</span>
                        <h3>
                          {product.status === 'published' ? (
                            <Link to={`/accounts/${product.id}`}>{product.title}</Link>
                          ) : (
                            product.title || '未命名草稿'
                          )}
                        </h3>
                        <p>
                          {product.server || '区服未填写'} ·{' '}
                          {product.mode === 'sale' ? '出售' : '租赁'} ·{' '}
                          <span className="mono">
                            {product.unit === 'unknown'
                              ? `${product.legacyPrice ?? '原站报价'} · 单位待核对`
                              : product.price == null
                                ? '联系客服报价'
                                : `¥ ${product.price.toLocaleString()}${units[product.unit]}`}
                          </span>
                        </p>
                        {tab === 'listings' &&
                          product.status === 'draft' &&
                          product.draftExpiresAt && (
                            <p className="draft-expiry">
                              有效期至{' '}
                              {new Date(product.draftExpiresAt).toLocaleString('zh-CN', {
                                month: 'numeric',
                                day: 'numeric',
                                hour: '2-digit',
                                minute: '2-digit',
                              })}
                            </p>
                          )}
                        {product.reviewNote && tab === 'listings' && (
                          <p className="review-note">审核意见：{product.reviewNote}</p>
                        )}
                      </div>
                      <div className="member-item-actions">
                        {tab === 'listings' ? (
                          <>
                            <span className={`status-badge status-${product.status}`}>
                              {publicationInterrupted(
                                product.status,
                                product.publicationInterrupted,
                              )
                                ? '上架已中止'
                                : listingStatus[product.status]}
                            </span>
                            {!isDemo &&
                            !['publishing', 'rejected_final'].includes(product.status) ? (
                              <Link className="text-button" to={`/me/listings/${product.id}/edit`}>
                                {['published', 'pending'].includes(product.status)
                                  ? '编辑并重新审核'
                                  : '继续编辑'}{' '}
                                →
                              </Link>
                            ) : product.status === 'published' ? (
                              <button
                                className="text-button"
                                onClick={() => contact(product.code ?? product.id, 'publish')}
                              >
                                联系修改资料
                              </button>
                            ) : (
                              !['pending', 'publishing', 'rejected_final'].includes(
                                product.status,
                              ) && (
                                <Link
                                  className="text-button"
                                  to={`/me/listings/${product.id}/edit`}
                                >
                                  {product.status === 'rejected' ? '修改后重新提交' : '继续编辑'} →
                                </Link>
                              )
                            )}
                            {canDeleteDraft(product) && (
                              <button
                                className="text-button delete-draft"
                                disabled={!!busy}
                                onClick={() => void deleteDraft(product)}
                              >
                                删除草稿
                              </button>
                            )}
                            {['published', 'pending', 'publishing'].includes(product.status) && (
                              <button
                                className="text-button"
                                disabled={!!busy}
                                onClick={() => withdraw(product)}
                              >
                                {busy === product.id
                                  ? '正在处理…'
                                  : product.status === 'pending'
                                    ? '撤回审核'
                                    : product.status === 'publishing'
                                      ? '取消上架'
                                      : '下架商品'}
                              </button>
                            )}
                          </>
                        ) : tab === 'favorites' ? (
                          <>
                            <span className={`status-badge status-${product.status}`}>
                              {product.status === 'published' ? '公开展示中' : '商品已失效'}
                            </span>
                            <button
                              className="text-button"
                              onClick={() => removeFavorite(product.id)}
                              disabled={!!busy}
                            >
                              {busy === product.id ? '正在移除…' : '取消收藏'}
                            </button>
                          </>
                        ) : (
                          <Link to={`/accounts/${product.id}`} className="text-button">
                            再次查看 →
                          </Link>
                        )}
                      </div>
                    </article>
                  ))}
                </div>
              )}
            </>
          )}
        </section>
      </div>
    </section>
  );
}
