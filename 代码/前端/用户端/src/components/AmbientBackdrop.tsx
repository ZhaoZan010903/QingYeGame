import { useEffect, useRef } from 'react';

/** Restored from the pre-redesign composition; mounted on the homepage only. */
export function HomeBackdrop() {
  const stage = useRef<HTMLDivElement>(null);
  const aura = useRef<HTMLDivElement>(null);
  useEffect(() => {
    let frame = 0;
    const motion = matchMedia('(prefers-reduced-motion: reduce)');
    const finePointer = matchMedia('(hover: hover) and (pointer: fine)');
    const pointer = (event: PointerEvent) => {
      if (
        motion.matches ||
        !finePointer.matches ||
        event.pointerType !== 'mouse' ||
        document.hidden
      )
        return;
      cancelAnimationFrame(frame);
      frame = requestAnimationFrame(() => {
        if (aura.current)
          aura.current.style.transform = `translate3d(${event.clientX}px,${event.clientY}px,0)`;
      });
    };
    const visibility = () => {
      if (stage.current) stage.current.dataset.paused = String(document.hidden);
      if (document.hidden) cancelAnimationFrame(frame);
    };
    visibility();
    window.addEventListener('pointermove', pointer, { passive: true });
    document.addEventListener('visibilitychange', visibility);
    return () => {
      cancelAnimationFrame(frame);
      window.removeEventListener('pointermove', pointer);
      document.removeEventListener('visibilitychange', visibility);
    };
  }, []);
  return (
    <div className="ambient-stage home-backdrop" ref={stage} aria-hidden="true">
      <div className="ambient-wash" />
      <div className="cursor-aura" ref={aura} />
      <svg className="light-ribbons" viewBox="0 0 1440 1000" preserveAspectRatio="xMidYMin slice">
        <defs>
          <linearGradient id="home-ribbon-gold">
            <stop stopColor="#D4AF37" stopOpacity="0" />
            <stop offset=".45" stopColor="#BDA978" stopOpacity=".55" />
            <stop offset=".68" stopColor="#FFF2CE" stopOpacity=".85" />
            <stop offset="1" stopColor="#D4AF37" stopOpacity="0" />
          </linearGradient>
        </defs>
        <path className="ribbon-base" d="M-160 710 C260 680 -20 70 490 140 S1020 680 1600 250" />
        <path className="ribbon-trace" d="M-160 710 C260 680 -20 70 490 140 S1020 680 1600 250" />
        <path
          className="ribbon-secondary"
          d="M-180 790 C230 690 170 220 590 235 S1160 750 1600 380"
        />
      </svg>
    </div>
  );
}

export function ReadingProgress() {
  const progress = useRef<HTMLDivElement>(null);
  useEffect(() => {
    let scrollFrame = 0;
    let scrollRange = 1;
    const drawProgress = () => {
      scrollFrame = 0;
      if (progress.current)
        progress.current.style.transform = `scaleX(${Math.max(0, Math.min(1, scrollY / scrollRange))})`;
    };
    const scroll = () => {
      if (!scrollFrame) scrollFrame = requestAnimationFrame(drawProgress);
    };
    const measure = () => {
      scrollRange = Math.max(1, document.documentElement.scrollHeight - innerHeight);
      scroll();
    };
    const observer = new ResizeObserver(measure);
    observer.observe(document.body);
    window.addEventListener('resize', measure, { passive: true });
    measure();
    window.addEventListener('scroll', scroll, { passive: true });
    return () => {
      cancelAnimationFrame(scrollFrame);
      observer.disconnect();
      window.removeEventListener('resize', measure);
      window.removeEventListener('scroll', scroll);
    };
  }, []);
  return <div className="reading-progress" ref={progress} aria-hidden="true" />;
}
