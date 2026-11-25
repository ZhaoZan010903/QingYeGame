import { responseCrypto, ResponseCryptoError } from "./response-crypto";
import {
  discardProductImage,
  readProductImage,
  storeProductImage,
} from "./product-image-cache";

const OSS_HOST = "qingye-images-20261.oss-cn-beijing.aliyuncs.com";
const IMAGE_TYPES = new Set([
  "image/jpeg",
  "image/png",
  "image/webp",
  "image/gif",
]);
export type DeliveredImage = {
  url: string;
  mode: "legacy" | "oss" | "oss-blob" | "go";
  expiresAt?: number;
  invalidate?: () => void;
  dispose: () => void;
};
export type ProductMediaScope = { productId?: string; productVersion?: number };
export function mediaReference(src: string, scope: ProductMediaScope = {}) {
  const url = new URL(src, window.location.href);
  const match = /^\/api\/v1\/media\/([a-zA-Z0-9-]+)(?:\/(file|preview))?$/.exec(
    url.pathname,
  );
  if (url.origin !== window.location.origin || !match)
    throw new Error("图片地址无效。");
  const urlProduct = url.searchParams.get("product_id");
  const urlVersion =
    url.searchParams.get("product_version") ?? url.searchParams.get("version");
  if (
    (urlProduct && scope.productId && urlProduct !== scope.productId) ||
    (urlVersion &&
      scope.productVersion !== undefined &&
      Number(urlVersion) !== scope.productVersion)
  )
    throw new Error("图集版本不一致，请刷新后查看。");
  const product = scope.productId ?? urlProduct;
  const version =
    scope.productVersion ??
    (urlVersion === null ? undefined : Number(urlVersion));
  if (
    (product || version !== undefined) &&
    (!product ||
      !/^[a-zA-Z0-9-]+$/.test(product) ||
      !Number.isSafeInteger(version) ||
      version! < 1)
  )
    throw new Error("图集版本不完整，请刷新后查看。");
  return {
    id: match[1],
    variant:
      url.searchParams.get("variant") === "thumb" || match[2] === "preview"
        ? "thumb"
        : "original",
    ...(product ? { product_id: product, product_version: version } : {}),
  };
}
function failure(status: number, message?: string) {
  return Object.assign(new Error(message || "图片暂时无法读取，请稍后重试。"), {
    status,
  });
}
export class ExpiredImageLink extends Error {}
class UnsupportedThumbnail extends Error {}

function blobImage(
  blob: Blob,
  mode: "go" | "oss-blob",
  key?: string,
): DeliveredImage {
  const url = URL.createObjectURL(blob);
  let disposed = false;
  return {
    url,
    mode,
    invalidate: key
      ? () => {
          void discardProductImage(key).catch(() => {});
        }
      : undefined,
    dispose: () => {
      if (!disposed) URL.revokeObjectURL(url);
      disposed = true;
    },
  };
}
async function requireOK(response: Response) {
  if (response.ok) return;
  let message: string | undefined;
  if (response.headers.get("Content-Type")?.includes("application/json")) {
    const body = await response.json();
    if (typeof body?.message === "string") message = body.message;
  }
  throw failure(response.status, message);
}
async function goImage(
  url: string,
  signal: AbortSignal,
  expectedMime?: string,
): Promise<DeliveredImage> {
  const epoch = responseCrypto.epoch();
  const response = await responseCrypto.fetch(url, {
    credentials: "include",
    signal,
  });
  await requireOK(response);
  const mime = response.headers
    .get("Content-Type")
    ?.split(";")[0]
    .trim()
    .toLowerCase();
  if (
    !mime ||
    !IMAGE_TYPES.has(mime) ||
    (expectedMime && mime !== expectedMime)
  )
    throw failure(502);
  const blob = await response.blob();
  if (!blob.size || blob.size > 64 * 1024 * 1024) throw failure(502);
  if (signal.aborted) throw signal.reason;
  if (epoch !== responseCrypto.epoch())
    throw new DOMException("登录状态已变更", "AbortError");
  return blobImage(blob, "go");
}

type Download = {
  abort: AbortController;
  result: Promise<Blob | undefined>;
  users: number;
};
const downloads = new Map<string, Download>();
let retryCorsAt = 0;

