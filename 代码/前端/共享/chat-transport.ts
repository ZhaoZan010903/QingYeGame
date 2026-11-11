// One authenticated command protocol, WebTransport first and same-origin WebSocket fallback.
export type TransportKind = "webtransport" | "websocket";
export type TransportTicket = {
  url: string;
  ticket: string;
  certificate_sha256?: string;
};
export type TransportState =
  "connecting" | "connected" | "reconnecting" | "unavailable";
type Frame = {
  type: string;
  id?: string;
  status?: number;
  body?: {
    message?: string;
    error?: { message?: string };
    [key: string]: unknown;
  };
};
type Pending = {
  resolve: (value: any) => void;
  reject: (error: Error) => void;
  timer: ReturnType<typeof setTimeout>;
};
type Writer = { write: (bytes: Uint8Array) => Promise<void> };
const FRAME_LIMIT = 1048576;

export class ChatTransport {
  private writer: Writer | null = null;
  private closeConnection: (() => void) | undefined;
  private pending = new Map<string, Pending>();
  private sequence = 0;
  private generation = 0;
  private retry: ReturnType<typeof setTimeout> | undefined;
  private retryAction: (() => void) | undefined;
  private ready = false;
  private output = Promise.resolve();
  isConnected() {
    return this.ready;
  }

  connect(
    ticket: (kind: TransportKind) => Promise<TransportTicket>,
    update: () => void,
    state: (state: TransportState, message: string) => void,
  ) {
    this.disconnect();
    const generation = this.generation;
    if (
      !globalThis.isSecureContext ||
      (!("WebTransport" in globalThis) && !("WebSocket" in globalThis))
    ) {
      state(
        "unavailable",
        "当前浏览器或访问地址不支持在线咨询，请使用新版浏览器通过安全地址打开。",
      );
      return;
    }
    let preferred: TransportKind =
      "WebTransport" in globalThis ? "webtransport" : "websocket";
    let failures = 0;
    const receive = (frame: Frame) => {
      if (generation !== this.generation) return;
      if (frame.type === "ready" && !this.ready) {
        this.ready = true;
        failures = 0;
        state("connected", "");
        update();
      } else if (frame.type === "update" && this.ready) update();
      else if (frame.type === "result" && frame.id && this.ready) {
        const entry = this.pending.get(frame.id);
        if (!entry) return;
        clearTimeout(entry.timer);
        this.pending.delete(frame.id);
        if ((frame.status || 500) >= 400)
          entry.reject(
            Object.assign(
              new Error(
                frame.body?.message ||
                  frame.body?.error?.message ||
                  "操作未完成，请重试",
              ),
              { status: frame.status },
            ),
          );
        else entry.resolve(frame.body);
      }
    };
    const attempt = async () => {
      if (generation !== this.generation) return;
      state(
        failures ? "reconnecting" : "connecting",
        failures ? "连接中断，正在重连；未发送的内容会保留。" : "正在连接客服…",
      );
      let reason = "连接暂不可用，正在重试；未发送的内容会保留。";
      let stopped = false;
      for (;;) {
        let connectionStarted = false;
        let fallback = false;
        try {
          // Tickets are one-use and bound to a transport. Never reuse the failed QUIC ticket.
          const config = await ticket(preferred);
          if (generation !== this.generation) return;
          connectionStarted = true;
          await this.runChannel(preferred, config, generation, receive);
        } catch (e) {
          const status = (e as { status?: number }).status;
          if (status === 401 || status === 403) {
            stopped = true;
            reason =
              status === 401
                ? "登录状态已失效，请重新登录。"
                : "当前连接未获授权，请刷新页面后重试。";
          } else if (status === 429)
            reason = "连接尝试较频繁，稍后会自动重试。未发送的内容已保留。";
          else if (
            preferred === "webtransport" &&
            "WebSocket" in globalThis &&
            (connectionStarted || status === 503)
          ) {
            preferred = "websocket";
            fallback = true;
          }
        } finally {
          if (generation === this.generation) {
            this.closeConnection?.();
            this.closeConnection = undefined;
            this.ready = false;
            this.writer = null;
            this.output = Promise.resolve();
            this.rejectPending(
              new Error(
                "连接已中断；消息可能已送达，使用原内容重试不会重复发送。",
              ),
            );
          }
        }
        if (generation !== this.generation) return;
        if (!fallback) break;
        state("connecting", "正在尝试兼容连接，未发送的内容会保留…");
      }
      state(stopped ? "unavailable" : "reconnecting", reason);
      if (!stopped)
        this.retry = setTimeout(
          () => {
            this.retry = undefined;
            void attempt();
          },
          Math.min(30000, 1000 * 2 ** Math.min(failures++, 5)) +
            Math.random() * 400,
        );
    };
    this.retryAction = () => void attempt();
    void attempt();
  }

