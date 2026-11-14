import {
  createProductUploader,
  PRODUCT_UPLOAD_BATCH_MAX,
  PRODUCT_UPLOAD_CONCURRENCY_MAX,
} from "../../../共享/product-upload";
import type { ImageItem, ImagePage, Product, User } from "../types";
import { ApiError } from "./query";
import { readLevel } from "../../../共享/level";
import {
  responseCrypto,
  ResponseCryptoError,
} from "../../../共享/response-crypto";
export { ApiError } from "./query";
export function createRequestId(): string {
  if (typeof crypto.randomUUID === "function") return crypto.randomUUID();
  const bytes = crypto.getRandomValues(new Uint8Array(16));
  bytes[6] = (bytes[6] & 15) | 64;
  bytes[8] = (bytes[8] & 63) | 128;
  const hex = [...bytes].map((b) => b.toString(16).padStart(2, "0")).join("");
  return `${hex.slice(0, 8)}-${hex.slice(8, 12)}-${hex.slice(12, 16)}-${hex.slice(16, 20)}-${hex.slice(20)}`;
}
const retryIds = new Map<string, string>();
export async function idempotentRequest(
  path: string,
  body: RecordData,
): Promise<Envelope> {
  const key = `${path}:${JSON.stringify(body)}`;
  const requestId = retryIds.get(key) ?? createRequestId();
  retryIds.set(key, requestId);
  // Keep the key after network/HTTP failure: a response can be lost after the server commits.
  const result = await request(path, "POST", {
    ...body,
    request_id: requestId,
  });
  retryIds.delete(key);
  return result;
}
const base = (import.meta.env.VITE_API_BASE_URL || "/api/v1").replace(
  /\/$/,
  "",
);
const positive = (value: unknown, fallback: number) =>
  Number.isFinite(Number(value)) && Number(value) > 0
    ? Number(value)
    : fallback;
export const uploadConfig = {
  maxBytes: positive(import.meta.env.VITE_UPLOAD_MAX_BYTES, 10 * 1024 * 1024),
  batchSize: PRODUCT_UPLOAD_BATCH_MAX,
  concurrency: Math.min(
    PRODUCT_UPLOAD_CONCURRENCY_MAX,
    Math.floor(positive(import.meta.env.VITE_UPLOAD_CONCURRENCY, 3)),
  ),
};
type RecordData = Record<string, any>;
type Envelope = {
  data?: any;
  next_cursor?: string | null;
  total?: number;
  version?: number;
  code?: string;
  message?: string;
  fields?: Record<string, string>;
};
export async function request(
  path: string,
  method = "GET",
  body?: unknown,
  signal?: AbortSignal,
): Promise<Envelope> {
  let response: Response;
  try {
    response = await responseCrypto.fetch(`${base}${path}`, {
      method,
      credentials: "include",
      headers: {
        Accept: "application/json",
        ...(body === undefined ? {} : { "Content-Type": "application/json" }),
      },
      body: body === undefined ? undefined : JSON.stringify(body),
      signal,
    });
  } catch (error) {
    if (error instanceof DOMException && error.name === "AbortError")
      throw error;
    if (error instanceof ResponseCryptoError)
      throw new ApiError(error.message, 0, "RESPONSE_SECURITY");
    throw new ApiError("无法连接服务，请检查网络后重试。", 0, "NETWORK_ERROR");
  }
  if (response.status === 204) {
    if (path === "/auth/logout") responseCrypto.reset(true, false);
    return {};
  }
  let value: Envelope;
  const responseEpoch = responseCrypto.epoch();
  try {
    value = await response.json();
    if (responseEpoch !== responseCrypto.epoch())
      throw new DOMException("登录状态已变更", "AbortError");
  } catch (error) {
    if (error instanceof DOMException && error.name === "AbortError")
      throw error;
    throw new ApiError(
      "服务返回了无法识别的响应，请稍后重试。",
      response.status,
      "INVALID_RESPONSE",
    );
  }
  if (!response.ok)
    throw new ApiError(
      value.message ||
        ({
          401: "登录已失效，请重新登录。",
          404: "内容不存在或尚未公开。",
          409: "内容已更新，请重新加载后操作。",
          410: "此账号已下架。",
          429: "操作频繁，请稍后重试。",
        }[response.status] ??
          "请求失败，请重试。"),
      response.status,
      value.code,
      value.fields,
    );
  if (
    path === "/auth/logout" ||
    (path === "/auth/login" && typeof value.data?.id === "string")
  )
    responseCrypto.reset(true, false);
  return value;
}
const unwrap = (e: Envelope) => e.data ?? e;
const records = (e: Envelope): RecordData[] => {
  const d = unwrap(e);
  return Array.isArray(d) ? d : (d.items ?? d.products ?? []);
};
const nextCursor = (e: Envelope): string | null =>
  e.next_cursor ?? unwrap(e).next_cursor ?? null;
