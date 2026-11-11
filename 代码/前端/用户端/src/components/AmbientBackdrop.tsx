import { useEffect, useRef } from 'react';

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