  private runChannel(
    kind: TransportKind,
    config: TransportTicket,
    generation: number,
    receive: (frame: Frame) => void,
  ): Promise<void> {
    return new Promise((resolve, reject) => {
      let finished = false;
      let buffer = "";
      let close = () => {};
      const finish = (error?: Error) => {
        if (finished) return;
        finished = true;
        clearTimeout(watchdog);
        close();
        if (error) reject(error);
        else resolve();
      };
      const watchdog = setTimeout(
        () => finish(new Error("connection timeout")),
        kind === "webtransport" ? 4500 : 10000,
      );
      this.closeConnection = () => finish();
      const push = (text: string) => {
        if (finished || generation !== this.generation) return;
        buffer += text;
        let newline: number;
        while ((newline = buffer.indexOf("\n")) !== -1) {
          if (newline > FRAME_LIMIT) throw new Error("咨询服务响应过大");
          const raw = buffer.slice(0, newline);
          buffer = buffer.slice(newline + 1);
          if (!raw) continue;
          const frame = JSON.parse(raw) as Frame;
          if (!frame || typeof frame.type !== "string")
            throw new Error("无效的咨询响应");
          if (frame.type === "ready") clearTimeout(watchdog);
          receive(frame);
        }
        if (buffer.length > FRAME_LIMIT) throw new Error("咨询服务响应过大");
      };
      try {
        if (kind === "websocket") {
          const base = new URL(globalThis.location.href);
          const url = new URL(config.url, base);
          // Only this app's HTTPS origin may receive the short-lived ticket and cookie.
          if (
            url.origin !== base.origin ||
            !["http:", "https:"].includes(url.protocol) ||
            url.username ||
            url.password
          )
            throw new Error("咨询服务地址配置错误");
          url.protocol = base.protocol === "https:" ? "wss:" : "ws:";
          url.searchParams.set("ticket", config.ticket);
          const socket = new WebSocket(url.toString());
          close = () => {
            socket.onopen =
              socket.onmessage =
              socket.onerror =
              socket.onclose =
                null;
            socket.close();
          };
          socket.onopen = () => {
            if (finished || generation !== this.generation) {
              close();
              return;
            }
            this.writer = {
              write: async (bytes) => {
                if (
                  socket.readyState !== WebSocket.OPEN ||
                  socket.bufferedAmount > 65536
                )
                  throw new Error("connection busy");
                socket.send(new TextDecoder().decode(bytes));
              },
            };
          };
          socket.onmessage = (event: MessageEvent) => {
            try {
              if (
                typeof event.data !== "string" ||
                new TextEncoder().encode(event.data).byteLength > FRAME_LIMIT
              )
                throw new Error("无效的咨询响应");
              push(event.data);
            } catch (e) {
              finish(e as Error);
            }
          };
          socket.onerror = () =>
            finish(new Error("WebSocket connection failed"));
          socket.onclose = () => finish(new Error("connection closed"));
        } else {
          const url = new URL(config.url);
          if (url.protocol !== "https:" || url.username || url.password)
            throw new Error("咨询服务地址配置错误");
          url.searchParams.set("ticket", config.ticket);
          const pin = config.certificate_sha256;
          if (pin && !/^[a-f0-9]{64}$/.test(pin))
            throw new Error("咨询服务证书配置错误");
          const transport = new WebTransport(
            url.toString(),
            pin
              ? {
                  serverCertificateHashes: [
                    {
                      algorithm: "sha-256",
                      value: Uint8Array.from(pin.match(/../g)!, (v) =>
                        parseInt(v, 16),
                      ).buffer,
                    },
                  ],
                }
              : undefined,
          );
          close = () => transport.close();
          void transport.closed.then(
            () => finish(new Error("connection closed")),
            (e: Error) => finish(e),
          );
          void (async () => {
            await transport.ready;
            if (finished) return;
            const stream = await transport.createBidirectionalStream();
            if (finished || generation !== this.generation) {
              transport.close();
              return;
            }
            const writer = stream.writable.getWriter();
            this.writer = writer;
            await writer.write(new TextEncoder().encode("\n"));
            const reader = stream.readable.getReader();
            const decoder = new TextDecoder("utf-8", { fatal: true });
            try {
              while (!finished) {
                const { value, done } = await reader.read();
                if (done) break;
                push(decoder.decode(value, { stream: true }));
              }
              finish(new Error("connection closed"));
            } finally {
              reader.releaseLock();
            }
          })().catch((e: Error) => finish(e));
        }
      } catch (e) {
        finish(e as Error);
      }
    });
  }

