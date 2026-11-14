import { ApiError, request } from "./api";

export interface Administrator {
  id: string;
  login: string;
  nickname: string;
  avatar_id: string;
  avatar_url: string;
  enabled: boolean;
  version: number;
  created_at: string;
  updated_at: string;
}
export interface CreateAdministrator {
  login: string;
  nickname: string;
  password: string;
  avatar_id: string;
  request_id: string;
}
export interface UpdateAdministrator {
  expected_version: number;
  nickname?: string;
  avatar_id?: string;
  enabled?: boolean;
}
export interface ResetAdministratorPassword {
  expected_version: number;
  password: string;
  request_id: string;
}
const path = (id: string) => `/admin/administrators/${encodeURIComponent(id)}`;
export const administratorApi = {
  async list(query: string, cursor: string | null, signal?: AbortSignal) {
    const params = new URLSearchParams({ q: query, limit: "20" });
    if (cursor) params.set("cursor", cursor);
    const response = await request(
      `/admin/administrators?${params}`,
      "GET",
      undefined,
      signal,
    );
    return {
      items: (response.data ?? []) as Administrator[],
      total: response.total ?? 0,
      nextCursor: response.next_cursor ?? null,
    };
  },
  async create(input: CreateAdministrator): Promise<Administrator> {
    return (await request("/admin/administrators", "POST", input)).data;
  },
  async update(id: string, input: UpdateAdministrator): Promise<Administrator> {
    return (await request(path(id), "PATCH", input)).data;
  },
  async resetPassword(
    id: string,
    input: ResetAdministratorPassword,
  ): Promise<{ id: string; version: number; reauthenticate: boolean }> {
    return (await request(`${path(id)}/password`, "POST", input)).data;
  },
};

export function administratorLoginError(login: string): string {
  return /^[A-Za-z0-9][A-Za-z0-9._-]{5,19}$/.test(login)
    ? ""
    : "登录账号需为 6–20 位字母、数字、点、下划线或短横线，并以字母或数字开头。";
}
export function administratorNameError(nickname: string): string {
  const length = [...nickname.trim()].length;
  return length >= 1 && length <= 30 ? "" : "管理员名称需为 1–30 个字符。";
}
export function administratorPasswordError(
  password: string,
  confirmation: string,
): string {
  const length = new TextEncoder().encode(password).byteLength;
  if (length < 12 || length > 72)
    return "密码需为 12–72 字节，中文及表情会占用多个字节。";
  return password === confirmation ? "" : "两次输入的密码不一致。";
}
export function administratorFailure(
  error: unknown,
): "unknown" | "refresh" | "rejected" {
  if (
    !(error instanceof ApiError) ||
    error.status === 0 ||
    error.status >= 500 ||
    error.code === "INVALID_RESPONSE"
  )
    return "unknown";
  if (error.status === 409 || error.status === 404) return "refresh";
  return "rejected";
}
