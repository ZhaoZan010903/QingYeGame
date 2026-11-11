import { afterEach, describe, expect, it, vi } from "vitest";
import { adminImageApi } from "../lib/admin-api";
import { runOrderedUploads } from "../../../共享/ordered-uploads";

afterEach(() => vi.unstubAllGlobals());
describe("管理端保序上传适配器", () => {
  it("后选先完成仍按选择顺序追加，并且每次使用最新版本与独立幂等ID", async () => {
    let version = 1;
    const writes: {
      media_ids: string[];
      expected_version: number;
      request_id: string;
    }[] = [];
    vi.stubGlobal(
      "fetch",
      vi.fn(async (url: string, init: RequestInit) => {
        expect(url).toMatch(/^\/api\/v1\/admin\/products\/admin-draft/);
        if (init.method === "POST") {
          writes.push(JSON.parse(String(init.body)));
          version++;
        }
        return new Response(
          JSON.stringify({
            data: {
              id: "admin-draft",
              title: "草稿",
              version,
              status: "draft",
            },
          }),
          { status: 200 },
        );
      }),
    );
    let release!: () => void;
    const first = new Promise<void>((resolve) => {
      release = resolve;
    });
    let current = await adminImageApi.myProduct("admin-draft");
    const completed: string[] = [];
    const running = runOrderedUploads(
      ["first", "second"],
      2,
      async (id) => {
        if (id === "first") await first;
        completed.push(id);
        return id;
      },
      async (_, id) => {
        current = await adminImageApi.appendImages(
          current.id,
          [id],
          current.version,
        );
      },
      (item, error) => {
        throw new Error(item + ": " + error);
      },
    );
    await vi.waitFor(() => expect(completed).toEqual(["second"]));
    expect(writes).toEqual([]);
    release();
    await running;
    expect(writes.map((body) => body.media_ids)).toEqual([
      ["first"],
      ["second"],
    ]);
    expect(writes.map((body) => body.expected_version)).toEqual([1, 2]);
    expect(new Set(writes.map((body) => body.request_id)).size).toBe(2);
    expect(current.version).toBe(3);
  });
});
