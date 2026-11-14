import {
  createProductUploader,
  PRODUCT_UPLOAD_BATCH_MAX,
  PRODUCT_UPLOAD_CONCURRENCY_MAX,
} from '../../../共享/product-upload';
import type {
  Category,
  Contact,
  ContentPage,
  HomeData,
  ImageItem,
  ImagePage,
  Product,
  ProductPage,
  ProductWrite,
  User,
} from '../types';
import { ApiError, toApiQuery } from './query';
import { readHomePresentation } from './home-content';
import { readLevel } from '../../../共享/level';

// The fixture module is loaded only by explicit demo branches, never as an API fallback.
const loadDemoCatalog = () => import('./demo-catalog');
export { ApiError } from './query';
export const isDemo = (import.meta.env.VITE_DATA_MODE ?? 'api') === 'demo';
export const environmentLabel = isDemo
  ? '演示环境 · 商品与图片为样例，操作仅在当前设备保存'
  : String(import.meta.env.VITE_ENVIRONMENT_LABEL ?? '').trim();
export function createRequestId(): string {
  if (typeof crypto.randomUUID === 'function') return crypto.randomUUID();
  const bytes = crypto.getRandomValues(new Uint8Array(16));
  bytes[6] = (bytes[6] & 15) | 64;
  bytes[8] = (bytes[8] & 63) | 128;
  const hex = [...bytes].map((b) => b.toString(16).padStart(2, '0')).join('');
  return `${hex.slice(0, 8)}-${hex.slice(8, 12)}-${hex.slice(12, 16)}-${hex.slice(16, 20)}-${hex.slice(20)}`;
}
const retryIds = new Map<string, string>();
export async function idempotentRequest(path: string, body: RecordData): Promise<Envelope> {
  const key = `${path}:${JSON.stringify(body)}`;
  const requestId = retryIds.get(key) ?? createRequestId();
  retryIds.set(key, requestId);
  // Keep the key after network/HTTP failure: a response can be lost after the server commits.
  const result = await request(path, 'POST', { ...body, request_id: requestId });
  retryIds.delete(key);
  return result;
}
const base = (import.meta.env.VITE_API_BASE_URL || '/api/v1').replace(/\/$/, '');
const positive = (value: unknown, fallback: number) =>
  Number.isFinite(Number(value)) && Number(value) > 0 ? Number(value) : fallback;
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
  method = 'GET',
  body?: unknown,
  signal?: AbortSignal,
): Promise<Envelope> {
  let response: Response;
  try {
    response = await fetch(`${base}${path}`, {
      method,
      credentials: 'include',
      headers: {
        Accept: 'application/json',
        ...(body === undefined ? {} : { 'Content-Type': 'application/json' }),
      },
      body: body === undefined ? undefined : JSON.stringify(body),
      signal,
    });
  } catch (error) {
    if (error instanceof DOMException && error.name === 'AbortError') throw error;
    throw new ApiError('无法连接服务，请检查网络后重试。', 0, 'NETWORK_ERROR');
  }
  if (response.status === 204) return {};
  let value: Envelope;
  try {
    value = await response.json();
  } catch {
    throw new ApiError(
      '服务返回了无法识别的响应，请稍后重试。',
      response.status,
      'INVALID_RESPONSE',
    );
  }
  if (!response.ok)
    throw new ApiError(
      value.message ||
        ({
          401: '登录已失效，请重新登录。',
          404: '内容不存在或尚未公开。',
          409: '内容已更新，请重新加载后操作。',
          410: '此账号已下架。',
          429: '操作频繁，请稍后重试。',
        }[response.status] ??
          '请求失败，请重试。'),
      response.status,
      value.code,
      value.fields,
    );
  return value;
}
const unwrap = (e: Envelope) => e.data ?? e;
const records = (e: Envelope): RecordData[] => {
  const d = unwrap(e);
  return Array.isArray(d) ? d : (d.items ?? d.products ?? []);
};
const nextCursor = (e: Envelope): string | null => e.next_cursor ?? unwrap(e).next_cursor ?? null;
const maskPhone = (phone: unknown) =>
  String(phone ?? '').replace(/^(\d{3})\d{4}(\d{4})$/, '$1****$2');
