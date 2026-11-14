import { afterEach, expect, it, vi } from "vitest";
vi.unmock("../../../共享/response-crypto");
import { responseCrypto } from "../../../共享/response-crypto";
import {
  fixtureInit,
  fixtureEncrypted,
} from "../../../共享/response-crypto-fixture";
import { request } from "./api";
afterEach(() => {
  responseCrypto.reset();
  vi.unstubAllGlobals();
});
it("管理API以独立admin AAD解密完整手机号并保留授权错误", async () => {
  vi.stubGlobal("location", new URL("https://qingye.test"));
  const fetcher = vi.fn(async (input: string, init: RequestInit = {}) => {
    if (input.includes("response-crypto/init")) return fixtureInit("admin");
    return fixtureEncrypted(
      input,
      init,
      { data: { phone: "13800000000" } },
      { surface: "admin" },
    );
  });
  vi.stubGlobal("fetch", fetcher);
  expect((await request("/admin/users")).data.phone).toBe("13800000000");
  expect(fetcher).toHaveBeenCalledTimes(2);
});
it("管理API拒绝public密文，不把传输失败变成操作成功", async () => {
  vi.stubGlobal("location", new URL("https://qingye.test"));
  vi.stubGlobal("fetch", async (input: string, init: RequestInit = {}) =>
    input.includes("response-crypto/init")
      ? fixtureInit("admin")
      : fixtureEncrypted(
          input,
          init,
          { data: { saved: true } },
          { surface: "public" },
        ),
  );
  await expect(
    request("/admin/products/a", "PATCH", { expected_version: 2 }),
  ).rejects.toMatchObject({ code: "RESPONSE_SECURITY" });
});
