export const PRODUCT_UPLOAD_BATCH_MAX = 99;
export const PRODUCT_UPLOAD_CONCURRENCY_MAX = 4;

export function validateProductBatch(
  files: readonly Pick<File, "name" | "size" | "type">[],
  maxBytes: number,
): string {
  if (!files.length || files.length > PRODUCT_UPLOAD_BATCH_MAX)
    return "每次请选择 1—99 张图片，可以分批继续追加。";
  for (const file of files) {
    if (!["image/jpeg", "image/png", "image/webp"].includes(file.type))
      return `${file.name}：请选择 JPG、PNG 或 WebP 图片。`;
    if (file.size <= 0 || file.size > maxBytes)
      return `${file.name}：图片不能为空，单张不能超过 ${Math.round(maxBytes / 1024 / 1024)} MB。`;
  }
  return "";
}

type RecordData = Record<string, any>;
type Request = (
  path: string,
  method?: string,
  body?: unknown,
  signal?: AbortSignal,
) => Promise<{ data?: any }>;
type Transfer = (
  url: string,
  bytes: Blob,
  headers: Record<string, string>,
  signal?: AbortSignal,
  progress?: (percent: number) => void,
) => Promise<void>;

/** Each application owns an instance: attempts and tickets never cross user/admin sessions. */
export function createProductUploader<T>(
  request: Request,
  image: (record: RecordData) => T,
  transfer: Transfer = transferProductChunk,
) {
  let attempts = new WeakMap<File, string>();
  let generation = 0;
  const data = (response: { data?: any }): RecordData =>
    response.data ?? response;
  return {
    reset() {
      generation++;
      attempts = new WeakMap();
    },
    async cancel(file: File) {
      const id = attempts.get(file);
      if (!id) return;
      await request(`/uploads/${encodeURIComponent(id)}`, "DELETE");
      attempts.delete(file);
    },
    async upload(
      file: File,
      progress?: (percent: number) => void,
      signal?: AbortSignal,
    ): Promise<T> {
      const startedIn = generation;
      const checkSession = () => {
        if (startedIn !== generation)
          throw new DOMException("登录状态已变更", "AbortError");
      };
      let id = attempts.get(file);
      let state: RecordData;
      if (!id) {
        state = data(
          await request(
            "/uploads",
            "POST",
            {
              filename: file.name,
              mime_type: file.type,
              byte_size: file.size,
              purpose: "product",
              resumable: true,
            },
            signal,
          ),
        );
        checkSession();
        if (!state.media_id) throw new Error("上传服务未返回有效凭据。");
        id = String(state.media_id);
        attempts.set(file, id);
      } else {
        state = data(
          await request(
            `/uploads/${encodeURIComponent(id)}/status`,
            "GET",
            undefined,
            signal,
          ),
        );
      }
      checkSession();
      if (
        ["cancelled", "expired", "blocked", "failed"].includes(state.status)
      ) {
        attempts.delete(file);
        throw new Error("此次上传已失效，请重新选择图片。");
      }
      let offset = Number(state.offset ?? 0);
      const chunkSize = Number(state.chunk_size);
      if (
        !Number.isSafeInteger(offset) ||
        offset < 0 ||
        offset > file.size ||
        !Number.isSafeInteger(chunkSize) ||
        chunkSize < 1 ||
        chunkSize > 8 * 1024 * 1024
      )
        throw new Error("上传进度无效，请重新选择图片。");
      progress?.(Math.min(99, Math.floor((offset / file.size) * 100)));
      while (offset < file.size) {
        checkSession();
        if (signal?.aborted) throw new DOMException("已暂停", "AbortError");
        const end = Math.min(offset + chunkSize, file.size);
        const start = offset;
        await transfer(
          String(state.upload_url ?? ""),
          file.slice(start, end),
          {
            "Content-Type": "application/octet-stream",
            "Content-Range": `bytes ${start}-${end - 1}/${file.size}`,
          },
          signal,
          (percent) =>
            progress?.(
              Math.min(
                99,
                Math.floor(
                  ((start + ((end - start) * percent) / 100) / file.size) * 100,
                ),
              ),
            ),
        );
        offset = end;
        progress?.(Math.min(99, Math.floor((offset / file.size) * 100)));
      }
      checkSession();
      // A lost final response is retried through status + idempotent complete, never a new object.
      const completed = data(
        await request(
          `/uploads/${encodeURIComponent(id)}/complete`,
          "POST",
          undefined,
          signal,
        ),
      );
      checkSession();
      if (completed.status !== "ready")
        throw new Error("图片仍在处理中，请稍后继续。");
      progress?.(100);
      return image({ ...completed, id, alt: file.name });
    },
  };
}

export function transferProductChunk(
  url: string,
  bytes: Blob,
  headers: Record<string, string>,
  signal?: AbortSignal,
  progress?: (percent: number) => void,
): Promise<void> {
  const target = new URL(url, window.location.origin);
  if (
    target.origin !== window.location.origin ||
    !/^\/api\/v1\/uploads\/[^/]+\//.test(target.pathname)
  )
    return Promise.reject(new Error("上传地址无效，请重新登录后再试。"));
  return new Promise((resolve, reject) => {
    if (signal?.aborted) {
      reject(new DOMException("已暂停", "AbortError"));
      return;
    }
    const xhr = new XMLHttpRequest();
    const abort = () => xhr.abort();
    const clean = () => signal?.removeEventListener("abort", abort);
    xhr.open("PUT", target.href);
    xhr.withCredentials = true;
    xhr.timeout = 120000;
    for (const [key, value] of Object.entries(headers))
      xhr.setRequestHeader(key, value);
    xhr.upload.onprogress = (event) => {
      if (event.lengthComputable)
        progress?.((event.loaded / event.total) * 100);
    };
    xhr.onload = () => {
      clean();
      xhr.status >= 200 && xhr.status < 300
        ? resolve()
        : reject(new Error(`图片上传未完成（${xhr.status}），请继续或重试。`));
    };
    xhr.onerror = xhr.ontimeout = () => {
      clean();
      reject(new Error("网络中断，已完成的部分可以继续上传。"));
    };
    xhr.onabort = () => {
      clean();
      reject(new DOMException("已暂停", "AbortError"));
    };
    signal?.addEventListener("abort", abort, { once: true });
    xhr.send(bytes);
  });
}
