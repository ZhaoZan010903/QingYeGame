import { afterEach, describe, expect, it, vi } from "vitest";
import { chatApi, chatTransport } from "./chat-api";
import {
  mergeMessages,
  nextChatPosition,
  type ChatMessage,
} from "../../../共享/chat-types";
afterEach(() => {
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});
const message = (id: string, body = id): ChatMessage => ({
  id,
  body,
  kind: "text",
  mine: false,
  sender_role: "member",
  created_at: "2026-11-11T00:00:00Z",
  client_id: "",
});
describe("聊天消息和接口边界", () => {
  it("会话续页在实时连接中传游标，仍兼容旧偏移参数", async () => {
    vi.spyOn(chatTransport, "isConnected").mockReturnValue(true);
    const command = vi
      .spyOn(chatTransport, "command")
      .mockResolvedValue({ data: [] });
    await chatApi.list("opaque-cursor");
    expect(command).toHaveBeenLastCalledWith("list", "", {
      cursor: "opaque-cursor",
    });
    await chatApi.list(50);
    expect(command).toHaveBeenLastCalledWith("list", "", { offset: 50 });
  });
  it("HTTP 查询编码游标，终页不回退旧偏移值", async () => {
    vi.spyOn(chatTransport, "isConnected").mockReturnValue(false);
    const fetch = vi
      .fn()
      .mockResolvedValue(
        new Response(
          JSON.stringify({
            data: [],
            total: 0,
            unread: 0,
            next_offset: null,
            next_cursor: null,
          }),
        ),
      );
    vi.stubGlobal("fetch", fetch);
    await chatApi.list("cursor&offset=99");
    expect(String(fetch.mock.calls[0][0])).toContain(
      "cursor=cursor%26offset%3D99",
    );
    const page = { data: [], total: 100, unread: 0, next_offset: 50 };
    expect(nextChatPosition(page)).toBe(50);
    expect(nextChatPosition({ ...page, next_cursor: "next" })).toBe("next");
    expect(nextChatPosition({ ...page, next_cursor: null })).toBeNull();
  });
  it("连接就绪后，消息正文通过 WebTransport 读取", async () => {
    vi.spyOn(chatTransport, "isConnected").mockReturnValue(true);
    const command = vi
      .spyOn(chatTransport, "command")
      .mockResolvedValue({ data: [] });
    const fetch = vi.fn();
    vi.stubGlobal("fetch", fetch);
    await chatApi.messages("owned", { after: "9007199254740993" });
    expect(command).toHaveBeenCalledWith("messages", "owned", {
      after: "9007199254740993",
    });
    expect(fetch).not.toHaveBeenCalled();
  });
  it("断线补页去重并保持 bigint ID 顺序，不损失精度", () => {
    const result = mergeMessages(
      [message("9007199254740993"), message("9007199254740992")],
      [message("9007199254740994"), message("9007199254740993", "重试更新")],
    );
    expect(result.map((item) => item.id)).toEqual([
      "9007199254740992",
      "9007199254740993",
      "9007199254740994",
    ]);
    expect(result[1].body).toBe("重试更新");
  });
  it("向前补页和历史分页组合后不漏消息", () => {
    const older = Array.from({ length: 50 }, (_, i) => message(String(i + 1)));
    const newer = Array.from({ length: 51 }, (_, i) => message(String(i + 50)));
    expect(mergeMessages(older, newer)).toHaveLength(100);
  });
  it("发送仅传内容和重试标识，身份由会话决定", async () => {
    const command = vi
      .spyOn(chatTransport, "command")
      .mockResolvedValue({ data: { id: "7" } });
    const fetch = vi.fn();
    vi.stubGlobal("fetch", fetch);
    await chatApi.send(
      "conversation-1",
      "<script>plain text</script>",
      "retry-1",
    );
    expect(command).toHaveBeenCalledWith("send", "conversation-1", {
      body: "<script>plain text</script>",
      client_id: "retry-1",
    });
    expect(fetch).not.toHaveBeenCalled();
  });
  it("其他会员的会话被拒绝时不退回演示消息", async () => {
    vi.stubGlobal(
      "fetch",
      vi
        .fn()
        .mockResolvedValue(
          new Response(
            JSON.stringify({ message: "会话不存在或不属于当前账户" }),
            { status: 404 },
          ),
        ),
    );
    await expect(chatApi.messages("not-owned")).rejects.toMatchObject({
      status: 404,
    });
  });
});
