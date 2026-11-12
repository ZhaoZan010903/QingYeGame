import { expect, it } from "vitest";
import {
  adminProductID,
  adminProductPath,
  productListReturn,
} from "./product-links";
it("咨询卡片进入独立管理商品页，并兼容旧链接", () => {
  expect(adminProductPath("product-1")).toBe("/products/product-1");
  expect(adminProductID("products/product-1")).toBe("product-1");
  expect(adminProductID("accounts/product-1")).toBe("product-1");
  expect(adminProductID("products")).toBeUndefined();
  expect(adminProductID("products/%ZZ")).toBeUndefined();
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
