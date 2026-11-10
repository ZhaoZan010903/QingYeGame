import { useEffect, useRef, useState } from 'react';
import { Link } from 'react-router-dom';
import {
  ArrowRight,
  ArrowUpRight,
  ChevronLeft,
  ChevronRight,
  Pause,
  Play,
  Plus,
} from 'lucide-react';
import type { Product } from '../types';
import { api, isDemo } from '../lib/api';
import { useLoad } from '../lib-use';
import { Artwork, AssetCard, Price, StateView } from '../components/shared';
import { Catalog } from './CatalogPage';
function EditorialCarousel({ products }: { products: Product[] }) {
  const [index, setIndex] = useState(0);
  const [paused, setPaused] = useState(
    () => matchMedia('(prefers-reduced-motion: reduce)').matches,
  );
  const [hover, setHover] = useState(false);
  const [visible, setVisible] = useState(true);
  const [hidden, setHidden] = useState(document.hidden);
  const [modal, setModal] = useState(false);
  const ref = useRef<HTMLDivElement>(null);
  const start = useRef({ x: 0, y: 0 });
  const manual = (n: number) => {
    setIndex((n + products.length) % products.length);
    setPaused(true);
  };
  useEffect(() => {
    const visibility = () => setHidden(document.hidden);
    document.addEventListener('visibilitychange', visibility);
    const observer = new IntersectionObserver((entries) => setVisible(entries[0].isIntersecting), {
      threshold: 0.2,
    });
    if (ref.current) observer.observe(ref.current);
    const modals = new MutationObserver(() =>
      setModal(document.body.classList.contains('modal-open')),
    );
    modals.observe(document.body, { attributes: true, attributeFilter: ['class'] });
    const motion = matchMedia('(prefers-reduced-motion: reduce)');
    const reduce = () => {
      if (motion.matches) setPaused(true);
    };
    motion.addEventListener('change', reduce);
    return () => {
      observer.disconnect();
      modals.disconnect();
      document.removeEventListener('visibilitychange', visibility);
      motion.removeEventListener('change', reduce);
    };
  }, []);
  useEffect(() => {
    if (paused || hover || hidden || !visible || modal || products.length < 2) return;
    const timer = setInterval(() => setIndex((n) => (n + 1) % products.length), 7500);
    return () => clearInterval(timer);
  }, [paused, hover, hidden, visible, modal, products.length]);
  return (
    <section className="editorial-section page-width mx-auto" aria-label="运营精选">
      <div className="editorial-caption">
        <span className="section-eyebrow mono">THE EDIT / 01</span>
        <span>
          本期甄选 <i>·</i> 运营精选
        </span>
      </div>
      <div
        ref={ref}
        className="editorial-carousel"
        role="region"
        aria-roledescription="轮播"
        aria-label="本期甄选"
        tabIndex={0}
        onMouseEnter={() => setHover(true)}
        onMouseLeave={() => setHover(false)}
        onFocusCapture={() => setHover(true)}
        onBlurCapture={(e) => {
          if (!e.currentTarget.contains(e.relatedTarget)) setHover(false);
        }}
        onKeyDown={(e) => {
          if (e.key === 'ArrowLeft') {
            e.preventDefault();
            manual(index - 1);
          }
          if (e.key === 'ArrowRight') {
            e.preventDefault();
            manual(index + 1);
          }
        }}
        onTouchStart={(e) => {
          start.current = { x: e.touches[0].clientX, y: e.touches[0].clientY };
        }}
        onTouchEnd={(e) => {
          const dx = e.changedTouches[0].clientX - start.current.x;
          const dy = e.changedTouches[0].clientY - start.current.y;
          if (Math.abs(dx) > 45 && Math.abs(dx) > Math.abs(dy) * 1.4)
            manual(index + (dx < 0 ? 1 : -1));
        }}
      >
        <div className="carousel-slides">
          {products.map((p, i) => (
            <article
              key={p.id}
              className={`editorial-slide ${i === index ? 'active' : ''}`}
              inert={i !== index}
              aria-hidden={i !== index}
              aria-label={`${i + 1} / ${products.length}`}
            >
              <div className="slide-visual">
                <Artwork product={p} />
              </div>
              <div className="slide-copy">
                <span className="slide-kicker mono">
                  {p.editorial?.kicker || 'QINGYE SELECTED'}
                </span>
                <h3>
                  {p.editorial?.name || p.title.split(' · ')[0]}
                  <span>{p.editorial?.collection || '风格珍藏'}</span>
                </h3>
                <p className="slide-description">{p.editorial?.description || p.description}</p>
                <p className="slide-meta">
                  <span>QQ炫舞</span>
                  <i>/</i>
                  <span>{p.server}</span>
                  <i>/</i>
                  <span>{p.mode === 'sale' ? '账号出售' : '账号租赁'}</span>
                </p>
                <div className="slide-action-row">
                  <Price product={p} />
                  <Link className="button-white" to={`/accounts/${p.id}`}>
                    查看这件资产 <ArrowRight />
                  </Link>
                </div>
              </div>
            </article>
          ))}
        </div>
        <div className="carousel-bottom">
          <span className="carousel-credit">
            QINGYE SELECTED <span>{isDemo ? '概念图 · 样例商品' : '运营人工甄选'}</span>
          </span>
          <div className="carousel-controls">
            <button
              className="carousel-arrow"
              aria-label="上一张精选"
              onClick={() => manual(index - 1)}
            >
              <ChevronLeft />
            </button>
            <span className="slide-number mono">
              <b>{String(index + 1).padStart(2, '0')}</b>
              <span>/ {String(products.length).padStart(2, '0')}</span>
            </span>
            <div className="carousel-dots">
              {products.map((p, i) => (
                <button
                  key={p.id}
                  aria-label={`第 ${i + 1} 张：${p.title}`}
                  aria-current={i === index}
                  onClick={() => manual(i)}
                />
              ))}
            </div>
            <button
              className="carousel-arrow"
              aria-label="下一张精选"
              onClick={() => manual(index + 1)}
            >
              <ChevronRight />
            </button>
            <button
              className="carousel-play"
              aria-label={paused ? '播放自动轮播' : '暂停自动轮播'}
              onClick={() => setPaused((v) => !v)}
            >
              {paused ? <Play /> : <Pause />}
            </button>
          </div>
        </div>
      </div>
      <p className="sr-only" role="status">
        第 {index + 1} 张，共 {products.length} 张{paused ? '，自动轮播已暂停' : ''}
      </p>
      <nav className="quick-access" aria-label="快捷浏览">
        {[
          ['/accounts?trade_type=sale', '账号出售', '找到值得拥有的珍藏'],
          ['/accounts?trade_type=rent', '账号租赁', '为下一次出场，换个风格'],
          ['/accounts?sort=newest', '最新上架', '看看刚刚公开的账号'],
          ['/publish', '发布账号', '自主提交，审核后展示'],
        ].map(([url, title, sub], i) => (
          <Link key={url} to={url}>
            <span className="quick-index mono">0{i + 1}</span>
            <span>
              <strong>{title}</strong>
              <small>{sub}</small>
            </span>
            {i === 3 ? <Plus /> : <ArrowUpRight />}
          </Link>
        ))}
      </nav>
    </section>
  );
}
export function HomePage() {
  const result = useLoad(() => api.home(), 'home');
  const [mode, setMode] = useState<'hot' | 'newest'>('hot');
  return (
    <>
      <section className="hero" aria-labelledby="hero-title">
        <div className="hero-lines" />
        <span className="hero-glow" />
        <div className="hero-inner mx-auto text-center">
          <div className="hero-eyebrow">
            <span className="gold-line" />
            <span>CURATED. CONSIDERED. YOURS.</span>
            <span className="gold-line" />
          </div>
          <h1 id="hero-title">
            <span className="hero-title-main">珍藏你的热爱。</span>
            <br />
            <span className="hero-title-trust">
              <span className="title-phrase">让每一次选择，</span>
              <span className="title-phrase">值得信任。</span>
            </span>
          </h1>
          <div className="hero-actions flex items-center justify-center">
            <Link className="button-white" to="/accounts">
              探索全部账号 <ArrowRight />
            </Link>
            <a className="button-quiet" href="#recommendations">
              看看热门推荐 <ArrowUpRight />
            </a>
          </div>
        </div>
      </section>
      {result.loading || result.error ? (
        <div className="page-width mx-auto">
          <StateView {...result} onRetry={result.retry} />
        </div>
      ) : (
        result.data && (
          <>
            {result.data.featured.length > 0 && (
              <EditorialCarousel products={result.data.featured} />
            )}
            <section className="recommendation-section page-width mx-auto" id="recommendations">
              <div className="section-heading">
                <div>
                  <span className="section-eyebrow mono">WORTH YOUR ATTENTION / 02</span>
                  <h2>此刻，值得关注</h2>
                </div>
                <Link className="section-more" to={`/accounts?sort=${mode}`}>
                  查看全部 <ArrowRight />
                </Link>
              </div>
              <div className="recommendation-toolbar">
                <div className="recommendation-tabs" role="tablist" aria-label="推荐方式">
                  {(['hot', 'newest'] as const).map((m) => (
                    <button
                      key={m}
                      role="tab"
                      aria-controls="recommendation-grid"
                      className={mode === m ? 'active' : ''}
                      aria-selected={mode === m}
                      tabIndex={mode === m ? 0 : -1}
                      onClick={() => setMode(m)}
                      onKeyDown={(e) => {
                        if (['ArrowLeft', 'ArrowRight', 'Home', 'End'].includes(e.key)) {
                          e.preventDefault();
                          const next =
                            e.key === 'Home'
                              ? 'hot'
                              : e.key === 'End'
                                ? 'newest'
                                : m === 'hot'
                                  ? 'newest'
                                  : 'hot';
                          setMode(next);
                          (
                            e.currentTarget.parentElement?.children[
                              next === 'hot' ? 0 : 1
                            ] as HTMLElement
                          )?.focus();
                        }
                      }}
                    >
                      {m === 'hot' ? '热门推荐' : '最新上架'}
                    </button>
                  ))}
                </div>
                <Link className="recommendation-explainer" to="/help/recommendation">
                  {isDemo ? '固定样例排序' : '公开商品推荐'} <span className="info-dot">i</span>
                </Link>
              </div>
              <div
                id="recommendation-grid"
                className="recommendation-grid"
                role="tabpanel"
                aria-label={mode === 'hot' ? '热门推荐' : '最新上架'}
              >
                {result.data[mode]
                  .filter((p) => !result.data!.featured.some((f) => f.id === p.id))
                  .map((p) => (
                    <AssetCard key={p.id} product={p} />
                  ))}
              </div>
            </section>
          </>
        )
      )}
      <Catalog home />
      <section className="sell-banner page-width mx-auto">
        <div>
          <span className="section-eyebrow mono">YOUR NEXT CHAPTER</span>
          <h2>让珍藏，遇见下一位知己。</h2>
          <p>自主提交资料，或由客服协助整理上架。</p>
        </div>
        <Link className="button-white" to="/publish">
          发布我的资产 <ArrowUpRight />
        </Link>
        <div className="banner-orbit" />
      </section>
    </>
  );
}
