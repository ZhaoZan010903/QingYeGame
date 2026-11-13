export interface AvatarLibraryItem {
  id: string;
  name: string;
  media_id: string;
  url: string;
  sort_order: number;
  enabled: boolean;
  version: number;
}
export interface NicknameRequest {
  id: string;
  nickname: string;
  status: "pending" | "approved" | "rejected";
  review_note: string;
  created_at: string;
  reviewed_at: string | null;
}
export interface MemberProfile {
  nickname: string;
  nickname_request: NicknameRequest | null;
  next_nickname_change_at: string | null;
  can_request_nickname: boolean;
}
export function profileDate(value: string | null | undefined): string {
  if (!value) return "";
  const date = new Date(value);
  return Number.isNaN(date.getTime())
    ? ""
    : date.toLocaleString("zh-CN", {
        timeZone: "Asia/Shanghai",
        year: "numeric",
        month: "2-digit",
        day: "2-digit",
        hour: "2-digit",
        minute: "2-digit",
        hour12: false,
      });
}
