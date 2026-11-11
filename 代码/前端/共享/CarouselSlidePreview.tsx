import { useEffect, useRef, type CSSProperties, type ReactNode } from "react";
import type { HomeSlide } from "./home-content";
import "./carousel-slide.css";

/** The editor and public carousel render the same layout to keep previews faithful. */
export function CarouselSlidePreview({
  slide,
  imageUrl,
  action,
  previewViewport,
}: {
  slide: HomeSlide;
  imageUrl?: string;
  action?: ReactNode;
  previewViewport?: "mobile";
}) {
  const root = useRef<HTMLDivElement>(null);
  const copy = useRef<HTMLDivElement>(null);
  const free = slide.layout === "overlay" && slide.placement === "free";
  useEffect(() => {
    if (!free || !copy.current || !root.current) return;
    const update = () =>
      root.current?.style.setProperty(
        "--copy-height",
        `${(copy.current?.offsetHeight ?? 0) + 64}px`,
      );
    const observer = new ResizeObserver(update);
    observer.observe(copy.current);
    update();
    return () => {
      observer.disconnect();
      root.current?.style.removeProperty("--copy-height");
    };
  }, [free]);
  const style = {
    "--slide-x": `${slide.imagePositionX}%`,
    "--slide-y": `${slide.imagePositionY}%`,
    "--slide-fit": slide.imageFit,
    "--copy-x": `${slide.textPositionX ?? 0}%`,
    "--copy-y": `${slide.textPositionY ?? 50}%`,
    "--copy-x-ratio": (slide.textPositionX ?? 0) / 100,
    "--copy-y-ratio": (slide.textPositionY ?? 50) / 100,
    "--copy-width": `${slide.textWidth ?? 55}%`,
    "--title-size": `${slide.titleSize ?? 40}px`,
    "--description-size": `${slide.descriptionSize ?? 16}px`,
    "--slide-font":
      slide.fontFamily === "serif"
        ? '"Noto Serif SC", "Songti SC", SimSun, serif'
        : "inherit",
    "--title-color": slide.titleColor || "inherit",
    "--description-color": slide.descriptionColor || "inherit",
  } as CSSProperties;
  return (
    <div
      ref={root}
      className={`curated-slide layout-${slide.layout} align-${slide.textAlign} vertical-${slide.verticalAlign}${free ? " placement-free" : ""}${previewViewport === "mobile" ? " preview-mobile" : ""}`}
      style={style}
    >
      <div className="curated-image">
        {imageUrl ? (
          <img
            src={imageUrl}
            alt={slide.title}
            loading="lazy"
            decoding="async"
          />
        ) : (
          <span>选择展示图片</span>
        )}
      </div>
      <div className="curated-copy" ref={copy}>
        <div className="curated-meta">
          {slide.tradeLabel && <span>{slide.tradeLabel}</span>}
          {slide.serverLabel && <span>{slide.serverLabel}</span>}
        </div>
        <h3>{slide.title || "填写轮播标题"}</h3>
        {slide.description && <p>{slide.description}</p>}
        <div className="curated-action">
          {slide.priceLabel && <strong>{slide.priceLabel}</strong>}
          {action}
        </div>
      </div>
    </div>
  );
}
