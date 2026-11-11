import { request } from './api';
import { ChatTransport, type TransportTicket } from '../../../共享/chat-transport';
export const chatTransport = new ChatTransport();
import type { ChatList, Conversation, MessagePage, ChatPresence } from '../../../共享/chat-types';
export const chatAdmin = false;
export const chatRoute = '/messages';
const prefix = '/chat';

export const chatApi = {
  async ticket(): Promise<TransportTicket> {
    const r = await request(`${prefix}/transport-ticket`, 'POST', {});
    return r.data as TransportTicket;
  },
  async list(offset = 0): Promise<ChatList> {
    if (chatTransport.isConnected()) return chatTransport.command('list', '', { offset });
    return (await request(`${prefix}/conversations?offset=${offset}`)) as unknown as ChatList;
  },
  async detail(id: string): Promise<Conversation> {
    if (chatTransport.isConnected())
      return (await chatTransport.command('detail', id)).data as Conversation;
    const r = await request(`${prefix}/conversations/${encodeURIComponent(id)}`);
    return r.data as Conversation;
  },
  async messages(id: string, page: { before?: string; after?: string } = {}): Promise<MessagePage> {
    if (chatTransport.isConnected()) return chatTransport.command('messages', id, page);
    return (await request(
      `${prefix}/conversations/${encodeURIComponent(id)}/messages?${new URLSearchParams(page)}`,
    )) as unknown as MessagePage;
  },
  async send(id: string, body: string, client_id: string) {
    return chatTransport.command('send', id, { body, client_id });
  },
  async read(id: string, through: string) {
    return chatTransport.command('read', id, { through });
  },
  async close(id: string) {
    return chatTransport.command('close', id);
  },
  async start(product_id: string): Promise<string> {
    const r = await chatTransport.command('start', '', { product_id });
    return String(r.data?.id);
  },
  async presence(accepting?: boolean, heartbeat = false): Promise<ChatPresence> {
    const r =
      heartbeat || accepting !== undefined
        ? await chatTransport.command('presence', '', accepting !== undefined ? { accepting } : {})
        : await request(`${prefix}/presence`);
    return r.data as ChatPresence;
  },
};
