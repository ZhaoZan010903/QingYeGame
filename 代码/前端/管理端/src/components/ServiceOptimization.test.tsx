import { afterEach, describe, expect, it, vi } from "vitest";
import { renderToStaticMarkup } from "react-dom/server";
import { CarouselEditor, emptySlide } from "./CarouselEditor";
import { CarouselSlidePreview } from "../../../共享/CarouselSlidePreview";
import { CategoryFields } from "./CategoryFields";
import { PrivateCredentials } from "./PrivateCredentials";
import { productFrom } from "../lib/api";
import { adminApi } from "../lib/admin-api";
import {
  buildRentalQuotes,
  customTagsForEditing,
  parseCustomTags,
} from "../lib/product-input";
import { readHomePresentation } from "../lib/home-content";

afterEach(() => vi.unstubAllGlobals());
describe("R15运营工作流", () => {
  it("清空轮播可保存为空，不自动补入推荐商品", () => {
    expect(readHomePresentation({ slides: [] }).slides).toEqual([]);
    const html = renderToStaticMarkup(
      <CarouselEditor slides={[]} onChange={() => {}} />,
    );
    expect(html).toContain("尚未配置轮播");
    expect(html).not.toContain("curated-slide");
  });
  it("主报价始终为首项，同租期旧金额不会覆盖编辑后的主价", () => {
    expect(
      buildRentalQuotes("rent", "month", "500", {
        day: "30",
        month: "450",
        week: "120",
      }),
    ).toEqual([
      { unit: "month", amount: "500" },
      { unit: "day", amount: "30" },
      { unit: "week", amount: "120" },
    ]);
  });
  it("未填写的勾选报价阻止保存，出售不携带旧租赁报价", () => {
    expect(() =>
      buildRentalQuotes("rent", "month", "500", { day: "" }),
    ).toThrow("有效金额");
    expect(buildRentalQuotes("sale", "account", "1000", { day: "" })).toEqual(
      [],
    );
  });
  it("模板仅允许日租时拒绝旧月价，并保留待人工核对的原输入", () => {
    const original = { month: "450" };
    expect(() =>
      buildRentalQuotes("rent", "day", "85", original, ["day"]),
    ).toThrow("手动移除");
    expect(original).toEqual({ month: "450" });
    expect(() =>
      buildRentalQuotes("rent", "month", "450", {}, ["day"]),
    ).toThrow("主报价单位");
    expect(
      buildRentalQuotes("rent", "day", "85", original, ["day", "month"]),
    ).toEqual([
      { unit: "day", amount: "85" },
      { unit: "month", amount: "450" },
    ]);
  });
  it("历史标签不转入有限额的自定义字段，编辑保留原标签", () => {
    const legacyTags = Array.from({ length: 40 }, (_, i) => `原标签${i}`);
    const attributes = { tags: legacyTags, custom_tags: ["可议价"] };
    expect(customTagsForEditing({ tags: legacyTags })).toBe("");
    expect(parseCustomTags(customTagsForEditing(attributes))).toEqual([
      "可议价",
    ]);
    expect(attributes.tags).toHaveLength(40);
  });
  it("标签去重并拒绝超过上限的内容", () => {
    expect(parseCustomTags("典藏、可议价，典藏")).toEqual(["典藏", "可议价"]);
    expect(() => parseCustomTags("长".repeat(25))).toThrow("24 字");
    expect(() =>
      parseCustomTags(
        Array.from({ length: 13 }, (_, i) => String(i)).join(","),
      ),
    ).toThrow("12 个");
  });
  it("新增轮播保持文案空白，由管理员撰写且预览不自动保存", () => {
    const change = vi.fn();
    const html = renderToStaticMarkup(
      <CarouselEditor slides={[emptySlide()]} onChange={change} />,
    );
    expect(html).toContain("大标题");
    expect(html).toContain("实时预览");
    expect(html).toContain("查找已上架商品");
    expect(change).not.toHaveBeenCalled();
    expect(emptySlide().title).toBe("");
  });
  it("轮播编辑和用户预览转义管理员文字，保留指定图文位置", () => {
    const slide = {
      ...emptySlide(),
      title: '<img onerror="alert(1)">',
      description: "第一行\n第二行",
      layout: "image-left" as const,
      textAlign: "right" as const,
      imagePositionX: 80,
    };
    const html = renderToStaticMarkup(<CarouselSlidePreview slide={slide} />);
    expect(html).toContain("&lt;img");
    expect(html).not.toContain("<img onerror=");
    expect(html).toContain("layout-image-left");
    expect(html).toContain("--slide-x:80%");
  });
  it("文本属性可配置为必填，不呈现无关的选项编辑", () => {
    const html = renderToStaticMarkup(
      <CategoryFields
        value={JSON.stringify([
          {
            key: "note",
            label: "账号说明",
            type: "text",
            required: true,
            enabled: true,
            placeholder: "填写特点",
            options: [],
          },
        ])}
        onChange={() => {}}
      />,
    );
    expect(html).toContain("填写特点");
    expect(html).toContain("发布时必须填写");
    expect(html).not.toContain("添加选项");
  });
  it("私密资料默认不读取，须管理员显式查看", () => {
    const fetch = vi.fn();
    vi.stubGlobal("fetch", fetch);
    const html = renderToStaticMarkup(<PrivateCredentials id="product-1" />);
    expect(html).toContain("查看私密资料");
    expect(html).not.toContain("QQ 密码</dt>");
    expect(fetch).not.toHaveBeenCalled();
  });
  it("私密接口拒绝权限时保留拒绝结果", async () => {
    const fetch = vi
      .fn()
      .mockResolvedValue(
        new Response(
          JSON.stringify({ code: "FORBIDDEN", message: "无权查看" }),
          { status: 403 },
        ),
      );
    vi.stubGlobal("fetch", fetch);
    await expect(
      adminApi.privateCredentials("product-1"),
    ).rejects.toMatchObject({ status: 403 });
    expect(fetch.mock.calls[0][0]).toBe(
      "/api/v1/admin/products/product-1/private-credentials",
    );
  });
  it("商品视图仅映射私密标记、多报价和咨询人数，不携带私密明文", () => {
    const p = productFrom({
      id: "p1",
      has_private_credentials: true,
      private_credentials: { qq_account: "12345", qq_password: "hidden" },
      rental_quotes: [
        { unit: "day", amount: "25.00" },
        { unit: "month", amount: "300.00" },
      ],
      consultation_count: 7,
      attributes: { tags: ["稀有"], custom_tags: ["稀有", "可议价"] },
    });
    expect(p.hasPrivateCredentials).toBe(true);
    expect(p.consultationCount).toBe(7);
    expect(p.rentalQuotes).toHaveLength(2);
    expect(p.tags).toEqual(["稀有", "可议价"]);
    expect(JSON.stringify(p)).not.toContain("hidden");
  });
});
