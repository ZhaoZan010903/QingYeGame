import type { Category, PriceUnit } from '../types';

const saleUnits: PriceUnit[] = ['account'];
const rentUnits: PriceUnit[] = ['hour', 'day', 'week', 'month'];

/** Empty template units mean business defaults, while explicit choices remain restrictive. */
export function catalogPriceUnits(categories: Category[], tradeType?: string | null): PriceUnit[] {
  const mode = tradeType === 'sale' || tradeType === 'rent' ? tradeType : undefined;
  const relevant = categories.filter(
    (category) => !mode || !category.tradeType || category.tradeType === mode,
  );
  const units = relevant.flatMap((category) => {
    if (category.priceUnits.length) return category.priceUnits;
    const type = category.tradeType ?? mode;
    return type === 'sale' ? saleUnits : type === 'rent' ? rentUnits : [...saleUnits, ...rentUnits];
  });
  return [...new Set(units)].filter(
    (unit) =>
      unit !== 'unknown' && (!mode || (mode === 'sale' ? unit === 'account' : unit !== 'account')),
  );
}
