import type { Category, FilterGroup, Product } from '../types';

export interface PublishDictionary {
  key: string;
  label: string;
  version: number;
  options: { value: string; label: string; enabled?: boolean; sort_order?: number }[];
}
const fields = [
  ['server', '所在区服'],
  ['gender', '角色性别'],
  ['level', 'VIP等级'],
  ['kind', '账号结构'],
] as const;

/** All transaction types share the same four single-choice dictionaries. */
export function fixedPublishFields(
  dictionaries: PublishDictionary[],
  categories: Category[],
): FilterGroup[] {
  return fields.map(([key, label]) => {
    const dictionary = dictionaries.find((entry) => entry.key === key);
    const options = dictionary
      ? dictionary.options.filter((option) => option.enabled !== false)
      : [
          ...new Map(
            categories
              .flatMap((category) =>
                category.filters
                  .filter((group) => group.key === key)
                  .flatMap((group) => group.options),
              )
              .map((option) => [option.value, option]),
          ).values(),
        ];
    return {
      key,
      label,
      type: 'select',
      multiple: false,
      required: key === 'server' || key === 'gender',
      options,
    };
  });
}
export function customPublishTags(value: string, unchanged?: string[]): string[] {
  if (unchanged) return [...unchanged];
  const tags = [
    ...new Set(
      value
        .split(/[,，、\n]/)
        .map((tag) => tag.trim())
        .filter(Boolean),
    ),
  ];
  if (tags.length > 20 || tags.some((tag) => Array.from(tag).length > 5))
    throw new Error('标签最多20个，每个最多5个字。');
  return tags;
}
export function publishAttributeLabel(group: FilterGroup, value: unknown, product: Product | null) {
  const values = Array.isArray(value) ? value.map(String) : [String(value ?? '')];
  const labels = values.map((item) => group.options.find((option) => option.value === item)?.label);
  if (labels.every(Boolean)) return labels.join('、');
  const originalLabel = product?.attributeLabels?.[group.key as 'server'];
  if (originalLabel && JSON.stringify(value) === JSON.stringify(product?.attributes?.[group.key]))
    return originalLabel;
  return values.map((item, index) => labels[index] ?? item).join('、');
}
export const canDeleteDraft = (product: Product) =>
  product.status === 'draft' &&
  product.source !== 'legacy' &&
  !product.publishedAt &&
  !!product.draftExpiresAt;
