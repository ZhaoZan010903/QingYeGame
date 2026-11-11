import { useEffect, useRef, useState } from "react";
import { ArrowDown, ArrowUp, Plus, Trash2 } from "lucide-react";
import { adminApi, adminImageApi } from "../lib/admin-api";
import { productFrom } from "../lib/api";
import type { ImageItem, Product } from "../types";
import type { HomeSlide } from "../../../共享/home-content";
import { CarouselSlidePreview } from "../../../共享/CarouselSlidePreview";

export const emptySlide = (): HomeSlide => ({
  productId: "",
  title: "",
  description: "",
  tradeLabel: "",
  serverLabel: "",
  priceLabel: "",
  imagePositionX: 50,
  imagePositionY: 50,
  imageFit: "contain",
  layout: "image-right",
  textAlign: "left",
  verticalAlign: "center",
});

export function CarouselEditor({
  slides,
  onChange,
}: {
  slides: HomeSlide[];
  onChange: (slides: HomeSlide[]) => void;
}) {
  const [index, setIndex] = useState(0),
    [q, setQ] = useState("");
  const [results, setResults] = useState<Product[]>([]),
    [product, setProduct] = useState<Product>();
  const [images, setImages] = useState<ImageItem[]>([]),
    [cursor, setCursor] = useState<string | null>(null);
  const [error, setError] = useState(""),
    [busy, setBusy] = useState(false),
    [loadingImages, setLoadingImages] = useState(false);
  const [mobile, setMobile] = useState(false);
  const sequence = useRef(0),
    searchSequence = useRef(0);
  const selectedIndex = Math.min(index, Math.max(slides.length - 1, 0)),
    slide = slides[selectedIndex];
  const patch = (change: Partial<HomeSlide>) =>
    onChange(
      slides.map((s, i) => (i === selectedIndex ? { ...s, ...change } : s)),
    );
  useEffect(() => {
    const current = ++sequence.current;
    setProduct(undefined);
    setImages([]);
    setCursor(null);
    setError("");
    setLoadingImages(false);
    if (!slide?.productId) return;
    setLoadingImages(true);
    void adminApi
      .product(slide.productId)
      .then(async (row) => {
        const p = productFrom(row);
        const page = await adminImageApi.myImages(p.id, null, p.version);
        if (current !== sequence.current) return;
        setProduct(p);
        setImages(page.items);
        setCursor(page.nextCursor);
      })
      .catch((e) => {
        if (current === sequence.current)
          setError(e instanceof Error ? e.message : "商品读取失败");
      })
      .finally(() => {
        if (current === sequence.current) setLoadingImages(false);
      });
    return () => {
      sequence.current++;
    };
  }, [slide?.productId]);
  async function search() {
    const current = ++searchSequence.current;
    setBusy(true);
    setError("");
    try {
      const page = await adminApi.products(
        new URLSearchParams({ q: q.trim(), status: "published", limit: "12" }),
      );
      if (current === searchSequence.current) setResults(page.items);
    } catch (e) {
      if (current === searchSequence.current)
        setError(e instanceof Error ? e.message : "搜索失败");
    } finally {
      if (current === searchSequence.current) setBusy(false);
    }
  }
  async function more() {
    if (!cursor || !product || loadingImages) return;
    const current = sequence.current;
    setLoadingImages(true);
    try {
      const page = await adminImageApi.myImages(
        product.id,
        cursor,
        product.version,
      );
      if (current !== sequence.current) return;
      setImages((old) => [
        ...old,
        ...page.items.filter((image) => !old.some((i) => i.id === image.id)),
      ]);
      setCursor(page.nextCursor);
    } catch (e) {
      if (current === sequence.current)
        setError(e instanceof Error ? e.message : "加载失败");
    } finally {
      if (current === sequence.current) setLoadingImages(false);
    }
  }
  function move(delta: number) {
    const next = selectedIndex + delta;
    if (next < 0 || next >= slides.length) return;
    const result = [...slides];
    [result[selectedIndex], result[next]] = [
      result[next],
      result[selectedIndex],
    ];
    onChange(result);
    setIndex(next);
  }
  const image =
    images.find((i) => i.id === slide?.imageId) ??
    (!slide?.imageId ? images[0] : undefined);
  // Media IDs come from the authenticated product image picker; no external URL is accepted.
  const imageUrl =
    image?.url ||
    (slide?.imageId
      ? `/api/v1/media/${encodeURIComponent(slide.imageId)}/preview`
      : undefined);
  return (
    <section className="carousel-editor">
      <div className="section-heading">
        <div>
          <h3>手工轮播</h3>
          <p className="field-hint">
            选择关联商品，填写独立文案并调整布局。预览不会自动发布。
          </p>
        </div>
        <button
          type="button"
          className="button-outline"
          disabled={slides.length >= 12}
          onClick={() => {
            onChange([...slides, emptySlide()]);
            setIndex(slides.length);
          }}
        >
          <Plus size={16} />
          添加轮播
        </button>
      </div>
      <div className="carousel-slide-tabs">
        {slides.map((s, i) => (
          <button
            key={i}
            type="button"
            className={selectedIndex === i ? "active" : ""}
            onClick={() => setIndex(i)}
          >
            {i + 1} · {s.title || "未填写标题"}
          </button>
        ))}
      </div>
      {!slide ? (
        <div className="admin-empty">
          尚未配置轮播。添加后填写内容，保存首页才会展示。
        </div>
      ) : (
        <div className="carousel-edit-layout">
          <div className="carousel-slide-fields">
            <div className="reorder-controls">
              <button
                type="button"
                className="button-outline"
                disabled={selectedIndex === 0}
                onClick={() => move(-1)}
              >
                <ArrowUp size={14} />
                前移
              </button>
              <button
                type="button"
                className="button-outline"
                disabled={selectedIndex === slides.length - 1}
                onClick={() => move(1)}
              >
                <ArrowDown size={14} />
                后移
              </button>
              <button
                type="button"
                className="text-button danger-text"
                onClick={() => {
                  onChange(slides.filter((_, i) => i !== selectedIndex));
                  setIndex(Math.max(selectedIndex - 1, 0));
                }}
              >
                <Trash2 size={14} />
                移除本张
              </button>
            </div>
            <div className="subform-card">
              <label className="form-field">
                关联商品
                <input
                  value={q}
                  onChange={(e) => setQ(e.target.value)}
                  placeholder="输入商品编号或关键词"
                  onKeyDown={(e) => {
                    if (e.key === "Enter") {
                      e.preventDefault();
                      void search();
                    }
                  }}
                />
              </label>
              <button
                type="button"
                className="button-outline"
                disabled={busy}
                onClick={() => void search()}
              >
                {busy ? "查询中…" : "查找已上架商品"}
              </button>
              {product && (
                <p className="field-hint">
                  当前：{product.code} · {product.title}
                  {product.status !== "published"
                    ? "（已停止展示，轮播将隐藏）"
                    : ""}
                </p>
              )}
              <div className="carousel-search-results">
                {results.map((p) => (
                  <button
                    key={p.id}
                    type="button"
                    onClick={() =>
                      patch({ productId: p.id, imageId: undefined })
                    }
                  >
                    <strong>{p.code}</strong>
                    <span>{p.title}</span>
                  </button>
                ))}
              </div>
            </div>
            <label className="form-field">
              大标题
              <input
                required
                maxLength={100}
                value={slide.title}
                placeholder="例如：月白极光 · 珍藏衣橱"
                onChange={(e) => patch({ title: e.target.value })}
              />
            </label>
            <label className="form-field">
              介绍文字
              <textarea
                maxLength={300}
                rows={3}
                value={slide.description}
                placeholder="用简短介绍突出账号特点"
                onChange={(e) => patch({ description: e.target.value })}
              />
            </label>
            <div className="admin-grid">
              <label className="form-field">
                租售说明
                <input
                  maxLength={24}
                  value={slide.tradeLabel}
                  placeholder="账号出售 / 账号出租"
                  onChange={(e) => patch({ tradeLabel: e.target.value })}
                />
              </label>
              <label className="form-field">
                区服说明
                <input
                  maxLength={60}
                  value={slide.serverLabel}
                  onChange={(e) => patch({ serverLabel: e.target.value })}
                />
              </label>
              <label className="form-field">
                展示价格
                <input
                  maxLength={60}
                  value={slide.priceLabel}
                  placeholder="例如 ¥ 350 / 月"
                  onChange={(e) => patch({ priceLabel: e.target.value })}
                />
              </label>
            </div>
            <p className="field-hint">
              轮播文案独立保存。调整商品价格后，请同步核对这里的展示价格。
            </p>
            <label className="form-field">
              图文布局
              <select
                value={slide.layout}
                onChange={(e) =>
                  patch({ layout: e.target.value as HomeSlide["layout"] })
                }
              >
                <option value="image-right">文字在左 · 图片在右</option>
                <option value="image-left">图片在左 · 文字在右</option>
                <option value="overlay">图片铺满 · 文字叠加</option>
              </select>
            </label>
            <div className="admin-grid">
              <label className="form-field">
                文字横向位置
                <select
                  value={slide.textAlign}
                  onChange={(e) =>
                    patch({
                      textAlign: e.target.value as HomeSlide["textAlign"],
                    })
                  }
                >
                  <option value="left">靠左</option>
                  <option value="center">居中</option>
                  <option value="right">靠右</option>
                </select>
              </label>
              <label className="form-field">
                文字纵向位置
                <select
                  value={slide.verticalAlign}
                  onChange={(e) =>
                    patch({
                      verticalAlign: e.target
                        .value as HomeSlide["verticalAlign"],
                    })
                  }
                >
                  <option value="top">顶部</option>
                  <option value="center">居中</option>
                  <option value="bottom">底部</option>
                </select>
              </label>
            </div>
          </div>
          <div className="carousel-preview-column">
            <div className="section-heading">
              <strong>实时预览</strong>
              <div className="zone-tabs">
                <button
                  type="button"
                  className={!mobile ? "active" : ""}
                  onClick={() => setMobile(false)}
                >
                  电脑
                </button>
                <button
                  type="button"
                  className={mobile ? "active" : ""}
                  onClick={() => setMobile(true)}
                >
                  手机
                </button>
              </div>
            </div>
            <div className={`carousel-preview ${mobile ? "mobile" : ""}`}>
              <CarouselSlidePreview
                slide={slide}
                imageUrl={imageUrl}
                action={<span className="wire-cta">查看账号 →</span>}
              />
            </div>
            <div className="subform-card">
              <h3>展示图片</h3>
              <div className="carousel-image-picker">
                {images.map((img, i) => (
                  <button
                    key={img.id}
                    type="button"
                    aria-label={`选择轮播图片 ${i + 1}`}
                    aria-pressed={(slide.imageId ?? images[0]?.id) === img.id}
                    onClick={() => patch({ imageId: img.id })}
                  >
                    <img
                      src={img.thumbnailUrl || img.url}
                      loading="lazy"
                      alt=""
                    />
                  </button>
                ))}
              </div>
              {cursor && (
                <button
                  type="button"
                  className="text-button"
                  disabled={loadingImages}
                  onClick={() => void more()}
                >
                  更多图片
                </button>
              )}
              {loadingImages && <p role="status">正在加载图片…</p>}
              <label className="form-field">
                图片适配
                <select
                  value={slide.imageFit}
                  onChange={(e) =>
                    patch({ imageFit: e.target.value as HomeSlide["imageFit"] })
                  }
                >
                  <option value="contain">完整展示</option>
                  <option value="cover">铺满裁剪</option>
                </select>
              </label>
              <label className="form-field">
                图片水平位置：{slide.imagePositionX}%
                <input
                  type="range"
                  min={0}
                  max={100}
                  value={slide.imagePositionX}
                  onChange={(e) =>
                    patch({ imagePositionX: Number(e.target.value) })
                  }
                />
              </label>
              <label className="form-field">
                图片垂直位置：{slide.imagePositionY}%
                <input
                  type="range"
                  min={0}
                  max={100}
                  value={slide.imagePositionY}
                  onChange={(e) =>
                    patch({ imagePositionY: Number(e.target.value) })
                  }
                />
              </label>
            </div>
          </div>
        </div>
      )}
      {error && (
        <p className="form-error" role="alert">
          {error}
        </p>
      )}
    </section>
  );
}
