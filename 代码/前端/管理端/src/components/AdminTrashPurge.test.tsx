import { afterEach, describe, expect, it, vi } from "vitest";
import { renderToStaticMarkup } from "react-dom/server";
import { MemoryRouter } from "react-router-dom";
import { ProductListRow } from "./AdminProducts";
import { AdminTrashPurge, PurgeScope } from "./AdminTrashPurge";
import { adminApi } from "../lib/admin-api";
import { ApiError, productFrom } from "../lib/api";
import {
  purgeFailureKind,
  readPurgeResult,
  readTrashPreview,
  trashPreviewLimit,
  type TrashPurgeInput,
} from "../lib/trash-purge";

const snapshot = "a".repeat(64);
const preview = { snapshot, total: 71, image_count: 310 };
const product = (status = "draft") =>
  productFrom({
    id: "item/a",
    product_code: "QY-77",
    title: "<script>商品标题</script>",
    status,
    version: 8,
    deleted_at: "2026-11-14T10:00:00Z",
    images: [],
  });
const response = (data: unknown, status = 200) =>
  new Response(JSON.stringify(data), { status });
afterEach(() => vi.unstubAllGlobals());

describe("永久删除请求契约", () => {
  it("清空预览读取整个回收站，不带列表筛选和分页，并支持取消", async () => {
    const fetch = vi.fn().mockResolvedValue(response({ data: preview }));
    vi.stubGlobal("fetch", fetch);
    const controller = new AbortController();
    await expect(adminApi.trashPreview(controller.signal)).resolves.toEqual(
      preview,
    );
    expect(fetch.mock.calls[0][0]).toBe("/api/v1/admin/products/trash/preview");
    expect(fetch.mock.calls[0][1]).toMatchObject({
      method: "GET",
      credentials: "include",
      signal: controller.signal,
    });
    expect(fetch.mock.calls[0][1].body).toBeUndefined();
  });
  it("单条删除发送所见版本、原因和确认文字，不预取新版本覆盖冲突", async () => {
    const fetch = vi
      .fn()
      .mockResolvedValue(
        response({ code: "VERSION_CONFLICT", message: "商品已变化" }, 409),
      );
    vi.stubGlobal("fetch", fetch);
    const input = {
      expected_version: 8,
      confirmation: "永久删除" as const,
      reason: "重复录入",
      request_id: "single-id",
    };
    await expect(adminApi.purgeProduct("item/a", input)).rejects.toMatchObject({
      status: 409,
      code: "VERSION_CONFLICT",
    });
    expect(fetch).toHaveBeenCalledTimes(1);
    expect(fetch.mock.calls[0][0]).toBe(
      "/api/v1/admin/products/item%2Fa/purge",
    );
    expect(fetch.mock.calls[0][1].method).toBe("POST");
    expect(JSON.parse(fetch.mock.calls[0][1].body)).toEqual(input);
  });
  it("清空只提交预览摘要，不将页内商品或筛选伪装成完整范围", async () => {
    const fetch = vi
      .fn()
      .mockResolvedValue(
        response({ data: { purged_count: 71, cleanup_pending: true } }),
      );
    vi.stubGlobal("fetch", fetch);
    const input: TrashPurgeInput = {
      snapshot,
      confirmation: "清空回收站",
      reason: "清理重复资料",
      request_id: "bulk-id",
    };
    await expect(adminApi.purgeTrash(input)).resolves.toEqual({
      purged_count: 71,
      cleanup_pending: true,
    });
    expect(fetch.mock.calls[0][0]).toBe("/api/v1/admin/products/trash/purge");
    expect(JSON.parse(fetch.mock.calls[0][1].body)).toEqual(input);
  });
  it("网络结果不明后的相同请求保留request_id和body，不自动换号或自动重试", async () => {
    const fetch = vi
      .fn()
      .mockRejectedValueOnce(new TypeError("connection lost"))
      .mockResolvedValueOnce(
        response({ data: { purged_count: 71, cleanup_pending: true } }),
      );
    vi.stubGlobal("fetch", fetch);
    const input: TrashPurgeInput = {
      snapshot,
      confirmation: "清空回收站",
      reason: "已核实重复",
      request_id: "same-retry-id",
    };
    await expect(adminApi.purgeTrash(input)).rejects.toMatchObject({
      code: "NETWORK_ERROR",
    });
    expect(fetch).toHaveBeenCalledTimes(1);
    await adminApi.purgeTrash(input);
    expect(fetch).toHaveBeenCalledTimes(2);
    expect(fetch.mock.calls[0][1].body).toBe(fetch.mock.calls[1][1].body);
    expect(JSON.parse(fetch.mock.calls[1][1].body).request_id).toBe(
      "same-retry-id",
    );
  });
  it.each(["TRASH_SNAPSHOT_CHANGED", "IDEMPOTENCY_CONFLICT", "INVALID_STATE"])(
    "%s不会当成成功或触发扩大范围的自动重试",
    async (code) => {
      const fetch = vi
        .fn()
        .mockResolvedValue(response({ code, message: "请重新确认" }, 409));
      vi.stubGlobal("fetch", fetch);
      await expect(
        adminApi.purgeTrash({
          snapshot,
          confirmation: "清空回收站",
          reason: "重复",
          request_id: "id",
        }),
      ).rejects.toMatchObject({ status: 409, code });
      expect(fetch).toHaveBeenCalledTimes(1);
    },
  );
});

