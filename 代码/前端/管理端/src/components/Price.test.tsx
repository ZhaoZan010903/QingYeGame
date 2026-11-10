import { describe, expect, it } from "vitest";
import { renderToStaticMarkup } from "react-dom/server";
import { Price } from "./shared";
import { productFrom } from "../lib/api";

describe("管理员报价显示", () => {
  it.each([
    ["account", "元"],
    ["day", "元/日"],
    ["month", "元/月"],
  ] as const)("已确认%s使用正式报价而不是原价备份", (unit, label) => {
    const product = productFrom({
      id: "confirmed",
      price_unit: unit,
      price_amount: "168.50",
      legacy_price: "160.00",
      price_note: "原站报价，单位待核对",
    });
    const html = renderToStaticMarkup(<Price product={product} />);
    expect(html).toContain("¥");
    expect(html).toContain("168.5");
    expect(html).toContain(label);
    expect(html).not.toContain("160.00");
    expect(html).not.toContain("待核对");
  });

  it("仍未知的条目继续保留原始文本和待核对说明", () => {
    const html = renderToStaticMarkup(
      <Price
        product={productFrom({
          id: "unknown",
          price_unit: "unknown",
          price_amount: null,
          legacy_price: "160.00",
        })}
      />,
    );
    expect(html).toContain("160.00");
    expect(html).toContain("单位待核对");
    expect(html).not.toContain("¥");
  });
});
