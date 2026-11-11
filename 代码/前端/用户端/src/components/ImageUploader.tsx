import { useCallback, useEffect, useRef, useState, type ChangeEvent, type DragEvent } from 'react';
import { api, isDemo, uploadConfig, createRequestId } from '../lib/api';
import type { ImageItem, Product } from '../types';
import { runOrderedUploads, UploadSelectionOrder } from '../../../共享/ordered-uploads';

type UploadState = 'queued' | 'uploading' | 'saving' | 'failed' | 'cancelled' | 'done';
interface QueueItem {
  key: string;
  file: File;
  preview: string;
  progress: number;
  status: UploadState;
  error?: string;
  uploaded?: ImageItem;
}
interface Props {
  product: Product | null;
  ensureDraft: () => Promise<Product>;
  onChange: (product: Product) => void;
  onBusyChange: (busy: boolean) => void;
  onPendingChange: (pending: boolean) => void;
  disabled?: boolean;
  transport?: Pick<
    typeof api,
    'myProduct' | 'myImages' | 'appendImages' | 'removeImage' | 'moveImage'
  >;
}
const text = (error: unknown) =>
  error instanceof Error ? error.message : '图片处理失败，请重试。';
export function ImageUploader({
  product,
  ensureDraft,
  onChange,
  onBusyChange,
  onPendingChange,
  disabled = false,
  transport = api,
}: Props) {
  const [images, setImages] = useState<ImageItem[]>([]);
  const [cursor, setCursor] = useState<string | null>(null);
  const [queue, setQueue] = useState<QueueItem[]>([]);
  const [error, setError] = useState('');
  const [busy, setBusy] = useState(false);
  const [dragging, setDragging] = useState(false);
  const fileInput = useRef<HTMLInputElement>(null);
  const current = useRef(product);
  const galleryVersion = useRef(product?.version);
  const controllers = useRef(new Map<string, AbortController>());
  const previews = useRef(new Set<string>());
  const cancelled = useRef(new Set<string>());
  const createdHere = useRef(false);
  const running = useRef(false);
  const alive = useRef(true);
  const selectionOrder = useRef(new UploadSelectionOrder());
  const productId = product?.id;
  current.current = product;
  useEffect(() => {
    alive.current = true;
    return () => {
      alive.current = false;
      controllers.current.forEach((c) => c.abort());
      previews.current.forEach((url) => URL.revokeObjectURL(url));
    };
  }, []);
  useEffect(() => {
    onBusyChange(busy);
  }, [busy, onBusyChange]);
  useEffect(() => {
    onPendingChange(queue.some((item) => item.status !== 'done'));
  }, [queue, onPendingChange]);
  const apply = useCallback(
    (next: Product) => {
      current.current = next;
      if (alive.current) onChange(next);
    },
    [onChange],
  );
  const load = useCallback(
    async (more = false) => {
      const p = current.current;
      if (!p) return;
      setBusy(true);
      setError('');
      try {
        const fresh = more ? p : await transport.myProduct(p.id);
        if (!more) apply(fresh);
        const append = more && galleryVersion.current === fresh.version;
        const page = await transport.myImages(fresh.id, append ? cursor : undefined, fresh.version);
        galleryVersion.current = page.version;
        if (alive.current) {
          setImages((previous) =>
            append
              ? [
                  ...previous,
                  ...page.items.filter((item) => !previous.some((p) => p.id === item.id)),
                ]
              : page.items,
          );
          setCursor(page.nextCursor);
        }
      } catch (e) {
        if (alive.current) setError(text(e));
      } finally {
        if (alive.current) setBusy(false);
      }
    },
    [cursor, apply, transport],
  );
  useEffect(() => {
    if (productId && !createdHere.current) void load();
    else if (!productId) {
      setImages([]);
      setCursor(null);
    }
  }, [productId]); // Writes keep already loaded pages.
  function update(key: string, values: Partial<QueueItem>) {
    if (alive.current)
      setQueue((items) => items.map((item) => (item.key === key ? { ...item, ...values } : item)));
  }
  async function transfer(item: QueueItem) {
    if (!alive.current || cancelled.current.has(item.key))
      throw new DOMException('已取消', 'AbortError');
    const controller = new AbortController();
    controllers.current.set(item.key, controller);
    update(item.key, { status: 'uploading', error: undefined, progress: item.uploaded ? 100 : 0 });
    const image =
      item.uploaded ||
      (await api.upload(
        item.file,
        (progress) => update(item.key, { progress }),
        controller.signal,
      ));
    update(item.key, { uploaded: image, status: 'saving' });
    return image;
  }
  async function associate(item: QueueItem, image: ImageItem) {
    if (!alive.current || controllers.current.get(item.key)?.signal.aborted)
      throw new DOMException('已取消', 'AbortError');
    let next = current.current;
    if (!next) throw new Error('草稿已失效，请重新保存后再试。');
    if (!selectionOrder.current.attached(item.key)) {
      next = await transport.appendImages(next.id, [image.id], next.version);
      apply(next);
      // A later move/read failure must not append this image again on retry.
      selectionOrder.current.markAttached(item.key, image.id);
    } else {
      next = await transport.myProduct(next.id);
      apply(next);
    }
    const before = selectionOrder.current.before(item.key);
    if (before) {
      next = await transport.moveImage(next.id, image.id, before, next.version);
      apply(next);
    }
    if (cursor) {
      const page = await transport.myImages(next.id, undefined, next.version);
      galleryVersion.current = page.version;
      if (alive.current) {
        setImages(page.items);
        setCursor(page.nextCursor);
      }
    } else if (alive.current) {
      galleryVersion.current = next.version;
      setImages((previous) => {
        const list = previous.filter((entry) => entry.id !== image.id);
        const at = before ? list.findIndex((entry) => entry.id === before) : -1;
        list.splice(at < 0 ? list.length : at, 0, image);
        return list;
      });
    }
    update(item.key, { status: 'done', progress: 100 });
    URL.revokeObjectURL(item.preview);
    previews.current.delete(item.preview);
  }
  async function run(items: QueueItem[]) {
    if (!items.length || running.current || busy || disabled) return;
    running.current = true;
    setBusy(true);
    setError('');
    try {
      if (!current.current) {
        createdHere.current = true;
        apply(await ensureDraft());
      }
      await runOrderedUploads(
        items,
        uploadConfig.concurrency,
        transfer,
        associate,
        (item, error) => {
          update(item.key, {
            status:
              error instanceof DOMException && error.name === 'AbortError' ? 'cancelled' : 'failed',
            error: text(error),
          });
        },
      );
    } catch (e) {
      setError(text(e));
      items.forEach((item) => update(item.key, { status: 'failed', error: text(e) }));
    } finally {
      items.forEach((item) => controllers.current.delete(item.key));
      running.current = false;
      if (alive.current) setBusy(false);
    }
  }
  function choose(files: FileList | File[]) {
    if (busy || disabled) return;
    const list = Array.from(files);
    if (list.length > uploadConfig.batchSize) {
      setError(
        `每批最多选择 ${uploadConfig.batchSize} 张。可以继续分批追加，商品图集不设总张数上限。`,
      );
      return;
    }
    const valid: QueueItem[] = [];
    const rejected: string[] = [];
    for (const file of list) {
      if (!['image/jpeg', 'image/png', 'image/webp'].includes(file.type)) {
        rejected.push(`${file.name}：请选择 JPG、PNG 或 WebP 图片。`);
        continue;
      }
      if (file.size > uploadConfig.maxBytes) {
        rejected.push(
          `${file.name}：单张不能超过 ${Math.round(uploadConfig.maxBytes / 1024 / 1024)} MB。`,
        );
        continue;
      }
      const preview = URL.createObjectURL(file);
      previews.current.add(preview);
      valid.push({ key: createRequestId(), file, preview, status: 'queued', progress: 0 });
    }
    setError(rejected.join('\n'));
    selectionOrder.current.register(valid.map((item) => item.key));
    setQueue((previous) => [...previous.filter((item) => item.status !== 'done'), ...valid]);
    void run(valid).then(() => {
      if (rejected.length && alive.current) setError(rejected.join('\n'));
    });
  }
  function inputChange(e: ChangeEvent<HTMLInputElement>) {
    if (e.target.files) choose(e.target.files);
    e.target.value = '';
  }
  function drop(e: DragEvent) {
    e.preventDefault();
    setDragging(false);
    choose(e.dataTransfer.files);
  }
  function removeQueued(key: string) {
    cancelled.current.add(key);
    selectionOrder.current.forget(key);
    controllers.current.get(key)?.abort();
    const item = queue.find((value) => value.key === key);
    if (item) {
      URL.revokeObjectURL(item.preview);
      previews.current.delete(item.preview);
    }
    setQueue((previous) => previous.filter((item) => item.key !== key));
  }
  async function mutateImage(image: ImageItem, action: 'remove' | 'left' | 'right') {
    const p = current.current;
    if (!p || busy) return;
    setBusy(true);
    setError('');
    try {
      if (action === 'remove') {
        apply(await transport.removeImage(p.id, image.id, p.version));
        selectionOrder.current.removeImage(image.id);
        setImages((list) => list.filter((i) => i.id !== image.id));
      } else {
        const at = images.findIndex((i) => i.id === image.id);
        const before = action === 'left' ? images[at - 1]?.id : (images[at + 2]?.id ?? null);
        if (action === 'left' && !before) return;
        apply(await transport.moveImage(p.id, image.id, before, p.version));
        const next = [...images];
        next.splice(at, 1);
        next.splice(action === 'left' ? at - 1 : at + 1, 0, image);
        setImages(next);
      }
      if (cursor && current.current) {
        const page = await transport.myImages(
          current.current.id,
          undefined,
          current.current.version,
        );
        galleryVersion.current = page.version;
        setImages(page.items);
        setCursor(page.nextCursor);
      } else galleryVersion.current = current.current?.version;
    } catch (e) {
      setError(text(e));
    } finally {
      setBusy(false);
    }
  }
  const pending = queue.filter((item) => item.status !== 'done');
  return (
    <div className="image-uploader">
      <div
        className={`upload-zone ${dragging ? 'dragging' : ''}`}
        onDragOver={(e) => {
          e.preventDefault();
          if (!busy && !disabled) setDragging(true);
        }}
        onDragLeave={() => setDragging(false)}
        onDrop={drop}
      >
        <span aria-hidden="true">＋</span>
        <p>把账号图片拖到这里，或分批选择图片</p>
        <button
          type="button"
          className="button-outline"
          disabled={busy || disabled}
          onClick={() => fileInput.current?.click()}
        >
          {busy ? '正在处理图片…' : images.length ? '追加图片' : '选择账号图片'}
        </button>
        <input
          ref={fileInput}
          type="file"
          accept="image/jpeg,image/png,image/webp"
          multiple
          onChange={inputChange}
          disabled={busy || disabled}
          aria-label="选择账号图片"
        />
        <small>
          JPG / PNG / WebP · 单张 ≤ {Math.round(uploadConfig.maxBytes / 1024 / 1024)} MB · 每批 ≤{' '}
          {uploadConfig.batchSize} 张<br />
          可以继续追加，商品图片不设总张数上限。第一张图片为封面。
          {isDemo && (
            <>
              <br />
              演示图片仅保存到此设备，没有上传至服务器。
            </>
          )}
        </small>
      </div>
      {error && (
        <div className="member-error">
          <p className="form-error" role="alert" style={{ whiteSpace: 'pre-line' }}>
            {error}
          </p>
          {product && (
            <button
              type="button"
              className="text-button"
              disabled={busy}
              onClick={() => void load()}
            >
              重新加载已保存图集
            </button>
          )}
        </div>
      )}
      {!!images.length && (
        <div className="upload-grid">
          {images.map((image, index) => (
            <div className="upload-tile" key={image.id}>
              <div className="upload-preview">
                {image.url ? (
                  <img
                    src={image.thumbnailUrl ?? image.url}
                    alt={image.alt}
                    loading="lazy"
                    onError={(e) => {
                      e.currentTarget.alt = '图片暂时无法加载';
                    }}
                  />
                ) : (
                  <div className={`asset-art art-${image.art ?? 0}`} />
                )}
                {index === 0 && <span className="upload-cover-tag">封面</span>}
              </div>
              <div className="upload-caption">
                <p>{image.alt || `图片 ${index + 1}`}</p>
                <div className="upload-buttons">
                  <button
                    type="button"
                    disabled={busy || disabled || index === 0}
                    aria-label={`前移图片 ${index + 1}`}
                    onClick={() => void mutateImage(image, 'left')}
                  >
                    ← 前移
                  </button>
                  <button
                    type="button"
                    disabled={
                      busy ||
                      disabled ||
                      index === images.length - 1 ||
                      (!!cursor && index === images.length - 2)
                    }
                    aria-label={`后移图片 ${index + 1}`}
                    onClick={() => void mutateImage(image, 'right')}
                  >
                    后移 →
                  </button>
                  <button
                    type="button"
                    disabled={busy || disabled}
                    aria-label={`移除图片 ${index + 1}`}
                    onClick={() => void mutateImage(image, 'remove')}
                  >
                    移除
                  </button>
                </div>
              </div>
            </div>
          ))}
        </div>
      )}
      {!!pending.length && (
        <div className="upload-grid" aria-label="待处理图片">
          {pending.map((item) => (
            <div className="upload-tile" key={item.key}>
              <div className="upload-preview">
                <img src={item.preview} alt={item.file.name} />
              </div>
              <div className="upload-caption">
                <p>{item.file.name}</p>
                <span>
                  {item.status === 'uploading'
                    ? isDemo
                      ? '正在保存到此设备'
                      : `上传中 ${item.progress}%`
                    : item.status === 'saving'
                      ? '正在关联到草稿'
                      : item.status === 'queued'
                        ? '等待处理'
                        : item.status === 'cancelled'
                          ? '已取消'
                          : '处理失败'}
                </span>
                {item.status === 'uploading' && (
                  <progress
                    value={item.progress}
                    max={100}
                    aria-label={`${item.file.name} 上传进度`}
                  />
                )}
                {item.error && item.status === 'failed' && (
                  <p className="upload-failure">{item.error}</p>
                )}
                <div className="upload-buttons">
                  {['failed', 'cancelled'].includes(item.status) ? (
                    <>
                      <button
                        type="button"
                        disabled={busy || disabled}
                        onClick={() => void run([item])}
                      >
                        重试
                      </button>
                      <button type="button" disabled={busy} onClick={() => removeQueued(item.key)}>
                        移除
                      </button>
                    </>
                  ) : (
                    <button
                      type="button"
                      disabled={item.status === 'saving'}
                      onClick={() => {
                        controllers.current.get(item.key)?.abort();
                        if (item.status === 'queued') removeQueued(item.key);
                      }}
                    >
                      取消
                    </button>
                  )}
                </div>
              </div>
            </div>
          ))}
        </div>
      )}
      <div className="upload-summary">
        <span>
          已保存 {product?.imageTotal ?? images.length} 张
          {pending.length ? ` · 待处理 ${pending.length} 张` : ''}
        </span>
        {cursor && (
          <button
            type="button"
            className="text-button"
            disabled={busy}
            onClick={() => void load(true)}
          >
            继续加载图集 ↓
          </button>
        )}
      </div>
    </div>
  );
}
