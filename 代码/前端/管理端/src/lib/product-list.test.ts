import { describe, it, expect, afterEach, vi } from "vitest";
import { readProductListQuery, pageItems, productTime } from "./product-list";
import { adminApi } from "./admin-api";
import { productFrom } from "./api";

afterEach(() => vi.unstubAllGlobals());
describe("管理商品分页与数据", () => {
  it("编辑返回时保留分页筛选，但不发送编辑参数", () => {
    const result = readProductListQuery(
      new URLSearchParams(
        "page=3&limit=50&q=QY&source=member&trade_type=rent&edit=abc",
      ),
    );
    expect(result.page).toBe(3);
    expect(result.size).toBe(50);
    expect(result.query.get("source")).toBe("member");
    expect(result.query.has("edit")).toBe(false);
  });
  it("审核与回收站不串用状态", () => {
    const params = new URLSearchParams(
      "status=published&trash=1&page=-1&limit=999",
    );
    const a = readProductListQuery(params);
    expect(a.page).toBe(1);
    expect(a.size).toBe(20);
    expect(a.query.has("status")).toBe(false);
    const b = readProductListQuery(params, true);
    expect(b.query.get("status")).toBe("pending");
    expect(b.query.has("trash")).toBe(false);
  });
  it("分页窗口保留首尾并且不重复页码", () => {
    expect(pageItems(1, 1)).toEqual([1]);
    expect(pageItems(6, 12)).toEqual([1, "gap-5", 5, 6, 7, "gap-12", 12]);
  });
  it("空时间不伪造上架记录，统一北京时间", () => {
    expect(productTime("", "尚未上架")).toBe("尚未上架");
    expect(productTime("broken")).toBe("—");
    expect(productTime("2026-11-12T00:02:00Z")).toBe("2026-11-12 08:02");
    const p = productFrom({
      id: "p",
      created_at: "a",
      updated_at: "b",
      published_at: null,
      deleted_at: "c",
    });
    expect(p.publishedAt).toBe("");
    expect(p.createdAt).toBe("a");
    expect(p.updatedAt).toBe("b");
    expect(p.deletedAt).toBe("c");
  });
  it("删除使用所见版本，不先读取并覆盖并发更新", async () => {
    const fetch = vi
      .fn()
      .mockResolvedValue(
        new Response(
          JSON.stringify({ code: "VERSION_CONFLICT", message: "商品已更新" }),
          { status: 409 },
        ),
      );
    vi.stubGlobal("fetch", fetch);
    await expect(
      adminApi.trashProduct("p", 4, "重复录入"),
    ).rejects.toMatchObject({ status: 409 });
    expect(fetch).toHaveBeenCalledTimes(1);
    expect(fetch.mock.calls[0][1].method).toBe("DELETE");
    expect(JSON.parse(fetch.mock.calls[0][1].body)).toEqual({
      expected_version: 4,
      reason: "重复录入",
    });
  });
});
