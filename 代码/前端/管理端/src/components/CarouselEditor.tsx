import {
  useEffect,
  useLayoutEffect,
  useId,
  useRef,
  useState,
  type PointerEvent,
} from "react";
import {
  ArrowDown,
  ArrowUp,
  Check,
  ImageIcon,
  Move,
  Plus,
  Search,
  Trash2,
} from "lucide-react";
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
  placement: "flow",
  textPositionX: 5,
  textPositionY: 50,
  textWidth: 50,
  titleSize: 42,
  descriptionSize: 16,
  fontFamily: "sans",
  titleColor: "",
  descriptionColor: "",
});
export const limitSlideText = (value: string, max: number) =>
  Array.from(value).slice(0, max).join("");
export const boundedPercent = (value: number) =>
  Math.max(0, Math.min(100, Math.round(value)));
export function previewGeometry(
  availableWidth: number,
  logicalHeight: number,
  mobile: boolean,
) {
  const width = mobile ? 390 : 1320;
  const scale = Math.min(1, availableWidth / width);
  return { width, scale, height: logicalHeight * scale };
}
export const textDragRange = (frame: number, copy: number, scale: number) =>
  frame - copy - 64 * scale;
interface DragState {
  pointer: number;
  x: number;
  y: number;
  fromX: number;
  fromY: number;
  width: number;
  height: number;
  kind: "text" | "image";
}

