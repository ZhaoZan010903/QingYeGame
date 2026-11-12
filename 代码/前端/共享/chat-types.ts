export interface ChatProduct {
  id: string;
  code: string;
  title: string;
  price: string | null;
  unit: string;
  cover_url: string;
  trade_type?: "sale" | "rent";
  category_name?: string;
  server?: string;
}
export interface Conversation {
  id: string;
  status: "waiting" | "active" | "closed";
  product: ChatProduct;
  product_count?: number;
  member_name: string;
  admin_name: string;
  member_avatar?: string;
  admin_avatar?: string;
  created_at: string;
  updated_at: string;
  last_message: string;
  last_message_id: string;
  last_message_mine: boolean;
  unread: number;
}
/** 商品卡片保存发送时的公开快照，切换咨询商品不会改写历史卡片。 */
export interface ChatImage {
  id: string;
  url: string;
  thumbnail_url: string;
}
export interface ChatMessage {
  id: string;
  body: string;
  kind: "text" | "product" | "image";
  images?: ChatImage[];
  sender_name?: string;
  avatar_url?: string;
  product?: ChatProduct | null;
  mine: boolean;
  sender_role: "admin" | "member";
  created_at: string;
  client_id: string;
}
export interface ChatList {
  data: Conversation[];
  unread: number;
  total: number;
  next_offset: number | null;
  next_cursor?: string | null;
}
/** Old servers return offsets; current servers return an opaque conversation cursor. */
export type ChatListPosition = string | number | null;
export function nextChatPosition(page: ChatList): ChatListPosition {
  return page.next_cursor !== undefined ? page.next_cursor : page.next_offset;
}
export function chatListPayload(
  position: Exclude<ChatListPosition, null> = 0,
): { cursor: string } | { offset: number } {
  return typeof position === "string"
    ? { cursor: position }
    : { offset: position };
}
export interface MessagePage {
  data: ChatMessage[];
  before: string | null;
  after: string | null;
}
export interface ChatPresence {
  accepting: boolean;
  online: boolean;
  active: number;
  capacity: number;
  waiting: number;
}
/** IDs are bigint strings, never lossy JS numbers. */
export function mergeMessages(
  previous: ChatMessage[],
  incoming: ChatMessage[],
): ChatMessage[] {
  const items = new Map(previous.map((item) => [item.id, item]));
  incoming.forEach((item) => items.set(item.id, item));
  return [...items.values()].sort((a, b) =>
    BigInt(a.id) < BigInt(b.id) ? -1 : BigInt(a.id) > BigInt(b.id) ? 1 : 0,
  );
}
