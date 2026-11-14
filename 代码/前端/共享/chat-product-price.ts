import type { ChatProduct } from "./chat-types";
import { rentalQuoteLabel, rentalUnitLabels } from "./rental-quotes";

const legacyUnits: Record<string, string> = {
  account: "元",
  hour: "元/小时",
  day: "元/日",
  week: "元/周",
  month: "元/月",
};

/** Use the saved consultation quote, so later edits cannot change the message's price. */
export function chatProductPrice(product: ChatProduct): string {
  const quote = product.rental_quotes?.[0];
  if (quote && quote.amount !== "" && Number.isFinite(Number(quote.amount))) {
    const unit =
      quote.unit === "period" ? (quote.billing_unit ?? "period") : quote.unit;
    const price = `¥ ${Number(quote.amount).toLocaleString("zh-CN")} 元/${rentalUnitLabels[unit]}`;
    return quote.unit === "period"
      ? `${rentalQuoteLabel(quote)} · ${price}`
      : price;
  }
  // Older snapshots contain only the original primary price and ordinary unit.
  if (product.unit === "period") return "联系客服确认时段报价";
  if (
    product.price == null ||
    product.price === "" ||
    !Number.isFinite(Number(product.price)) ||
    !legacyUnits[product.unit]
  )
    return "联系客服报价";
  return `¥ ${Number(product.price).toLocaleString("zh-CN")} ${legacyUnits[product.unit]}`;
}
