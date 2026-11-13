import { ProtectedImage } from '../../../共享/MediaSecurity';
import { useEffect, useMemo, useState, useSyncExternalStore } from 'react';
import { ChevronLeft, ChevronRight } from 'lucide-react';
import { api } from '../lib/api';
import { GalleryPager } from '../lib/gallery-pager';
import { Modal } from './shared';
import type { Product, RentalQuote, TradeType } from '../types';
import '../gallery.css';

const unitLabels = { hour: '元 / 小时', day: '元 / 日', week: '元 / 周', month: '元 / 月' };
export function PublishPreview({
  product,
  title,
  description,
  mode,
  tags,
  quotes,
  price,
  fields,
  onClose,
}: {
  product: Product | null;
  title: string;
  description: string;
  mode: TradeType;
  tags: string[];
  quotes: RentalQuote[];
  price: string;
  fields: { label: string; value: string }[];
  onClose: () => void;
}) {
  const pager = useMemo(
    () =>
      new GalleryPager(
        (cursor, version) =>
          product
            ? api.myImages(product.id, cursor, version)
            : Promise.resolve({ items: [], total: 0, nextCursor: null, version: 1 }),
        {
          items: product?.images ?? [],
          total: product?.imageTotal ?? 0,
          version: product?.version ?? 1,
        },
      ),
    [product?.id, product?.version],
  );
  const gallery = useSyncExternalStore(pager.subscribe, pager.getSnapshot, pager.getSnapshot);
  const [selected, setSelected] = useState(0);
  const [failed, setFailed] = useState(false);
  const [imageAttempt, setImageAttempt] = useState(0);
  useEffect(() => {
    if (product?.imageTotal) void pager.load();
  }, [pager]);
  useEffect(() => setFailed(false), [selected]);
  async function select(index: number) {
    if (index < 0 || index >= gallery.total) return;
    if (index >= gallery.items.length) await pager.load();
    if (pager.getSnapshot().items[index]) setSelected(index);
  }
  const active = gallery.items[selected];
  return (
    <Modal title="上架预览" wide className="publish-preview-dialog" onClose={onClose}>
      <div className="publish-preview-layout">
        <section className="preview-media" aria-label="预览商品图片">
          <div className="preview-stage">
            {active?.url && !failed ? (
              <ProtectedImage
                key={`${active.id}:${imageAttempt}`}
                src={active.url}
                onError={() => setFailed(true)}
                alt={`商品展示图 ${selected + 1}`}
                decoding="async"
              />
            ) : (
              <span>
                {failed ? (
                  <>
                    图片暂时无法加载{' '}
                    <button
                      type="button"
                      className="preview-retry"
                      onClick={() => {
                        setFailed(false);
                        setImageAttempt((value) => value + 1);
                      }}
                    >
                      重试当前图片
                    </button>
                  </>
                ) : (
                  '尚未添加图片'
                )}
              </span>
            )}
            {gallery.total > 0 && (
              <span className="preview-image-count">
                {selected + 1} / {gallery.total}
              </span>
            )}
            {gallery.total > 1 && (
              <>
                <button
                  type="button"
                  className="preview-previous"
                  aria-label="上一张预览图片"
                  disabled={selected === 0 || gallery.loading}
                  onClick={() => void select(selected - 1)}
                >
                  <ChevronLeft />
                </button>
                <button
                  type="button"
                  className="preview-next"
                  aria-label="下一张预览图片"
                  disabled={selected >= gallery.total - 1 || gallery.loading}
                  onClick={() => void select(selected + 1)}
                >
                  <ChevronRight />
                </button>
              </>
            )}
          </div>
          <div className="preview-thumbnails">
            {gallery.items
              .slice(Math.floor(selected / 12) * 12, Math.floor(selected / 12) * 12 + 12)
              .map((image, offset) => {
                const index = Math.floor(selected / 12) * 12 + offset;
                return (
                  <button
                    type="button"
                    key={image.id}
                    className={index === selected ? 'selected' : ''}
                    aria-label={`预览第${index + 1}张图片`}
                    onClick={() => setSelected(index)}
                  >
                    <ProtectedImage src={image.thumbnailUrl ?? image.url} alt="" loading="lazy" />
                  </button>
                );
              })}
          </div>
          {!!gallery.error && (
            <p className="form-error" role="alert">
              {gallery.error instanceof Error ? gallery.error.message : '图片读取失败'}{' '}
              <button type="button" className="text-button" onClick={() => void pager.load()}>
                重试
              </button>
            </p>
          )}
        </section>
        <section className="detail-summary preview-summary">
          <div className="detail-kicker">
            <span className="asset-id">{product?.code || '待发布'}</span>
            <span>{mode === 'sale' ? '账号出售' : '账号租赁'}</span>
          </div>
          <h3 className="preview-title">{title || '尚未填写标题'}</h3>
          <p className="preview-game">QQ炫舞</p>
          <div className="pill-tags">
            {tags.map((tag, index) => (
              <span key={index}>{tag}</span>
            ))}
          </div>
          <div className="preview-prices">
            {mode === 'rent' ? (
              quotes.length ? (
                quotes.map((quote) => (
                  <strong key={quote.unit}>
                    {quote.amount} <small>{unitLabels[quote.unit]}</small>
                  </strong>
                ))
              ) : (
                <strong>
                  {product?.unit === 'unknown' && product.legacyPrice
                    ? `${product.legacyPrice} · 单位待核对`
                    : '联系客服报价'}
                </strong>
              )
            ) : (
              <strong>
                {price || '联系客服报价'} {price && <small>元</small>}
              </strong>
            )}
          </div>
          <dl className="preview-attributes">
            {fields.map((field) => (
              <div key={field.label}>
                <dt>{field.label}</dt>
                <dd>{field.value || '未填写'}</dd>
              </div>
            ))}
          </dl>
          <div className="preview-description">
            <h4>账号介绍</h4>
            <p>{description || '尚未填写介绍'}</p>
          </div>
        </section>
      </div>
    </Modal>
  );
}