const maskPhone = (phone: unknown) =>
  String(phone ?? "").replace(/^(\d{3})\d{4}(\d{4})$/, "$1****$2");
const userFrom = (value: RecordData): User => ({
  id: String(value.id),
  nickname: value.nickname || "清叶用户",
  phone: maskPhone(value.phone_masked ?? value.phone),
  role: value.role === "admin" ? "admin" : "member",
});
function imageFrom(i: RecordData): ImageItem {
  return {
    id: String(i.id ?? i.media_id),
    url: i.url ?? i.preview_url ?? i.thumbnail_url,
    thumbnailUrl: i.thumbnail_url ?? i.thumbnailUrl,
    art: i.art,
    alt: i.alt ?? i.filename ?? "账号展示图片",
  };
}
export function productFrom(value: RecordData): Product {
  const a = value.attributes ?? {};
  const labels = value.attribute_labels ?? {};
  const images = (
    Array.isArray(value.images) ? value.images : (value.images?.items ?? [])
  ).map(imageFrom);
  const price = value.price_amount ?? value.price;
  return {
    id: String(value.id),
    code: String(value.product_code ?? value.public_id ?? value.id),
    categoryId: value.category_id,
    source: value.source,
    recommendationBoost: Number(value.recommendation_boost ?? 0),
    recommendationVersion: Number(value.recommendation_version ?? 1),
    legacyPrice:
      value.legacy_price == null ? undefined : String(value.legacy_price),
    legacyCategory: value.legacy_category,
    priceNote: value.price_note,
    rentalCategory:
      value.rental_category === "hour_day" ||
      value.rental_category === "week_month"
        ? value.rental_category
        : undefined,
    rentalQuotes: Array.isArray(value.rental_quotes)
      ? value.rental_quotes.map((q: RecordData) => ({
          unit: q.unit,
          amount: String(q.amount),
          ...(q.unit === "period"
            ? {
                label: q.label,
                billing_unit: q.billing_unit || "period",
                ...(q.start_time
                  ? { start_time: q.start_time, end_time: q.end_time }
                  : {}),
              }
            : {}),
        }))
      : [],
    hasPrivateCredentials: !!value.has_private_credentials,
    consultationCount: Number(value.consultation_count ?? 0),
    attributes: a,
    attributeLabels: labels,
    title: value.title ?? "未命名账号",
    mode: value.trade_type ?? value.mode ?? "sale",
    unit: value.price_unit ?? value.unit ?? "account",
    price:
      price === undefined || price === null || price === ""
        ? null
        : Number(price),
    server: labels.server ?? a.server ?? value.server ?? "未填写",
    gender: a.gender ?? value.gender ?? "unknown",
    level: readLevel(a.level ?? a.xvip ?? value.level),
    kind: a.kind ?? value.kind ?? "single",
    tags: [
      ...new Set(
        [
          ...(Array.isArray(a.tags ?? value.tags)
            ? (a.tags ?? value.tags)
            : []),
          ...(Array.isArray(a.custom_tags) ? a.custom_tags : []),
        ].filter((tag): tag is string => typeof tag === "string"),
      ),
    ],
    description: value.description ?? "",
    art: Number(value.art ?? -1),
    images,
    imageTotal:
      value.image_total ??
      value.image_count ??
      value.images?.total ??
      images.length,
    status:
      value.status === "approved" ? "published" : (value.status ?? "published"),
    publicationInterrupted: value.publication_interrupted === true,
    version: Number(value.version ?? 1),
    verification:
      value.verification_status ?? value.verification ?? "unprovided",
    rare: !!value.rare,
    publishedAt: value.first_published_at ?? value.published_at ?? "",
    createdAt: value.created_at,
    updatedAt: value.updated_at,
    deletedAt: value.deleted_at,
    reviewNote: value.review_note ?? value.review_reason,
    favorite: value.favorite,
    editorial: value.editorial,
  };
}
export function imagePageFrom(e: Envelope): ImagePage {
  return {
    items: records(e).map(imageFrom),
    total: Number(e.total ?? unwrap(e).total ?? records(e).length),
    nextCursor: nextCursor(e),
    version: Number(e.version ?? unwrap(e).version ?? 1),
  };
}
const uploaded = new Map<string, ImageItem>();
const productUploader = createProductUploader(request, (record) => {
  const image = imageFrom({
    ...record,
    url:
      record.url ??
      record.preview_url ??
      `${base}/media/${encodeURIComponent(record.id)}/preview`,
  });
  uploaded.set(image.id, image);
  return image;
});
async function xhrUpload(
  url: string,
  method: string,
  body: XMLHttpRequestBodyInit,
  headers: Record<string, string>,
  onProgress?: (percent: number) => void,
  signal?: AbortSignal,
): Promise<void> {
  const context = await responseCrypto.prepare(url, method, headers);
  return new Promise((resolve, reject) => {
    const xhr = new XMLHttpRequest();
    const abort = () => xhr.abort();
    const cleanup = () => signal?.removeEventListener("abort", abort);
    xhr.open(method, url);
    xhr.responseType = "arraybuffer";
    context.headers.forEach((v, k) => xhr.setRequestHeader(k, v));
    xhr.upload.onprogress = (e) => {
      if (e.lengthComputable)
        onProgress?.(Math.round((e.loaded / e.total) * 100));
    };
    xhr.onload = async () => {
      cleanup();
      try {
        const response = await responseCrypto.decodeXHR(xhr, context);
        if (!response.ok)
          throw new ApiError(
            `图片上传失败（${xhr.status}），可重试。`,
            xhr.status,
            "UPLOAD_FAILED",
          );
        resolve();
      } catch (error) {
        reject(error);
      }
    };
    xhr.onerror = () => {
      cleanup();
      reject(new ApiError("图片上传中断，请重试。", 0, "UPLOAD_FAILED"));
    };
    xhr.onabort = () => {
      cleanup();
      reject(new DOMException("已取消", "AbortError"));
    };
    signal?.addEventListener("abort", abort, { once: true });
    if (signal?.aborted) {
      cleanup();
      reject(new DOMException("已取消", "AbortError"));
      return;
    }
    xhr.send(body);
  });
}
interface UploadAttempt {
  id: string;
  signed: RecordData;
  transferred: boolean;
}
const uploadAttempts = new WeakMap<File, UploadAttempt>();
function pause(ms: number, signal?: AbortSignal): Promise<void> {
  return new Promise((resolve, reject) => {
    const abort = () => {
      clearTimeout(timer);
      reject(new DOMException("已取消", "AbortError"));
    };
    const timer = setTimeout(() => {
      signal?.removeEventListener("abort", abort);
      resolve();
    }, ms);
    signal?.addEventListener("abort", abort, { once: true });
    if (signal?.aborted) abort();
  });
}
async function uploadRemote(
  file: File,
  onProgress?: (percent: number) => void,
  signal?: AbortSignal,
): Promise<ImageItem> {
  let attempt = uploadAttempts.get(file);
  if (!attempt) {
    const signed = unwrap(
      await request(
        "/uploads",
        "POST",
        {
          filename: file.name,
          mime_type: file.type,
          byte_size: file.size,
          purpose: "contact",
        },
        signal,
      ),
    );
    if (!(signed.media_id ?? signed.id) || !(signed.upload_url ?? signed.url))
      throw new ApiError(
        "上传服务未返回有效地址。",
        502,
        "INVALID_UPLOAD_RESPONSE",
      );
    attempt = {
      id: String(signed.media_id ?? signed.id),
      signed,
      transferred: false,
    };
    uploadAttempts.set(file, attempt);
  }
  const { id, signed } = attempt;
  if (!attempt.transferred) {
    let body: XMLHttpRequestBodyInit = file;
    if (signed.fields) {
      const form = new FormData();
      for (const [key, value] of Object.entries(signed.fields))
        form.append(key, String(value));
      form.append("file", file);
      body = form;
    }
    try {
      await xhrUpload(
        signed.upload_url ?? signed.url,
        signed.method ?? (signed.fields ? "POST" : "PUT"),
        body,
        signed.headers ?? {},
        onProgress,
        signal,
      );
    } catch (error) {
      if (error instanceof ApiError && [401, 403].includes(error.status))
        uploadAttempts.delete(file);
      throw error;
    }
    attempt.transferred = true;
  }
  // Completion is idempotent. Retry processing without retransmitting the same object.
  for (let check = 0; check < 5; check++) {
    const completed = unwrap(
      await request(
        `/uploads/${encodeURIComponent(id)}/complete`,
        "POST",
        undefined,
        signal,
      ),
    );
    const state = completed.status ?? completed.state;
    if (state === "ready") {
      const image = imageFrom({
        ...completed,
        id,
        url:
          completed.url ??
          completed.preview_url ??
          `${base}/media/${encodeURIComponent(id)}/preview`,
        alt: file.name,
      });
      uploaded.set(id, image);
      return image;
    }
    if (["failed", "blocked"].includes(state))
      throw new ApiError(
        "图片未通过处理或校验，请更换图片重试。",
        422,
        "MEDIA_REJECTED",
      );
    if (!["uploaded", "pending", "processing"].includes(state))
      throw new ApiError(
        "上传服务尚未确认图片可用，请稍后重试。",
        502,
        "INVALID_UPLOAD_RESPONSE",
      );
    if (check < 4) await pause(700, signal);
  }
  throw new ApiError(
    "图片仍在处理中，可稍后重试；已传输的文件不会重复上传。",
    409,
    "MEDIA_NOT_READY",
  );
}

