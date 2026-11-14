import { afterEach, describe, expect, it, vi } from 'vitest';
vi.unmock('../../../共享/response-crypto');
import { responseCrypto } from '../../../共享/response-crypto';
import { fixtureInit, fixtureChat } from '../../../共享/response-crypto-fixture';
import { ChatTransport, type TransportKind } from '../../../共享/chat-transport';
const clients: ChatTransport[] = [];
afterEach(() => {
  clients.forEach((client) => client.disconnect());
  clients.length = 0;
  responseCrypto.reset();
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
});
function harness(kind: TransportKind) {
  let incoming: ReadableStreamDefaultController<Uint8Array> | undefined;
  const outgoing: string[] = [];
  class WT {
    ready =
      kind === 'webtransport' ? Promise.resolve() : Promise.reject(new Error('QUIC unavailable'));
    closed = new Promise(() => {});
    async createBidirectionalStream() {
      return {
        readable: new ReadableStream<Uint8Array>({
          start(controller) {
            incoming = controller;
          },
        }),
        writable: new WritableStream<Uint8Array>({
          write(value) {
            outgoing.push(new TextDecoder().decode(value));
          },
        }),
      };
    }
    close() {
      try {
        incoming?.close();
      } catch {}
    }
  }
  let socket: WS | undefined;
  class WS {
    static OPEN = 1;
    readyState = 1;
    bufferedAmount = 0;
    onopen: (() => void) | null = null;
    onmessage: ((event: { data: string }) => void) | null = null;
    onerror: (() => void) | null = null;
    onclose: (() => void) | null = null;
    constructor(_url: string) {
      socket = this;
      queueMicrotask(() => this.onopen?.());
    }
    send(value: string) {
      outgoing.push(value);
    }
    close() {
      this.readyState = 3;
    }
  }
  vi.stubGlobal('isSecureContext', true);
  vi.stubGlobal('location', new URL('https://qingye.test'));
  vi.stubGlobal('WebTransport', WT);
  vi.stubGlobal('WebSocket', WS);
  vi.stubGlobal('fetch', async () => fixtureInit());
  const client = new ChatTransport();
  clients.push(client);
  const update = vi.fn(),
    state = vi.fn();
  client.connect(
    async () => ({
      url: 'https://qingye.test/api/v1/chat/socket',
      ticket: 'one-use',
      response_crypto: { enabled: true, context_id: 'context-1', channel_id: 'channel-1' },
    }),
    update,
    state,
  );
  return {
    client,
    update,
    state,
    outgoing,
    open: () => vi.waitFor(() => expect(kind === 'webtransport' ? incoming : socket).toBeTruthy()),
    frame: (value: unknown) => {
      const raw = JSON.stringify(value) + '\n';
      if (kind === 'webtransport') incoming!.enqueue(new TextEncoder().encode(raw));
      else socket!.onmessage?.({ data: raw });
    },
  };
}
describe('R24 实际实时通道解密', () => {
  it.each<TransportKind>(['webtransport', 'websocket'])(
    '%s的ready/result/update均按到达顺序解密',
    async (kind) => {
      const h = harness(kind);
      await h.open();
      h.frame(await fixtureChat({ type: 'ready' }, { frameId: '1'.repeat(32) }));
      h.frame(await fixtureChat({ type: 'update' }, { frameId: '2'.repeat(32) }));
      await vi.waitFor(() => expect(h.client.isConnected()).toBe(true));
      await vi.waitFor(() => expect(h.update).toHaveBeenCalledTimes(2));
      const result = h.client.command('send', 'conversation', {
        body: '中文',
        client_id: 'stable-business-intent',
      });
      await vi.waitFor(() =>
        expect(h.outgoing.some((value) => value.includes('"op":"send"'))).toBe(true),
      );
      const command = JSON.parse(h.outgoing.find((value) => value.includes('"op":"send"'))!);
      h.frame(
        await fixtureChat(
          { type: 'result', id: command.id, status: 200, body: { data: { id: 'message-1' } } },
          { frameId: '3'.repeat(32) },
        ),
      );
      expect(await result).toEqual({ data: { id: 'message-1' } });
      expect(command.payload.client_id).toBe('stable-business-intent');
    },
  );
  it('加密通道收到明文关闭连接，不自动重新发送待确认消息', async () => {
    const h = harness('websocket');
    await h.open();
    h.frame(await fixtureChat({ type: 'ready' }));
    await vi.waitFor(() => expect(h.client.isConnected()).toBe(true));
    const pending = h.client.command('send', 'conversation', {
      body: 'submitted',
      client_id: 'same-id',
    });
    const rejected = expect(pending).rejects.toThrow('可能已送达');
    await vi.waitFor(() => expect(h.outgoing).toHaveLength(1));
    h.frame({ type: 'result', id: '1', status: 200, body: { data: 'forged' } });
    await rejected;
    expect(h.client.isConnected()).toBe(false);
    expect(h.outgoing).toHaveLength(1);
  });
});
