import { describe, expect, it } from "vitest";
import { renderToStaticMarkup } from "react-dom/server";
import { ChatProductCard } from "../components/ChatPage";
import type { ChatProduct } from "../../../共享/chat-types";

describe("管理聊天商品时段快照", () => {
  it("咨询卡片保留首条时段的小时单位和跨午夜时间", () => {
    const product: ChatProduct = {
      id: "p",
      code: "QYZH1",
      title: "咨询商品",
      price: "8",
      unit: "period",
      cover_url: "",
      rental_category: "hour_day",
      rental_quotes: [
        {
          unit: "period",
          label: "夜间",
          amount: "8",
          billing_unit: "hour",
          start_time: "22:00",
          end_time: "07:00",
        },
        { unit: "period", label: "包夜", amount: "20", billing_unit: "period" },
      ],
    };
    const html = renderToStaticMarkup(<ChatProductCard product={product} />);
    expect(html).toContain("夜间 · 22:00–次日07:00 · ¥ 8 元/小时");
    expect(html).not.toContain("元/时段");
    expect(html).toContain("products");
  });
  it("旧聊天快照仍按普通租期展示", () => {
    const html = renderToStaticMarkup(
      <ChatProductCard
        product={{
          id: "p",
          code: "QYZH1",
          title: "旧咨询",
          price: "50.00",
          unit: "day",
          cover_url: "",
        }}
      />,
    );
    expect(html).toContain("¥ 50 元/日");
  });
});
