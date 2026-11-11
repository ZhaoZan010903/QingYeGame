import { useEffect, useRef } from 'react';

/** Decoration stays local to the first screen; hidden tabs and offscreen lights stop animating. */
export function HeroAtmosphere() {
  const atmosphere = useRef<HTMLDivElement>(null);

  useEffect(() => {
    const element = atmosphere.current;
    if (!element) return;
    let visible = true;
    const sync = () => {
      element.dataset.paused = String(!visible || document.hidden);
    };
    const observer = new IntersectionObserver(
      ([entry]) => {
        visible = entry.isIntersecting;
        sync();
      },
      { threshold: 0 },
    );
    observer.observe(element);
    document.addEventListener('visibilitychange', sync);
    sync();
    return () => {
      observer.disconnect();
      document.removeEventListener('visibilitychange', sync);
    };
  }, []);

  return (
    <div className="hero-atmosphere" ref={atmosphere} aria-hidden="true">
      <div className="hero-precision-grid" />
      <div className="hero-ambient-light hero-ambient-warm" />
      <div className="hero-ambient-light hero-ambient-cool" />
      <div className="hero-film-grain" />
    </div>
  );
}