async function productBytes(
  key: string,
  url: URL,
  mime: string,
  expires: number,
  epoch: number,
  thumbnail: boolean,
  signal: AbortSignal,
) {
  const cached = await readProductImage(key).catch(() => undefined);
  signal.throwIfAborted();
  if (cached && cached.type === mime) return cached;
  if (expires <= performance.now()) throw new ExpiredImageLink();
  if (Date.now() < retryCorsAt) return;
  let response: Response;
  try {
    response = await fetch(url.href, {
      mode: "cors",
      credentials: "omit",
      cache: "no-store",
      redirect: "error",
      referrerPolicy: "no-referrer",
      signal,
    });
  } catch (error) {
    signal.throwIfAborted();
    if (!(error instanceof TypeError)) throw error;
    // A bucket or browser may allow <img> while disallowing readable fetches.
    retryCorsAt = Date.now() + 60000;
    return;
  }
  if (response.status === 403) throw new ExpiredImageLink();
  if (thumbnail && response.status === 400) throw new UnsupportedThumbnail();
  if (
    !response.ok ||
    response.headers.get("Content-Type")?.split(";")[0].trim().toLowerCase() !==
      mime ||
    Number(response.headers.get("Content-Length")) > 64 * 1024 * 1024
  )
    throw failure(502);
  const bytes = await response.blob();
  if (!bytes.size || bytes.size > 64 * 1024 * 1024) throw failure(502);
  signal.throwIfAborted();
  if (responseCrypto.epoch() !== epoch)
    throw new DOMException("登录状态已变更", "AbortError");
  const blob = bytes.type === mime ? bytes : bytes.slice(0, bytes.size, mime);
  void storeProductImage(key, blob).catch(() => {});
  return blob;
}

