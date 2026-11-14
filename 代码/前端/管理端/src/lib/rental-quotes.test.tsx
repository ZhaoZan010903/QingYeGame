import { describe, expect, it } from "vitest";
import { renderToStaticMarkup } from "react-dom/server";
import { productFrom } from "./api";
import { buildRentalQuotes } from "./product-input";
import { editorRecordFromProduct } from "./media-sync-state";
import { Price } from "../components/shared";
import type { RentalQuote } from "../types";

const quotes: RentalQuote[] = [
  {
    unit: "period",
    label: "白天",
    amount: "8",
    billing_unit: "hour",
    start_time: "08:00",
    end_time: "20:00",
  },
  { unit: "period", label: "包夜", amount: "20", billing_unit: "period" },
];
describe("R21-2 管理报价与分类", () => {
  it("编辑数组保留首报价与不同时段，不合并或换算", () => {
    const input = buildRentalQuotes("rent", "period", "8", quotes, ["period"]);
    expect(input).toEqual(
      quotes.map((q) => ({ ...q, amount: Number(q.amount).toFixed(2) })),
    );
    expect(() =>
      buildRentalQuotes("rent", "period", "8", quotes, ["day"]),
    ).toThrow("不支持");
  });
  it("API和图集刷新完整保留时段及管理员分类", () => {
    const product = productFrom({
      id: "p",
      trade_type: "rent",
      price_unit: "period",
      price_amount: "8",
      rental_category: "hour_day",
      rental_quotes: quotes,
    });
    expect(product.rentalQuotes).toEqual(quotes);
    expect(product.rentalCategory).toBe("hour_day");
    const snapshot = editorRecordFromProduct(null, product);
    expect(snapshot.rental_category).toBe("hour_day");
    expect(snapshot.rental_quotes).toEqual(quotes);
    const html = renderToStaticMarkup(<Price product={product} />);
    expect(html).toContain("白天");
    expect(html).toContain("08:00–20:00");
    expect(html).toContain("元/小时");
  });
});
