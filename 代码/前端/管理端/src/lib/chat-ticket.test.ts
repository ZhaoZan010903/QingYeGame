import { afterEach, describe, expect, it, vi } from "vitest";
import { chatApi } from "./chat-api";

afterEach(() => vi.unstubAllGlobals());
describe("管理端聊天实际票据请求", () => {
  it.each(["webtransport", "websocket"] as const)(
    "将 %s 传到管理接口",
    async (transport) => {
      const fetch = vi
        .fn()
        .mockResolvedValue(
          new Response(
            JSON.stringify({
              data: { url: "/api/v1/admin/chat/socket", ticket: "one-use" },
            }),
          ),
        );
      vi.stubGlobal("fetch", fetch);
      await chatApi.ticket(transport);
      const [path, options] = fetch.mock.calls[0];
      expect(path).toBe("/api/v1/admin/chat/transport-ticket");
      expect(JSON.parse(options.body)).toEqual({ transport });
      expect(options.credentials).toBe("include");
    },
  );
});
