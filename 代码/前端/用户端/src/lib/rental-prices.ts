import type { Product, RentalQuote } from '../types';
import { formatRentalAmount, periodLabel } from '../../../共享/rental-quotes';

export const rentalBillingLabels = {
  period: '元/时段',
  hour: '元/小时',
  day: '元/日',
  week: '元/周',
  month: '元/月',
};
const ordinaryLabels = { hour: '时租', day: '日租', week: '周租', month: '月租' };
const ordinaryOrder = ['hour', 'day', 'week', 'month'];

export function rentalCategoryLabel(product: Product): string {
  return product.mode === 'sale'
    ? '账号出售'
    : product.rentalCategory === 'hour_day'
      ? '时日租号'
      : product.rentalCategory === 'week_month'
        ? '周月租号'
        : '账号租赁';
}

/** A minimum is meaningful only inside one billing unit; never compare a day with an hour. */
export function rentalPriceRows(quotes: RentalQuote[], allPeriods = false) {
  const ordinary = quotes
    .filter((quote) => quote.unit !== 'period')
    .sort((a, b) => ordinaryOrder.indexOf(a.unit) - ordinaryOrder.indexOf(b.unit));
  const periods = quotes.filter((quote) => quote.unit === 'period');
  const selected = allPeriods
    ? periods.map((quote) => ({ quote, starting: false }))
    : Object.keys(rentalBillingLabels).flatMap((basis) => {
        const group = periods.filter((quote) => (quote.billing_unit || 'period') === basis);
        if (!group.length) return [];
        const quote = group.reduce((lowest, item) =>
          Number(item.amount) < Number(lowest.amount) ? item : lowest,
        );
        return [{ quote, starting: group.length > 1 }];
      });
  return [...ordinary.map((quote) => ({ quote, starting: false })), ...selected].map(
    ({ quote, starting }) => {
      const basis = quote.unit === 'period' ? quote.billing_unit || 'period' : quote.unit;
      return {
        label: quote.unit === 'period' ? quote.label || '时段' : ordinaryLabels[quote.unit],
        time:
          quote.unit === 'period' && quote.start_time && quote.end_time ? periodLabel(quote) : '',
        amount: formatRentalAmount(quote.amount),
        unit: rentalBillingLabels[basis],
        period: quote.unit === 'period',
        starting,
      };
    },
  );
}

export function matchesQuoteUnit(product: Product, unit: string, periodBasis = 'period') {
  if (
    product.mode === 'rent' &&
    product.rentalQuotes?.some(
      (quote) =>
        quote.unit === unit &&
        (unit !== 'period' || (quote.billing_unit || 'period') === periodBasis),
    )
  )
    return true;
  if (unit === 'period') return false;
  return product.unit === unit;
}

export function selectedQuotePrice(product: Product, unit: string, periodBasis = 'period') {
  const quotes =
    product.mode === 'rent'
      ? (product.rentalQuotes ?? []).filter(
          (quote) =>
            quote.unit === unit &&
            (unit !== 'period' || (quote.billing_unit || 'period') === periodBasis),
        )
      : [];
  if (quotes.length) return Math.min(...quotes.map((quote) => Number(quote.amount)));
  // Period's primary amount has no basis of its own; a different basis must not reuse it.
  if (unit === 'period') return null;
  return product.unit === unit ? product.price : null;
}
