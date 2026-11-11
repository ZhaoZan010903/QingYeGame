import { useEffect, useRef, useState } from "react";
import { Link } from "react-router-dom";
import { ArrowLeft, ChevronLeft, ChevronRight, X } from "lucide-react";
import { adminApi, adminImageApi } from "../lib/admin-api";
import { productFrom } from "../lib/api";
import { Price, StateView } from "./shared";
import { statusNames } from "./AdminProductEditor";
import type { Product, ImageItem } from "../types";
import { PrivateCredentials } from "./PrivateCredentials";

export function AdminProductView({ id }: { id: string }) {
  const [product, setProduct] = useState<Product>();
  const [images, setImages] = useState<ImageItem[]>([]);
  const [cursor, setCursor] = useState<string | null>(null);
  const [active, setActive] = useState(0);
  const [loading, setLoading] = useState(true);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [revision, setRevision] = useState(0);
  const dialog = useRef<HTMLDialogElement>(null);
  const sequence = useRef(0);
  useEffect(() => {
    const version = ++sequence.current;
    setLoading(true);
    setError("");
    setProduct(undefined);
    setCursor(null);
    setBusy(false);
    setImages([]);
    setActive(0);
    void (async () => {
      try {
        const row = productFrom(await adminApi.product(id));
        const page = await adminImageApi.myImages(id, null, row.version);
        if (sequence.current !== version) return;
        setProduct(row);
        setImages(page.items);
        setCursor(page.nextCursor);
      } catch (e) {
        if (sequence.current === version)
          setError(e instanceof Error ? e.message : "读取商品失败");
      } finally {
        if (sequence.current === version) setLoading(false);
      }
    })();
    return () => {
      sequence.current++;
    };
  }, [id, revision]);
  async function more() {
    if (busy || !cursor || !product) return;
    const version = sequence.current;
    setBusy(true);
    setError("");
    try {
      const page = await adminImageApi.myImages(id, cursor, product.version);
      if (sequence.current !== version) return;
      setImages((old) => [
        ...old,
        ...page.items.filter(
          (image) => !old.some((item) => item.id === image.id),
        ),
      ]);
      setCursor(page.nextCursor);
    } catch (e) {
      if (sequence.current === version)
        setError(e instanceof Error ? e.message : "图片加载失败");
    } finally {
      if (sequence.current === version) setBusy(false);
    }
  }
  if (loading || !product)
    return (
      <StateView
        loading={loading}
        error={error || undefined}
        onRetry={() => setRevision((v) => v + 1)}
      />
    );
  const current = images[active];
  return (
    <div className="admin-product-view">
      <div className="admin-page-heading">
        <div>
          <Link to="/chat">
            <ArrowLeft size={16} /> 返回在线咨询
          </Link>
          <h1>咨询商品资料</h1>
          <p>这里显示商品当前资料；聊天卡片保留发起咨询时的价格与标题。</p>
        </div>
        <Link
          className="button-white"
          to={`/products?edit=${encodeURIComponent(id)}`}
        >
          编辑此商品
        </Link>
      </div>
      <div className="admin-product-view-layout">
        <section className="admin-product-read-panel">
          {current?.url ? (
            <button
              className="admin-product-stage"
              onClick={() => dialog.current?.showModal()}
              aria-label="放大当前商品图片"
            >
              <img src={current.url} alt={current.alt || product.title} />
            </button>
          ) : (
            <p>此商品暂无可用图片</p>
          )}
          <div className="admin-product-image-tools">
            <span>
              {images.length ? active + 1 : 0} / {product.imageTotal} 张
            </span>
            <button
              disabled={active === 0}
              onClick={() => setActive((v) => v - 1)}
              aria-label="上一张"
            >
              <ChevronLeft size={18} />
            </button>
            <button
              disabled={active >= images.length - 1}
              onClick={() => setActive((v) => v + 1)}
              aria-label="下一张"
            >
              <ChevronRight size={18} />
            </button>
          </div>
          <div className="admin-product-thumbs">
            {images.map((image, index) => (
              <button
                key={image.id}
                className={index === active ? "active" : ""}
                onClick={() => setActive(index)}
                aria-label={`查看第 ${index + 1} 张`}
                aria-pressed={index === active}
              >
                {image.url && (
                  <img
                    src={image.thumbnailUrl || image.url}
                    loading="lazy"
                    alt=""
                  />
                )}
              </button>
            ))}
          </div>
          {cursor && (
            <button
              className="button-outline"
              disabled={busy}
              onClick={() => void more()}
            >
              {busy ? "正在加载…" : "继续加载图片"}
            </button>
          )}
          {error && (
            <p className="form-error" role="alert">
              {error}{" "}
              <button onClick={() => setRevision((v) => v + 1)}>
                重新加载资料
              </button>
            </p>
          )}
        </section>
        <section className="admin-product-read-panel">
          <div className="admin-product-meta">
            <code>{product.code || product.id}</code>
            <span>{statusNames[product.status] || product.status}</span>
          </div>
          <h2>{product.title}</h2>
          <Price product={product} />
          {!!product.rentalQuotes?.length && (
            <div className="preview-tags">
              {product.rentalQuotes.map((q) => (
                <span key={q.unit}>
                  {q.amount} 元/
                  {
                    (
                      {
                        hour: "小时",
                        day: "日",
                        week: "周",
                        month: "月",
                      } as Record<string, string>
                    )[q.unit]
                  }
                </span>
              ))}
            </div>
          )}
          <p className="field-hint">
            累计 {product.consultationCount ?? 0} 人咨询
          </p>
          {product.hasPrivateCredentials && <PrivateCredentials id={id} />}
          <dl className="admin-product-facts">
            <div>
              <dt>交易方式</dt>
              <dd>{product.mode === "rent" ? "出租" : "出售"}</dd>
            </div>
            <div>
              <dt>所在区服</dt>
              <dd>{product.server || "未填写"}</dd>
            </div>
            <div>
              <dt>角色等级</dt>
              <dd>
                {product.attributeLabels?.level || product.level || "未填写"}
              </dd>
            </div>
            <div>
              <dt>资料版本</dt>
              <dd>{product.version}</dd>
            </div>
          </dl>
          <h3>完整商品说明</h3>
          <p className="admin-product-description">
            {product.description || "未填写商品说明"}
          </p>
          {!!product.tags.length && (
            <div className="admin-product-tags">
              {product.tags.map((tag) => (
                <span key={tag}>{tag}</span>
              ))}
            </div>
          )}
          {product.reviewNote && (
            <p className="notice-box">审核备注：{product.reviewNote}</p>
          )}
        </section>
      </div>
      <dialog
        className="admin-product-lightbox"
        ref={dialog}
        aria-label="商品原图"
      >
        <button
          className="admin-lightbox-close"
          onClick={() => dialog.current?.close()}
          aria-label="关闭原图"
        >
          <X />
        </button>
        {current?.url && (
          <img src={current.url} alt={current.alt || "商品原图"} />
        )}
        <div className="admin-product-image-tools">
          <button
            disabled={active === 0}
            onClick={() => setActive((v) => v - 1)}
            aria-label="上一张"
          >
            <ChevronLeft />
          </button>
          <span>
            {active + 1} / {product.imageTotal}
          </span>
          <button
            disabled={active >= images.length - 1}
            onClick={() => setActive((v) => v + 1)}
            aria-label="下一张"
          >
            <ChevronRight />
          </button>
          {cursor && (
            <button disabled={busy} onClick={() => void more()}>
              继续加载
            </button>
          )}
        </div>
      </dialog>
    </div>
  );
}