export function CarouselEditor({
  slides,
  onChange,
  maxSlides = 12,
}: {
  slides: HomeSlide[];
  onChange: (slides: HomeSlide[]) => void;
  maxSlides?: number;
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
  const [narrow, setNarrow] = useState(false);
  const [mobile, setMobile] = useState(false),
    [picker, setPicker] = useState(false),
    [imagePicker, setImagePicker] = useState(false),
    [editTarget, setEditTarget] = useState<"none" | "text" | "image">("none");
  const isMobile = mobile || narrow;
  useEffect(() => {
    const media = window.matchMedia("(max-width: 640px)");
    const update = () => {
      setNarrow(media.matches);
      setEditTarget("none");
    };
    update();
    media.addEventListener("change", update);
    return () => media.removeEventListener("change", update);
  }, []);
  const sequence = useRef(0),
    searchSequence = useRef(0),
    preview = useRef<HTMLDivElement>(null),
    previewStage = useRef<HTMLDivElement>(null),
    drag = useRef<DragState | undefined>(undefined);
  const selectedIndex = Math.min(index, Math.max(slides.length - 1, 0)),
    slide = slides[selectedIndex];
  const logicalWidth = isMobile ? 390 : 1320;
  const [previewSize, setPreviewSize] = useState({ scale: 1, height: 382 });
  useLayoutEffect(() => {
    const viewport = preview.current,
      stage = previewStage.current;
    if (!viewport || !stage) return;
    let animation = 0;
    // Measure unscaled height; the outer box must reserve the entire transformed content.
    const measure = () => {
      if (!viewport.clientWidth) return;
      const geometry = previewGeometry(
        viewport.clientWidth,
        stage.offsetHeight,
        isMobile,
      );
      const height =
        geometry.height + viewport.offsetHeight - viewport.clientHeight;
      setPreviewSize((old) =>
        old.scale === geometry.scale && Math.abs(old.height - height) < 0.1
          ? old
          : { scale: geometry.scale, height },
      );
    };
    const observer = new ResizeObserver(() => {
      cancelAnimationFrame(animation);
      animation = requestAnimationFrame(measure);
    });
    observer.observe(viewport);
    observer.observe(stage);
    measure();
    return () => {
      observer.disconnect();
      cancelAnimationFrame(animation);
    };
  }, [isMobile, !!slide]);
  const patch = (change: Partial<HomeSlide>) =>
    onChange(
      slides.map((value, i) =>
        i === selectedIndex ? { ...value, ...change } : value,
      ),
    );
  useEffect(() => {
    setPicker(!slide?.productId);
    setImagePicker(false);
    setEditTarget("none");
    setQ("");
  }, [selectedIndex]);
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
        const next = productFrom(row);
        if (current !== sequence.current) return;
        setProduct(next);
        const page = await adminImageApi.myImages(next.id, null, next.version);
        if (current !== sequence.current) return;
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
  useEffect(() => {
    if (!picker) return;
    const current = ++searchSequence.current;
    setBusy(true);
    const timer = setTimeout(() => {
      void adminApi
        .products(
          new URLSearchParams({
            q: q.trim(),
            status: "published",
            limit: "12",
          }),
        )
        .then((page) => {
          if (current === searchSequence.current) setResults(page.items);
        })
        .catch((e) => {
          if (current === searchSequence.current)
            setError(e instanceof Error ? e.message : "搜索失败");
        })
        .finally(() => {
          if (current === searchSequence.current) setBusy(false);
        });
    }, 250);
    return () => {
      clearTimeout(timer);
      searchSequence.current++;
    };
  }, [q, picker]);
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
        ...page.items.filter(
          (image) => !old.some((value) => value.id === image.id),
        ),
      ]);
      setCursor(page.nextCursor);
    } catch (e) {
      if (current === sequence.current)
        setError(e instanceof Error ? e.message : "图片加载失败");
    } finally {
      if (current === sequence.current) setLoadingImages(false);
    }
  }
  function move(delta: number) {
    const next = selectedIndex + delta;
    if (next < 0 || next >= slides.length) return;
    const values = [...slides];
    [values[selectedIndex], values[next]] = [
      values[next],
      values[selectedIndex],
    ];
    onChange(values);
    setIndex(next);
  }
  function beginDrag(event: PointerEvent<HTMLDivElement>) {
    if (!slide || editTarget === "none" || !preview.current) return;
    const frame = preview.current.querySelector<HTMLElement>(".curated-slide"),
      copy = preview.current.querySelector<HTMLElement>(".curated-copy"),
      picture = preview.current.querySelector<HTMLElement>(".curated-image");
    if (!frame || !copy || !picture) return;
    if (
      editTarget === "text" &&
      (isMobile ||
        getComputedStyle(copy).position !== "absolute" ||
        slide.layout !== "overlay" ||
        slide.placement !== "free")
    )
      return;
    const rect = (
      editTarget === "text" ? frame : picture
    ).getBoundingClientRect();
    let width = rect.width,
      height = rect.height;
    if (editTarget === "text") {
      const block = copy.getBoundingClientRect();
      const scale = rect.width / frame.offsetWidth;
      width = textDragRange(rect.width, block.width, scale);
      height = textDragRange(rect.height, block.height, scale);
    } else {
      const image = picture.querySelector("img");
      if (image?.naturalWidth && image.naturalHeight) {
        const scale =
          slide.imageFit === "cover"
            ? Math.max(
                rect.width / image.naturalWidth,
                rect.height / image.naturalHeight,
              )
            : Math.min(
                rect.width / image.naturalWidth,
                rect.height / image.naturalHeight,
              );
        width -= image.naturalWidth * scale;
        height -= image.naturalHeight * scale;
      }
    }
    drag.current = {
      pointer: event.pointerId,
      x: event.clientX,
      y: event.clientY,
      fromX:
        editTarget === "text"
          ? (slide.textPositionX ?? 5)
          : slide.imagePositionX,
      fromY:
        editTarget === "text"
          ? (slide.textPositionY ?? 50)
          : slide.imagePositionY,
      width,
      height,
      kind: editTarget,
    };
    event.currentTarget.setPointerCapture(event.pointerId);
    event.preventDefault();
  }
  function moveDrag(event: PointerEvent<HTMLDivElement>) {
    const current = drag.current;
    if (!current || current.pointer !== event.pointerId) return;
    const x = boundedPercent(
        current.fromX +
          (Math.abs(current.width) > 1
            ? ((event.clientX - current.x) / current.width) * 100
            : 0),
      ),
      y = boundedPercent(
        current.fromY +
          (Math.abs(current.height) > 1
            ? ((event.clientY - current.y) / current.height) * 100
            : 0),
      );
    patch(
      current.kind === "text"
        ? { textPositionX: x, textPositionY: y }
        : { imagePositionX: x, imagePositionY: y },
    );
  }
  function endDrag(event: PointerEvent<HTMLDivElement>) {
    if (drag.current?.pointer === event.pointerId) {
      drag.current = undefined;
      if (event.currentTarget.hasPointerCapture(event.pointerId))
        event.currentTarget.releasePointerCapture(event.pointerId);
    }
  }
  const image =
    images.find((value) => value.id === slide?.imageId) ??
    (!slide?.imageId ? images[0] : undefined);
  const imageUrl =
    image?.url ||
    (slide?.imageId
      ? `/api/v1/media/${encodeURIComponent(slide.imageId)}/preview`
      : undefined);
  return (
    <section className="carousel-editor">
      <div className="section-heading">
        <div>
          <h3>轮播内容</h3>
          <p className="field-hint">
            选择商品 → 填写文案 → 调整展示，保存首页后生效。
          </p>
        </div>
        <button
          type="button"
          className="button-outline"
          disabled={slides.length >= maxSlides}
          onClick={() => {
            onChange([...slides, emptySlide()]);
            setIndex(slides.length);
            setPicker(true);
          }}
        >
          <Plus size={16} />
          添加轮播{" "}
          <small>
            {slides.length}/{maxSlides}
          </small>
        </button>
      </div>
      {slides.length > maxSlides && (
        <p role="alert" className="admin-warning">
          现有轮播超过设置上限，请移除多余轮播或提高上限后保存。
        </p>
      )}
      <div className="carousel-slide-tabs">
        {slides.map((value, i) => (
          <button
            key={i}
            type="button"
            className={selectedIndex === i ? "active" : ""}
            onClick={() => setIndex(i)}
          >
            {i + 1} · {value.title || "未填写标题"}
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
                  setIndex(Math.max(0, selectedIndex - 1));
                }}
              >
                <Trash2 size={14} />
                移除
              </button>
            </div>
            <section className="carousel-product-picker">
              <div className="section-heading">
                <h4>1. 关联商品</h4>
                <button
                  type="button"
                  className="text-button"
                  onClick={() => setPicker(!picker)}
                >
                  {picker
                    ? "收起选择"
                    : slide.productId
                      ? "更换商品"
                      : "选择商品"}
                </button>
              </div>
              {product && (
                <div className="carousel-selected-product">
                  <img
                    src={
                      product.images[0]?.thumbnailUrl || product.images[0]?.url
                    }
                    alt=""
                  />
                  <div>
                    <span>
                      <Check size={14} />
                      已选 {product.code}
                    </span>
                    <strong>{product.title}</strong>
                    <small>
                      {product.mode === "sale" ? "账号出售" : "账号租赁"} ·{" "}
                      {product.server} ·{" "}
                      {product.price === null
                        ? "咨询报价"
                        : `¥ ${product.price}`}
                    </small>
                    {product.status !== "published" && (
                      <small className="danger-text">
                        商品已停止展示，请更换关联商品。
                      </small>
                    )}
                  </div>
                </div>
              )}
              {!product && slide.productId && (
                <p className="field-hint">
                  {loadingImages
                    ? "正在读取关联商品…"
                    : "关联商品暂不可读取，可更换后重试。"}
                </p>
              )}
              {(picker || !slide.productId) && (
                <div className="carousel-search-panel">
                  <label className="dictionary-search">
                    <Search size={17} />
                    <input
                      value={q}
                      onChange={(event) => setQ(event.target.value)}
                      onKeyDown={(event) => {
                        if (event.key === "Enter") event.preventDefault();
                      }}
                      placeholder="搜索商品编号、标题或区服"
                      aria-label="查找关联商品"
                    />
                  </label>
                  <p className="field-hint">
                    {busy
                      ? "正在查找已上架商品…"
                      : "选择后保留当前轮播文案，可继续单独编辑。"}
                  </p>
                  <div className="carousel-search-results">
                    {results.map((value) => {
                      const duplicate = slides.some(
                        (existing, i) =>
                          i !== selectedIndex &&
                          existing.productId === value.id,
                      );
                      return (
                        <button
                          key={value.id}
                          type="button"
                          disabled={duplicate}
                          onClick={() => {
                            patch({ productId: value.id, imageId: undefined });
                            setPicker(false);
                          }}
                        >
                          <img
                            src={
                              value.images[0]?.thumbnailUrl ||
                              value.images[0]?.url
                            }
                            alt=""
                            loading="lazy"
                          />
                          <div>
                            <strong>
                              {value.code}
                              {duplicate ? " · 已在轮播中" : ""}
                            </strong>
                            <span>{value.title}</span>
                            <small>
                              {value.mode === "sale" ? "出售" : "租赁"} ·{" "}
                              {value.server}
                            </small>
                          </div>
                        </button>
                      );
                    })}
                    {!busy && !results.length && (
                      <p className="admin-empty">没有匹配的已上架商品。</p>
                    )}
                  </div>
                </div>
              )}
            </section>
            <section className="carousel-copy-fields">
              <h4>2. 轮播文案</h4>
              <label className="form-field">
                大标题
                <input
                  required
                  maxLength={200}
                  value={slide.title}
                  placeholder="填写简洁醒目的轮播标题"
                  onChange={(e) =>
                    patch({ title: limitSlideText(e.target.value, 100) })
                  }
                />
              </label>
              <label className="form-field">
                介绍文字
                <textarea
                  aria-label="介绍文字"
                  rows={3}
                  maxLength={600}
                  value={slide.description}
                  placeholder="简短介绍账号亮点"
                  onChange={(e) =>
                    patch({ description: limitSlideText(e.target.value, 300) })
                  }
                />
              </label>
              <div className="admin-grid">
                <label className="form-field">
                  租售说明
                  <input
                    maxLength={48}
                    value={slide.tradeLabel}
                    placeholder="账号出售 / 账号租赁"
                    onChange={(e) =>
                      patch({ tradeLabel: limitSlideText(e.target.value, 24) })
                    }
                  />
                </label>
                <label className="form-field">
                  区服
                  <input
                    maxLength={120}
                    value={slide.serverLabel}
                    onChange={(e) =>
                      patch({ serverLabel: limitSlideText(e.target.value, 60) })
                    }
                  />
                </label>
                <label className="form-field">
                  展示价格
                  <input
                    maxLength={120}
                    value={slide.priceLabel}
                    placeholder="例如 ¥ 450 / 月"
                    onChange={(e) =>
                      patch({ priceLabel: limitSlideText(e.target.value, 60) })
                    }
                  />
                </label>
              </div>
              <p className="field-hint">
                文案与商品资料独立保存，请核对展示价格。按钮统一为“查看详情”。
              </p>
            </section>
          </div>
          <div className="carousel-preview-column">
            <div className="section-heading">
              <h4>3. 展示预览</h4>
              <div className="zone-tabs">
                <button
                  type="button"
                  disabled={narrow}
                  className={!isMobile ? "active" : ""}
                  onClick={() => {
                    setMobile(false);
                    setEditTarget("none");
                  }}
                >
                  电脑
                </button>
                <button
                  type="button"
                  className={isMobile ? "active" : ""}
                  onClick={() => {
                    setMobile(true);
                    setEditTarget("none");
                  }}
                >
                  手机
                </button>
              </div>
            </div>
            <label className="form-field">
              图文布局
              <select
                aria-label="图文布局"
                value={slide.layout}
                onChange={(e) => {
                  patch({
                    layout: e.target.value as HomeSlide["layout"],
                    placement: "flow",
                  });
                  setEditTarget("none");
                }}
              >
                <option value="image-right">文字在左 · 图片在右</option>
                <option value="image-left">图片在左 · 文字在右</option>
                <option value="overlay">图片铺满 · 文字叠加</option>
              </select>
            </label>
            {slide.layout === "overlay" && (
              <label className="agreement-check">
                <input
                  type="checkbox"
                  checked={slide.placement === "free"}
                  onChange={(e) => {
                    patch({ placement: e.target.checked ? "free" : "flow" });
                    setEditTarget("none");
                  }}
                />
                自由放置文字（电脑端）
              </label>
            )}
            <div className="preview-drag-tools">
              <button
                type="button"
                className={editTarget === "image" ? "active" : ""}
                disabled={!imageUrl}
                onClick={() =>
                  setEditTarget(editTarget === "image" ? "none" : "image")
                }
              >
                <ImageIcon size={15} />
                拖动图片焦点
              </button>
              <button
                type="button"
                className={editTarget === "text" ? "active" : ""}
                disabled={
                  mobile ||
                  slide.layout !== "overlay" ||
                  slide.placement !== "free"
                }
                onClick={() =>
                  setEditTarget(editTarget === "text" ? "none" : "text")
                }
              >
                <Move size={15} />
                拖动文字
              </button>
            </div>
            <div
              ref={preview}
              style={{ height: previewSize.height }}
              className={`carousel-preview ${isMobile ? "mobile" : "desktop"} ${editTarget !== "none" ? "preview-drag-active" : ""}`}
              onPointerDown={beginDrag}
              onPointerMove={moveDrag}
              onPointerUp={endDrag}
              onPointerCancel={endDrag}
            >
              <div
                ref={previewStage}
                className="carousel-preview-stage"
                style={{
                  width: logicalWidth,
                  transform: `scale(${previewSize.scale})`,
                }}
              >
                <CarouselSlidePreview
                  previewViewport={isMobile ? "mobile" : undefined}
                  slide={slide}
                  imageUrl={imageUrl}
                  action={<span className="preview-cta">查看详情 →</span>}
                />
              </div>
            </div>
            <p className="field-hint">
              {isMobile
                ? "手机端自动排列图文，避免自由定位遮挡。"
                : editTarget === "text"
                  ? "在预览中拖动文字，可用下方滑块精调。"
                  : editTarget === "image"
                    ? "拖动调整图片焦点；完整展示时只可移动有留白的方向。"
                    : "电脑端等比例预览；可拖动调整，或展开精细设置。"}
            </p>
            <button
              type="button"
              className="button-outline"
              onClick={() => setImagePicker(!imagePicker)}
            >
              <ImageIcon size={16} />
              {imagePicker ? "收起选图" : "选择展示图片"}
            </button>
            {imagePicker && (
              <div className="carousel-image-panel">
                <div className="carousel-image-picker">
                  {images.map((value, i) => (
                    <button
                      type="button"
                      key={value.id}
                      aria-label={`选择轮播图片 ${i + 1}`}
                      aria-pressed={
                        (slide.imageId ?? images[0]?.id) === value.id
                      }
                      onClick={() => patch({ imageId: value.id })}
                    >
                      <img
                        src={value.thumbnailUrl || value.url}
                        alt=""
                        loading="lazy"
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
                    加载更多图片
                  </button>
                )}
                {loadingImages && <p role="status">正在加载图片…</p>}
              </div>
            )}
            <details className="carousel-advanced">
              <summary>图片与文字精细设置</summary>
              <label className="form-field">
                图片适配
                <select
                  aria-label="图片适配"
                  value={slide.imageFit}
                  onChange={(e) =>
                    patch({ imageFit: e.target.value as HomeSlide["imageFit"] })
                  }
                >
                  <option value="contain">完整展示</option>
                  <option value="cover">铺满裁剪</option>
                </select>
              </label>
              <Range
                label="图片水平焦点"
                value={slide.imagePositionX}
                min={0}
                max={100}
                onChange={(imagePositionX) => patch({ imagePositionX })}
              />
              <Range
                label="图片垂直焦点"
                value={slide.imagePositionY}
                min={0}
                max={100}
                onChange={(imagePositionY) => patch({ imagePositionY })}
              />
              {slide.layout === "overlay" && slide.placement === "free" ? (
                <>
                  <Range
                    label="文字水平位置"
                    value={slide.textPositionX ?? 5}
                    min={0}
                    max={100}
                    onChange={(textPositionX) => patch({ textPositionX })}
                  />
                  <Range
                    label="文字垂直位置"
                    value={slide.textPositionY ?? 50}
                    min={0}
                    max={100}
                    onChange={(textPositionY) => patch({ textPositionY })}
                  />
                  <Range
                    label="文字宽度"
                    value={slide.textWidth ?? 50}
                    min={25}
                    max={85}
                    onChange={(textWidth) => patch({ textWidth })}
                  />
                </>
              ) : (
                <label className="form-field">
                  文字纵向对齐
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
              )}
              <label className="form-field">
                文字横向对齐
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
                字体
                <select
                  value={slide.fontFamily ?? "sans"}
                  onChange={(e) =>
                    patch({
                      fontFamily: e.target.value as HomeSlide["fontFamily"],
                    })
                  }
                >
                  <option value="sans">现代黑体</option>
                  <option value="serif">典雅宋体</option>
                </select>
              </label>
              <Range
                label="标题字号"
                value={slide.titleSize ?? 42}
                min={24}
                max={72}
                unit="px"
                onChange={(titleSize) => patch({ titleSize })}
              />
              <Range
                label="介绍字号"
                value={slide.descriptionSize ?? 16}
                min={12}
                max={24}
                unit="px"
                onChange={(descriptionSize) => patch({ descriptionSize })}
              />
              <div className="admin-grid">
                <Color
                  label="标题颜色"
                  value={slide.titleColor ?? ""}
                  onChange={(titleColor) => patch({ titleColor })}
                />
                <Color
                  label="介绍颜色"
                  value={slide.descriptionColor ?? ""}
                  onChange={(descriptionColor) => patch({ descriptionColor })}
                />
              </div>
            </details>
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
function Range({
  label,
  value,
  min,
  max,
  unit = "%",
  onChange,
}: {
  label: string;
  value: number;
  min: number;
  max: number;
  unit?: string;
  onChange: (value: number) => void;
}) {
  const id = useId();
  return (
    <label className="form-field carousel-range" htmlFor={id}>
      <span>
        {label}
        <output>
          {value}
          {unit}
        </output>
      </span>
      <input
        type="range"
        id={id}
        aria-label={label}
        min={min}
        max={max}
        value={value}
        onChange={(event) => onChange(Number(event.target.value))}
      />
    </label>
  );
}
function Color({
  label,
  value,
  onChange,
}: {
  label: string;
  value: string;
  onChange: (value: string) => void;
}) {
  return (
    <div className="form-field">
      <label className="carousel-color">
        <span>{label}</span>
        <input
          type="color"
          aria-label={label}
          value={value || "#f2f2f4"}
          onChange={(event) => onChange(event.target.value)}
        />
      </label>
      <button
        type="button"
        className="text-button"
        disabled={!value}
        onClick={() => onChange("")}
      >
        {value ? "恢复主题颜色" : "跟随页面主题"}
      </button>
    </div>
  );
}
