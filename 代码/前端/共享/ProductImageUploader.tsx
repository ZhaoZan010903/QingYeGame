import {
  useCallback,
  useEffect,
  useRef,
  useState,
  type ChangeEvent,
  type DragEvent,
} from "react";
import {
  validateProductBatch,
  PRODUCT_UPLOAD_BATCH_MAX,
} from "./product-upload";
import type { ImageItem, ImagePage, Product } from "./types";
import { summarizeUploadProgress } from "./upload-progress";
import {
  locateProductImage,
  removePendingProductImage,
} from "./product-image-recovery";
import "./upload-progress.css";
import {
  runOrderedUploads,
  UploadSelectionOrder,
  UploadScope,
} from "./ordered-uploads";

type UploadState =
  | "queued"
  | "uploading"
  | "saving"
  | "failed"
  | "cancelled"
  | "paused"
  | "done";
interface QueueItem {
  key: string;
  file: File;
  preview: string;
  progress: number;
  status: UploadState;
  error?: string;
  uploaded?: ImageItem;
}
export interface ProductImageUploaderProps {
  product: Product | null;
  ensureDraft: () => Promise<Product>;
  onChange: (product: Product) => void;
  onBusyChange: (busy: boolean) => void;
  onPendingChange: (pending: boolean) => void;
  disabled?: boolean;
  transport: {
    myProduct: (id: string) => Promise<Product>;
    myImages: (
      id: string,
      cursor?: string | null,
      version?: number,
    ) => Promise<ImagePage>;
    appendImages: (
      id: string,
      imageIds: string[],
      version: number,
    ) => Promise<Product>;
    removeImage: (
      id: string,
      imageId: string,
      version: number,
    ) => Promise<Product>;
    moveImage: (
      id: string,
      imageId: string,
      before: string | null,
      version: number,
    ) => Promise<Product>;
  };
  upload: (
    file: File,
    progress: (value: number) => void,
    signal: AbortSignal,
  ) => Promise<ImageItem>;
  cancelUpload: (file: File) => Promise<void>;
  uploadConfig: { maxBytes: number; batchSize: number; concurrency: number };
  createRequestId: () => string;
  isDemo?: boolean;
}
const text = (error: unknown) =>
  error instanceof Error ? error.message : "图片处理失败，请重试。";
