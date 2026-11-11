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
import type { HomePresentation } from '../lib/home-content';
import { isDemo } from '../lib/api';
import { CarouselSlidePreview } from '../../../共享/CarouselSlidePreview';
import '../live-editorial.css';
import { carouselWindow, preloadCarouselSlide } from '../lib/carousel-window';

export function EditorialCarousel({
  products: inventory,
  presentation,
}: {
  products: Product[];
  presentation: HomePresentation;
}) {
  const slides = (presentation.slides ?? []).filter((slide) =>
    inventory.some((p) => p.id === slide.productId),
  );
  const products = slides.map((slide) => inventory.find((p) => p.id === slide.productId)!);
  const [index, setIndex] = useState(0);
  const activeIndex = Math.min(index, Math.max(0, products.length - 1));
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
    if (!products.length) return;
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
    const timer = setInterval(
      () => setIndex((n) => (n + 1) % products.length),
      presentation.editorial.intervalMs,
    );
    return () => clearInterval(timer);
  }, [paused, hover, hidden, visible, modal, products.length, presentation.editorial.intervalMs]);
  if (!products.length) return null;
  return (
    <section
      className={`editorial-section page-width mx-auto ${isDemo ? '' : 'live-editorial'}`}
      aria-label="运营精选"
    >
      <div className="editorial-caption">
        <h2>{presentation.editorial.title}</h2>
        <span>{presentation.editorial.description}</span>
      </div>
      <div
        ref={ref}
        className="editorial-carousel"
        role="region"
        aria-roledescription="轮播"
        aria-label={presentation.editorial.title}
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
            manual(activeIndex - 1);
          }
          if (e.key === 'ArrowRight') {
            e.preventDefault();
            manual(activeIndex + 1);
          }
        }}
        onTouchStart={(e) => {
          start.current = { x: e.touches[0].clientX, y: e.touches[0].clientY };
        }}
        onTouchEnd={(e) => {
          const dx = e.changedTouches[0].clientX - start.current.x;
          const dy = e.changedTouches[0].clientY - start.current.y;
          if (Math.abs(dx) > 45 && Math.abs(dx) > Math.abs(dy) * 1.4)
            manual(activeIndex + (dx < 0 ? 1 : -1));
        }}
      >
        <div className="carousel-slides">
          {products.map((p, i) => (
            <article
              key={p.id}
              className={`editorial-slide ${i === activeIndex ? 'active' : ''}`}
              inert={i !== activeIndex}
              aria-hidden={i !== activeIndex}
              aria-label={`${i + 1} / ${products.length}`}
            >
              <CarouselSlidePreview
                slide={slides[i]}
                imageUrl={
                  preloadCarouselSlide(i, activeIndex, products.length)
                    ? (p.images.find((image) => image.id === slides[i].imageId) ?? p.images[0])?.url
                    : undefined
                }
                action={
                  <Link className="button-white" to={`/accounts/${p.id}`}>
                    查看详情 <ArrowRight />
                  </Link>
                }
              />
            </article>
          ))}
        </div>
        <div className="carousel-bottom">
          <span className="carousel-credit">{isDemo ? '概念图 · 样例商品' : '运营人工甄选'}</span>
          <div className="carousel-controls">
            <button
              className="carousel-arrow"
              aria-label="上一张精选"
              onClick={() => manual(activeIndex - 1)}
            >
              <ChevronLeft />
            </button>
            <span className="slide-number mono">
              <b>{String(activeIndex + 1).padStart(2, '0')}</b>
              <span>/ {String(products.length).padStart(2, '0')}</span>
            </span>
            <div className="carousel-dots">
              {carouselWindow(products.length, activeIndex).map((i) => (
                <button
                  key={products[i].id}
                  aria-label={`第 ${i + 1} 张：${slides[i].title}`}
                  aria-current={i === activeIndex}
                  onClick={() => manual(i)}
                />
              ))}
            </div>
            <button
              className="carousel-arrow"
              aria-label="下一张精选"
              onClick={() => manual(activeIndex + 1)}
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
        第 {activeIndex + 1} 张，共 {products.length} 张{paused ? '，自动轮播已暂停' : ''}
      </p>
      {presentation.shortcuts.length > 0 && (
        <nav className="quick-access" aria-label="快捷浏览">
          {presentation.shortcuts.map(({ href, title, description, icon }) => (
            <Link key={href} to={href}>
              <span className="quick-copy">
                <strong>{title}</strong>
                <small>{description}</small>
              </span>
              {icon === 'plus' ? <Plus /> : <ArrowUpRight />}
            </Link>
          ))}
        </nav>
      )}
    </section>
  );
}
