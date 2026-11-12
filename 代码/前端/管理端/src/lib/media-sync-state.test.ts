import { describe, expect, it } from "vitest";
import {
  canAcceptFreshProduct,
  mergeMediaSyncDisplay,
  editorRecordFromProduct,
} from "./media-sync-state";
import type { Product } from "../types";

describe("图片同步摘要不能更新编辑表单的乐观锁版本", () => {
  it("完成后全量刷新必须仍是同一个未编辑快照，迟到响应不覆盖新编辑或新商品", () => {
    const before = { id: "p", version: 10 };
    expect(canAcceptFreshProduct(before, before, false)).toBe(true);
    expect(canAcceptFreshProduct(before, before, true)).toBe(false);
    expect(canAcceptFreshProduct(before, { id: "p", version: 11 }, false)).toBe(
      false,
    );
    expect(
      canAcceptFreshProduct(before, { id: "other", version: 10 }, false),
    ).toBe(false);
  });
  it("图库完整快照必须同时刷新可编辑内容，不能只替换version", () => {
    const value = editorRecordFromProduct(
      { title: "旧标题", attributes: { server: "旧服" }, version: 3 },
      {
        id: "p",
        title: "新标题",
        version: 4,
        mode: "rent",
        unit: "day",
        price: 8,
        description: "新描述",
        attributes: { server: "新服" },
        images: [],
      } as unknown as Product,
    );
    expect(value).toMatchObject({
      title: "新标题",
      version: 4,
      price_amount: "8",
      description: "新描述",
      attributes: { server: "新服" },
    });
  });
  it.each([true, false])(
    "dirty=%s时保留原版本与内容，仅显示新业务状态并要求显式重读",
    (dirty) => {
      const original = {
        id: "p",
        title: "本页标题",
        version: 10,
        status: "publishing",
        attributes: { server: "old" },
      };
      const merged = mergeMediaSyncDisplay(
        original,
        { status: "published", version: 11 },
        dirty,
      );
      expect(merged.record).toMatchObject({
        title: "本页标题",
        status: "published",
        version: 10,
        attributes: { server: "old" },
      });
      expect(merged.requiresReload).toBe(true);
      expect(merged.message).toContain("重新加载");
      expect(original.status).toBe("publishing");
    },
  );
  it("状态相同但另一管理员修改版本时同样要求重新加载", () => {
    expect(
      mergeMediaSyncDisplay(
        { status: "publishing", version: 10 },
        { status: "publishing", version: 12 },
        true,
      ),
    ).toMatchObject({ record: { version: 10 }, requiresReload: true });
  });
  it("同版本只更新安全中止显示，不丢失原表单快照", () => {
    expect(
      mergeMediaSyncDisplay(
        { status: "publishing", version: 10, title: "old" },
        { status: "publishing", version: 10, interrupted: true },
        true,
      ),
    ).toMatchObject({
      record: { version: 10, title: "old", publication_interrupted: true },
      requiresReload: false,
    });
  });
});
