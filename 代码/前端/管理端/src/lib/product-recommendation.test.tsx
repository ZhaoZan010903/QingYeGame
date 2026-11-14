import { afterEach, describe, expect, it, vi } from "vitest";
import { renderToStaticMarkup } from "react-dom/server";
import { MemoryRouter } from "react-router-dom";
import {
  parseRecommendationBoost,
  readRecommendation,
  saveRecommendation,
} from "./product-recommendation";
import { productFrom } from "./api";
import { ProductListRow } from "../components/AdminProducts";
import { defaultProductCategory } from "./product-input";
import {
  editorRecordFromProduct,
  mergeRecommendationVersion,
} from "./media-sync-state";

afterEach(() => vi.unstubAllGlobals());

describe("R23 管理商品推荐与列表", () => {
  it("图片操作的完整快照同步较新的推荐版本，缺失字段时保留已有设置", () => {
    const old = { recommendation_boost: 10, recommendation_version: 3 };
    const product = productFrom({
      id: "p",
      recommendation_boost: 70,
      recommendation_version: 7,
    });
    expect(editorRecordFromProduct(old, product)).toMatchObject({
      recommendation_boost: 70,
      recommendation_version: 7,
    });
    expect(
      editorRecordFromProduct(old, {
        ...product,
        recommendationBoost: undefined,
        recommendationVersion: undefined,
      }),
    ).toMatchObject(old);
  });
  it("迟到的内容刷新不回退已确认推荐版本，也不阻止新内容状态生效", () => {
    const current = {
      id: "p",
      version: 8,
      status: "publishing",
      recommendation_boost: 70,
      recommendation_version: 7,
    };
    const incoming = {
      id: "p",
      version: 9,
      status: "published",
      recommendation_boost: 10,
      recommendation_version: 3,
    };
    expect(mergeRecommendationVersion(current, incoming)).toEqual({
      ...incoming,
      recommendation_boost: 70,
      recommendation_version: 7,
    });
    expect(
      mergeRecommendationVersion(current, { ...incoming, id: "other" }),
    ).toEqual({ ...incoming, id: "other" });
  });
  it.each(["", "-1", "101", "1.5", "1e2", "NaN"])(
    "拒绝非法推荐加分 %s",
    (value) => {
      expect(() => parseRecommendationBoost(value)).toThrow("0–100");
    },
  );
  it("允许零值和上限，完整读取独立推荐版本", () => {
    expect(parseRecommendationBoost(" 0 ")).toBe(0);
    expect(parseRecommendationBoost("100")).toBe(100);
    expect(
      readRecommendation({
        id: "p",
        recommendation_boost: 100,
        recommendation_version: 7,
      }),
    ).toEqual({ id: "p", boost: 100, version: 7 });
    expect(() =>
      readRecommendation({ id: "p", recommendation_boost: 0 }),
    ).toThrow("响应不完整");
  });
  it("独立保存携带所见推荐版本，不触发商品保存或自动重试冲突", async () => {
    const fetch = vi.fn().mockResolvedValue(
      new Response(
        JSON.stringify({
          code: "RECOMMENDATION_VERSION_CONFLICT",
          message: "推荐设置已更新",
        }),
        { status: 409 },
      ),
    );
    vi.stubGlobal("fetch", fetch);
    await expect(saveRecommendation("p/1", 100, 3)).rejects.toMatchObject({
      status: 409,
    });
    expect(fetch).toHaveBeenCalledTimes(1);
    expect(fetch.mock.calls[0][0]).toBe(
      "/api/v1/admin/products/p%2F1/recommendation",
    );
    expect(fetch.mock.calls[0][1].method).toBe("PATCH");
    expect(JSON.parse(fetch.mock.calls[0][1].body)).toEqual({
      boost: 100,
      expected_version: 3,
    });
  });
  it("丢失响应不自动重发写入", async () => {
    const fetch = vi.fn().mockRejectedValue(new TypeError("connection lost"));
    vi.stubGlobal("fetch", fetch);
    await expect(saveRecommendation("p", 20, 3)).rejects.toMatchObject({
      code: "NETWORK_ERROR",
    });
    expect(fetch).toHaveBeenCalledTimes(1);
  });
  it("管理列表首个信息格独立展示编号，推荐值与商品版本分别映射", () => {
    const product = productFrom({
      id: "p",
      product_code: "QYCH123",
      title: "测试账号",
      version: 12,
      recommendation_boost: 45,
      recommendation_version: 3,
    });
    expect(product.recommendationBoost).toBe(45);
    expect(product.recommendationVersion).toBe(3);
    expect(product.version).toBe(12);
    const html = renderToStaticMarkup(
      <MemoryRouter>
        <table>
          <tbody>
            <ProductListRow
              product={product}
              trash={false}
              review={false}
              disabled={false}
              returnTo="/products"
              edit={() => {}}
              act={() => {}}
              purge={() => {}}
              recommend={() => {}}
            />
          </tbody>
        </table>
      </MemoryRouter>,
    );
    expect(html).toMatch(
      /<tr><td class="product-code-cell" data-label="商品编号"><span[^>]*>QYCH123<\/span><\/td><td class="product-cell"/,
    );
    expect(html).toContain("推荐加分");
    const fallback = productFrom({ id: "old" });
    expect(fallback.recommendationBoost).toBe(0);
    expect(fallback.recommendationVersion).toBe(1);
  });
  it("新租赁自动选择兼容报价分类，已有归属不被停用或重排覆盖", () => {
    const categories = [
      {
        id: "disabled",
        enabled: false,
        trade_type: "rent",
        price_units: ["day"],
      },
      {
        id: "sale-only-unit",
        enabled: true,
        trade_type: "all",
        price_units: ["account"],
      },
      {
        id: "rent",
        enabled: true,
        trade_type: "rent",
        price_units: ["hour", "month"],
      },
    ];
    expect(defaultProductCategory(categories, "rent")).toBe("rent");
    expect(defaultProductCategory(categories, "rent", "disabled")).toBe(
      "disabled",
    );
  });
});
