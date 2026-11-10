import { afterEach, describe, expect, it, vi } from "vitest";
import { adminApi, canManage } from "./admin-api";

afterEach(() => vi.unstubAllGlobals());
const response = (body: unknown, status = 200) =>
  new Response(JSON.stringify(body), { status });
describe("管理员权限与写入错误", () => {
  it("停用会员失败时保留服务端拒绝结果", async () => {
    vi.stubGlobal(
      "fetch",
      vi
        .fn()
        .mockResolvedValue(
          response(
            { code: "LAST_ADMIN", message: "需保留至少一位管理员" },
            409,
          ),
        ),
    );
    await expect(
      adminApi.setUserEnabled("admin-1", false),
    ).rejects.toMatchObject({
      status: 409,
      code: "LAST_ADMIN",
    });
  });
  it("只允许明确的管理员角色进入管理界面", () => {
    expect(canManage(null)).toBe(false);
    expect(canManage({ id: "1", nickname: "会员", phone: "138****0000" })).toBe(
      false,
    );
    expect(
      canManage({
        id: "1",
        nickname: "会员",
        phone: "138****0000",
        role: "member",
      }),
    ).toBe(false);
    expect(
      canManage({
        id: "2",
        nickname: "管理",
        phone: "138****0001",
        role: "admin",
      }),
    ).toBe(true);
  });
  it("服务端403必须显示拒绝，不返回演示列表", async () => {
    vi.stubGlobal(
      "fetch",
      vi
        .fn()
        .mockResolvedValue(
          response({ code: "FORBIDDEN", message: "需要管理员权限" }, 403),
        ),
    );
    await expect(
      adminApi.products(new URLSearchParams()),
    ).rejects.toMatchObject({
      status: 403,
      message: "需要管理员权限",
    });
  });
  it("保存携带版本并保留自定义属性，冲突不会被当作成功", async () => {
    const fetch = vi
      .fn()
      .mockResolvedValue(
        response({ code: "VERSION_CONFLICT", message: "资料已更新" }, 409),
      );
    vi.stubGlobal("fetch", fetch);
    const input = {
      title: "旧商品",
      price_unit: "unknown",
      price_amount: null,
      attributes: { legacy_origin: "自定义原始字段", server: "华东一区" },
    };
    await expect(
      adminApi.saveProduct(input, "legacy-1", 4),
    ).rejects.toMatchObject({
      status: 409,
      code: "VERSION_CONFLICT",
    });
    expect(JSON.parse(fetch.mock.calls[0][1].body)).toEqual({
      ...input,
      expected_version: 4,
    });
    expect(fetch.mock.calls[0][1].credentials).toBe("include");
  });
  it("审核校验失败保留字段错误，不继续请求已更新的商品", async () => {
    const fetch = vi
      .fn()
      .mockResolvedValue(
        response(
          { message: "请填写退回原因", fields: { reason: "必填" } },
          422,
        ),
      );
    vi.stubGlobal("fetch", fetch);
    await expect(
      adminApi.action("member-1", "review", {
        decision: "reject",
        expected_version: 2,
      }),
    ).rejects.toMatchObject({ status: 422, fields: { reason: "必填" } });
    expect(fetch).toHaveBeenCalledTimes(1);
  });
});
