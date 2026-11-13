import { afterEach, describe, expect, it, vi } from "vitest";
import { renderToStaticMarkup } from "react-dom/server";
import { NicknameReviewCard } from "../components/AdminProfiles";
afterEach(() => {
  vi.unstubAllGlobals();
  vi.resetModules();
});
const response = (data: unknown, status = 200) =>
  new Response(JSON.stringify(data), {
    status,
    headers: { "Content-Type": "application/json" },
  });
describe("管理员昵称审核与头像库", () => {
  it("头像保存携带所见版本，并保留服务端版本冲突", async () => {
    const fetch = vi
      .fn()
      .mockResolvedValue(
        response({ message: "头像设置已更新，请重新加载。" }, 409),
      );
    vi.stubGlobal("fetch", fetch);
    const { profileAdminApi } = await import("./profile-api");
    await expect(
      profileAdminApi.updateAvatar({
        id: "a",
        media_id: "m",
        name: "猫",
        url: "/m",
        sort_order: 5,
        enabled: false,
        version: 3,
      }),
    ).rejects.toMatchObject({ status: 409 });
    expect(JSON.parse(fetch.mock.calls[0][1].body)).toEqual({
      name: "猫",
      sort_order: 5,
      enabled: false,
      version: 3,
    });
  });
  it("审核动作使用独立管理接口并保留审核说明", async () => {
    const fetch = vi.fn().mockResolvedValue(response({ data: { ok: true } }));
    vi.stubGlobal("fetch", fetch);
    const { profileAdminApi } = await import("./profile-api");
    await profileAdminApi.reviewNickname("r/1", "rejected", "请调整昵称");
    expect(fetch.mock.calls[0][0]).toBe(
      "/api/v1/admin/nickname-requests/r%2F1/review",
    );
    expect(JSON.parse(fetch.mock.calls[0][1].body)).toEqual({
      decision: "rejected",
      note: "请调整昵称",
    });
  });
  it("已审核申请不再显示再次审核按钮，所有昵称内容转义", () => {
    const html = renderToStaticMarkup(
      <NicknameReviewCard
        item={{
          id: "r",
          user_id: "u",
          current_nickname: "原昵称",
          nickname: "<script>名字</script>",
          status: "approved",
          review_note: "合适",
          created_at: "2026-11-13T00:00:00Z",
          reviewed_at: "2026-11-13T01:00:00Z",
        }}
        disabled={false}
        onReview={() => {}}
      />,
    );
    expect(html).toContain("已通过");
    expect(html).toContain("&lt;script&gt;");
    expect(html).not.toContain("<script>");
    expect(html).not.toContain("<button");
  });
});