export function ProductImageUploader({
  product,
  ensureDraft,
  onChange,
  onBusyChange,
  onPendingChange,
  disabled = false,
  transport,
  upload,
  cancelUpload,
  uploadConfig,
  createRequestId,
  isDemo = false,
}: ProductImageUploaderProps) {
  const [images, setImages] = useState<ImageItem[]>([]);
  const [cursor, setCursor] = useState<string | null>(null);
  const [queue, setQueue] = useState<QueueItem[]>([]);
  const [completed, setCompleted] = useState({ count: 0, bytes: 0 });
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const [dragging, setDragging] = useState(false);
  const [queueVisible, setQueueVisible] = useState(24);
  const paused = useRef(false);
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
  const scope = useRef(new UploadScope(productId));
  const previousProductId = useRef(productId);
  scope.current.select(productId, createdHere.current);
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
    onPendingChange(queue.some((item) => item.status !== "done"));
  }, [queue, onPendingChange]);
  const apply = useCallback(
    (next: Product) => {
      if (!alive.current || (current.current && current.current.id !== next.id))
        throw new DOMException("当前商品已变更", "AbortError");
      current.current = next;
      if (alive.current) {
        try {
          onChange(next);
        } catch (error) {
          paused.current = true;
          controllers.current.forEach((controller) => controller.abort());
          throw error;
        }
      }
    },
    [onChange],
  );
  const load = useCallback(
    async (more = false) => {
      const p = current.current;
      if (!p) return;
      const checkScope = scope.current.capture();
      setBusy(true);
      setError("");
      try {
        const fresh = more ? p : await transport.myProduct(p.id);
        checkScope();
        if (!more) apply(fresh);
        const append = more && galleryVersion.current === fresh.version;
        const page = await transport.myImages(
          fresh.id,
          append ? cursor : undefined,
          fresh.version,
        );
        checkScope();
        galleryVersion.current = page.version;
        if (alive.current) {
          setImages((previous) =>
            append
              ? [
                  ...previous,
                  ...page.items.filter(
                    (item) => !previous.some((p) => p.id === item.id),
                  ),
                ]
              : page.items,
          );
          setCursor(page.nextCursor);
        }
      } catch (e) {
        if (
          alive.current &&
          !(e instanceof DOMException && e.name === "AbortError")
        )
          setError(text(e));
      } finally {
        if (alive.current && current.current?.id === p.id) setBusy(false);
      }
    },
    [cursor, apply, transport],
  );
  useEffect(() => {
    const changed = previousProductId.current !== productId;
    const adopted =
      previousProductId.current === undefined && createdHere.current;
    previousProductId.current = productId;
    if (changed && !adopted) {
      controllers.current.forEach((controller) => controller.abort());
      previews.current.forEach((url) => URL.revokeObjectURL(url));
      previews.current.clear();
      selectionOrder.current = new UploadSelectionOrder();
      cancelled.current.clear();
      setQueue([]);
      setCompleted({ count: 0, bytes: 0 });
      setImages([]);
      setCursor(null);
      setError("");
      createdHere.current = false;
    }
    if (productId && !createdHere.current) void load();
    else if (!productId) {
      setImages([]);
      setCursor(null);
    }
  }, [productId]); // Writes keep already loaded pages.
  function update(key: string, values: Partial<QueueItem>) {
    if (alive.current)
      setQueue((items) =>
        items.map((item) => (item.key === key ? { ...item, ...values } : item)),
      );
  }
  async function transfer(item: QueueItem) {
    if (!alive.current || paused.current || cancelled.current.has(item.key))
      throw new DOMException("已取消", "AbortError");
    const controller = new AbortController();
    controllers.current.set(item.key, controller);
    update(item.key, {
      status: "uploading",
      error: undefined,
      progress: item.uploaded ? 100 : item.progress,
    });
    const image =
      item.uploaded ||
      (await upload(
        item.file,
        (progress) => update(item.key, { progress }),
        controller.signal,
      ));
    update(item.key, { uploaded: image, status: "saving" });
    return image;
  }
  async function associate(
    item: QueueItem,
    image: ImageItem,
    checkScope: () => void,
  ) {
    checkScope();
    if (
      !alive.current ||
      paused.current ||
      cancelled.current.has(item.key) ||
      controllers.current.get(item.key)?.signal.aborted
    )
      throw new DOMException("已取消", "AbortError");
    let next = current.current;
    if (!next) throw new Error("草稿已失效，请重新保存后再试。");
    if (item.uploaded) {
      const recovered = await locateProductImage(
        transport,
        next.id,
        image.id,
        checkScope,
      );
      next = recovered.product;
      apply(next);
      if (recovered.attached)
        selectionOrder.current.markAttached(item.key, image.id);
      else selectionOrder.current.unmarkAttached(item.key);
    }
    if (!selectionOrder.current.attached(item.key)) {
      next = await transport.appendImages(next.id, [image.id], next.version);
      checkScope();
      apply(next);
      // A later move/read failure must not append this image again on retry.
      selectionOrder.current.markAttached(item.key, image.id);
    } else {
      next = await transport.myProduct(next.id);
      checkScope();
      apply(next);
    }
    const before = selectionOrder.current.before(item.key);
    if (before) {
      next = await transport.moveImage(next.id, image.id, before, next.version);
      checkScope();
      apply(next);
    }
    if (cursor) {
      const page = await transport.myImages(next.id, undefined, next.version);
      checkScope();
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
    setCompleted((previous) => ({
      count: previous.count + 1,
      bytes: previous.bytes + item.file.size,
    }));
    setQueue((items) => items.filter((entry) => entry.key !== item.key));
    URL.revokeObjectURL(item.preview);
    previews.current.delete(item.preview);
  }
  async function run(items: QueueItem[]) {
    if (!items.length || running.current || busy || disabled) return;
    const checkScope = scope.current.capture();
    const inScope = () => {
      try {
        checkScope();
        return alive.current;
      } catch {
        return false;
      }
    };
    running.current = true;
    paused.current = false;
    setBusy(true);
    setError("");
    try {
      if (!current.current) {
        createdHere.current = true;
        const draft = await ensureDraft();
        checkScope();
        apply(draft);
      }
      await runOrderedUploads(
        items,
        uploadConfig.concurrency,
        async (item) => {
          checkScope();
          const image = await transfer(item);
          checkScope();
          return image;
        },
        (item, image) => associate(item, image, checkScope),
        (item, error) => {
          update(item.key, {
            status:
              error instanceof DOMException && error.name === "AbortError"
                ? "paused"
                : "failed",
            error: text(error),
          });
        },
      );
    } catch (e) {
      if (!inScope()) return;
      setError(text(e));
      items.forEach((item) =>
        update(item.key, { status: "failed", error: text(e) }),
      );
    } finally {
      items.forEach((item) => controllers.current.delete(item.key));
      running.current = false;
      if (inScope()) setBusy(false);
    }
  }
  function choose(files: FileList | File[]) {
    if (busy || disabled) return;
    const list = Array.from(files);
    const invalid = validateProductBatch(list, uploadConfig.maxBytes);
    if (invalid) {
      setError(invalid);
      return;
    }
    const valid: QueueItem[] = [];
    const rejected: string[] = [];
    for (const file of list) {
      if (!["image/jpeg", "image/png", "image/webp"].includes(file.type)) {
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
      valid.push({
        key: createRequestId(),
        file,
        preview,
        status: "queued",
        progress: 0,
      });
    }
    setError(rejected.join("\n"));
    selectionOrder.current.register(valid.map((item) => item.key));
    setQueue((previous) => [
      ...previous.filter((item) => item.status !== "done"),
      ...valid,
    ]);
    // Select several batches and arrange the queue before starting uploads.
  }
  function inputChange(e: ChangeEvent<HTMLInputElement>) {
    if (e.target.files) choose(e.target.files);
    e.target.value = "";
  }
  function drop(e: DragEvent) {
    e.preventDefault();
    setDragging(false);
    choose(e.dataTransfer.files);
  }
  async function removeQueued(key: string) {
    const capturedScope = scope.current.capture();
    const checkScope = () => {
      capturedScope();
      if (!alive.current) throw new DOMException("页面已关闭", "AbortError");
    };
    cancelled.current.add(key);
    controllers.current.get(key)?.abort();
    const item = queue.find((value) => value.key === key);
    if (item) {
      try {
        if (item.uploaded && current.current) {
          const next = await removePendingProductImage(
            transport,
            current.current.id,
            item.uploaded.id,
            () => cancelUpload(item.file),
            checkScope,
          );
          apply(next);
          setImages((previous) =>
            previous.filter((image) => image.id !== item.uploaded!.id),
          );
          selectionOrder.current.removeImage(item.uploaded.id);
          galleryVersion.current = next.version;
        } else {
          await cancelUpload(item.file);
          checkScope();
        }
      } catch (e) {
        if (e instanceof DOMException && e.name === "AbortError") return;
        cancelled.current.delete(key);
        update(key, { status: "failed", error: text(e) });
        return;
      }
      URL.revokeObjectURL(item.preview);
      previews.current.delete(item.preview);
    }
    selectionOrder.current.forget(key);
    setQueue((previous) => previous.filter((item) => item.key !== key));
    if (item?.uploaded && cursor) void load();
  }
  function moveQueued(key: string, direction: -1 | 1) {
    if (busy || disabled) return;
    setQueue((items) => {
      const from = items.findIndex((item) => item.key === key);
      const to = from + direction;
      if (from < 0 || to < 0 || to >= items.length) return items;
      const next = [...items];
      next.splice(to, 0, next.splice(from, 1)[0]);
      selectionOrder.current.reorderPending(next.map((item) => item.key));
      return next;
    });
  }
  function pauseUploads() {
    paused.current = true;
    controllers.current.forEach((controller) => controller.abort());
  }
  async function mutateImage(
    image: ImageItem,
    action: "remove" | "left" | "right",
  ) {
    const p = current.current;
    if (!p || busy) return;
    const checkScope = scope.current.capture();
    setBusy(true);
    setError("");
    try {
      if (action === "remove") {
        const result = await transport.removeImage(p.id, image.id, p.version);
        checkScope();
        apply(result);
        selectionOrder.current.removeImage(image.id);
        setImages((list) => list.filter((i) => i.id !== image.id));
      } else {
        const at = images.findIndex((i) => i.id === image.id);
        const before =
          action === "left" ? images[at - 1]?.id : (images[at + 2]?.id ?? null);
        if (action === "left" && !before) return;
        const result = await transport.moveImage(
          p.id,
          image.id,
          before,
          p.version,
        );
        checkScope();
        apply(result);
        const next = [...images];
        next.splice(at, 1);
        next.splice(action === "left" ? at - 1 : at + 1, 0, image);
        selectionOrder.current.reorderImages(next.map((item) => item.id));
        setImages(next);
      }
      if (cursor && current.current) {
        const page = await transport.myImages(
          current.current.id,
          undefined,
          current.current.version,
        );
        checkScope();
        galleryVersion.current = page.version;
        setImages(page.items);
        setCursor(page.nextCursor);
      } else galleryVersion.current = current.current?.version;
    } catch (e) {
      if (!(e instanceof DOMException && e.name === "AbortError"))
        setError(text(e));
    } finally {
      try {
        checkScope();
        if (alive.current) setBusy(false);
      } catch {
        /* Another gallery owns the controls now. */
      }
    }
  }
  const pending = queue.filter((item) => item.status !== "done");
  const progress = summarizeUploadProgress(completed, pending);
  return (
    <div className="image-uploader">
      <div
        className={`upload-zone ${dragging ? "dragging" : ""}`}
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
          {busy ? "正在处理图片…" : images.length ? "追加图片" : "选择账号图片"}
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
          JPG / PNG / WebP · 单张 ≤{" "}
          {Math.round(uploadConfig.maxBytes / 1024 / 1024)} MB · 每批 ≤{" "}
          {PRODUCT_UPLOAD_BATCH_MAX} 张<br />
          可以继续追加，商品图片不设总张数上限。第一张图片为封面。
          <br />
          选好并排序后，点击“开始上传”；全部上传完成后再提交。
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
          <p
            className="form-error"
            role="alert"
            style={{ whiteSpace: "pre-line" }}
          >
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
                      e.currentTarget.alt = "图片暂时无法加载";
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
                    onClick={() => void mutateImage(image, "left")}
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
                    onClick={() => void mutateImage(image, "right")}
                  >
                    后移 →
                  </button>
                  <button
                    type="button"
                    disabled={busy || disabled}
                    aria-label={`移除图片 ${index + 1}`}
                    onClick={() => void mutateImage(image, "remove")}
                  >
                    移除
                  </button>
                </div>
              </div>
            </div>
          ))}
        </div>
      )}
      {progress.total > 0 && (
        <section
          className="upload-overall-progress"
          aria-label="图片上传总进度"
        >
          <div className="upload-progress-heading">
            <strong>{isDemo ? "图片保存进度" : "图片上传进度"}</strong>
            <span>{progress.percent}%</span>
          </div>
          <progress
            value={progress.percent}
            max={100}
            aria-label="图片上传总进度"
          />
          <div className="upload-progress-details">
            <span>
              本次上传完成 {progress.completed} / {progress.total} 张
            </span>
            <span>
              {progress.failed
                ? `${progress.failed} 张失败，可重试`
                : progress.paused
                  ? `${progress.paused} 张已暂停`
                  : progress.saving
                    ? "正在保存图片"
                    : pending.length
                      ? busy
                        ? "正在上传"
                        : "等待开始上传"
                      : "全部上传完成"}
            </span>
          </div>
        </section>
      )}
      {!!pending.length && (
        <div className="upload-queue">
          <div className="upload-queue-toolbar">
            <strong>待上传 {pending.length} 张</strong>
            <span>先调整顺序，再开始上传；第一张为封面。</span>
            {busy ? (
              <button type="button" onClick={pauseUploads}>
                暂停上传
              </button>
            ) : (
              <button
                type="button"
                disabled={disabled}
                onClick={() => void run(pending)}
              >
                开始 / 继续上传
              </button>
            )}
          </div>
          <div className="upload-grid" aria-label="待处理图片">
            {pending.slice(0, queueVisible).map((item, index) => (
              <div className="upload-tile" key={item.key}>
                <div className="upload-preview">
                  <img
                    src={item.preview}
                    alt={item.file.name}
                    loading="lazy"
                    decoding="async"
                  />
                </div>
                <div className="upload-caption">
                  <p>{item.file.name}</p>
                  <span>
                    {item.status === "uploading"
                      ? isDemo
                        ? "正在保存到此设备"
                        : `上传中 ${item.progress}%`
                      : item.status === "saving"
                        ? "正在关联到草稿"
                        : item.status === "queued"
                          ? "等待处理"
                          : item.status === "paused"
                            ? "已暂停，可继续"
                            : item.status === "cancelled"
                              ? "已取消"
                              : "处理失败"}
                  </span>
                  {item.status === "uploading" && (
                    <progress
                      value={item.progress}
                      max={100}
                      aria-label={`${item.file.name} 上传进度`}
                    />
                  )}
                  {item.error && item.status === "failed" && (
                    <p className="upload-failure">{item.error}</p>
                  )}
                  <div className="upload-buttons">
                    <button
                      type="button"
                      disabled={busy || disabled || index === 0}
                      aria-label={`前移待上传图片 ${index + 1}`}
                      onClick={() => moveQueued(item.key, -1)}
                    >
                      前移
                    </button>
                    <button
                      type="button"
                      disabled={
                        busy || disabled || index === pending.length - 1
                      }
                      aria-label={`后移待上传图片 ${index + 1}`}
                      onClick={() => moveQueued(item.key, 1)}
                    >
                      后移
                    </button>
                    {["failed", "cancelled", "paused"].includes(item.status) ? (
                      <>
                        <button
                          type="button"
                          disabled={busy || disabled}
                          onClick={() => void run([item])}
                        >
                          重试
                        </button>
                        <button
                          type="button"
                          disabled={busy}
                          onClick={() => removeQueued(item.key)}
                        >
                          移除
                        </button>
                      </>
                    ) : (
                      <button
                        type="button"
                        disabled={item.status === "saving"}
                        onClick={() => void removeQueued(item.key)}
                      >
                        取消
                      </button>
                    )}
                  </div>
                </div>
              </div>
            ))}
          </div>
          {queueVisible < pending.length && (
            <button
              type="button"
              className="text-button"
              onClick={() => setQueueVisible((count) => count + 24)}
            >
              显示更多待上传图片
            </button>
          )}
        </div>
      )}
      <div className="upload-summary">
        <span>
          已保存 {product?.imageTotal ?? images.length} 张
          {pending.length ? ` · 待处理 ${pending.length} 张` : ""}
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
