import type { CSSProperties, ReactNode } from "react";
import type { HomeSlide } from "./home-content";
import "./carousel-slide.css";

/** The editor and public carousel render the same layout to keep previews faithful. */
export function CarouselSlidePreview({
  slide,
  imageUrl,
  action,
}: {
  slide: HomeSlide;
  imageUrl?: string;
  action?: ReactNode;
}) {
  const style = {
    "--slide-x": `${slide.imagePositionX}%`,
    "--slide-y": `${slide.imagePositionY}%`,
    "--slide-fit": slide.imageFit,
  } as CSSProperties;
  return (
    <div
      className={`curated-slide layout-${slide.layout} align-${slide.textAlign} vertical-${slide.verticalAlign}`}
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
      <div className="curated-copy">
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
