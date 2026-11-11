import { afterEach, describe, expect, it, vi } from 'vitest';
import { ChatTransport, type TransportKind } from '../../../共享/chat-transport';
const clients: ChatTransport[] = [];
afterEach(() => {
  clients.forEach((c) => c.disconnect());
  clients.length = 0;
  vi.unstubAllGlobals();
  vi.useRealTimers();
});
function setup(wt: 'failed' | 'pending' = 'failed') {
  vi.useFakeTimers();
  const wtClose = vi.fn();
  class FakeWT {
    ready =
      wt === 'failed' ? Promise.reject(new Error('proxy blocked QUIC')) : new Promise(() => {});
    closed = new Promise(() => {});
    close = wtClose;
  }
  class Socket {
    static OPEN = 1;
    static instances: Socket[] = [];
    readyState = 0;
    bufferedAmount = 0;
    onopen: (() => void) | null = null;
    onmessage: ((event: { data: string }) => void) | null = null;
    onerror: (() => void) | null = null;
    onclose: (() => void) | null = null;
    close = vi.fn(() => {
      this.readyState = 3;
    });
    sent: string[] = [];
    constructor(public url: string) {
      Socket.instances.push(this);
    }
    send(value: string) {
      this.sent.push(value);
    }
    open() {
      this.readyState = 1;
      this.onopen?.();
      this.frame({ type: 'ready' });
    }
    frame(data: unknown) {
      this.onmessage?.({ data: JSON.stringify(data) + '\n' });
    }
  }
  vi.stubGlobal('isSecureContext', true);
  vi.stubGlobal('location', new URL('https://qingye.test/messages'));
  vi.stubGlobal('WebTransport', FakeWT);
  vi.stubGlobal('WebSocket', Socket);
  const client = new ChatTransport();
  clients.push(client);
  const state = vi.fn(),
    update = vi.fn();
  const tickets = vi.fn(async (kind: TransportKind) => ({
    url: kind === 'webtransport' ? 'https://qingye.test/chat' : '/api/v1/chat/socket',
    ticket: `${kind}-new-ticket`,
  }));
  return {
    client,
    state,
    update,
    tickets,
    Socket,
    wtClose,
    connect: () => client.connect(tickets, update, state),
  };
}
describe('proxy-compatible chat', () => {
  it('closes failed QUIC, obtains a new transport-bound ticket and uses same-origin WSS', async () => {
    const f = setup();
    f.connect();
    await vi.advanceTimersByTimeAsync(0);
    expect(f.tickets.mock.calls.map((call) => call[0])).toEqual(['webtransport', 'websocket']);
    expect(f.wtClose).toHaveBeenCalledTimes(1);
    const socket = f.Socket.instances[0];
    expect(socket.url).toBe('wss://qingye.test/api/v1/chat/socket?ticket=websocket-new-ticket');
    socket.open();
    expect(f.client.isConnected()).toBe(true);
    const result = f.client.command('send', 'owned', { body: '中文', client_id: 'stable-key' });
    await vi.advanceTimersByTimeAsync(0);
    const command = JSON.parse(socket.sent[0]);
    expect(command.payload.client_id).toBe('stable-key');
    socket.frame({
      type: 'result',
      id: command.id,
      status: 200,
      body: { data: { id: '9007199254740993' } },
    });
    await expect(result).resolves.toEqual({ data: { id: '9007199254740993' } });
    socket.frame({ type: 'update' });
    expect(f.update).toHaveBeenCalledTimes(2);
  });
  it('falls back after the QUIC handshake deadline rather than retrying it forever', async () => {
    const f = setup('pending');
    f.connect();
    await vi.advanceTimersByTimeAsync(4499);
    expect(f.Socket.instances).toHaveLength(0);
    await vi.advanceTimersByTimeAsync(1);
    expect(f.Socket.instances).toHaveLength(1);
    expect(f.wtClose).toHaveBeenCalledTimes(1);
    f.Socket.instances[0].open();
    await vi.advanceTimersByTimeAsync(30000);
    expect(f.client.isConnected()).toBe(true);
  });
  it('uses WSS when the browser has no WebTransport', async () => {
    const f = setup();
    delete (globalThis as any).WebTransport;
    f.connect();
    await vi.advanceTimersByTimeAsync(0);
    expect(f.tickets).toHaveBeenCalledExactlyOnceWith('websocket');
    f.Socket.instances[0].open();
    expect(f.client.isConnected()).toBe(true);
  });
  it('does not switch transports to bypass expired authentication', async () => {
    const f = setup();
    f.tickets.mockRejectedValue(Object.assign(new Error('expired'), { status: 401 }));
    f.connect();
    await vi.advanceTimersByTimeAsync(60000);
    expect(f.tickets).toHaveBeenCalledTimes(1);
    expect(f.Socket.instances).toHaveLength(0);
    expect(f.state).toHaveBeenLastCalledWith('unavailable', '登录状态已失效，请重新登录。');
  });
  it('ignores a late ticket after logout and opens no socket', async () => {
    const f = setup();
    let deliver!: (value: any) => void;
    f.tickets.mockImplementation(
      () =>
        new Promise((resolve) => {
          deliver = resolve;
        }),
    );
    f.connect();
    f.client.disconnect();
    deliver({ url: 'https://qingye.test/chat', ticket: 'old' });
    await vi.advanceTimersByTimeAsync(60000);
    expect(f.Socket.instances).toHaveLength(0);
    expect(f.client.isConnected()).toBe(false);
  });
  it('rejects cross-origin socket destinations without exposing tickets', async () => {
    const f = setup();
    f.tickets.mockImplementation(async () => ({
      url: 'https://attacker.invalid/socket',
      ticket: 'secret',
    }));
    f.connect();
    await vi.advanceTimersByTimeAsync(0);
    expect(f.Socket.instances).toHaveLength(0);
    expect(f.client.isConnected()).toBe(false);
  });
  it('reconnects on WSS without parallel sessions or replaying an unacknowledged message', async () => {
    const f = setup();
    f.connect();
    await vi.advanceTimersByTimeAsync(0);
    const first = f.Socket.instances[0];
    first.open();
    const pending = f.client.command('send', 'owned', { body: 'draft', client_id: 'same-key' });
    const rejection = expect(pending).rejects.toThrow('消息可能已送达');
    await vi.advanceTimersByTimeAsync(0);
    first.onclose?.();
    await vi.advanceTimersByTimeAsync(0);
    await rejection;
    expect(f.client.reconnect()).toBe(true);
    expect(f.client.reconnect()).toBe(false);
    await vi.advanceTimersByTimeAsync(0);
    expect(f.tickets.mock.calls.map((call) => call[0])).toEqual([
      'webtransport',
      'websocket',
      'websocket',
    ]);
    const second = f.Socket.instances[1];
    second.open();
    expect(second.sent).toEqual([]);
    f.client.disconnect();
    await vi.advanceTimersByTimeAsync(60000);
    expect(f.Socket.instances).toHaveLength(2);
  });
  it('bounds a stalled socket and rejects oversized incoming frames', async () => {
    const f = setup();
    f.connect();
    await vi.advanceTimersByTimeAsync(0);
    const first = f.Socket.instances[0];
    first.open();
    first.bufferedAmount = 65537;
    const rejection = expect(f.client.command('read', 'owned')).rejects.toThrow('发送失败');
    await vi.advanceTimersByTimeAsync(0);
    await rejection;
    expect(first.sent).toEqual([]);
    first.onmessage?.({ data: 'x'.repeat(1048577) });
    await vi.advanceTimersByTimeAsync(0);
    expect(f.client.isConnected()).toBe(false);
    expect(first.close).toHaveBeenCalledTimes(1);
  });
});