// Repeated covers (hero + catalog, or two fragments of an animated slide)
// share image bytes. Unmounting one consumer cannot cancel another's request.
function sharedProductBytes(
  key: string,
  get: (signal: AbortSignal) => Promise<Blob | undefined>,
  signal: AbortSignal,
) {
  signal.throwIfAborted();
  let download = downloads.get(key);
  if (!download) {
    const abort = new AbortController();
    const entry = { abort, result: get(abort.signal), users: 0 };
    download = entry;
    downloads.set(key, entry);
    const done = () => {
      if (downloads.get(key) === entry) downloads.delete(key);
    };
    void entry.result.then(done, done);
  }
  const entry = download;
  entry.users++;
  return new Promise<Blob | undefined>((resolve, reject) => {
    let finished = false;
    const leave = () => {
      if (finished) return;
      finished = true;
      signal.removeEventListener("abort", abort);
      if (--entry.users === 0 && downloads.get(key) === entry) {
        downloads.delete(key);
        entry.abort.abort();
      }
    };
    const abort = () => {
      leave();
      reject(signal.reason);
    };
    signal.addEventListener("abort", abort, { once: true });
    void entry.result.then(
      (blob) => {
        leave();
        resolve(blob);
      },
      (error) => {
        leave();
        reject(error);
      },
    );
  });
}
/** Plans are requested at display time, never for the complete off-screen gallery. */
export async function resolveMediaImage(
  src: string,
  signal: AbortSignal,
  scope: ProductMediaScope = {},
): Promise<DeliveredImage> {
  const reference = mediaReference(src, scope);
  const policy = await responseCrypto.ensure();
  if (signal.aborted) throw signal.reason;
  if (!policy.media_access_plan) {
    if (policy.enabled) return goImage(src, signal);
    return { url: src, mode: "legacy", dispose: () => {} };
  }
  const requestedAt = performance.now();
  const epoch = responseCrypto.epoch();
  const response = await responseCrypto.fetch("/api/v1/media/access-plan", {
    method: "POST",
    credentials: "include",
    signal,
    headers: { "Content-Type": "application/json", Accept: "application/json" },
    body: JSON.stringify({ items: [reference] }),
  });
  await requireOK(response);
  const data = (await response.json())?.data;
  if (epoch !== responseCrypto.epoch())
    throw new DOMException("登录状态已变更", "AbortError");
  const item = data?.items?.[0];
  if (
    !Array.isArray(data?.items) ||
    data.items.length !== 1 ||
    !item ||
    item.id !== reference.id ||
    item.variant !== reference.variant ||
    !["oss", "go"].includes(item.mode) ||
    !IMAGE_TYPES.has(item.mime_type)
  )
    throw new ResponseCryptoError("图片访问凭据无效，请稍后重试。");
  const lifetime = Date.parse(item.expires_at) - Date.parse(data.server_time);
  if (
    !Number.isFinite(lifetime) ||
    lifetime <= 0 ||
    lifetime > (item.mode === "oss" ? 10000 : 60000)
  )
    throw new ResponseCryptoError();
  const url = new URL(item.url, window.location.origin);
  if (url.username || url.password || url.hash) throw new ResponseCryptoError();
  if (item.mode === "oss") {
    if (url.protocol !== "https:" || url.hostname !== OSS_HOST || url.port)
      throw new ResponseCryptoError();
    const identity = responseCrypto.imageCacheIdentity();
    if (
      identity &&
      typeof item.cache_key === "string" &&
      /^[a-f0-9]{64}$/.test(item.cache_key)
    ) {
      const key = JSON.stringify([
        identity,
        reference.id,
        reference.variant,
        item.cache_key,
      ]);
      const download = sharedProductBytes(
        JSON.stringify([epoch, key]),
        (abort) =>
          productBytes(
            key,
            url,
            item.mime_type,
            requestedAt + lifetime,
            epoch,
            reference.variant === "thumb",
            abort,
          ),
        signal,
      );
      let blob: Blob | undefined;
      try {
        blob = await download;
      } catch (error) {
        if (
          error instanceof UnsupportedThumbnail &&
          epoch === responseCrypto.epoch()
        ) {
          // Historical images can exceed OSS processing dimension limits. An
          // explicitly authorized original has its own budget, MIME and cache key.
          const original = new URL(src, window.location.href);
          original.pathname = `/api/v1/media/${reference.id}/file`;
          original.searchParams.set("variant", "original");
          return resolveMediaImage(original.href, signal, scope);
        }
        throw error;
      }
      signal.throwIfAborted();
      if (epoch !== responseCrypto.epoch())
        throw new DOMException("登录状态已变更", "AbortError");
      if (blob) return blobImage(blob, "oss-blob", key);
    }
    return {
      url: url.href,
      mode: "oss",
      expiresAt: requestedAt + lifetime,
      dispose: () => {},
    };
  }
  if (
    url.origin !== window.location.origin ||
    url.pathname !== `/api/v1/media/${reference.id}/file`
  )
    throw new ResponseCryptoError();
  // A server-generated thumbnail may use JPEG even when its source is PNG/WebP.
  // Its authenticated response MIME still must be an allowed raster image type.
  return goImage(
    url.pathname + url.search,
    signal,
    reference.variant === "original" ? item.mime_type : undefined,
  );
}

let active = 0;
type Waiter = {
  signal: AbortSignal;
  start: (release: () => void) => void;
  reject: (reason: unknown) => void;
  abort: () => void;
};
const pending: Waiter[] = [];
function drain() {
  while (active < 4 && pending.length) {
    const next = pending.shift()!;
    next.signal.removeEventListener("abort", next.abort);
    if (next.signal.aborted) {
      next.reject(next.signal.reason);
      continue;
    }
    active++;
    let released = false;
    next.start(() => {
      if (released) return;
      released = true;
      active--;
      drain();
    });
  }
}
/** Hold the slot through image decode; URL signing cannot outrun browser image requests. */
export function acquireMediaSlot(signal: AbortSignal): Promise<() => void> {
  if (signal.aborted) return Promise.reject(signal.reason);
  return new Promise((resolve, reject) => {
    const waiter: Waiter = {
      signal,
      start: resolve,
      reject,
      abort: () => {
        const index = pending.indexOf(waiter);
        if (index >= 0) pending.splice(index, 1);
        reject(signal.reason);
      },
    };
    signal.addEventListener("abort", waiter.abort, { once: true });
    pending.push(waiter);
    drain();
  });
}
