import { ProtectedImage } from '../../../共享/MediaSecurity';
import { useEffect, useId, useRef, useState, type ReactNode } from 'react';
import { Link, useLocation } from 'react-router-dom';
import { ArrowRight, X, Search, ShieldCheck, RefreshCw } from 'lucide-react';
import { isDemo } from '../lib/api';
import type { Product } from '../types';
import { rentalCategoryLabel, rentalPriceRows } from '../lib/rental-prices';
import '../card-readability.css';
import '../rental-prices.css';

export const unitLabels: Record<string, string> = {
  account: '元',
  hour: '元/小时',
  day: '元/日',
  week: '元/周',
  month: '元/月',
  period: '元/时段',
  unknown: '原站报价 · 单位待核对',
};
export const genderLabel = (gender: Product['gender']) =>
  gender === 'female' ? '女角色' : gender === 'male' ? '男角色' : '角色未注明';
export function Price({
  product: p,
  compact = false,
  allPeriods = false,
}: {
  product: Product;
  compact?: boolean;
  allPeriods?: boolean;
}) {
  if (p.mode === 'rent' && p.rentalQuotes?.length) {
    const rows = rentalPriceRows(p.rentalQuotes, allPeriods);
    const periodCount = p.rentalQuotes.filter((quote) => quote.unit === 'period').length;
    const grouped = periodCount > rows.filter((row) => row.period).length;
    return (
      <span
        className={`rental-price-list ${compact ? 'rental-price-compact' : ''}`}
        role="group"
        aria-label="租赁报价"
      >
        {rows.map((row, index) => (
          <span
            className={`rental-price-item ${row.period ? 'rental-price-period' : ''}`}
            key={index}
          >
            <span className="rental-price-label">
              {row.label}
              {row.time && <small>{row.time}</small>}
            </span>
            <span className="rental-price-value">
              <b className="mono">{row.amount}</b>
              <small>
                {row.unit}
                {row.starting ? '起' : ''}
              </small>
            </span>
          </span>
        ))}
        {grouped && (
          <small className="rental-price-more">共 {periodCount} 档时段报价，详情查看</small>
        )}
      </span>
    );
  }
  const legacy = p.unit === 'unknown' && p.legacyPrice !== undefined;
  return (
    <span className={`price-box ${compact ? 'price-compact' : ''}`}>
      <span className="price mono">
        {legacy ? (
          p.legacyPrice
        ) : p.price === null ? (
          '联系客服'
        ) : (
          <>
            {p.unit !== 'unknown' && <span className="currency">¥ </span>}
            {p.price.toLocaleString('zh-CN')}
          </>
        )}
      </span>
      <small className="price-unit">
        {p.unit === 'unknown'
          ? p.priceNote || unitLabels.unknown
          : p.price === null
            ? '报价咨询'
            : compact && p.unit !== 'account'
              ? `/ ${unitLabels[p.unit].split('/')[1]}`
              : unitLabels[p.unit]}
      </small>
    </span>
  );
}
export function Artwork({
  product: p,
  className = '',
  original = false,
}: {
  product: Product;
  className?: string;
  original?: boolean;
}) {
  const [failed, setFailed] = useState(false);
  const first = p.images?.[0];
  const src = original ? first?.url : (first?.thumbnailUrl ?? first?.url);
  useEffect(() => setFailed(false), [src]);
  return (
    <div
      className={`artwork ${className}`}
      role="img"
      aria-label={`${p.title}${src ? '' : isDemo ? ' · 概念展示图' : ' · 图片暂不可用'}`}
    >
      {src && !failed ? (
        <ProtectedImage
          src={src}
          alt={p.title}
          loading="lazy"
          decoding="async"
          onError={() => setFailed(true)}
        />
      ) : isDemo ? (
        <div className={`asset-art art-${p.art ?? 0}`} />
      ) : (
        <span className="image-unavailable">图片暂不可用</span>
      )}
    </div>
  );
}
export function AssetCard({ product: p }: { product: Product }) {
  const location = useLocation();
  const tradeKind =
    p.mode === 'sale'
      ? 'sale'
      : p.rentalCategory === 'week_month'
        ? 'long-rent'
        : p.rentalCategory === 'hour_day'
          ? 'short-rent'
          : 'rent';
  const tradeLabel = rentalCategoryLabel(p);
  return (
    <article className="asset-card">
      <Link
        className="asset-open"
        to={`/accounts/${p.id}`}
        state={{ from: location.pathname + location.search }}
      >
        <div className="asset-image">
          <Artwork product={p} />
          <span className={`trade-badge ${tradeKind}`}>{tradeLabel}</span>
        </div>
        <div className="asset-body">
          <div className="asset-topline">
            <span className="asset-id">{p.code ?? p.id}</span>
          </div>
          <h3 className="asset-title">{p.title}</h3>
          <div className="asset-meta">
            <span>QQ炫舞</span>
            <i />
            <span>{p.server}</span>
            <i />
            <span>{p.attributeLabels?.gender || genderLabel(p.gender)}</span>
          </div>
          <div className="pill-tags">
            {p.tags.slice(0, 3).map((t) => (
              <span key={t}>{t}</span>
            ))}
          </div>
          <div
            className={`asset-bottom ${p.mode === 'rent' && p.rentalQuotes?.length ? 'asset-bottom-rental' : ''}`}
          >
            <span className="card-caption">查看详情</span>
            <div className="asset-price-side">
              <Price product={p} compact />
              <span className="asset-arrow">
                <ArrowRight />
              </span>
            </div>
          </div>
        </div>
      </Link>
    </article>
  );
}
export function StateView({
  error,
  empty,
  loading,
  onRetry,
}: {
  error?: unknown;
  empty?: string;
  loading?: boolean;
  onRetry?: () => void;
}) {
  return (
    <div className="state-view" role={error ? 'alert' : 'status'}>
      {loading ? (
        <>
          <span className="loader" />
          <h3>正在读取…</h3>
        </>
      ) : error ? (
        <>
          <RefreshCw />
          <h3>暂时无法读取</h3>
          <p>{error instanceof Error ? error.message : '请稍后重试'}</p>
          {onRetry && (
            <button className="button-outline" onClick={onRetry}>
              重新加载
            </button>
          )}
        </>
      ) : (
        <>
          <Search />
          <h3>{empty || '暂无内容'}</h3>
          <p>可以调整筛选条件，或稍后再来看看。</p>
        </>
      )}
    </div>
  );
}
export function Modal({
  title,
  children,
  onClose,
  wide = false,
  className = '',
}: {
  title: string;
  children: ReactNode;
  onClose: () => void;
  wide?: boolean;
  className?: string;
}) {
  const titleId = useId();
  const ref = useRef<HTMLDialogElement>(null);
  const close = useRef(onClose);
  close.current = onClose;
  useEffect(() => {
    const d = ref.current;
    const previous = document.activeElement as HTMLElement;
    d?.showModal();
    document.body.classList.add('modal-open');
    const cancel = (e: Event) => {
      e.preventDefault();
      close.current();
    };
    const trap = (e: KeyboardEvent) => {
      if (e.key !== 'Tab' || !d) return;
      const items = [
        ...d.querySelectorAll<HTMLElement>(
          'a[href],button:not([disabled]),input:not([disabled]),select:not([disabled]),textarea:not([disabled]),[tabindex]:not([tabindex="-1"])',
        ),
      ].filter(
        (el) =>
          el.getClientRects().length > 0 &&
          !el.closest('[inert]') &&
          el.getAttribute('aria-hidden') !== 'true',
      );
      const first = items[0],
        last = items[items.length - 1];
      if (!first) {
        e.preventDefault();
        d.focus();
        return;
      }
      if (
        e.shiftKey &&
        (document.activeElement === first || !items.includes(document.activeElement as HTMLElement))
      ) {
        e.preventDefault();
        last.focus();
      } else if (
        !e.shiftKey &&
        (document.activeElement === last || !items.includes(document.activeElement as HTMLElement))
      ) {
        e.preventDefault();
        first.focus();
      }
    };
    d?.addEventListener('cancel', cancel);
    d?.addEventListener('keydown', trap);
    return () => {
      d?.removeEventListener('cancel', cancel);
      d?.removeEventListener('keydown', trap);
      d?.close();
      if (!document.querySelector('dialog[open]')) document.body.classList.remove('modal-open');
      if (previous?.isConnected) previous.focus();
    };
  }, []);
  return (
    <dialog
      ref={ref}
      aria-labelledby={titleId}
      className={`app-dialog ${wide ? 'wide' : ''} ${className}`}
      onClick={(e) => {
        if (e.target === e.currentTarget) {
          const r = e.currentTarget.getBoundingClientRect();
          if (
            e.clientX < r.left ||
            e.clientX > r.right ||
            e.clientY < r.top ||
            e.clientY > r.bottom
          )
            onClose();
        }
      }}
    >
      <div className="dialog-header">
        <div>
          <h2 id={titleId}>{title}</h2>
        </div>
        <button className="icon-button" aria-label="关闭" onClick={onClose}>
          <X />
        </button>
      </div>
      {children}
    </dialog>
  );
}
export function Verification({ product: p, onClose }: { product: Product; onClose: () => void }) {
  return (
    <Modal title="账号资料核验" onClose={onClose}>
      <div className="verification-info">
        <ShieldCheck />
        <span className="asset-id">{p.code ?? p.id}</span>
        <h3>核验记录与发布审核分别管理</h3>
        <p>当前未提供可展示的正式核验记录。商品资料审核通过，不代表账号安全认证或包赔承诺。</p>
        <p>咨询时请向平台客服确认账号绑定、资料来源与实际交付条件。</p>
      </div>
    </Modal>
  );
}
