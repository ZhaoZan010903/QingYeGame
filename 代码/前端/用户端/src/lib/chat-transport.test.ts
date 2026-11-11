import { afterEach, describe, expect, it, vi } from 'vitest';
import { ChatTransport } from '../../../共享/chat-transport';

const tickets = () =>
  Promise.resolve({ url: 'https://localhost:5443/chat', ticket: 'short-lived-ticket' });
const clients: ChatTransport[] = [];
afterEach(() => {
  clients.forEach((c) => c.disconnect());
  clients.length = 0;
  vi.unstubAllGlobals();
  vi.useRealTimers();
});
function fixture() {
  let input: ReadableStreamDefaultController<Uint8Array>;
  const outgoing: string[] = [];
  let finish: () => void = () => {};
  class FakeWebTransport {
    ready = Promise.resolve();
    closed = new Promise<void>((resolve) => {
      finish = resolve;
    });
    async createBidirectionalStream() {
      return {
        readable: new ReadableStream<Uint8Array>({
          start(controller) {
            input = controller;
          },
        }),
        writable: new WritableStream<Uint8Array>({
          write(chunk) {
            outgoing.push(new TextDecoder().decode(chunk));
          },
        }),
      };
    }
    close() {
      try {
        input?.close();
      } catch {}
      finish();
    }
  }
  vi.stubGlobal('isSecureContext', true);
  vi.stubGlobal('WebTransport', FakeWebTransport);
  const client = new ChatTransport();
  clients.push(client);
  const state = vi.fn(),
    update = vi.fn();
  client.connect(tickets, update, state);
  return {
    client,
    state,
    update,
    outgoing,
    frame: (value: unknown) =>
      input.enqueue(new TextEncoder().encode(JSON.stringify(value) + '\n')),
    raw: (value: string) => input.enqueue(new TextEncoder().encode(value)),
  };
}
async function opened(f: ReturnType<typeof fixture>) {
  await vi.waitFor(() => expect(f.outgoing).toContain('\n'));
  f.frame({ type: 'ready' });
  await vi.waitFor(() => expect(f.state).toHaveBeenCalledWith('connected', ''));
}
describe('WebTransport reliable protocol', () => {
  it('stops retrying an expired session instead of displaying a network failure', async () => {
    vi.useFakeTimers();
    vi.stubGlobal('isSecureContext', true);
    vi.stubGlobal('WebTransport', class {});
    const ticket = vi.fn().mockRejectedValue(Object.assign(new Error('expired'), { status: 401 }));
    const state = vi.fn();
    const client = new ChatTransport();
    clients.push(client);
    client.connect(ticket, vi.fn(), state);
    await vi.advanceTimersByTimeAsync(60000);
    expect(state).toHaveBeenLastCalledWith('unavailable', '登录状态已失效，请重新登录。');
    expect(ticket).toHaveBeenCalledTimes(1);
    expect(client.reconnect()).toBe(false);
  });
  it('sends message frames, waits for durable acknowledgement, and accepts UTF-8 split frames', async () => {
    const f = fixture();
    await opened(f);
    const result = f.client.command('send', 'owned', { body: '中文内容', client_id: 'retry-key' });
    await vi.waitFor(() => expect(f.outgoing).toHaveLength(2));
    const command = JSON.parse(f.outgoing[1]);
    expect(command.op).toBe('send');
    expect(command.payload.client_id).toBe('retry-key');
    const raw =
      JSON.stringify({
        type: 'result',
        id: command.id,
        status: 200,
        body: { data: { id: '9007199254740993' } },
      }) + '\n';
    f.raw(raw.slice(0, 15));
    f.raw(raw.slice(15));
    await expect(result).resolves.toEqual({ data: { id: '9007199254740993' } });
    f.frame({ type: 'update' });
    await vi.waitFor(() => expect(f.update).toHaveBeenCalledTimes(2));
  });
  it('does not silently send over HTTP when the connection is unavailable', async () => {
    const client = new ChatTransport();
    clients.push(client);
    const fetch = vi.fn();
    vi.stubGlobal('fetch', fetch);
    await expect(client.command('send', 'id', { body: 'keep draft' })).rejects.toThrow(
      '内容已保留',
    );
    expect(fetch).not.toHaveBeenCalled();
  });
  it('rejects outstanding commands on account switch and ignores later acknowledgements', async () => {
    const f = fixture();
    await opened(f);
    const result = f.client.command('send', 'owned', { body: 'draft' });
    const rejected = expect(result).rejects.toThrow('咨询连接已关闭');
    f.client.disconnect();
    await rejected;
  });
  it('propagates permission failures without pretending to deliver', async () => {
    const f = fixture();
    await opened(f);
    const result = f.client.command('send', 'other', { body: 'x' });
    const rejected = expect(result).rejects.toMatchObject({ status: 404, message: '会话不存在' });
    await vi.waitFor(() => expect(f.outgoing).toHaveLength(2));
    f.frame({
      type: 'result',
      id: JSON.parse(f.outgoing[1]).id,
      status: 404,
      body: { message: '会话不存在' },
    });
    await rejected;
  });
  it('reports unsupported browsers without requesting tickets', () => {
    vi.stubGlobal('isSecureContext', false);
    const client = new ChatTransport();
    clients.push(client);
    const ticket = vi.fn(),
      state = vi.fn();
    client.connect(ticket, vi.fn(), state);
    expect(state.mock.calls[0][0]).toBe('unavailable');
    expect(ticket).not.toHaveBeenCalled();
  });
  it('limits pending commands and rejects oversized frames before transmission', async () => {
    const f = fixture();
    await opened(f);
    await expect(f.client.command('send', 'id', { body: '中'.repeat(6000) })).rejects.toThrow(
      '消息内容过长',
    );
    const pending = Array.from({ length: 16 }, () =>
      f.client.command('read', 'owned', {}).catch(() => {}),
    );
    await expect(f.client.command('read', 'owned', {})).rejects.toThrow('操作较多');
    f.client.disconnect();
    await Promise.all(pending);
  });
});
