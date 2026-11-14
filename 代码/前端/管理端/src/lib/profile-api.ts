import { request } from "./api";
import type {
  AvatarLibraryItem,
  NicknameRequest,
} from "../../../共享/profile-types";

export interface AdminNicknameRequest extends NicknameRequest {
  user_id: string;
  current_nickname?: string;
  phone_masked?: string;
}
export const profileAdminApi = {
  async nicknameRequests(
    status: string,
    page: number,
  ): Promise<{
    data: AdminNicknameRequest[];
    pagination: { page: number; limit: number; total: number };
  }> {
    return (await request(
      `/admin/nickname-requests?${new URLSearchParams({ status, page: String(page), limit: "20" })}`,
    )) as any;
  },
  async reviewNickname(
    id: string,
    decision: "approved" | "rejected",
    note: string,
  ): Promise<void> {
    await request(
      `/admin/nickname-requests/${encodeURIComponent(id)}/review`,
      "POST",
      { decision, note },
    );
  },
  async avatars(signal?: AbortSignal): Promise<AvatarLibraryItem[]> {
    return (await request("/admin/avatar-library", "GET", undefined, signal))
      .data;
  },
  async createAvatar(input: {
    name: string;
    media_id: string;
    sort_order: number;
    enabled: boolean;
  }): Promise<void> {
    await request("/admin/avatar-library", "POST", input);
  },
  async updateAvatar(item: AvatarLibraryItem): Promise<void> {
    await request(
      `/admin/avatar-library/${encodeURIComponent(item.id)}`,
      "PATCH",
      {
        name: item.name,
        sort_order: item.sort_order,
        enabled: item.enabled,
        version: item.version,
      },
    );
  },
};
