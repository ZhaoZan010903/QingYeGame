import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";
import { BatchReviewActions } from "./AdminLegacySync";
import { LegacySyncProgress } from "./LegacySyncControls";

const blocked: Parameters<typeof BatchReviewActions>[0]["batch"] = {
  id: "protected-batch",
  status: "blocked",
  review_kind: "bulk_offline",
  can_continue: true,
  can_cancel: true,
  planned_at: null,
  started_at: "2026-11-25T08:00:00Z",
  source_updated_at: null,
  finished_at: "2026-11-25T08:01:00Z",
  rollback_until: null,
  source_count: 510,
  reason: "触发批量下架保护",
  summary: {
    created: 0,
    updated: 0,
    removed: 0,
    restored: 0,
    review: 66,
    missing: 60,
  },
  progress: { stage: "verify_source", completed: 12, total: 12 },
};
function actions(batch: typeof blocked, canStart = true) {
  return renderToStaticMarkup(
    <BatchReviewActions
      batch={batch}
      canStart={canStart}
      expanded={false}
      pending={null}
      showItems={() => {}}
      review={() => {}}
    />,
  );
}

describe("批量下架保护", () => {
  it("确认数量使用清单总数，包含旧站已标下架但仍在目录中的商品", () => {
    const html = actions(blocked);
    expect(html).toContain("拟下架清单（66）");
    expect(html).toContain("确认并继续同步");
    expect(html).toContain("取消本批");
  });
  it("旧批次和其他校验错误只能查看、取消，不能沿用一次下架确认", () => {
    for (const batch of [
      { ...blocked, can_continue: false },
      { ...blocked, review_kind: "" as const, reason: "批量下架保护" },
    ]) {
      const html = actions(batch);
      expect(html).not.toContain("确认并继续同步");
      expect(html).toContain("取消本批");
    }
  });
  it("同步服务停用时可以查看清单及取消本批", () => {
    const html = actions(blocked, false);
    expect(html).toContain("拟下架清单");
    expect(html).toContain("取消本批");
    expect(html).not.toContain("确认并继续同步");
  });
  it("已取消的批次仅保留核对清单", () => {
    const html = actions({
      ...blocked,
      status: "cancelled",
      can_cancel: false,
      can_continue: false,
    });
    expect(html).toContain("拟下架清单");
    expect(html).not.toContain("取消本批");
    expect(html).not.toContain("确认并继续同步");
  });
  it("中止时保留停止阶段与读取页数，运行时才显示进行中的进度条", () => {
    const stopped = renderToStaticMarkup(
      <LegacySyncProgress progress={blocked.progress} running={false} failed />,
    );
    expect(stopped).toContain("复核旧站目录");
    expect(stopped).toContain("12 / 12 页");
    expect(stopped).not.toContain("progressbar");
    expect(stopped).not.toContain("38%");
    const running = renderToStaticMarkup(
      <LegacySyncProgress progress={blocked.progress} running failed={false} />,
    );
    expect(running).toContain('role="progressbar"');
    expect(running).toContain('aria-valuenow="38"');
  });
});
