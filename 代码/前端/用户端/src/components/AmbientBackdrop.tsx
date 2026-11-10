import { useEffect, useRef } from 'react';

export function AmbientBackdrop() {
  const aura = useRef<HTMLDivElement>(null);
  const progress = useRef<HTMLDivElement>(null);
  useEffect(() => {
    let pointerFrame = 0;
    let scrollFrame = 0;
    let scrollRange = 1;
    const motion = matchMedia('(prefers-reduced-motion: reduce)');
    const finePointer = matchMedia('(hover: hover) and (pointer: fine)');
    const pointer = (e: PointerEvent) => {
      if (motion.matches || !finePointer.matches || e.pointerType !== 'mouse') return;
      cancelAnimationFrame(pointerFrame);
      pointerFrame = requestAnimationFrame(() => {
        if (aura.current)
          aura.current.style.transform = `translate3d(${e.clientX}px,${e.clientY}px,0)`;
      });
    };
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
    window.addEventListener('pointermove', pointer, { passive: true });
    window.addEventListener('scroll', scroll, { passive: true });
    return () => {
      cancelAnimationFrame(pointerFrame);
      cancelAnimationFrame(scrollFrame);
      observer.disconnect();
      window.removeEventListener('resize', measure);
      window.removeEventListener('pointermove', pointer);
      window.removeEventListener('scroll', scroll);
    };
  }, []);
  return (
    <>
      <div className="reading-progress" ref={progress} />
      <div className="ambient-stage" aria-hidden="true">
        <div className="ambient-wash" />
        <div className="cursor-aura" ref={aura} />
        <svg className="light-ribbons" viewBox="0 0 1440 1000" preserveAspectRatio="xMidYMin slice">
          <defs>
            <linearGradient id="ribbon-gold">
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
    </>
  );
}
