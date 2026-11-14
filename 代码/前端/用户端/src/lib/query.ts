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
export const queryValues = (params: URLSearchParams, key: string) =>
  params
    .getAll(key)
    .flatMap((v) => v.split(','))
    .filter(Boolean);
/** Translate human-readable URL filters to the documented Go query contract. */
export function toApiQuery(source: URLSearchParams): URLSearchParams {
  const p = new URLSearchParams(source);
  const filters: Record<string, string[]> = {};
  for (const key of ['server', 'gender', 'level', 'kind', 'tag', 'keyword']) {
    const list = queryValues(p, key).filter((v) => v !== 'all');
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
  if (p.get('trade_type') !== 'rent') p.delete('rental_category');
  if (p.get('price_unit') !== 'period') p.delete('period_basis');
  for (const [key, value] of [...p]) if (value === 'all' || value === '') p.delete(key);
  if (
    p.get('price_unit') === 'unknown' &&
    (p.has('min_price') || p.has('max_price') || p.get('sort')?.startsWith('price'))
  )
    throw new ApiError(
      '旧商品报价单位尚未核对，不能按价格筛选或排序。',
      422,
      'PRICE_UNIT_REQUIRED',
    );
  return p;
}