describe("预览与结果不确定性", () => {
  it("空回收站可读取为0，损坏预览不可默认为空或任意摘要", () => {
    expect(
      readTrashPreview({ ...preview, total: 0, image_count: 0 }).total,
    ).toBe(0);
    for (const invalid of [
      null,
      {},
      { ...preview, total: "71" },
      { ...preview, snapshot: "short" },
      { ...preview, image_count: -1 },
    ]) {
      expect(() => readTrashPreview(invalid)).toThrow("预览数据不完整");
    }
  });
  it("错误成功载荷保留结果不明语义，不能伪造删除成功", () => {
    for (const invalid of [
      null,
      {},
      { purged_count: -1, cleanup_pending: true },
      { purged_count: 2, cleanup_pending: false },
    ]) {
      expect(() => readPurgeResult(invalid)).toThrow("结果暂未确认");
      try {
        readPurgeResult(invalid);
      } catch (error) {
        expect(purgeFailureKind(error)).toBe("unknown");
      }
    }
  });
  it("网络/5xx/无法识别响应保留旧请求，明确冲突要求重新确认", () => {
    for (const error of [
      new Error("lost"),
      new ApiError("lost", 0),
      new ApiError("timeout", 408),
      new ApiError("down", 503),
      new ApiError("broken", 200, "INVALID_RESPONSE"),
    ])
      expect(purgeFailureKind(error)).toBe("unknown");
    for (const status of [404, 409, 410])
      expect(purgeFailureKind(new ApiError("changed", status))).toBe("refresh");
    for (const status of [400, 401, 403, 422, 429])
      expect(purgeFailureKind(new ApiError("rejected", status))).toBe(
        "rejected",
      );
  });
  it("超过服务器批量限制时不能继续提交同一批", () => {
    expect(
      trashPreviewLimit({ ...preview, total: 1000, image_count: 100000 }),
    ).toBe("");
    expect(trashPreviewLimit({ ...preview, total: 1001 })).toContain(
      "先单条删除",
    );
    expect(trashPreviewLimit({ ...preview, image_count: 100001 })).toContain(
      "先单条删除",
    );
  });
});

describe("回收站入口与确认文案", () => {
  const row = (trash: boolean, status = "draft", disabled = false) =>
    renderToStaticMarkup(
      <MemoryRouter>
        <table>
          <tbody>
            <ProductListRow
              product={product(status)}
              trash={trash}
              review={false}
              disabled={disabled}
              returnTo="/products?trash=1"
              edit={() => {}}
              act={() => {}}
              purge={() => {}}
            />
          </tbody>
        </table>
      </MemoryRouter>,
    );
  it("正常列表没有永久删除，回收站保留恢复草稿并添加永久删除", () => {
    expect(row(false)).not.toContain("永久删除");
    expect(row(true)).toContain("恢复草稿");
    expect(row(true)).toContain("永久删除");
  });
  it("最终驳回在回收站仍可永久删除，但不能恢复草稿", () => {
    const html = row(true, "rejected_final");
    expect(html).toContain("永久删除");
    expect(html).not.toContain("恢复草稿");
    expect(html).toContain("最终驳回，不可编辑或恢复");
    expect(row(false, "rejected_final")).not.toContain("永久删除");
  });
  it("读取或提交期间永久删除入口禁用", () => {
    expect(row(true, "draft", true)).toContain(
      'class="product-delete product-purge" disabled=""',
    );
  });
  it("单条展示真实编号、标题并转义，交代后台图片与记录保留边界", () => {
    const html = renderToStaticMarkup(
      <PurgeScope
        target={{ kind: "single", product: product() }}
        preview={null}
      />,
    );
    expect(html).toContain("QY-77");
    expect(html).toContain("&lt;script&gt;商品标题&lt;/script&gt;");
    expect(html).not.toContain("<script>");
    expect(html).toContain("永久删除这 1 个商品");
    expect(html).toContain("无法恢复为草稿");
    expect(html).toContain("其他地方仍引用的图片会保留");
    expect(html).toContain("用户聊天文字及必要操作记录保留");
    expect(html).toContain("备份不会即时擦除");
  });
  it("清空数量来自服务器预览，明确全回收站范围与变化重确认", () => {
    const html = renderToStaticMarkup(
      <PurgeScope target={{ kind: "all" }} preview={preview} />,
    );
    expect(html).toContain("整个回收站");
    expect(html).toContain("不受当前搜索、筛选或页码限制");
    expect(html).toContain("<strong>71</strong>");
    expect(html).toContain("<strong>310</strong>");
    expect(html).toContain("需要重新预览并确认");
    expect(html).not.toContain("新增不包含");
  });
  it("初始单条确认必须输入文字及原因，初始清空预览未返回不能提交", () => {
    const props = {
      onClose: () => {},
      onSuccess: () => {},
      onBusyChange: () => {},
    };
    const single = renderToStaticMarkup(
      <AdminTrashPurge
        target={{ kind: "single", product: product() }}
        {...props}
      />,
    );
    expect(single).toContain("请输入“永久删除”确认");
    expect(single).toContain("操作原因");
    expect(single).toContain('class="product-danger-button" disabled=""');
    const all = renderToStaticMarkup(
      <AdminTrashPurge target={{ kind: "all" }} {...props} />,
    );
    expect(all).toContain("正在读取整个回收站的预览数量");
    expect(all).toContain('class="product-danger-button" disabled=""');
    expect(all).not.toContain("请输入“清空回收站”确认");
  });
});
