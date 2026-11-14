import type { FilterGroup } from '../types';

const orderedLabels: Record<string, string[]> = {
  server: ['一舞时光', '拾忆光阴', '十年之约', '其他'],
  level: ['星钻', '银钻', '金钻', '彩钻', '王钻'],
};

/** Keep persisted codes intact; only display labels and requested filter order change. */
export function catalogFilters(groups: FilterGroup[]): FilterGroup[] {
  return groups
    .filter((group) => group.key !== 'kind')
    .map((group) => {
      const order = orderedLabels[group.key];
      const options = group.options.map((option) => ({
        ...option,
        label: group.key === 'server' && option.label === '其他大区' ? '其他' : option.label,
      }));
      if (order) {
        const rank = (label: string) => {
          const index = order.indexOf(label);
          return index === -1 ? order.length : index;
        };
        options.sort((a, b) => rank(a.label) - rank(b.label));
      }
      return { ...group, options };
    })
    .sort((a, b) => Number(b.key === 'keyword') - Number(a.key === 'keyword'));
}

export function catalogRequest(params: URLSearchParams, groups: FilterGroup[], limit: number) {
  const request = new URLSearchParams(params);
  // Retired links must not silently keep the removed account-structure filter active.
  request.delete('kind');
  request.delete('filters');
  if (request.get('trade_type') !== 'rent') request.delete('rental_category');
  if (request.get('price_unit') !== 'period') request.delete('period_basis');
  request.set('limit', String(limit));
  const filterValues: Record<string, string[]> = {};
  groups.forEach((group) => {
    const values = request.getAll(group.key);
    if (values.length) filterValues[group.key] = values;
    request.delete(group.key);
  });
  if (Object.keys(filterValues).length) request.set('filters', JSON.stringify(filterValues));
  return request;
}
