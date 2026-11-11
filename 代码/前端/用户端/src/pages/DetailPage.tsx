import { useEffect, useState } from 'react';
import { Link, useLocation, useNavigate, useParams } from 'react-router-dom';
import { ArrowLeft, ArrowRight, Heart, Headphones, ShieldCheck } from 'lucide-react';
import { api, ApiError } from '../lib/api';
import { useLoad } from '../lib-use';
import { useSession, useUi } from '../context';
import type { Product } from '../types';
import { Price, StateView, Verification, genderLabel } from '../components/shared';
import { AccountGallery } from '../components/AccountGallery';
import { ConsultationOptions } from '../components/ConsultationOptions';
import { levelLabel } from '../../../共享/level';
import { AssetArchive } from '../components/AssetArchive';
import { quoteTerms } from '../lib/asset-copy';
import '../gallery.css';
import '../service-refinement.css';
function DetailContent({
  product: p,
  reloadProduct,
}: {
  product: Product;
  reloadProduct: () => void;
}) {
  const [verify, setVerify] = useState(false);
  const [consulting, setConsulting] = useState(false);
  const [favorite, setFavorite] = useState(!!p.favorite);
  const [busy, setBusy] = useState(false);
  const { user } = useSession();
  const { notify, contact } = useUi();
  const navigate = useNavigate();
  const location = useLocation();
  const quotes = quoteTerms(p.title + '\n' + p.description);
  const deposit = quotes.find((quote) => quote.label === '押金');
  const rates =
    p.mode === 'rent'
      ? p.rentalQuotes?.length
        ? p.rentalQuotes.map((q) => ({
            label: { hour: '时租', day: '日租', week: '周租', month: '月租' }[q.unit],
            value: q.amount,
          }))
        : quotes.filter((quote) => quote.label !== '押金')
      : [];
  useEffect(() => {
    void api.view(p.id).catch(() => {});
    try {
      const old = JSON.parse(localStorage.getItem('qingye-history') || '[]');
      localStorage.setItem(
        'qingye-history',
        JSON.stringify(
          [p.id, ...(Array.isArray(old) ? old : []).filter((id: string) => id !== p.id)].slice(
            0,
            50,
          ),
        ),
      );
    } catch {}
  }, [p.id, p.version]);
  useEffect(() => {
    if (!user || new URLSearchParams(location.search).get('favorite') === '1') return;
    let active = true;
    api
      .favorites()
      .then((page) => {
        if (active) setFavorite(page.some((item) => item.id === p.id));
      })
      .catch(() => {});
    return () => {
      active = false;
    };
  }, [user, p.id, location.search]);
  const toggleFavorite = async () => {
    if (!user) {
      navigate(`/login?returnTo=${encodeURIComponent(location.pathname + '?favorite=1')}`);
      return;
    }
    setBusy(true);
    try {
      await api.favorite(p.id, !favorite);
      setFavorite((v) => !v);
      notify(favorite ? '已取消收藏' : '已加入收藏');
    } catch (e) {
      notify(e instanceof Error ? e.message : '收藏失败');
    } finally {
      setBusy(false);
    }
  };
  useEffect(() => {
    if (!user || new URLSearchParams(location.search).get('favorite') !== '1') return;
    let active = true;
    setBusy(true);
    api
      .favorite(p.id, true)
      .then(() => {
        if (active) {
          setFavorite(true);
          notify('已加入收藏');
          navigate(location.pathname, { replace: true });
        }
      })
      .catch((e) => {
        if (active) notify(e.message);
      })
      .finally(() => {
        if (active) setBusy(false);
      });
    return () => {
      active = false;
    };
  }, [user, p.id, location.search]);
  return (
    <>
      <div className="detail-layout">
        <div className="detail-left-column">
          <AccountGallery product={p} reloadProduct={reloadProduct} />
          <AssetArchive product={p} />
        </div>
        <section className="detail-summary">
          <div className="detail-kicker">
            <span className="asset-id">{p.code ?? p.id}</span>
            <span
              className={p.verification === 'verified' ? 'verified-badge' : 'verification-pending'}
            >
              {p.verification === 'verified' ? (
                <>
                  <ShieldCheck />
                  官方人工已验号
                </>
              ) : p.verification === 'pending' ? (
                '资料核验中'
              ) : (
                '核验资料待补充'
              )}
            </span>
          </div>
          <h1 className="detail-title">{p.title}</h1>
          <p className="detail-subtitle">QQ炫舞 / {p.server}</p>
          <div className="pill-tags">
            {p.tags.map((t) => (
              <span key={t}>{t}</span>
            ))}
          </div>
          <div className="detail-price">
            <div className="detail-price-line">
              <Price product={p} compact />
              {deposit && (
                <span className="deposit-badge">
                  押金 <b className="mono">¥{deposit.value}</b>
                </span>
              )}
            </div>
            {rates.length > 0 && (
              <div className="rental-quotes">
                {rates.map((quote) => (
                  <div key={quote.label}>
                    <span>{quote.label}</span>
                    <strong className="mono">¥{quote.value}</strong>
                  </div>
                ))}
              </div>
            )}
            <p>
              {rates.length || deposit
                ? '分档报价与押金来自卖家说明，实际条件请向客服确认。'
                : '展示报价，实际租期与交付安排请向客服确认。'}
            </p>
          </div>
          <dl className="property-grid">
            <div>
              <dt>所属游戏</dt>
              <dd>QQ炫舞</dd>
            </div>
            <div>
              <dt>所在大区</dt>
              <dd>{p.server}</dd>
            </div>
            <div>
              <dt>角色特征</dt>
              <dd>
                {genderLabel(p.gender)} · <span className="mono">{levelLabel(p.level)}</span>
              </dd>
            </div>
            <div>
              <dt>交易模式</dt>
              <dd>
                {p.mode === 'sale' ? '出售' : '租赁'} ·{' '}
                {p.kind === 'linked' ? '双号连体' : '独立账号'}
              </dd>
            </div>
          </dl>
          <button className="verification-row" onClick={() => setVerify(true)}>
            <ShieldCheck />
            <span>查看资料核验说明</span>
            <ArrowRight />
          </button>
          <div className="detail-actions">
            <button className="button-white" onClick={() => setConsulting(true)}>
              <Headphones />
              {p.mode === 'rent' ? '咨询租赁与报价' : '咨询购买与报价'}
            </button>
            <button
              className={`button-outline favorite-button ${favorite ? 'selected' : ''}`}
              aria-pressed={favorite}
              aria-label={favorite ? '取消收藏' : '收藏账号'}
              title={favorite ? '取消收藏' : '收藏账号'}
              disabled={busy}
              onClick={toggleFavorite}
            >
              <Heart fill={favorite ? 'currentColor' : 'none'} />
            </button>
          </div>
          <p className="detail-action-note">选择在线客服或 QQ 咨询，由客服协助确认与办理。</p>
          {(p.consultationCount ?? 0) > 0 && (
            <p className="consultation-count">已有 {p.consultationCount} 位用户咨询此账号</p>
          )}
          <div className="detail-trust">
            <span>
              <ShieldCheck />
              资料核实
            </span>
            <span>
              <Headphones />
              客服协助交付
            </span>
            <button type="button" onClick={() => contact(p.code || p.id)}>
              售后联系
            </button>
          </div>
        </section>
      </div>
      {consulting && (
        <ConsultationOptions
          productId={p.id}
          title={p.title}
          code={p.code || p.id}
          onClose={() => setConsulting(false)}
          onQQ={() => {
            setConsulting(false);
            contact(p.code || p.id, p.mode === 'rent' ? 'rent' : 'buy', 'qq');
          }}
        />
      )}
      {verify && <Verification product={p} onClose={() => setVerify(false)} />}{' '}
    </>
  );
}
export function DetailPage() {
  const { id = '' } = useParams();
  const location = useLocation();
  const result = useLoad(() => api.product(id), id);
  return (
    <div className="detail-page page-width mx-auto">
      <Link
        className="back-link"
        to="/accounts"
        onClick={(e) => {
          if (location.state?.from && window.history.state?.idx > 0) {
            e.preventDefault();
            window.history.back();
          }
        }}
      >
        <ArrowLeft />
        返回账号列表
      </Link>
      {result.loading ? (
        <StateView loading />
      ) : result.error ? (
        <div className="state-view">
          <h1>
            {result.error instanceof ApiError && result.error.status === 410
              ? '该账号已下架'
              : result.error instanceof ApiError && result.error.status === 404
                ? '暂时无法查看这个账号'
                : '账号读取失败'}
          </h1>
          <p>{result.error instanceof Error ? result.error.message : '请稍后再试'}</p>
          <Link className="button-white" to="/accounts">
            浏览其他账号
          </Link>
          <button className="text-button" onClick={result.retry}>
            重新加载
          </button>
        </div>
      ) : result.data ? (
        <DetailContent
          key={`${id}:${result.data.version}`}
          product={result.data}
          reloadProduct={result.retry}
        />
      ) : null}
    </div>
  );
}
