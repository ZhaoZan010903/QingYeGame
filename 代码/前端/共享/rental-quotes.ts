import type { Product, RentalQuote } from "./types";

export const ordinaryRentalUnits = ["hour", "day", "week", "month"] as const;
export const rentalUnits = [...ordinaryRentalUnits, "period"] as const;
export const rentalUnitLabels = {
  hour: "小时",
  day: "日",
  week: "周",
  month: "月",
  period: "时段",
};

export function periodLabel(
  quote: Pick<RentalQuote, "start_time" | "end_time">,
) {
  const { start_time: start = "", end_time: end = "" } = quote;
  if (!start || !end) return "";
  return `${start}–${end < start ? "次日" : ""}${end}`;
}
export function rentalQuoteLabel(quote: RentalQuote) {
  return quote.unit === "period"
    ? [quote.label || "未命名时段", periodLabel(quote)]
        .filter(Boolean)
        .join(" · ")
    : rentalUnitLabels[quote.unit];
}
export function formatRentalAmount(value: string | number) {
  const amount = Number(value);
  return value !== "" && Number.isFinite(amount)
    ? String(amount)
    : String(value);
}

/** Preserve saved order: the first quote remains the public card's primary price. */
export function quotesFromProduct(product: Product | null): RentalQuote[] {
  if (product?.rentalQuotes?.length)
    return product.rentalQuotes.map((q) => ({ ...q }));
  if (
    product?.mode === "rent" &&
    product.price != null &&
    ordinaryRentalUnits.includes(
      product.unit as (typeof ordinaryRentalUnits)[number],
    )
  )
    return [
      {
        unit: product.unit as RentalQuote["unit"],
        amount: String(product.price),
      },
    ];
  return [];
}

export function validateRentalQuotes(
  quotes: RentalQuote[],
  allowed: readonly string[] = rentalUnits,
): RentalQuote[] {
  const seen = new Set<string>();
  let periods = 0;
  return quotes.map((quote) => {
    if (!rentalUnits.includes(quote.unit) || !allowed.includes(quote.unit))
      throw new Error("存在当前分类不支持的租期报价，请核对后手动移除。");
    const amount = quote.amount.trim();
    if (!/^\d{1,10}(?:\.\d{1,2})?$/.test(amount))
      throw new Error("已选择的租期需要填写有效金额，最多两位小数。");
    let key: string = quote.unit;
    if (quote.unit === "period") {
      const { start_time = "", end_time = "" } = quote;
      const label = quote.label?.trim() ?? "";
      if (
        !label ||
        Array.from(label).length > 30 ||
        /[\u0000-\u001f\u007f-\u009f]/u.test(label)
      )
        throw new Error("时段名称需为 1 至 30 个有效字符。");
      if (
        (start_time || end_time) &&
        (!/^([01]\d|2[0-3]):[0-5]\d$/.test(start_time) ||
          !/^([01]\d|2[0-3]):[0-5]\d$/.test(end_time))
      )
        throw new Error("时段起止时间可同时留空；填写时请补全有效起止时间。");
      if (start_time && start_time === end_time)
        throw new Error("时段开始与结束时间不能相同。");
      if (!rentalUnits.includes(quote.billing_unit ?? "period"))
        throw new Error("请选择有效的时段计价单位。");
      if (++periods > 12) throw new Error("最多添加 12 条时段报价。");
      key += `:${label}:${start_time}:${end_time}:${quote.billing_unit ?? "period"}`;
    } else if (
      quote.start_time ||
      quote.end_time ||
      quote.label ||
      quote.billing_unit
    ) {
      throw new Error("普通租期报价不能附带时段信息。");
    }
    if (seen.has(key)) throw new Error("租期或时段报价不能重复。");
    seen.add(key);
    return quote.unit === "period"
      ? {
          unit: quote.unit,
          amount: Number(amount).toFixed(2),
          label: quote.label!.trim(),
          billing_unit: quote.billing_unit ?? "period",
          ...(quote.start_time
            ? { start_time: quote.start_time, end_time: quote.end_time }
            : {}),
        }
      : { unit: quote.unit, amount: Number(amount).toFixed(2) };
  });
}
