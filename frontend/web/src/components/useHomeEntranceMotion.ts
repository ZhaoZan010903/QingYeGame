import { useLayoutEffect, useRef } from 'react';

const clamp = (value: number) => Math.max(0, Math.min(1, value));
const ease = (value: number) => value * value * (3 - 2 * value);
const SCROLL_DURATION_MS = 1150;

type TitlePiece = {
  element: HTMLElement;
  fontSize: number;
  x: number;
  y: number;
  endX: number;
  endY: number;
  scale: number;
};

/** Layout is read only when it changes; scrolling updates three small text layers. */
export function useHomeEntranceMotion() {
  const wrapper = useRef<HTMLDivElement>(null);
  const intro = useRef<HTMLElement>(null);
  const source = useRef<HTMLDivElement>(null);
  const destination = useRef<HTMLDivElement>(null);
  const title = useRef<HTMLHeadingElement>(null);
  const discovery = useRef<HTMLDivElement>(null);
  const subtitle = useRef<HTMLParagraphElement>(null);
  const arrow = useRef<HTMLButtonElement>(null);
  const note = useRef<HTMLParagraphElement>(null);
  const contentHero = useRef<HTMLElement>(null);
  const enterAction = useRef(() => {});

  useLayoutEffect(() => {
    const motion = matchMedia('(prefers-reduced-motion: reduce)');
    let frame = 0;
    let scrollFrame = 0;
    let disposed = false;
    let needsMeasure = true;
    let startScroll = 0;
    let endScroll = 1;
    let wrapperTop = 0;
    let joiningLines = false;
    let lastScroll = Number.NaN;
    let lastProgress = Number.NaN;
    let viewportWidth = document.documentElement.clientWidth;
    let pieces: TitlePiece[] = [];

    const cancelScroll = () => {
      cancelAnimationFrame(scrollFrame);
      scrollFrame = 0;
    };

    const draw = () => {
      const scroll = motion.matches
        ? 0
        : Math.max(startScroll, Math.min(endScroll, window.scrollY));
      if (scroll === lastScroll) return;
      lastScroll = scroll;
      const progress = motion.matches
        ? 1
        : clamp((scroll - startScroll) / (endScroll - startScroll));
      const amount = ease(progress);

      // Keep changes local: inherited custom properties on the page invalidate every card.
      if (progress !== lastProgress) {
        lastProgress = progress;
        if (subtitle.current) subtitle.current.style.opacity = String(clamp(1 - progress * 3.5));
        if (arrow.current) {
          arrow.current.style.opacity = String(clamp(1 - progress * 2));
          arrow.current.dataset.paused = String(progress >= 0.5);
        }
        if (contentHero.current)
          contentHero.current.style.opacity = String(
            motion.matches ? 1 : clamp((progress - 0.2) / 0.8),
          );
        if (note.current) {
          note.current.style.opacity = String(motion.matches ? 1 : clamp((progress - 0.65) / 0.35));
          note.current.style.transform = `translate3d(0, ${motion.matches ? 0 : scroll - endScroll}px, 0)`;
        }
      }

      pieces.forEach((piece, index) => {
        // Make horizontal room before joining the two mobile phrases on one baseline.
        const shape = joiningLines && index > 0 ? ease(clamp(progress / 0.55)) : amount;
        const x = piece.x + (piece.endX - piece.x) * shape;
        let y = piece.y + (piece.endY - piece.y) * amount;
        if (joiningLines && index === 2) {
          const previous = pieces[1];
          const gap = (piece.y - previous.y) * (1 - ease(clamp((progress - 0.4) / 0.6)));
          y = previous.y + (previous.endY - previous.y) * amount + gap;
        }
        // Absolute positioning lets the completed heading scroll with the document.
        // Once progress reaches an endpoint, further scrolling causes no DOM writes.
        piece.element.style.transform = `translate3d(${x}px, ${y + scroll - wrapperTop}px, 0) scale(${1 + (piece.scale - 1) * shape})`;
      });
      title.current?.classList.add('is-ready');
    };

    const measure = () => {
      if (
        !wrapper.current ||
        !source.current ||
        !destination.current ||
        !title.current ||
        !intro.current ||
        !discovery.current
      )
        return;
      const y = window.scrollY;
      const wrapperBox = wrapper.current.getBoundingClientRect();
      wrapperTop = wrapperBox.top + y;
      startScroll = intro.current.getBoundingClientRect().top + y;
      const padding = parseFloat(getComputedStyle(document.documentElement).scrollPaddingTop) || 0;
      endScroll = motion.matches
        ? 0
        : Math.max(startScroll + 1, discovery.current.getBoundingClientRect().top + y - padding);
      const origins = source.current.querySelectorAll<HTMLElement>('[data-title-piece]');
      const targets = destination.current.querySelectorAll<HTMLElement>('[data-title-piece]');
      const visible = title.current.querySelectorAll<HTMLElement>('.morph-title-piece');

      // Finish all layout reads before writing font sizes (avoids read/write interleaving).
      pieces = Array.from(visible, (element, index) => {
        const first = origins[index].getBoundingClientRect();
        const last = targets[index].getBoundingClientRect();
        const endFont = parseFloat(getComputedStyle(targets[index]).fontSize);
        const firstFont = motion.matches
          ? endFont
          : parseFloat(getComputedStyle(origins[index]).fontSize);
        return {
          element,
          fontSize: firstFont,
          x: (motion.matches ? last.left : first.left) - wrapperBox.left,
          y: motion.matches ? last.top + y : first.top + y - startScroll,
          endX: last.left - wrapperBox.left,
          endY: last.top + y - endScroll,
          scale: endFont / firstFont,
        };
      });
      joiningLines =
        !motion.matches &&
        pieces[2].y > pieces[1].y + 5 &&
        Math.abs(pieces[2].endY - pieces[1].endY) < 2;
      for (const piece of pieces) piece.element.style.fontSize = `${piece.fontSize}px`;
      lastScroll = Number.NaN;
      lastProgress = Number.NaN;
    };

    const flush = () => {
      frame = 0;
      if (disposed) return;
      if (needsMeasure) {
        needsMeasure = false;
        measure();
      }
      draw();
    };
    const schedule = () => {
      if (!frame && !disposed) frame = requestAnimationFrame(flush);
    };
    const scheduleMeasure = () => {
      needsMeasure = true;
      schedule();
    };
    const resize = () => {
      const width = document.documentElement.clientWidth;
      if (width === viewportWidth) return;
      viewportWidth = width;
      cancelScroll();
      scheduleMeasure();
    };
    const motionChanged = () => {
      cancelScroll();
      scheduleMeasure();
    };

    enterAction.current = () => {
      cancelScroll();
      if (needsMeasure) flush();
      discovery.current?.focus({ preventScroll: true });
      const from = window.scrollY;
      const to = endScroll;
      if (motion.matches) {
        window.scrollTo({ top: to, behavior: 'instant' });
        return;
      }
      const started = performance.now();
      const step = (now: number) => {
        const progress = clamp((now - started) / SCROLL_DURATION_MS);
        window.scrollTo({ top: from + (to - from) * ease(progress), behavior: 'instant' });
        draw();
        if (progress < 1) scrollFrame = requestAnimationFrame(step);
      };
      scrollFrame = requestAnimationFrame(step);
    };

    // svh keeps the intro stable while the mobile address bar changes visual height.
    // Observe actual layout changes instead of remeasuring on every such resize event.
    const observer = new ResizeObserver(() => {
      cancelScroll();
      scheduleMeasure();
    });
    for (const element of [intro.current, source.current, destination.current])
      if (element) observer.observe(element);
    flush();
    void document.fonts.ready.then(() => {
      if (!disposed) scheduleMeasure();
    });
    window.addEventListener('scroll', schedule, { passive: true });
    window.addEventListener('resize', resize);
    motion.addEventListener('change', motionChanged);
    for (const event of ['wheel', 'touchstart', 'pointerdown', 'keydown'])
      window.addEventListener(event, cancelScroll, { passive: true });
    return () => {
      disposed = true;
      enterAction.current = () => {};
      cancelAnimationFrame(frame);
      cancelScroll();
      observer.disconnect();
      window.removeEventListener('scroll', schedule);
      window.removeEventListener('resize', resize);
      motion.removeEventListener('change', motionChanged);
      for (const event of ['wheel', 'touchstart', 'pointerdown', 'keydown'])
        window.removeEventListener(event, cancelScroll);
    };
  }, []);

  return {
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
    enter: () => enterAction.current(),
  };
}
