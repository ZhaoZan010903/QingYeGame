import { useEffect, useLayoutEffect, useRef, useState } from 'react';
import { createPortal } from 'react-dom';
import { ArrowRight, Search, X } from 'lucide-react';
import '../catalog-search.css';

const clamp = (value: number) => Math.max(0, Math.min(1, value));
const mix = (from: number, to: number, progress: number) => from + (to - from) * progress;
const ease = (value: number) => value * value * (3 - 2 * value);
const COMPACT_AT = 0.78;
const MOBILE_TEXT_FADE_END = 0.24;

type SearchGeometry = {
  origin: { left: number; top: number; width: number; height: number };
  dock: { left: number; top: number; width: number; height: number };
  distance: number;
  navBottom: number;
  viewportWidth: number;
  narrow: boolean;
};

export function CatalogSearch({
  query,
  onChange,
  onSearch,
  onClear,
}: {
  query: string;
  onChange: (value: string) => void;
  onSearch: () => void;
  onClear: () => void;
}) {
  const origin = useRef<HTMLDivElement>(null);
  const form = useRef<HTMLFormElement>(null);
  const icon = useRef<SVGSVGElement>(null);
  const fields = useRef<HTMLDivElement>(null);
  const submit = useRef<HTMLButtonElement>(null);
  const input = useRef<HTMLInputElement>(null);
  const toggle = useRef<HTMLButtonElement>(null);
  const [layer, setLayer] = useState<HTMLElement | null>(null);
  const [expanded, setExpanded] = useState(false);
  const [compact, setCompact] = useState(false);
  const compactRef = useRef(false);
  const mobileTransition = useRef(false);
  const collapsedMode = useRef(false);
  const restoreFocus = useRef(false);
  const restoreInputFocus = useRef(false);
  const expandedRef = useRef(false);
  const scheduleMotion = useRef<() => void>(() => {});
  expandedRef.current = expanded;

  useLayoutEffect(() => {
    setLayer(document.getElementById('catalog-search-layer') || document.body);
  }, []);

  useLayoutEffect(() => {
    if (!layer) return;
    const dock = document.getElementById('catalog-search-dock');
    const nav = document.querySelector('.site-nav');
    const motion = matchMedia('(prefers-reduced-motion: reduce)');
    let frame = 0;
    let needsMeasure = true;
    let geometry: SearchGeometry | undefined;
    let lastFrame = '';
    let layout = '';

    // Layout reads happen only on mount or resize, never in a normal scroll frame.
    const measure = (): SearchGeometry | undefined => {
      if (!origin.current || !dock) return;
      const start = origin.current.getBoundingClientRect();
      const end = dock.getBoundingClientRect();
      const top = start.top + window.scrollY;
      return {
        origin: { left: start.left, top, width: start.width, height: start.height },
        dock: { left: end.left, top: end.top, width: end.width, height: end.height },
        distance: Math.max(1, top - end.top),
        navBottom: nav?.getBoundingClientRect().bottom ?? end.bottom,
        viewportWidth: innerWidth,
        narrow: end.width < 100,
      };
    };

    const render = () => {
      frame = 0;
      const element = form.current;
      if (!element || !icon.current || !fields.current || !submit.current) return;
      const measured = needsMeasure;
      if (needsMeasure) {
        geometry = measure();
        needsMeasure = false;
      }
      if (!geometry) return;
      const { origin: start, dock: destination, distance, narrow } = geometry;
      const scrollTop = window.scrollY;
      const raw = clamp(scrollTop / distance);
      const progress = motion.matches ? (raw >= 1 ? 1 : 0) : ease(raw);
      const dockAmount = motion.matches ? progress : raw;
      collapsedMode.current = narrow && dockAmount >= MOBILE_TEXT_FADE_END;
      mobileTransition.current = narrow && dockAmount > 0.01;
      const isCompact = collapsedMode.current && !expandedRef.current;
      const isExpanded = mobileTransition.current && expandedRef.current;
      const focused = document.activeElement === input.current;
      const key = `${progress}:${isExpanded}:${focused}:${motion.matches ? scrollTop : ''}`;
      // Once docked, further scrolling requires no style writes or React updates.
      if (!measured && key === lastFrame) return;
      lastFrame = key;
      if (compactRef.current !== isCompact) {
        if (!isCompact && document.activeElement === toggle.current)
          restoreInputFocus.current = true;
        compactRef.current = isCompact;
        setCompact(isCompact);
      }
      const top = motion.matches
        ? raw >= 1
          ? destination.top
          : start.top - scrollTop
        : mix(start.top, destination.top, progress);
      const box = isExpanded
        ? { left: 12, top: geometry.navBottom + 10, width: geometry.viewportWidth - 24, height: 56 }
        : {
            left: mix(start.left, destination.left, progress),
            top,
            width: mix(start.width, destination.width, progress),
            height: mix(start.height, destination.height, progress),
          };

      // FLIP: only three resting layouts. Interpolate their visual bounds with transforms.
      // The text fades across the layout switch; inverse scaling keeps glyphs undistorted.
      const nextLayout = isExpanded
        ? 'expanded'
        : dockAmount >= (narrow ? COMPACT_AT : 0.5)
          ? 'dock'
          : 'origin';
      const base = nextLayout === 'expanded' ? box : nextLayout === 'dock' ? destination : start;
      if (measured || nextLayout !== layout) {
        layout = nextLayout;
        element.dataset.layout = layout;
        element.style.width = `${base.width}px`;
        element.style.height = `${base.height}px`;
      }
      const scaleX = box.width / base.width;
      const scaleY = box.height / base.height;
      element.style.transform = `translate3d(${box.left}px,${box.top}px,0) scale(${scaleX},${scaleY})`;
      const radius = isExpanded ? 10 : mix(12, 21, progress);
      element.style.borderRadius = `${radius / scaleX}px / ${radius / scaleY}px`;

      const iconSize = 18;
      const iconLeft =
        narrow && !isExpanded
          ? mix(
              16,
              (box.width - iconSize) / 2,
              ease(
                clamp((dockAmount - MOBILE_TEXT_FADE_END) / (COMPACT_AT - MOBILE_TEXT_FADE_END)),
              ),
            )
          : mix(16, 12, isExpanded ? 0 : progress);
      icon.current.style.transform = `translate3d(${iconLeft / scaleX}px,${(box.height - iconSize) / 2 / scaleY}px,0) scale(${1 / scaleX},${1 / scaleY})`;
      const fieldLeft = layout === 'dock' ? 38 : 44;
      const fieldTop = layout === 'dock' ? 4 : 7;
      const fieldHeight = base.height - fieldTop * 2;
      const fieldOffsetY = (box.height - fieldHeight) / 2 - fieldTop * scaleY;
      fields.current.style.transform = `translate3d(${fieldLeft * (1 / scaleX - 1)}px,${fieldOffsetY / scaleY}px,0) scale(${1 / scaleX},${1 / scaleY})`;
      submit.current.style.transform = `translate3d(${Math.min(0, box.width - base.width)}px,0,0)`;
      const textOpacity =
        isExpanded || motion.matches || (focused && !isCompact)
          ? isCompact
            ? 0
            : 1
          : narrow
            ? 1 - ease(clamp(raw / MOBILE_TEXT_FADE_END))
            : raw < 0.5
              ? 1 - ease(clamp(raw / 0.45))
              : ease(clamp((raw - 0.55) / 0.45));
      fields.current.style.opacity = String(textOpacity);
      const setFlag = (flag: string, value: boolean) => {
        const text = String(value);
        if (element.dataset[flag] !== text) element.dataset[flag] = text;
      };
      setFlag('narrow', narrow);
      setFlag('expanded', isExpanded);
      setFlag('docked', raw >= 0.999);
      element.style.visibility = 'visible';
      if (expandedRef.current && !mobileTransition.current) setExpanded(false);
      // Keep a focused mobile input usable while its resting state becomes an icon.
      if (isCompact && focused) setExpanded(true);
    };
    const schedule = () => {
      if (!frame) frame = requestAnimationFrame(render);
    };
    const resize = () => {
      needsMeasure = true;
      schedule();
    };
    scheduleMotion.current = schedule;
    const observer = new ResizeObserver(resize);
    if (origin.current) observer.observe(origin.current);
    if (origin.current?.parentElement) observer.observe(origin.current.parentElement);
    if (dock) observer.observe(dock);
    if (nav) observer.observe(nav);
    window.addEventListener('scroll', schedule, { passive: true });
    window.addEventListener('resize', resize);
    motion.addEventListener('change', resize);
    render();
    return () => {
      cancelAnimationFrame(frame);
      observer.disconnect();
      window.removeEventListener('scroll', schedule);
      window.removeEventListener('resize', resize);
      motion.removeEventListener('change', resize);
      scheduleMotion.current = () => {};
    };
  }, [layer]);

  useLayoutEffect(() => {
    scheduleMotion.current();
  }, [expanded]);

  const focusSearch = () => {
    if (document.querySelector('dialog[open]')) return;
    if (compactRef.current || mobileTransition.current) setExpanded(true);
    requestAnimationFrame(() => input.current?.focus({ preventScroll: true }));
  };

  useLayoutEffect(() => {
    if (expanded && !compact) input.current?.focus({ preventScroll: true });
    if (!compact && restoreInputFocus.current) {
      restoreInputFocus.current = false;
      input.current?.focus({ preventScroll: true });
    }
    if (!expanded && restoreFocus.current && compact === collapsedMode.current) {
      restoreFocus.current = false;
      (compact ? toggle.current : input.current)?.focus({ preventScroll: true });
    }
  }, [expanded, compact]);

  const closeSearch = () => {
    restoreFocus.current = true;
    input.current?.blur();
    setExpanded(false);
  };

  useEffect(() => {
    const focus = () => focusSearch();
    const outside = (event: PointerEvent) => {
      if (expandedRef.current && !form.current?.contains(event.target as Node)) setExpanded(false);
    };
    window.addEventListener('qingye:focus-catalog-search', focus);
    document.addEventListener('pointerdown', outside);
    return () => {
      window.removeEventListener('qingye:focus-catalog-search', focus);
      document.removeEventListener('pointerdown', outside);
    };
  }, []);

  return (
    <>
      <div className="catalog-search-origin" ref={origin} />
      {layer &&
        createPortal(
          <form
            ref={form}
            className="catalog-floating-search"
            data-compact={compact}
            role="search"
            aria-label="搜索账号集市"
            onSubmit={(event) => {
              event.preventDefault();
              onSearch();
              if (expanded) {
                input.current?.blur();
                setExpanded(false);
              }
            }}
            onKeyDown={(event) => {
              if (event.key === 'Escape' && expanded) {
                event.preventDefault();
                closeSearch();
              }
            }}
          >
            <Search ref={icon} className="catalog-floating-icon" aria-hidden="true" />
            <div
              ref={fields}
              className="catalog-floating-fields"
              aria-hidden={compact || undefined}
            >
              <input
                ref={input}
                id="catalog-query"
                aria-label="搜索商品编号、标题或区服"
                placeholder="搜索编号、标题或区服"
                autoComplete="off"
                onFocus={() => scheduleMotion.current()}
                onBlur={() => scheduleMotion.current()}
                tabIndex={compact ? -1 : 0}
                value={query}
                onChange={(event) => onChange(event.target.value)}
              />
              {query && (
                <button
                  type="button"
                  className="catalog-floating-clear"
                  aria-label="清空搜索"
                  tabIndex={compact ? -1 : 0}
                  onClick={() => {
                    onClear();
                    input.current?.focus({ preventScroll: true });
                  }}
                >
                  <X />
                </button>
              )}
              <button
                ref={submit}
                className="catalog-floating-submit"
                type="submit"
                aria-label="搜索列表"
                tabIndex={compact ? -1 : 0}
              >
                <span>搜索</span>
                <ArrowRight aria-hidden="true" />
              </button>
              {expanded && (
                <button
                  type="button"
                  className="catalog-floating-close"
                  aria-label="收起搜索"
                  onClick={closeSearch}
                >
                  <X />
                </button>
              )}
            </div>
            <button
              ref={toggle}
              className="catalog-floating-expand"
              type="button"
              aria-label="展开账号搜索"
              aria-expanded={expanded}
              aria-controls="catalog-query"
              tabIndex={compact ? 0 : -1}
              onClick={focusSearch}
              hidden={!compact}
            />
          </form>,
          layer,
        )}
    </>
  );
}
