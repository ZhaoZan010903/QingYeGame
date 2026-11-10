import type { Product, ProductPage } from '../types';
import { fixtureScores } from './fixtures';

export class ApiError extends Error {
  constructor(
    message: string,
    public status = 500,
    public code = 'REQUEST_FAILED',
    public fields?: Record<string, string>,
  ) {
    super(message);
    this.name = 'ApiError';
  }
}
export const stableNewest = (a: Product, b: Product) =>
  b.publishedAt.localeCompare(a.publishedAt) || a.id.localeCompare(b.id);
export const demoHot = (a: Product, b: Product) =>
  (fixtureScores[b.id] ?? 0) - (fixtureScores[a.id] ?? 0) || stableNewest(a, b);
const values = (params: URLSearchParams, key: string) =>
  params
    .getAll(key)
    .flatMap((v) => v.split(','))
    .filter(Boolean);
export function filterProducts(products: Product[], params: URLSearchParams): Product[] {
  const q = (params.get('q') ?? '').trim().toLowerCase();
  const mode = params.get('trade_type') ?? params.get('mode') ?? params.get('category') ?? 'all';
  const unit = params.get('price_unit') ?? params.get('unit') ?? 'all';
  const sort = params.get('sort') ?? 'featured';
  const priceSort = ['price-up', 'price-down', 'price_asc', 'price_desc'].includes(sort);
  const low = params.get('min_price') ?? params.get('low');
  const high = params.get('max_price') ?? params.get('high');
  if ((priceSort || low !== null || high !== null) && unit === 'all')
    throw new ApiError('请先选择一种报价单位，再按价格筛选或排序。', 422, 'PRICE_UNIT_REQUIRED');
  if (
    (low !== null && (!Number.isFinite(Number(low)) || Number(low) < 0)) ||
    (high !== null && (!Number.isFinite(Number(high)) || Number(high) < 0)) ||
    (low !== null && high !== null && Number(low) > Number(high))
  )
    throw new ApiError('请输入有效的价格区间。', 422, 'INVALID_PRICE_RANGE');
  let groups: Record<string, string[]> = {};
  if (params.has('filters')) {
    try {
      groups = JSON.parse(params.get('filters')!);
    } catch {
      throw new ApiError('筛选条件格式错误。', 422, 'INVALID_FILTERS');
    }
  }
  const selected = (key: string) =>
    [...values(params, key), ...(Array.isArray(groups[key]) ? groups[key] : [])].filter(
      (v) => v !== 'all',
    );
  const matches = (key: string, value: string) =>
    !selected(key).length || selected(key).includes(value);
  const result = products.filter(
    (p) =>
      p.status === 'published' &&
      (!q ||
        `${p.id} ${p.title} ${p.server} QQ炫舞 ${p.tags.join(' ')}`.toLowerCase().includes(q)) &&
      (mode === 'all' || mode === 'qq-dance' || (mode === 'rare' ? p.rare : p.mode === mode)) &&
      (unit === 'all' || p.unit === unit) &&
      matches('server', p.server) &&
      matches('gender', p.gender) &&
      matches('level', String(p.level)) &&
      matches('kind', p.kind) &&
      (!selected('tag').length ||
        selected('tag').some((t) => p.tags.some((tag) => tag.includes(t)))) &&
      (params.get('verified') !== 'true' || p.verification === 'verified') &&
      (low === null || (p.price !== null && p.price >= Number(low))) &&
      (high === null || (p.price !== null && p.price <= Number(high))),
  );
  if (sort === 'hot') result.sort(demoHot);
  if (sort === 'newest') result.sort(stableNewest);
  if (priceSort)
    result.sort((a, b) =>
      a.price === null
        ? b.price === null
          ? stableNewest(a, b)
          : 1
        : b.price === null
          ? -1
          : (a.price - b.price) * (sort === 'price-down' || sort === 'price_desc' ? -1 : 1) ||
            stableNewest(a, b),
    );
  return result;
}
export function paginate(products: Product[], params: URLSearchParams): ProductPage {
  const limit = Math.min(50, Math.max(1, Number(params.get('limit')) || 12));
  const page = Math.max(1, Number(params.get('page')) || 1);
  const start = Math.max(0, Number(params.get('cursor')) || (page - 1) * limit);
  return {
    items: products.slice(start, start + limit),
    total: products.length,
    nextCursor: start + limit < products.length ? String(start + limit) : null,
  };
}
/** Translate human-readable URL filters to the documented Go query contract. */
export function toApiQuery(source: URLSearchParams): URLSearchParams {
  const p = new URLSearchParams(source);
  const filters: Record<string, string[]> = {};
  for (const key of ['server', 'gender', 'level', 'kind', 'tag']) {
    const list = values(p, key).filter((v) => v !== 'all');
    if (list.length) filters[key] = list;
    p.delete(key);
  }
  if (Object.keys(filters).length) p.set('filters', JSON.stringify(filters));
  const mode = p.get('mode') ?? p.get('category');
  if (mode === 'sale' || mode === 'rent') p.set('trade_type', mode);
  if (p.has('unit')) p.set('price_unit', p.get('unit')!);
  if (p.has('low')) p.set('min_price', p.get('low')!);
  if (p.has('high')) p.set('max_price', p.get('high')!);
  ['mode', 'unit', 'low', 'high', 'page'].forEach((key) => p.delete(key));
  if (mode === 'sale' || mode === 'rent' || mode === 'all') p.delete('category');
  for (const [key, value] of [...p]) if (value === 'all' || value === '') p.delete(key);
  return p;
}
