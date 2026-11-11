export interface ChatProduct { id: string; code: string; title: string; price: string | null; unit: string; cover_url: string }
export interface Conversation { id: string; status: 'waiting' | 'active' | 'closed'; product: ChatProduct; member_name: string; admin_name: string; created_at: string; updated_at: string; last_message: string; last_message_id: string; last_message_mine: boolean; unread: number }
export interface ChatMessage { id: string; body: string; kind: 'text' | 'product'; mine: boolean; sender_role: 'admin' | 'member'; created_at: string; client_id: string }
export interface ChatList { data: Conversation[]; unread: number; total: number; next_offset: number | null }
export interface MessagePage { data: ChatMessage[]; before: string | null; after: string | null }
export interface ChatPresence { accepting: boolean; online: boolean; active: number; capacity: number; waiting: number }
/** IDs are bigint strings, never lossy JS numbers. */
export function mergeMessages(previous: ChatMessage[], incoming: ChatMessage[]): ChatMessage[] {
  const items = new Map(previous.map(item => [item.id, item]));
  incoming.forEach(item => items.set(item.id, item));
  return [...items.values()].sort((a,b) => BigInt(a.id) < BigInt(b.id) ? -1 : BigInt(a.id) > BigInt(b.id) ? 1 : 0);
}
