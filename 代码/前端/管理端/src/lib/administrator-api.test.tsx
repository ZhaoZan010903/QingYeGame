import { afterEach, describe, expect, it, vi } from "vitest";
import { renderToStaticMarkup } from "react-dom/server";
import {
  AdministratorAvatarPicker,
  AdministratorCard,
} from "../components/AdminAdministrators";
import { MemberAccountCard } from "../components/AdminOperations";
import {
  administratorApi,
  administratorFailure,
  administratorLoginError,
  administratorNameError,
  administratorPasswordError,
  type Administrator,
} from "./administrator-api";
import { adminApi } from "./admin-api";
import { ApiError } from "./api";

afterEach(() => vi.unstubAllGlobals());
const response = (data: unknown, status = 200) =>
  new Response(JSON.stringify(data), {
    status,
    headers: { "Content-Type": "application/json" },
  });
const administrator: Administrator = {
  id: "admin/1",
  login: "admin.name",
  nickname: "管理员甲",
  avatar_id: "",
  avatar_url: "",
  enabled: true,
  version: 4,
  created_at: "",
  updated_at: "",
};

describe("管理员独立维护", () => {
  it("会员列表固定请求member角色并保留完整手机号，不能把管理员混入", async () => {
    const fetch = vi
      .fn()
      .mockResolvedValue(
        response({ data: [{ id: "u", phone: "13800138000" }], total: 1 }),
      );
    vi.stubGlobal("fetch", fetch);
    const params = new URLSearchParams({ q: "小叶", role: "admin" });
    const signal = new AbortController().signal;
    const result = await adminApi.users(params, signal);
    const url = new URL(fetch.mock.calls[0][0], "https://example.test");
    expect(url.searchParams.get("role")).toBe("member");
    expect(params.get("role")).toBe("admin");
    expect(fetch.mock.calls[0][1].signal).toBe(signal);
    expect(result.items[0].phone).toBe("13800138000");
  });
  it("独立管理员查询传递游标和取消信号", async () => {
    const fetch = vi
      .fn()
      .mockResolvedValue(
        response({ data: [administrator], total: 25, next_cursor: "next" }),
      );
    vi.stubGlobal("fetch", fetch);
    const signal = new AbortController().signal;
    const result = await administratorApi.list("a&b", "cursor&1", signal);
    const url = new URL(fetch.mock.calls[0][0], "https://example.test");
    expect(url.pathname).toBe("/api/v1/admin/administrators");
    expect(url.searchParams.get("q")).toBe("a&b");
    expect(url.searchParams.get("cursor")).toBe("cursor&1");
    expect(fetch.mock.calls[0][1].signal).toBe(signal);
    expect(result).toEqual({
      items: [administrator],
      total: 25,
      nextCursor: "next",
    });
  });
  it("创建重试保留调用者的请求号和密码空格，不进入URL", async () => {
    const fetch = vi
      .fn()
      .mockRejectedValueOnce(new Error("response lost"))
      .mockResolvedValue(response({ data: administrator }));
    vi.stubGlobal("fetch", fetch);
    const input = {
      login: "admin.new",
      nickname: "新管理员",
      password: " long-password ",
      avatar_id: "media-avatar",
      request_id: "request-1",
    };
    await expect(administratorApi.create(input)).rejects.toMatchObject({
      status: 0,
    });
    await administratorApi.create(input);
    expect(fetch.mock.calls[0][1].body).toBe(fetch.mock.calls[1][1].body);
    expect(JSON.parse(fetch.mock.calls[1][1].body)).toEqual(input);
    expect(fetch.mock.calls[1][0]).toBe("/api/v1/admin/administrators");
  });
  it("编辑只发送明确修改的字段，默认头像用空字符串", async () => {
    const fetch = vi.fn().mockResolvedValue(response({ data: administrator }));
    vi.stubGlobal("fetch", fetch);
    await administratorApi.update("admin/1", {
      expected_version: 4,
      avatar_id: "",
    });
    expect(fetch.mock.calls[0][0]).toBe(
      "/api/v1/admin/administrators/admin%2F1",
    );
    expect(fetch.mock.calls[0][1].method).toBe("PATCH");
    expect(JSON.parse(fetch.mock.calls[0][1].body)).toEqual({
      expected_version: 4,
      avatar_id: "",
    });
  });
  it("改密保留所见版本和重登标志", async () => {
    const fetch = vi
      .fn()
      .mockResolvedValue(
        response({ data: { id: "admin/1", version: 5, reauthenticate: true } }),
      );
    vi.stubGlobal("fetch", fetch);
    const result = await administratorApi.resetPassword("admin/1", {
      expected_version: 4,
      password: "password-123456",
      request_id: "reset-1",
    });
    expect(fetch.mock.calls[0][0]).toBe(
      "/api/v1/admin/administrators/admin%2F1/password",
    );
    expect(JSON.parse(fetch.mock.calls[0][1].body).expected_version).toBe(4);
    expect(result.reauthenticate).toBe(true);
  });
  it("版本冲突不自行读取新版本覆盖", async () => {
    const fetch = vi
      .fn()
      .mockResolvedValue(
        response({ code: "VERSION_CONFLICT", message: "资料已更新" }, 409),
      );
    vi.stubGlobal("fetch", fetch);
    await expect(
      administratorApi.update("admin/1", {
        expected_version: 4,
        enabled: false,
      }),
    ).rejects.toMatchObject({ code: "VERSION_CONFLICT" });
    expect(fetch).toHaveBeenCalledTimes(1);
  });
  it("密码长度按UTF8字节校验，保留空格并检查确认值", () => {
    expect(administratorPasswordError("中".repeat(4), "中".repeat(4))).toBe("");
    expect(administratorPasswordError("中".repeat(24), "中".repeat(24))).toBe(
      "",
    );
    expect(
      administratorPasswordError("中".repeat(25), "中".repeat(25)),
    ).toContain("12–72");
    expect(
      administratorPasswordError("a".repeat(11), "a".repeat(11)),
    ).toContain("12–72");
    expect(
      administratorPasswordError(" password123 ", "password123"),
    ).toContain("不一致");
  });
  it("新登录账号字符与长度边界独立于旧账号，名称计Unicode字符", () => {
    expect(administratorLoginError("a12345")).toBe("");
    expect(administratorLoginError("13800138000")).toBe("");
    expect(administratorLoginError(".admin")).not.toBe("");
    expect(administratorLoginError("管理员123456")).not.toBe("");
    expect(administratorLoginError("a".repeat(21))).not.toBe("");
    expect(administratorNameError("🌿".repeat(30))).toBe("");
    expect(administratorNameError("🌿".repeat(31))).not.toBe("");
    expect(administratorNameError("   ")).not.toBe("");
  });
  it("结果不明、版本冲突和明确拒绝分别处理", () => {
    expect(administratorFailure(new ApiError("网络", 0, "NETWORK_ERROR"))).toBe(
      "unknown",
    );
    expect(
      administratorFailure(new ApiError("坏响应", 200, "INVALID_RESPONSE")),
    ).toBe("unknown");
    expect(administratorFailure(new ApiError("未知", 503))).toBe("unknown");
    expect(
      administratorFailure(new ApiError("版本", 409, "VERSION_CONFLICT")),
    ).toBe("refresh");
    expect(
      administratorFailure(new ApiError("格式", 422, "INVALID_PASSWORD")),
    ).toBe("rejected");
  });
  it("头像选择只显示启用素材且以media_id匹配，保留默认头像", () => {
    const html = renderToStaticMarkup(
      <AdministratorAvatarPicker
        items={[
          {
            id: "library-id",
            media_id: "media-id",
            name: "启用头像",
            url: "/media/one",
            enabled: true,
            version: 1,
            sort_order: 0,
          },
          {
            id: "hidden",
            media_id: "hidden-media",
            name: "停用头像",
            url: "/media/two",
            enabled: false,
            version: 1,
            sort_order: 1,
          },
        ]}
        value="media-id"
        onChange={() => {}}
      />,
    );
    expect(html).toContain("默认头像");
    expect(html).toContain("启用头像");
    expect(html).not.toContain("停用头像");
    expect(html).toContain('aria-pressed="true"');
    expect(html).not.toContain('type="file"');
  });
  it("管理员条目显示完整登录账号和本人标识，没有权限分级或会员入口", () => {
    const html = renderToStaticMarkup(
      <AdministratorCard
        item={{ ...administrator, nickname: "<script>管理员</script>" }}
        current
        disabled={false}
        onEdit={() => {}}
        onPassword={() => {}}
      />,
    );
    expect(html).toContain("admin.name");
    expect(html).toContain("当前登录账号");
    expect(html).toContain("重置密码");
    expect(html).toContain("&lt;script&gt;");
    expect(html).not.toContain("<script>");
    expect(html).not.toContain("超级管理员");
    expect(html).not.toContain("上传权限");
  });
  it("会员卡显示完整手机号，缺失不伪造号码", () => {
    const props = {
      disabled: false,
      onToggle: () => {},
      onRestriction: () => {},
    };
    const html = renderToStaticMarkup(
      <MemberAccountCard
        {...props}
        member={{
          nickname: "会员",
          phone: "13800138000",
          phone_masked: "138****8000",
          enabled: true,
        }}
      />,
    );
    expect(html).toContain("13800138000");
    expect(html).not.toContain("138****8000");
    expect(
      renderToStaticMarkup(
        <MemberAccountCard
          {...props}
          member={{ nickname: "会员", enabled: true }}
        />,
      ),
    ).toContain("未填写手机号");
  });
});
