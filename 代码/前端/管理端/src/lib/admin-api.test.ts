import { afterEach, describe, expect, it, vi } from "vitest";
import { adminApi, adminImageApi, canManage } from "./admin-api";
import { productFrom } from "./api";

afterEach(() => vi.unstubAllGlobals());
const response = (body: unknown, status = 200) =>
  new Response(JSON.stringify(body), { status });
describe("管理员权限与写入错误", () => {
  it("管理端保留上架中止标记", () => {
    expect(
      productFrom({
        id: "a",
        status: "publishing",
        publication_interrupted: true,
      }).publicationInterrupted,
    ).toBe(true);
    expect(
      productFrom({ id: "old", status: "draft" }).publicationInterrupted,
    ).toBe(false);
  });
  it("删除已上传封面后重读服务端图集，失败时不伪造删除成功", async () => {
    const fetch = vi
      .fn()
      .mockResolvedValueOnce(response({ data: {} }))
      .mockResolvedValueOnce(
        response({
          data: {
            id: "p",
            title: "商品",
            image_total: 1,
            version: 8,
            images: [{ id: "second" }],
          },
        }),
      )
      .mockResolvedValueOnce(
        response({ code: "VERSION_CONFLICT", message: "请刷新图集" }, 409),
      );
    vi.stubGlobal("fetch", fetch);
    const result = await adminImageApi.removeImage("p", "first", 7);
    expect(result.images.map((image) => image.id)).toEqual(["second"]);
    expect(result).toMatchObject({ imageTotal: 1, version: 8 });
    expect(fetch.mock.calls[0][1].method).toBe("DELETE");
    expect(JSON.parse(fetch.mock.calls[0][1].body)).toEqual({
      expected_version: 7,
    });
    await expect(
      adminImageApi.removeImage("p", "second", 7),
    ).rejects.toMatchObject({ status: 409 });
    expect(fetch).toHaveBeenCalledTimes(3);
    expect(result.images[0].id).toBe("second");
  });
  it("上传停权与登录开关分别调用，处置携带读取版本和清理选择", async () => {
    const fetch = vi
      .fn()
      .mockResolvedValue(response({ data: { blocked: true, version: 3 } }));
    vi.stubGlobal("fetch", fetch);
    await adminApi.saveUploadRestriction("member/a", {
      blocked: true,
      expected_version: 2,
      reason: "已核实恶意上传",
      purge_staging: false,
    });
    expect(fetch.mock.calls[0][0]).toBe(
      "/api/v1/admin/users/member%2Fa/upload-restriction",
    );
    expect(fetch.mock.calls[0][1].method).toBe("PUT");
    expect(JSON.parse(fetch.mock.calls[0][1].body)).toEqual({
      blocked: true,
      expected_version: 2,
      reason: "已核实恶意上传",
      purge_staging: false,
    });
  });
  it("上传处置版本冲突保持服务端错误，不能当保存成功", async () => {
    vi.stubGlobal(
      "fetch",
      vi
        .fn()
        .mockResolvedValue(
          response({ code: "VERSION_CONFLICT", message: "请刷新状态" }, 409),
        ),
    );
    await expect(
      adminApi.saveUploadRestriction("member", {
        blocked: false,
        expected_version: 1,
        reason: "人工复核恢复",
        purge_staging: false,
      }),
    ).rejects.toMatchObject({ status: 409 });
  });
  it("图片准备状态与重试使用管理员接口", async () => {
    const fetch = vi
      .fn()
      .mockImplementation(async () =>
        response({ data: { total: 4, ready: 2, failed: 1, attempts: 3 } }),
      );
    vi.stubGlobal("fetch", fetch);
    await expect(adminApi.mediaSync("product")).resolves.toMatchObject({
      total: 4,
      ready: 2,
    });
    await adminApi.retryMediaSync("product", 12);
    expect(fetch.mock.calls.map((call) => call[0])).toEqual([
      "/api/v1/admin/products/product/media-sync",
      "/api/v1/admin/products/product/media-sync/retry",
    ]);
    expect(fetch.mock.calls[1][1].method).toBe("POST");
    expect(JSON.parse(fetch.mock.calls[1][1].body)).toEqual({
      expected_version: 12,
    });
  });
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
