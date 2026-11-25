import { expect, it } from "vitest";
import {
  adminProductID,
  adminProductPath,
  chatProductPath,
  productDetailOrigin,
  productListReturn,
} from "./product-links";
it("咨询卡片进入独立管理商品页，并兼容旧链接", () => {
  expect(adminProductPath("product-1")).toBe("/products/product-1");
  expect(adminProductID("products/product-1")).toBe("product-1");
  expect(adminProductID("accounts/product-1")).toBe("product-1");
  expect(adminProductID("products")).toBeUndefined();
  expect(adminProductID("products/%ZZ")).toBeUndefined();
});
it("从看板、待审核、指定会话进入详情后能回到原入口，直接打开回到商品列表", () => {
  expect(productDetailOrigin("/", "").href).toBe("/");
  expect(productDetailOrigin("/?days=30&metric=consultations", "").href).toBe(
    "/?days=30&metric=consultations",
  );
  expect(productDetailOrigin("/reviews?page=2", "").href).toBe(
    "/reviews?page=2",
  );
  const url = new URL(chatProductPath("p", "a&b"), "https://example.test");
  expect(productDetailOrigin(null, url.search).href).toBe(
    "/chat?conversation=a%26b",
  );
  expect(productDetailOrigin(null, "").href).toBe("/products");
  expect(productDetailOrigin("//other.test", "?from=unknown").href).toBe(
    "/products",
  );
});
it("详情返回列表保留分页筛选，并拒绝外部返回地址", () => {
  expect(productListReturn("/products?page=3&q=账号")).toBe(
    "/products?page=3&q=账号",
  );
  expect(productListReturn("/reviews?page=2")).toBe("/reviews?page=2");
  for (const value of [
    undefined,
    {},
    "//external.test",
    "https://external.test",
    "/products/other",
    "/productsx",
  ]) {
    expect(productListReturn(value)).toBeUndefined();
  }
});