const userFrom = (value: RecordData): User => ({
  id: String(value.id),
  nickname: value.nickname || '清叶用户',
  phone: maskPhone(value.phone_masked ?? value.phone),
  role: value.role === 'admin' ? 'admin' : 'member',
});
function imageFrom(i: RecordData): ImageItem {
  return {
    id: String(i.id ?? i.media_id),
    url: i.url ?? i.preview_url ?? i.thumbnail_url,
    thumbnailUrl: i.thumbnail_url ?? i.thumbnailUrl,
    art: i.art,
    alt: i.alt ?? i.filename ?? '账号展示图片',
  };
}
export function productFrom(value: RecordData): Product {
  const a = value.attributes ?? {};
  const attributeLabels = Object.fromEntries(
    ['server', 'gender', 'level', 'kind'].flatMap((key) =>
      typeof value.attribute_labels?.[key] === 'string' && value.attribute_labels[key]
        ? [[key, value.attribute_labels[key]]]
        : [],
    ),
  );
  const images = (Array.isArray(value.images) ? value.images : (value.images?.items ?? [])).map(
    imageFrom,
  );
  const price = value.price_amount ?? value.price;
  return {
    id: String(value.id),
    draftExpiresAt: value.draft_expires_at ?? null,
    code: String(value.product_code ?? value.public_id ?? value.id),
    categoryId: value.category_id,
    source: value.source,
    legacyPrice: value.legacy_price == null ? undefined : String(value.legacy_price),
    legacyCategory: value.legacy_category,
    priceNote: value.price_note,
    attributes: a,
    attributeLabels,
    title: value.title ?? '未命名账号',
    mode: value.trade_type ?? value.mode ?? 'sale',
    rentalCategory: value.rental_category || value.rentalCategory || undefined,
    unit: value.price_unit ?? value.unit ?? 'account',
    price: price === undefined || price === null || price === '' ? null : Number(price),
    server: attributeLabels.server ?? a.server ?? value.server ?? '未填写',
    gender: a.gender ?? value.gender ?? 'unknown',
    level: readLevel(a.level ?? a.xvip ?? value.level),
    kind: a.kind ?? value.kind ?? 'single',
    tags: [
      ...new Set([
        ...(Array.isArray(a.tags) ? a.tags : (value.tags ?? [])),
        ...(Array.isArray(a.custom_tags) ? a.custom_tags : []),
      ]),
    ],
    description: value.description ?? '',
    art: Number(value.art ?? -1),
    images,
    imageTotal: value.image_total ?? value.image_count ?? value.images?.total ?? images.length,
    status: value.status === 'approved' ? 'published' : (value.status ?? 'published'),
    publicationInterrupted: value.publication_interrupted === true,
    version: Number(value.version ?? 1),
    verification: value.verification_status ?? value.verification ?? 'unprovided',
    rare: !!value.rare,
    publishedAt: value.first_published_at ?? value.published_at ?? '',
    reviewNote: value.review_note ?? value.review_reason,
    favorite: value.favorite,
    editorial: value.editorial,
    rentalQuotes: Array.isArray(value.rental_quotes) ? value.rental_quotes : [],
    hasPrivateCredentials: !!value.has_private_credentials,
    consultationCount: Number(value.consultation_count ?? 0),
  };
}
export function productToWire(input: ProductWrite): RecordData {
  const attributes = { ...input.attributes };
  // Imported tags are preserved separately; only editable custom tags use the new limit.
  return {
    category_id: input.categoryId ?? 'qq-dance',
    title: input.title,
    trade_type: input.mode,
    price_unit: input.unit,
    price_amount: input.price === null ? null : input.price.toFixed(2),
    attributes: {
      server: input.server,
      gender: input.gender,
      level: input.level,
      ...attributes,
      custom_tags: input.tags,
    },
    description: input.description,
    rental_quotes: input.mode === 'rent' ? (input.rentalQuotes ?? []) : [],
    ...(input.privateCredentials
      ? {
          private_credentials: {
            qq_account: input.privateCredentials.qqAccount,
            qq_password: input.privateCredentials.qqPassword,
          },
        }
      : {}),
  };
}
function pageFrom(e: Envelope): ProductPage {
  return {
    items: records(e).map(productFrom),
    total: Number(e.total ?? unwrap(e).total ?? records(e).length),
    nextCursor: nextCursor(e),
  };
}
async function updatedProduct(id: string, e: Envelope): Promise<Product> {
  const data = unwrap(e);
  return data.id && typeof data.title === 'string'
    ? productFrom(data)
    : productFrom(unwrap(await request(`/me/products/${encodeURIComponent(id)}`)));
}
export function imagePageFrom(e: Envelope): ImagePage {
  return {
    items: records(e).map(imageFrom),
    total: Number(e.total ?? unwrap(e).total ?? records(e).length),
    nextCursor: nextCursor(e),
    version: Number(e.version ?? unwrap(e).version ?? 1),
  };
}
async function allPages(path: string): Promise<Product[]> {
  const out: Product[] = [];
  const seen = new Set<string>();
  let cursor: string | null = null;
  do {
    const e = await request(
      `${path}${path.includes('?') ? '&' : '?'}limit=50${cursor ? `&cursor=${encodeURIComponent(cursor)}` : ''}`,
    );
    out.push(...records(e).map(productFrom));
    cursor = nextCursor(e);
    if (cursor && seen.has(cursor))
      throw new ApiError('分页游标重复，请重新加载。', 500, 'INVALID_CURSOR');
    if (cursor) seen.add(cursor);
  } while (cursor);
  return out;
}

