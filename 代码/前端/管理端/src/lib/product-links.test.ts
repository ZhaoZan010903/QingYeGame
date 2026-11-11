import { expect, it } from "vitest";
import { adminProductID, adminProductPath } from "./product-links";
it("咨询卡片进入独立管理商品页，并兼容旧链接", () => {
  expect(adminProductPath("product-1")).toBe("/products/product-1");
  expect(adminProductID("products/product-1")).toBe("product-1");
  expect(adminProductID("accounts/product-1")).toBe("product-1");
  expect(adminProductID("products")).toBeUndefined();
  expect(adminProductID("products/%ZZ")).toBeUndefined();
});
