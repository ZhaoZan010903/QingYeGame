import { useEffect, useRef, useState } from 'react';
import { ChevronLeft, ChevronRight, Maximize2 } from 'lucide-react';
import type { Product } from '../types';
import { ApiError, isDemo } from '../lib/api';
import { Artwork, Modal } from './shared';
import { useProductGallery } from './useProductGallery';
import '../gallery.css';

const THUMBNAILS_PER_PAGE = 12; // Viewport size only; the full gallery has no total-image cap.

export function AccountGallery({
  product,
  reloadProduct,
}: {
  product: Product;
  reloadProduct: () => void;
}) {
  const gallery = useProductGallery(product);
  const [selected, setSelected] = useState(0);
  const [zoom, setZoom] = useState(false);
  const navigation = useRef(0);
  useEffect(
    () => () => {
      navigation.current++;
    },
    [],
  );
  const { items, total, error, loading } = gallery;
  const start = 0;
  const visible = items.slice(0, THUMBNAILS_PER_PAGE);
  const active = {
    ...product,
    images: items[selected] ? [items[selected]] : [],
    art: items[selected]?.art ?? product.art,
  };

  async function go(index: number) {
    if (index < 0 || index >= total) return;
    const request = ++navigation.current;
    let state = gallery.getSnapshot();
    // Request the next existing API page only when navigation needs it.
    while (
      index >= state.items.length &&
      (!state.initialized || state.nextCursor) &&
      !state.error
    ) {
      await gallery.load();
      if (request !== navigation.current) return;
      state = gallery.getSnapshot();
    }
    if (request === navigation.current && state.items[index]) setSelected(index);
  }
  const conflict = error instanceof ApiError && error.status === 409;
  const errors = error ? (
    <div className="inline-error" role="alert">
      <p>
        {conflict
          ? '图集已更新，请重新加载后查看。'
          : error instanceof Error
            ? error.message
            : '图片加载失败'}
      </p>
      <button
        className="text-button"
        disabled={loading}
        onClick={() => (conflict ? reloadProduct() : void gallery.load())}
      >
        {conflict ? '重新加载最新图集' : '重试图片加载'}
      </button>
    </div>
  ) : null;
  const nextDisabled =
    selected >= total - 1 || (selected >= items.length - 1 && (loading || !!error));
  return (
    <section className="detail-gallery" aria-label="账号图片">
      <div className="gallery-stage">
        <button
          className="gallery-main"
          aria-label="放大当前账号图片"
          disabled={!items.length}
          onClick={() => setZoom(true)}
        >
          <Artwork product={active} original />
          <span className="gallery-label">
            <b className="mono">
              {items.length ? selected + 1 : 0} / {total}
            </b>
            <span>{isDemo ? '概念图' : '张账号截图'}</span>
          </span>
        </button>
        {total > 1 && (
          <>
            <button
              className="gallery-step previous"
              aria-label="上一张图片"
              disabled={!selected}
              onClick={() => void go(selected - 1)}
            >
              <ChevronLeft />
            </button>
            <button
              className="gallery-step next"
              aria-label="下一张图片"
              disabled={nextDisabled}
              onClick={() => void go(selected + 1)}
            >
              <ChevronRight />
            </button>
          </>
        )}
      </div>
      <div className="gallery-toolbar">
        <span>
          完整图集 <b className="mono">{total}</b> 张
        </span>
        <button className="text-button" disabled={!items.length} onClick={() => setZoom(true)}>
          放大查看 <Maximize2 />
        </button>
      </div>
      <div className="gallery-thumbs" aria-label="当前组缩略图">
        {visible.map((item, offset) => (
          <button
            key={item.id}
            className={selected === start + offset ? 'selected' : ''}
            aria-label={`查看第 ${start + offset + 1} 张图片`}
            aria-pressed={selected === start + offset}
            onClick={() => void go(start + offset)}
          >
            <Artwork product={{ ...product, images: [item], art: item.art ?? product.art }} />
            <span className="mono">{start + offset + 1}</span>
          </button>
        ))}
      </div>
      {total > THUMBNAILS_PER_PAGE && (
        <button className="gallery-expand" onClick={() => setZoom(true)}>
          展开查看全部 <b className="mono">{total}</b> 张账号截图 <Maximize2 />
        </button>
      )}
      {!zoom && errors}
      {!items.length && !loading && !error && (
        <p className="gallery-empty">暂无账号图片，请联系客服了解资料。</p>
      )}
      {zoom && (
        <Modal
          title={`${product.code || '账号图片'} · ${items.length ? selected + 1 : 0}/${total}`}
          className="gallery-viewer"
          onClose={() => setZoom(false)}
          wide
        >
          <div
            className="lightbox-layout"
            onKeyDown={(event) => {
              if (event.key === 'ArrowLeft' && selected) {
                event.preventDefault();
                void go(selected - 1);
              }
              if (event.key === 'ArrowRight' && !nextDisabled) {
                event.preventDefault();
                void go(selected + 1);
              }
            }}
          >
            <div className="lightbox-preview">
              <div className="zoom-art">
                <Artwork product={active} original />
              </div>
              <div className="zoom-controls">
                <button
                  className="button-outline"
                  disabled={!selected}
                  onClick={() => void go(selected - 1)}
                >
                  <ChevronLeft />
                  上一张
                </button>
                <span className="mono" aria-live="polite">
                  {items.length ? selected + 1 : 0} / {total}
                </span>
                <button
                  className="button-outline"
                  disabled={nextDisabled}
                  onClick={() => void go(selected + 1)}
                >
                  {loading ? '加载中…' : '下一张'}
                  <ChevronRight />
                </button>
              </div>
              {errors}
            </div>
            <aside className="lightbox-library" aria-label="全部缩略图">
              <div className="lightbox-library-heading">
                <h3>完整图集</h3>
                <span className="mono">
                  {items.length} / {total}
                </span>
              </div>
              <div className="lightbox-thumbs">
                {items.map((item, index) => (
                  <button
                    key={item.id}
                    aria-label={`查看第 ${index + 1} 张高清图片`}
                    aria-pressed={selected === index}
                    className={selected === index ? 'selected' : ''}
                    onClick={() => void go(index)}
                  >
                    <Artwork
                      product={{ ...product, images: [item], art: item.art ?? product.art }}
                    />
                    <span className="mono">{index + 1}</span>
                  </button>
                ))}
              </div>
              {gallery.nextCursor && (
                <button
                  className="button-outline lightbox-more"
                  disabled={loading || !!error}
                  onClick={() => void gallery.load()}
                >
                  {loading ? '读取中…' : '加载更多缩略图'}
                </button>
              )}
              <p className="lightbox-hint">方向键或按钮切换大图，原图按需加载。</p>
            </aside>
          </div>
        </Modal>
      )}
    </section>
  );
}