export const api = {
  async cancelUpload(file: File): Promise<void> {
    await productUploader.cancel(file);
  },
  async me(): Promise<User | null> {
    try {
      const user = userFrom(unwrap(await request("/me")));
      if (user.role !== "admin")
        throw new ApiError("当前账户没有管理员权限。", 403, "ADMIN_REQUIRED");
      return user;
    } catch (error) {
      if (error instanceof ApiError && error.status === 401) return null;
      throw error;
    }
  },
  async login(phone: string, password: string): Promise<User> {
    productUploader.reset();
    const user = userFrom(
      unwrap(
        await request("/auth/login", "POST", {
          method: "password",
          phone,
          password,
        }),
      ),
    );
    if (user.role !== "admin") {
      await request("/auth/logout", "POST").catch(() => {});
      throw new ApiError("当前账户没有管理员权限。", 403, "ADMIN_REQUIRED");
    }
    return user;
  },
  async logout() {
    productUploader.reset();
    await request("/auth/logout", "POST");
  },
  async uploadProduct(
    file: File,
    onProgress?: (percent: number) => void,
    signal?: AbortSignal,
  ): Promise<ImageItem> {
    if (!["image/jpeg", "image/png", "image/webp"].includes(file.type))
      throw new ApiError(
        "请选择 JPG、PNG 或 WebP 图片。",
        422,
        "INVALID_IMAGE_TYPE",
      );
    if (file.size > uploadConfig.maxBytes)
      throw new ApiError("图片超过单文件大小限制。", 413, "IMAGE_TOO_LARGE");
    if (signal?.aborted) throw new DOMException("已取消", "AbortError");
    onProgress?.(0);
    return productUploader.upload(file, onProgress, signal);
  },
  async upload(
    file: File,
    onProgress?: (percent: number) => void,
    signal?: AbortSignal,
  ): Promise<ImageItem> {
    if (!["image/jpeg", "image/png", "image/webp"].includes(file.type))
      throw new ApiError(
        "请选择 JPG、PNG 或 WebP 图片。",
        422,
        "INVALID_IMAGE_TYPE",
      );
    if (file.size > uploadConfig.maxBytes)
      throw new ApiError("图片超过单文件大小限制。", 413, "IMAGE_TOO_LARGE");
    if (signal?.aborted) throw new DOMException("已取消", "AbortError");
    onProgress?.(0);
    return uploadRemote(file, onProgress, signal);
  },
};
