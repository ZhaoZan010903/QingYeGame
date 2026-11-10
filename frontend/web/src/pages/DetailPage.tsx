import { useEffect, useState } from 'react';
import { Link, useLocation, useNavigate, useParams } from 'react-router-dom';
import { ArrowLeft, ArrowRight, Heart, Headphones, ShieldCheck, Maximize2 } from 'lucide-react';
import { api, ApiError, isDemo } from '../lib/api';
import { useLoad } from '../lib-use';
import { useSession, useUi } from '../context';
import type { ImageItem, Product } from '../types';
import { Artwork, Modal, Price, StateView, Verification } from '../components/shared';
function DetailContent({ product: p }: { product: Product }) {
  const [images, setImages] = useState<ImageItem[]>(p.images);
  const [selected, setSelected] = useState(0);
  const [cursor, setCursor] = useState<string | null>(null);
  const [galleryLoading, setGalleryLoading] = useState(false);
  const [galleryError, setGalleryError] = useState<unknown>();
  const [version, setVersion] = useState(p.version);
  const [zoom, setZoom] = useState(false);
  const [verify, setVerify] = useState(false);
  const [favorite, setFavorite] = useState(!!p.favorite);
  const [busy, setBusy] = useState(false);
  const { user } = useSession();
  const { notify, contact } = useUi();
  const navigate = useNavigate();
  const location = useLocation();
  useEffect(() => {
    let active = true;
    api
      .images(p.id, undefined, p.version)
      .then((page) => {
        if (active) {
          setImages(page.items);
          setCursor(page.nextCursor);
          setVersion(page.version);
        }
      })
      .catch((e) => {
        if (active) setGalleryError(e);
      });
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
    return () => {
      active = false;
    };
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
  const more = async (reset = false) => {
    setGalleryLoading(true);
    setGalleryError(undefined);
    try {
      const page = await api.images(
        p.id,
        reset ? undefined : cursor || undefined,
        reset ? undefined : version,
      );
      setImages((old) => (reset ? page.items : [...old, ...page.items]));
      setVersion(page.version);
      setCursor(page.nextCursor);
      if (reset) setSelected(0);
    } catch (e) {
      setGalleryError(e);
    } finally {
      setGalleryLoading(false);
    }
  };
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
  const activeProduct = {
    ...p,
    art: images[selected]?.art ?? p.art,
    images: images[selected] ? [images[selected]] : [],
  };
  return (
    <>
      <div className="detail-layout">
        <section className="detail-gallery">
          <button className="gallery-main" aria-label="放大账号图片" onClick={() => setZoom(true)}>
            <Artwork product={activeProduct} />
            <span className="gallery-label">
              {isDemo ? '概念展示图' : '账号图片'} · {images.length ? selected + 1 : 0} /{' '}
              {p.imageTotal}
              <Maximize2 />
            </span>
          </button>
          <div className="gallery-thumbs">
            {images.map((image, i) => (
              <button
                key={image.id}
                className={selected === i ? 'selected' : ''}
                aria-label={`查看第 ${i + 1} 张图片`}
                aria-pressed={selected === i}
                onClick={() => setSelected(i)}
              >
                <Artwork product={{ ...p, art: image.art ?? p.art, images: [image] }} />
              </button>
            ))}
          </div>
          {!!galleryError && (
            <div className="inline-error" role="alert">
              <p>
                {galleryError instanceof ApiError && galleryError.status === 409
                  ? '图集已更新，请重新加载后查看。'
                  : galleryError instanceof Error
                    ? galleryError.message
                    : '图片加载失败'}
              </p>
              <button className="text-button" onClick={() => more(true)}>
                重新加载图集
              </button>
            </div>
          )}
          {cursor && (
            <button className="button-outline" disabled={galleryLoading} onClick={() => more()}>
              {galleryLoading ? '读取中…' : '加载更多图片'} <ArrowRight />
            </button>
          )}
        </section>
        <section className="detail-summary">
          <div className="detail-kicker">
            <span className="asset-id">{p.code ?? p.id}</span>
            <span className="rarity">{p.mode === 'sale' ? '账号出售' : '账号租赁'}</span>
          </div>
          <h1>{p.title}</h1>
          <p className="detail-subtitle">QQ炫舞 / {p.server}</p>
          <div className="pill-tags">
            {p.tags.map((t) => (
              <span key={t}>{t}</span>
            ))}
          </div>
          <div className="detail-price">
            <Price product={p} />
            <p>展示报价，实际资料与交付安排请咨询客服。</p>
          </div>
          <dl className="property-grid">
            <div>
              <dt>游戏区服</dt>
              <dd>{p.server}</dd>
            </div>
            <div>
              <dt>角色性别</dt>
              <dd>{p.gender === 'female' ? '女角色' : '男角色'}</dd>
            </div>
            <div>
              <dt>XVIP 等级</dt>
              <dd className="mono">VIP {p.level}</dd>
            </div>
            <div>
              <dt>账号类型</dt>
              <dd>{p.kind === 'linked' ? '双号连体' : '独立账号'}</dd>
            </div>
          </dl>
          <button className="verification-row" onClick={() => setVerify(true)}>
            <ShieldCheck />
            <span>查看资料核验说明</span>
            <ArrowRight />
          </button>
          <div className="detail-actions">
            <button
              className="button-white"
              onClick={() => contact(p.code ?? p.id, p.mode === 'rent' ? 'rent' : 'buy')}
            >
              <Headphones />
              咨询这个账号
            </button>
            <button
              className={`button-outline favorite-button ${favorite ? 'selected' : ''}`}
              aria-pressed={favorite}
              disabled={busy}
              onClick={toggleFavorite}
            >
              <Heart fill={favorite ? 'currentColor' : 'none'} />
              {favorite ? '已收藏' : '收藏'}
            </button>
          </div>
        </section>
      </div>
      <section className="detail-description panel">
        <span className="section-eyebrow mono">ABOUT THIS COLLECTION</span>
        <h2>关于这份珍藏</h2>
        <p>{p.description || '暂无补充介绍，请联系客服了解详情。'}</p>
        {isDemo && (
          <p className="notice-box">
            此账号为交互演示样例，图像为概念作品，不能用于判断真实账号库存或交易保障。
          </p>
        )}
      </section>
      {verify && <Verification product={p} onClose={() => setVerify(false)} />}{' '}
      {zoom && (
        <Modal
          title={`${p.title} · ${selected + 1}/${images.length}`}
          onClose={() => setZoom(false)}
          wide
        >
          <div className="zoom-art">
            <Artwork product={activeProduct} />
          </div>
          <div className="zoom-controls">
            <button
              className="button-outline"
              disabled={!selected}
              onClick={() => setSelected((i) => i - 1)}
            >
              上一张
            </button>
            <span className="mono">
              {selected + 1} / {p.imageTotal}
            </span>
            <button
              className="button-outline"
              disabled={selected >= images.length - 1}
              onClick={() => setSelected((i) => i + 1)}
            >
              下一张
            </button>
          </div>
          {cursor && (
            <button className="text-button" onClick={() => more()} disabled={galleryLoading}>
              继续加载图集
            </button>
          )}
        </Modal>
      )}
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
        <DetailContent key={id} product={result.data} />
      ) : null}
    </div>
  );
}
