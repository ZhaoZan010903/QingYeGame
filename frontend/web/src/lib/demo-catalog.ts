import type { Product, ProductPage } from '../types';
import { fixtureScores, fixtureProducts, editorialIds } from './fixtures';
import { ApiError, queryValues } from './query';
import { resolveHomePresentation, selectHomeSections } from './home-content';
export { fixtureProducts, fixtureCategories, fixturePages } from './fixtures';

// Local-only preview algorithms. API results are never passed through these rankings.
export const stableNewest = (a: Product, b: Product) =>
  b.publishedAt.localeCompare(a.publishedAt) || a.id.localeCompare(b.id);
export const demoHot = (a: Product, b: Product) =>
  (fixtureScores[b.id] ?? 0) - (fixtureScores[a.id] ?? 0) || stableNewest(a, b);
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
    [...queryValues(params, key), ...(Array.isArray(groups[key]) ? groups[key] : [])].filter(
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

export function demoHome() {
  const presentation = resolveHomePresentation();
  const featured = editorialIds.flatMap((id) => {
    const product = fixtureProducts.find((item) => item.id === id);
    return product ? [product] : [];
  });
  const inventory = {
    featured,
    hot: [...fixtureProducts].sort(demoHot),
    newest: [...fixtureProducts].sort(stableNewest),
  };
  const sections = selectHomeSections(inventory, presentation.sections);
  return {
    featured,
    hot: sections.find((section) => section.source === 'hot')?.products ?? [],
    newest: sections.find((section) => section.source === 'newest')?.products ?? [],
    presentation,
    ruleVersion: 'demo-v3',
  };
}
