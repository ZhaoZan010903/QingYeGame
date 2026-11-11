import type { FilterGroup, PriceUnit, ProductWrite, RentalQuote } from '../types';

const builtins = new Set(['server', 'gender', 'level', 'kind']);

/** Unknown units remain unknown until explicit rental quotes replace them; legacy prices are separate. */
export function rentalPriceForWrite(
  quotes: RentalQuote[],
  currentUnit: PriceUnit,
  allowed: RentalQuote['unit'][],
): { unit: PriceUnit; price: number | null } {
  if (quotes.length) return { unit: quotes[0].unit, price: Number(quotes[0].amount) };
  if (currentUnit === 'unknown') return { unit: 'unknown', price: null };
  return {
    unit: allowed.includes(currentUnit as RentalQuote['unit'])
      ? currentUnit
      : (allowed[0] ?? 'unknown'),
    price: null,
  };
}

/** Only normalize configured choices; free text must survive a former select configuration. */
export function publishAttributes(form: ProductWrite, groups: FilterGroup[]) {
  const attributes = { ...form.attributes };
  attributes.custom_tags = form.tags;
  for (const group of groups) {
    if (group.enabled === false || group.type === 'text' || !builtins.has(group.key)) continue;
    const raw = attributes[group.key] ?? form[group.key as 'server'] ?? '';
    if (group.options.length) {
      const allowed = new Set(group.options.map((option) => option.value));
      attributes[group.key] = Array.isArray(raw)
        ? raw.map(String).filter((value) => allowed.has(value))
        : allowed.has(String(raw))
          ? String(raw)
          : '';
    }
  }
  return attributes;
}

export function missingPublishField(form: ProductWrite, groups: FilterGroup[]) {
  return groups.find((group) => {
    if (group.enabled === false || !group.required) return false;
    const value =
      form.attributes?.[group.key] ?? (form as unknown as Record<string, unknown>)[group.key];
    return (
      value === undefined ||
      value === null ||
      String(value).trim() === '' ||
      (Array.isArray(value) && !value.length)
    );
  });
}