const storageKey = 'qingye.demo.v1';
interface DemoState {
  user: User | null;
  favorites: string[];
  products: Product[];
}
const emptyState = (): DemoState => ({ user: null, favorites: [], products: [] });
function readState(): DemoState {
  try {
    const s = JSON.parse(localStorage.getItem(storageKey) ?? 'null');
    return s && Array.isArray(s.products) && Array.isArray(s.favorites) ? s : emptyState();
  } catch {
    return emptyState();
  }
}
function writeState(state: DemoState) {
  try {
    localStorage.setItem(storageKey, JSON.stringify(state));
  } catch {
    throw new ApiError(
      '浏览器无法保存演示资料，请检查存储空间或隐私设置。',
      507,
      'LOCAL_STORAGE_FAILED',
    );
  }
}
function requireDemo(): DemoState {
  const state = readState();
  if (!state.user) throw new ApiError('请先登录演示账号。', 401, 'UNAUTHENTICATED');
  return state;
}
const clone = <T>(value: T): T => structuredClone(value);
async function demoProduct(id: string, privateView = false): Promise<Product> {
  const product = privateView
    ? requireDemo().products.find((p) => p.id === id)
    : (await loadDemoCatalog()).fixtureProducts.find((p) => p.id === id);
  if (!product) throw new ApiError('内容不存在或尚未公开。', 404, 'NOT_FOUND');
  return clone(product);
}
function editDemo(id: string, version: number, mutate: (p: Product) => void): Product {
  const state = requireDemo();
  const p = state.products.find((p) => p.id === id);
  if (!p) throw new ApiError('找不到这份资料。', 404, 'NOT_FOUND');
  if (p.version !== version)
    throw new ApiError('资料已更新，请重新加载后操作。', 409, 'VERSION_CONFLICT');
  if (p.status === 'published')
    throw new ApiError(
      '已展示商品的修改规则尚未确认，请联系客服处理。',
      409,
      'REVIEW_POLICY_PENDING',
    );
  mutate(p);
  p.version++;
  writeState(state);
  return clone(p);
}
function validateWrite(p: ProductWrite, submit = false) {
  if ((p.mode === 'sale' && p.unit !== 'account') || (p.mode === 'rent' && p.unit === 'account'))
    throw new ApiError('交易类型与报价单位不一致。', 422, 'INVALID_PRICE_UNIT');
  if (p.price !== null && (!Number.isFinite(p.price) || p.price < 0))
    throw new ApiError('参考报价须为非负金额。', 422, 'INVALID_PRICE');

  if (submit && (!p.title.trim() || !p.server.trim() || !p.description.trim()))
    throw new ApiError('请补全标题、游戏区服与账号介绍后提交。', 422, 'INCOMPLETE_PRODUCT');
}
function demoUnsupported(): never {
  throw new ApiError(
    '演示模式未连接短信和会员服务。请使用“体验演示账号”，不要填写真实凭据。',
    503,
    'DEMO_ONLY',
  );
}

