import { afterEach, describe, expect, it, vi } from "vitest";
import { api, productFrom } from "./api";
const response = (data: unknown, status = 200) =>
  new Response(JSON.stringify(data), { status });
afterEach(() => {
  vi.unstubAllGlobals();
  vi.unstubAllEnvs();
});
describe("独立管理会话与接口边界", () => {
  it("管理资料保留中文等级，不转换成NaN", () => {
    expect(
      productFrom({ id: "legacy", attributes: { level: "彩钻" } }).level,
    ).toBe("彩钻");
    expect(productFrom({ id: "legacy", attributes: { level: 9 } }).level).toBe(
      9,
    );
  });
  it("管理登录仅接受明确管理员身份，并使用服务端 Cookie", async () => {
    const fetch = vi.fn().mockResolvedValue(
      response({
        data: {
          id: "a1",
          nickname: "管理",
          role: "admin",
          phone_masked: "138****0000",
        },
      }),
    );
    vi.stubGlobal("fetch", fetch);
    await expect(
      api.login("13800000000", "test-password-only"),
    ).resolves.toMatchObject({ role: "admin" });
    expect(fetch.mock.calls[0][0]).toBe("/api/v1/auth/login");
    expect(fetch.mock.calls[0][1].credentials).toBe("include");
  });
  it("误返回会员身份时清理管理会话并拒绝进入", async () => {
    const fetch = vi
      .fn()
      .mockResolvedValueOnce(response({ data: { id: "m1", role: "member" } }))
      .mockResolvedValueOnce(response({ data: { ok: true } }));
    vi.stubGlobal("fetch", fetch);
    await expect(
      api.login("13800000000", "test-password-only"),
    ).rejects.toMatchObject({ status: 403, code: "ADMIN_REQUIRED" });
    expect(fetch.mock.calls[1][0]).toBe("/api/v1/auth/logout");
  });
  it("过期管理会话显示登录，服务异常明确保留错误", async () => {
    vi.stubGlobal(
      "fetch",
      vi
        .fn()
        .mockResolvedValueOnce(response({ message: "请登录" }, 401))
        .mockResolvedValueOnce(response({ message: "服务不可用" }, 503)),
    );
    await expect(api.me()).resolves.toBeNull();
    await expect(api.me()).rejects.toMatchObject({ status: 503 });
  });
  it("即使环境误设demo，管理端仍必须请求真实管理会话", async () => {
    vi.stubEnv("VITE_DATA_MODE", "demo");
    const fetch = vi
      .fn()
      .mockResolvedValue(response({ message: "请登录" }, 401));
    vi.stubGlobal("fetch", fetch);
    expect(await api.me()).toBeNull();
    expect(fetch).toHaveBeenCalledTimes(1);
  });
  it("未授权上传失败不会产生本地样例图", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn().mockResolvedValue(response({ message: "管理员会话已失效" }, 401)),
    );
    await expect(
      api.upload(new File(["test"], "check.png", { type: "image/png" })),
    ).rejects.toMatchObject({ status: 401 });
  });
});