  /** Retry a scheduled attempt only; never open parallel sessions or replay a draft. */
  reconnect(): boolean {
    if (!this.retry || !this.retryAction || this.ready) return false;
    clearTimeout(this.retry);
    this.retry = undefined;
    this.retryAction();
    return true;
  }
  command(op: string, conversation = "", payload: unknown = {}): Promise<any> {
    if (!this.ready || !this.writer)
      return Promise.reject(
        new Error("客服连接尚未就绪，请连接恢复后重试。内容已保留。"),
      );
    if (this.pending.size >= 16)
      return Promise.reject(new Error("操作较多，请稍候再试。"));
    const id = String(++this.sequence),
      writer = this.writer,
      generation = this.generation;
    const bytes = new TextEncoder().encode(
      JSON.stringify({ id, op, conversation, payload }) + "\n",
    );
    if (bytes.byteLength > 16384)
      return Promise.reject(new Error("消息内容过长"));
    return new Promise((resolve, reject) => {
      const timer = setTimeout(() => {
        this.pending.delete(id);
        reject(
          new Error("确认超时，请使用原内容重试；同一条消息不会重复发送。"),
        );
      }, 12000);
      this.pending.set(id, { resolve, reject, timer });
      this.output = this.output
        .then(() => {
          if (
            generation !== this.generation ||
            writer !== this.writer ||
            !this.pending.has(id)
          )
            throw new Error("connection changed");
          return writer.write(bytes);
        })
        .catch(() => {
          const entry = this.pending.get(id);
          if (entry) {
            clearTimeout(entry.timer);
            this.pending.delete(id);
            entry.reject(new Error("发送失败，内容已保留，请重试。"));
          }
        });
    });
  }
  private rejectPending(error: Error) {
    for (const p of this.pending.values()) {
      clearTimeout(p.timer);
      p.reject(error);
    }
    this.pending.clear();
  }
  disconnect() {
    this.generation++;
    clearTimeout(this.retry);
    this.retry = undefined;
    this.retryAction = undefined;
    this.ready = false;
    this.closeConnection?.();
    this.closeConnection = undefined;
    this.writer = null;
    this.output = Promise.resolve();
    this.rejectPending(new Error("咨询连接已关闭"));
  }
}