let dbPromise: Promise<IDBDatabase> | undefined;
function mediaDb(): Promise<IDBDatabase> {
  return (dbPromise ??= new Promise((resolve, reject) => {
    const r = indexedDB.open('qingye-demo-media', 1);
    r.onupgradeneeded = () => r.result.createObjectStore('files');
    r.onsuccess = () => resolve(r.result);
    r.onerror = () => {
      dbPromise = undefined;
      reject(new ApiError('无法打开浏览器图片存储。', 507, 'MEDIA_STORAGE_FAILED'));
    };
  }));
}
async function storeBlob(id: string, file: File, signal?: AbortSignal): Promise<void> {
  const db = await mediaDb();
  await new Promise<void>((resolve, reject) => {
    const tx = db.transaction('files', 'readwrite');
    const abort = () => {
      try {
        tx.abort();
      } catch {}
    };
    signal?.addEventListener('abort', abort, { once: true });
    tx.objectStore('files').put(file, id);
    tx.oncomplete = () => {
      signal?.removeEventListener('abort', abort);
      resolve();
    };
    tx.onerror = () => {
      signal?.removeEventListener('abort', abort);
      reject(
        new ApiError('图片保存失败，请检查浏览器存储空间后重试。', 507, 'MEDIA_STORAGE_FAILED'),
      );
    };
    tx.onabort = () => {
      signal?.removeEventListener('abort', abort);
      reject(new DOMException('已取消', 'AbortError'));
    };
    if (signal?.aborted) abort();
  });
}
const objectUrls = new Map<string, string>();
async function hydrateImage(image: ImageItem): Promise<ImageItem> {
  if (!image.id.startsWith('local-')) return image;
  let url = objectUrls.get(image.id);
  if (!url) {
    const db = await mediaDb();
    const blob = await new Promise<Blob | undefined>((resolve, reject) => {
      const r = db.transaction('files').objectStore('files').get(image.id);
      r.onsuccess = () => resolve(r.result);
      r.onerror = () => reject(r.error);
    });
    if (blob) {
      url = URL.createObjectURL(blob);
      objectUrls.set(image.id, url);
    }
  }
  return { ...image, url };
}
async function hydrateProduct(product: Product): Promise<Product> {
  return { ...product, images: await Promise.all(product.images.slice(0, 20).map(hydrateImage)) };
}
const uploaded = new Map<string, ImageItem>();
const productUploader = createProductUploader(request, (record) => {
  const image = imageFrom({
    ...record,
    url:
      record.url ?? record.preview_url ?? `${base}/media/${encodeURIComponent(record.id)}/preview`,
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
  return new Promise((resolve, reject) => {
    const xhr = new XMLHttpRequest();
    const abort = () => xhr.abort();
    const cleanup = () => signal?.removeEventListener('abort', abort);
    xhr.open(method, url);
    for (const [k, v] of Object.entries(headers)) xhr.setRequestHeader(k, v);
    xhr.upload.onprogress = (e) => {
      if (e.lengthComputable) onProgress?.(Math.round((e.loaded / e.total) * 100));
    };
    xhr.onload = () => {
      cleanup();
      xhr.status >= 200 && xhr.status < 300
        ? resolve()
        : reject(
            new ApiError(`图片上传失败（${xhr.status}），可重试。`, xhr.status, 'UPLOAD_FAILED'),
          );
    };
    xhr.onerror = () => {
      cleanup();
      reject(new ApiError('图片上传中断，请重试。', 0, 'UPLOAD_FAILED'));
    };
    xhr.onabort = () => {
      cleanup();
      reject(new DOMException('已取消', 'AbortError'));
    };
    signal?.addEventListener('abort', abort, { once: true });
    if (signal?.aborted) {
      cleanup();
      reject(new DOMException('已取消', 'AbortError'));
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
      reject(new DOMException('已取消', 'AbortError'));
    };
    const timer = setTimeout(() => {
      signal?.removeEventListener('abort', abort);
      resolve();
    }, ms);
    signal?.addEventListener('abort', abort, { once: true });
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
        '/uploads',
        'POST',
        { filename: file.name, mime_type: file.type, byte_size: file.size },
        signal,
      ),
    );
    if (!(signed.media_id ?? signed.id) || !(signed.upload_url ?? signed.url))
      throw new ApiError('上传服务未返回有效地址。', 502, 'INVALID_UPLOAD_RESPONSE');
    attempt = { id: String(signed.media_id ?? signed.id), signed, transferred: false };
    uploadAttempts.set(file, attempt);
  }
  const { id, signed } = attempt;
  if (!attempt.transferred) {
    let body: XMLHttpRequestBodyInit = file;
    if (signed.fields) {
      const form = new FormData();
      for (const [key, value] of Object.entries(signed.fields)) form.append(key, String(value));
      form.append('file', file);
      body = form;
    }
    try {
      await xhrUpload(
        signed.upload_url ?? signed.url,
        signed.method ?? (signed.fields ? 'POST' : 'PUT'),
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
      await request(`/uploads/${encodeURIComponent(id)}/complete`, 'POST', undefined, signal),
    );
    const state = completed.status ?? completed.state;
    if (state === 'ready') {
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
    if (['failed', 'blocked'].includes(state))
      throw new ApiError('图片未通过处理或校验，请更换图片重试。', 422, 'MEDIA_REJECTED');
    if (!['uploaded', 'pending', 'processing'].includes(state))
      throw new ApiError('上传服务尚未确认图片可用，请稍后重试。', 502, 'INVALID_UPLOAD_RESPONSE');
    if (check < 4) await pause(700, signal);
  }
  throw new ApiError(
    '图片仍在处理中，可稍后重试；已传输的文件不会重复上传。',
    409,
    'MEDIA_NOT_READY',
  );
}

export const api = {
  async cancelUpload(file: File): Promise<void> {
    if (isDemo) return;
    await productUploader.cancel(file);
  },
  async categories(): Promise<Category[]> {
    if (isDemo) return clone((await loadDemoCatalog()).fixtureCategories);
    const e = await request('/categories');
    return records(e).map((c) => ({
      id: String(c.id),
      name: c.name,
      tradeType: c.trade_type === 'sale' || c.trade_type === 'rent' ? c.trade_type : undefined,
      priceUnits: c.price_units ?? [],
      filters: (c.filter_config ?? c.filters ?? [])
        .filter((group: RecordData) => group.enabled !== false)
        .map((group: RecordData) => ({
          ...group,
          options: (group.options ?? []).filter((option: RecordData) => option.enabled !== false),
        })),
    }));
  },
  async home(): Promise<HomeData> {
    if (isDemo) return clone((await loadDemoCatalog()).demoHome());
    const d = unwrap(await request('/home'));
    return {
      featured: (d.featured ?? []).map(productFrom),
      hot: (d.hot ?? []).map(productFrom),
      recommended: (d.recommended ?? []).map(productFrom),
      personalizationSource: d.personalization_source,
      newest: (d.newest ?? []).map(productFrom),
      ruleVersion: d.rule_version,
      computedAt: d.computed_at,
      presentation: readHomePresentation(d.presentation),
    };
  },
  async products(params = new URLSearchParams()): Promise<ProductPage> {
    if (isDemo) {
      const { paginate, filterProducts, fixtureProducts } = await loadDemoCatalog();
      return clone(paginate(filterProducts(fixtureProducts, params), params));
    }
    return pageFrom(await request(`/products?${toApiQuery(params)}`));
  },
  async product(id: string): Promise<Product> {
    if (isDemo) return demoProduct(id);
    const product = productFrom(unwrap(await request(`/products/${encodeURIComponent(id)}`)));
    if (product.status === 'offline') throw new ApiError('此账号已下架。', 410, 'OFFLINE');
    if (product.status !== 'published')
      throw new ApiError('内容不存在或尚未公开。', 404, 'NOT_FOUND');
    return product;
  },
  async images(id: string, cursor?: string | null, version?: number): Promise<ImagePage> {
    if (isDemo) {
      const p = await demoProduct(id);
      if (version && p.version !== version)
        throw new ApiError('图集已更新，请重新打开账号。', 409, 'VERSION_CONFLICT');
      return { items: p.images, total: p.imageTotal, nextCursor: null, version: p.version };
    }
    return imagePageFrom(
      await request(
        `/products/${encodeURIComponent(id)}/images?limit=20${cursor ? `&cursor=${encodeURIComponent(cursor)}` : ''}${version ? `&expected_version=${version}` : ''}`,
      ),
    );
  },
  async view(id: string): Promise<void> {
    if (!isDemo) await request(`/products/${encodeURIComponent(id)}/view`, 'POST');
  },
  async contacts(purpose?: string): Promise<Contact[]> {
    if (isDemo) return [];
    const apiPurpose = purpose === 'buy' ? 'sale' : purpose === 'publish' ? 'listing' : purpose;
    return records(
      await request(`/contacts${apiPurpose ? `?purpose=${encodeURIComponent(apiPurpose)}` : ''}`),
    ).map((c) => ({
      id: String(c.id),
      name: c.name,
      channel: c.channel,
      value: c.contact_value ?? c.value,
      qrUrl: c.qr_url ?? c.qrUrl,
      purpose: Array.isArray(c.purpose) ? c.purpose : [c.purpose],
      hours: c.hours,
    }));
  },
  async page(slug: string): Promise<ContentPage> {
    if (isDemo) {
      const p = (await loadDemoCatalog()).fixturePages[slug];
      if (!p) throw new ApiError('该说明页面尚未发布。', 404, 'NOT_FOUND');
      return clone(p);
    }
    const d = unwrap(await request(`/pages/${encodeURIComponent(slug)}`));
    return {
      slug,
      title: d.title,
      body: d.body,
      published: d.published !== false,
      version: d.version == null ? undefined : String(d.version),
    };
  },
  async me(): Promise<User | null> {
    if (isDemo) return readState().user;
    try {
      return userFrom(unwrap(await request('/me')));
    } catch (error) {
      if (error instanceof ApiError && error.status === 401) return null;
      throw error;
    }
  },
  async demoLogin(): Promise<User> {
    if (!isDemo) throw new ApiError('正式环境不提供演示登录。', 403, 'DEMO_DISABLED');
    const state = readState();
    state.user = {
      id: 'demo-user',
      nickname: '清叶体验官',
      phone: '演示账号 · 无真实手机号',
      demo: true,
    };
    writeState(state);
    return state.user;
  },
  async authCapabilities(): Promise<{
    registration_sms: boolean;
    sms_login: boolean;
    password_reset: boolean;
  }> {
    if (isDemo) return { registration_sms: false, sms_login: false, password_reset: false };
    return unwrap(await request('/auth/capabilities'));
  },
  async smsChallenge(
    phone: string,
    purpose: 'register' | 'login' = 'register',
  ): Promise<{ challenge_id: string; site_key: string; action: string }> {
    if (isDemo) return demoUnsupported();
    return unwrap(await request('/auth/sms/challenges', 'POST', { phone, purpose }));
  },
  async sms(
    phone: string,
    purpose: string,
    challenge_id?: string,
    token?: string,
  ): Promise<{ challenge_id: string; retry_after: number }> {
    if (isDemo) return demoUnsupported();
    return unwrap(await request('/auth/sms', 'POST', { phone, purpose, challenge_id, token }));
  },
  async login(payload: RecordData): Promise<User> {
    productUploader.reset();
    if (isDemo) return demoUnsupported();
    return userFrom(unwrap(await request('/auth/login', 'POST', payload)));
  },
  async smsLogin(payload: {
    phone: string;
    code: string;
    challenge_id: string;
  }): Promise<
    | { status: 'registration_required'; challenge_id: string }
    | { status: 'authenticated'; user: User }
  > {
    if (isDemo) return demoUnsupported();
    const data = unwrap(
      await request('/auth/login', 'POST', { ...payload, method: 'sms' }),
    ) as RecordData;
    if (data.status === 'registration_required' && data.challenge_id === payload.challenge_id)
      return { status: 'registration_required', challenge_id: payload.challenge_id };
    if (data.status !== 'authenticated' || !data.user)
      throw new ApiError('登录结果未确认，请稍后重试。', 502, 'INVALID_AUTH_RESPONSE');
    productUploader.reset();
    return { status: 'authenticated', user: userFrom(data.user as RecordData) };
  },
  async register(payload: RecordData): Promise<User> {
    if (isDemo) return demoUnsupported();
    return userFrom(unwrap(await request('/auth/register', 'POST', payload)));
  },
  async resetPassword(payload: RecordData): Promise<void> {
    if (isDemo) return demoUnsupported();
    await request('/auth/password-reset', 'POST', payload);
  },
  async logout(): Promise<void> {
    productUploader.reset();
    if (isDemo) {
      const state = readState();
      state.user = null;
      writeState(state);
    } else await request('/auth/logout', 'POST');
  },
  async updateProfile(nickname: string): Promise<User> {
    if (!nickname.trim() || nickname.length > 30)
      throw new ApiError('昵称请输入 1–30 个字符。', 422, 'INVALID_NICKNAME');
    if (isDemo) {
      const state = requireDemo();
      state.user!.nickname = nickname.trim();
      writeState(state);
      return state.user!;
    }
    return userFrom(unwrap(await request('/me', 'PATCH', { nickname: nickname.trim() })));
  },
  async favorites(): Promise<Product[]> {
    if (isDemo) {
      const state = requireDemo();
      return clone(
        (await loadDemoCatalog()).fixtureProducts
          .filter((p) => state.favorites.includes(p.id))
          .map((p) => ({ ...p, favorite: true })),
      );
    }
    return allPages('/me/favorites');
  },
  async favorite(id: string, selected: boolean): Promise<void> {
    if (isDemo) {
      if (selected) await demoProduct(id);
      const state = requireDemo();
      state.favorites = selected
        ? [...new Set([...state.favorites, id])]
        : state.favorites.filter((x) => x !== id);
      writeState(state);
    } else await request(`/me/favorites/${encodeURIComponent(id)}`, selected ? 'PUT' : 'DELETE');
  },
  async myProducts(status?: string): Promise<Product[]> {
    if (isDemo)
      return Promise.all(
        requireDemo()
          .products.filter((p) => !status || status === 'all' || p.status === status)
          .map((p) => hydrateProduct(clone(p))),
      );
    return allPages(
      `/me/products${status && status !== 'all' ? `?status=${encodeURIComponent(status)}` : ''}`,
    );
  },
  async myProduct(id: string): Promise<Product> {
    return isDemo
      ? hydrateProduct(await demoProduct(id, true))
      : productFrom(unwrap(await request(`/me/products/${encodeURIComponent(id)}`)));
  },
  async myImages(id: string, cursor?: string | null, version?: number): Promise<ImagePage> {
    if (isDemo) {
      const p = await demoProduct(id, true);
      if (version && p.version !== version)
        throw new ApiError('图集已更新，请刷新资料。', 409, 'VERSION_CONFLICT');
      const start = Number(cursor) || 0;
      return {
        items: await Promise.all(p.images.slice(start, start + 20).map(hydrateImage)),
        total: p.images.length,
        nextCursor: start + 20 < p.images.length ? String(start + 20) : null,
        version: p.version,
      };
    }
    return imagePageFrom(
      await request(
        `/me/products/${encodeURIComponent(id)}/images?limit=20${cursor ? `&cursor=${encodeURIComponent(cursor)}` : ''}${version ? `&expected_version=${version}` : ''}`,
      ),
    );
  },
  async saveProduct(input: ProductWrite, id?: string, version?: number): Promise<Product> {
    validateWrite(input);
    if (!id && input.title.length > 1000)
      throw new ApiError('标题最多1000字。', 422, 'TITLE_TOO_LONG');
    if (isDemo) {
      if (input.privateCredentials)
        throw new ApiError('演示模式不保存私密账号资料。', 422, 'PRIVATE_DATA_UNSUPPORTED');
      if (id)
        return hydrateProduct(
          editDemo(id, version!, (p) => Object.assign(p, input, { status: 'draft' })),
        );
      const state = requireDemo();
      const p: Product = {
        ...input,
        id: `DEMO-${Date.now().toString(36).toUpperCase()}`,
        kind: input.kind ?? 'single',
        art: 0,
        images: [],
        imageTotal: 0,
        status: 'draft',
        version: 1,
        verification: 'unprovided',
        rare: false,
        publishedAt: '',
      };
      state.products.unshift(p);
      writeState(state);
      return clone(p);
    }
    const body = { ...productToWire(input), ...(id ? { expected_version: version } : {}) };
    return productFrom(
      unwrap(
        id
          ? await request(`/me/products/${encodeURIComponent(id)}`, 'PATCH', body)
          : await idempotentRequest('/me/products', body),
      ),
    );
  },
  async submitProduct(id: string, version: number): Promise<Product> {
    if (isDemo)
      return hydrateProduct(
        editDemo(id, version, (p) => {
          if (!['draft', 'rejected', 'offline'].includes(p.status))
            throw new ApiError('当前状态不能提交审核。', 409, 'INVALID_STATUS');
          validateWrite(p, true);
          if (!p.images.length) throw new ApiError('请至少添加一张图片。', 422, 'IMAGE_REQUIRED');
          p.status = 'pending';
          p.reviewNote = '演示提交，仅保存在本浏览器，未发送至真实审核后台。';
        }),
      );
    return updatedProduct(
      id,
      await request(`/me/products/${encodeURIComponent(id)}/submit`, 'POST', {
        expected_version: version,
      }),
    );
  },
  async deleteDraft(id: string, version: number): Promise<void> {
    if (isDemo) {
      const state = requireDemo();
      const product = state.products.find((item) => item.id === id);
      if (!product || product.status !== 'draft' || product.publishedAt)
        throw new ApiError('只能删除未上架的草稿。', 409, 'INVALID_STATUS');
      state.products = state.products.filter((item) => item.id !== id);
      writeState(state);
      return;
    }
    await request(`/me/products/${encodeURIComponent(id)}`, 'DELETE', {
      expected_version: version,
    });
  },
  async withdrawProduct(id: string, version: number): Promise<Product> {
    if (isDemo)
      return hydrateProduct(
        editDemo(id, version, (p) => {
          if (p.status !== 'pending')
            throw new ApiError('当前资料不在审核中。', 409, 'INVALID_STATUS');
          p.status = 'draft';
          p.reviewNote = undefined;
        }),
      );
    return updatedProduct(
      id,
      await request(`/me/products/${encodeURIComponent(id)}/withdraw`, 'POST', {
        expected_version: version,
      }),
    );
  },
  async uploadProduct(
    file: File,
    onProgress?: (percent: number) => void,
    signal?: AbortSignal,
  ): Promise<ImageItem> {
    if (!['image/jpeg', 'image/png', 'image/webp'].includes(file.type))
      throw new ApiError('请选择 JPG、PNG 或 WebP 图片。', 422, 'INVALID_IMAGE_TYPE');
    if (file.size > uploadConfig.maxBytes)
      throw new ApiError(
        `单张图片不能超过 ${Math.round(uploadConfig.maxBytes / 1024 / 1024)} MB。`,
        413,
        'IMAGE_TOO_LARGE',
      );
    if (signal?.aborted) throw new DOMException('已取消', 'AbortError');
    onProgress?.(0);
    if (isDemo) {
      requireDemo();
      const image: ImageItem = { id: `local-${createRequestId()}`, alt: file.name };
      await storeBlob(image.id, file, signal);
      uploaded.set(image.id, image);
      onProgress?.(100);
      return hydrateImage(image);
    }
    return productUploader.upload(file, onProgress, signal);
  },
  async upload(
    file: File,
    onProgress?: (percent: number) => void,
    signal?: AbortSignal,
  ): Promise<ImageItem> {
    if (!['image/jpeg', 'image/png', 'image/webp'].includes(file.type))
      throw new ApiError('请选择 JPG、PNG 或 WebP 图片。', 422, 'INVALID_IMAGE_TYPE');
    if (file.size > uploadConfig.maxBytes)
      throw new ApiError(
        `单张图片不能超过 ${Math.round(uploadConfig.maxBytes / 1024 / 1024)} MB。`,
        413,
        'IMAGE_TOO_LARGE',
      );
    if (signal?.aborted) throw new DOMException('已取消', 'AbortError');
    onProgress?.(0);
    if (isDemo) {
      requireDemo();
      const image: ImageItem = { id: `local-${createRequestId()}`, alt: file.name };
      await storeBlob(image.id, file, signal);
      uploaded.set(image.id, image);
      onProgress?.(100);
      return hydrateImage(image);
    }
    return uploadRemote(file, onProgress, signal);
  },
  async appendImages(id: string, mediaIds: string[], version: number): Promise<Product> {
    if (mediaIds.length > uploadConfig.batchSize)
      throw new ApiError(
        `每批最多追加 ${uploadConfig.batchSize} 张，请继续分批添加。`,
        422,
        'BATCH_TOO_LARGE',
      );
    if (isDemo)
      return hydrateProduct(
        editDemo(id, version, (p) => {
          for (const mediaId of mediaIds) {
            const image = uploaded.get(mediaId);
            if (!image) throw new ApiError('图片尚未完成保存，请重试。', 422, 'MEDIA_NOT_READY');
            if (!p.images.some((i) => i.id === mediaId)) p.images.push(image);
          }
          p.imageTotal = p.images.length;
          p.status = 'draft';
        }),
      );
    return updatedProduct(
      id,
      await idempotentRequest(`/me/products/${encodeURIComponent(id)}/images`, {
        media_ids: mediaIds,
        expected_version: version,
      }),
    );
  },
  async removeImage(id: string, mediaId: string, version: number): Promise<Product> {
    if (isDemo)
      return hydrateProduct(
        editDemo(id, version, (p) => {
          p.images = p.images.filter((i) => i.id !== mediaId);
          p.imageTotal = p.images.length;
          p.status = 'draft';
        }),
      );
    return updatedProduct(
      id,
      await request(
        `/me/products/${encodeURIComponent(id)}/images/${encodeURIComponent(mediaId)}`,
        'DELETE',
        { expected_version: version },
      ),
    );
  },
  async moveImage(
    id: string,
    mediaId: string,
    beforeId: string | null,
    version: number,
  ): Promise<Product> {
    if (isDemo)
      return hydrateProduct(
        editDemo(id, version, (p) => {
          const i = p.images.find((x) => x.id === mediaId);
          if (!i) throw new ApiError('找不到图片。', 404, 'NOT_FOUND');
          const list = p.images.filter((x) => x.id !== mediaId);
          const at = beforeId ? list.findIndex((x) => x.id === beforeId) : list.length;
          if (at < 0) throw new ApiError('图集已更新，请重新加载。', 409, 'VERSION_CONFLICT');
          list.splice(at, 0, i);
          p.images = list;
          p.status = 'draft';
        }),
      );
    return updatedProduct(
      id,
      await request(
        `/me/products/${encodeURIComponent(id)}/images/${encodeURIComponent(mediaId)}`,
        'PATCH',
        { before_media_id: beforeId, expected_version: version },
      ),
    );
  },
};
