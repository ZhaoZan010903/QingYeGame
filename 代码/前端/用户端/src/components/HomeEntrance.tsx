import type { ReactNode } from 'react';
import { Link } from 'react-router-dom';
import { ArrowRight, ArrowUpRight } from 'lucide-react';
import { environmentLabel } from '../lib/api';
import { useHomeEntranceMotion } from './useHomeEntranceMotion';

const words = ['珍藏你的热爱。', '让每一次选择，', '值得信任。'];

function TitleTemplate() {
  return (
    <>
      <span className="hero-title-main" data-title-piece>
        {words[0]}
      </span>
      <span className="hero-title-trust">
        <span className="title-phrase" data-title-piece>
          {words[1]}
        </span>
        <span className="title-phrase" data-title-piece>
          {words[2]}
        </span>
      </span>
    </>
  );
}

/** One visible heading moves between two layout measurements, including mobile line changes. */
export function HomeEntrance({ children }: { children: ReactNode }) {
  const {
    wrapper,
    intro,
    source,
    destination,
    title,
    discovery,
    subtitle,
    arrow,
    note,
    contentHero,
    enter,
  } = useHomeEntranceMotion();

  return (
    <div className="home-experience morph-experience" ref={wrapper}>
      <h1 className="morph-title" id="hero-title" aria-label={words.join('')} ref={title}>
        {words.map((word, index) => (
          <span
            key={word}
            className={`morph-title-piece ${index ? 'gold' : 'ivory'}`}
            aria-hidden="true"
          >
            {word}
          </span>
        ))}
      </h1>
      <section className="hero immersive-intro" ref={intro} aria-labelledby="hero-title">
        <div className="intro-halo" aria-hidden="true" />
        <div className="hero-inner mx-auto text-center">
          <div className="intro-title-template" aria-hidden="true" ref={source}>
            <TitleTemplate />
          </div>
          <p className="intro-subtitle" ref={subtitle}>
            在清叶，遇见下一份珍藏。
          </p>
        </div>
        <button
          type="button"
          className="intro-scroll"
          ref={arrow}
          aria-label="向下探索精选账号"
          onClick={enter}
        >
          <span>向下探索</span>
          <span className="intro-scroll-icon" aria-hidden="true">
            <svg className="intro-chevrons" viewBox="0 0 32 36" fill="none">
              <path d="m5 7 11 9L27 7" />
              <path d="m5 19 11 9 11-9" />
            </svg>
          </span>
        </button>
      </section>
      <div className="home-discover" id="home-discover" ref={discovery} tabIndex={-1}>
        {environmentLabel && (
          <p className="home-demo-note" ref={note}>
            <span className="status-dot" />
            {environmentLabel}
          </p>
        )}
        <section className="hero home-content-hero" aria-label="探索清叶" ref={contentHero}>
          <div className="hero-inner mx-auto text-center">
            <div className="landing-title-template" aria-hidden="true" ref={destination}>
              <TitleTemplate />
            </div>
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
        {children}
      </div>
    </div>
  );
}
