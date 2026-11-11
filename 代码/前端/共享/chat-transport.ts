// Protocol only: each app supplies its own authenticated ticket endpoint and lifecycle.
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
export class ChatTransport {
  isConnected() {
    return this.ready;
  }
  private transport: WebTransport | null = null;
  private writer: WritableStreamDefaultWriter<Uint8Array> | null = null;
  private pending = new Map<string, Pending>();
  private sequence = 0;
  private generation = 0;
  private retry: ReturnType<typeof setTimeout> | undefined;
  private retryAction: (() => void) | undefined;
  private ready = false;
  private output = Promise.resolve();
  private listener:
    ((state: TransportState, message: string) => void) | undefined;
  connect(
    ticket: () => Promise<TransportTicket>,
    update: () => void,
    state: (state: TransportState, message: string) => void,
  ) {
    this.disconnect();
    const generation = this.generation;
    this.listener = state;
    if (!globalThis.isSecureContext || !("WebTransport" in globalThis)) {
      state(
        "unavailable",
        "当前浏览器或访问地址不支持在线咨询，请使用新版浏览器通过安全地址打开。",
      );
      return;
    }
    let failures = 0;
    const attempt = async () => {
      if (generation !== this.generation) return;
      state(
        failures ? "reconnecting" : "connecting",
        failures ? "连接中断，正在重连；未发送的内容会保留。" : "正在连接客服…",
      );
      let transport: WebTransport | undefined;
      let watchdog: ReturnType<typeof setTimeout> | undefined;
      let stage: "ticket" | "handshake" | "stream" | "session" = "ticket";
      let reason = "连接暂不可用，正在重试；未发送的内容会保留。";
      try {
        const config = await ticket();
        if (generation !== this.generation) return;
        const url = new URL(config.url);
        if (url.protocol !== "https:") throw new Error("咨询服务地址配置错误");
        url.searchParams.set("ticket", config.ticket);
        const pin = config.certificate_sha256;
        if (pin && !/^[a-f0-9]{64}$/.test(pin))
          throw new Error("咨询服务证书配置错误");
        stage = "handshake";
        transport = new WebTransport(
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
        this.transport = transport;
        // Attach rejection handling immediately; ready and closed can fail independently.
        void transport.closed.catch(() => {});
        watchdog = setTimeout(
          () =>
            transport?.close({ closeCode: 1, reason: "connection timeout" }),
          12000,
        );
        await transport.ready;
        stage = "stream";
        const stream = await transport.createBidirectionalStream();
        if (generation !== this.generation) {
          transport.close();
          return;
        }
        this.writer = stream.writable.getWriter();
        // The first byte makes the stream visible to the server before it sends ready.
        await this.writer.write(new TextEncoder().encode("\n"));
        const reader = stream.readable.getReader();
        const decoder = new TextDecoder("utf-8", { fatal: true });
        let buffer = "";
        try {
          for (;;) {
            const { value, done } = await reader.read();
            if (generation !== this.generation) break;
            if (done) break;
            buffer += decoder.decode(value, { stream: true });
            let newline: number;
            while ((newline = buffer.indexOf("\n")) !== -1) {
              if (newline > 1048576) throw new Error("咨询服务响应过大");
              const raw = buffer.slice(0, newline);
              buffer = buffer.slice(newline + 1);
              if (!raw) continue;
              const frame = JSON.parse(raw) as Frame;
              if (frame.type === "ready") {
                stage = "session";
                clearTimeout(watchdog);
                this.ready = true;
                failures = 0;
                state("connected", "");
                update();
              } else if (frame.type === "update") update();
              else if (frame.type === "result" && frame.id) {
                const entry = this.pending.get(frame.id);
                if (!entry) continue;
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
            }
            if (buffer.length > 1048576) throw new Error("咨询服务响应过大");
          }
        } finally {
          reader.releaseLock();
        }
      } catch (e) {
        if ((e as { status?: number }).status === 401) {
          reason = "登录状态已失效，请重新登录。";
          failures = 100;
        } else if (
          e instanceof Error &&
          (e as { status?: number }).status === 503
        )
          reason = e.message;
        else if ((e as { status?: number }).status === 429)
          reason = "连接尝试较频繁，稍后会自动重试。未发送的内容已保留。";
        else if (stage === "handshake")
          reason =
            "安全聊天连接未建立。代理或网络限制可能阻止连接；未发送的内容已保留。";
        else if (stage === "stream")
          reason = "聊天通道未就绪，正在重试；未发送的内容已保留。";
      } finally {
        clearTimeout(watchdog);
        transport?.close();
        if (generation === this.generation) {
          this.ready = false;
          this.writer = null;
          this.output = Promise.resolve();
          this.rejectPending(
            new Error(
              "连接已中断；消息可能已送达，使用原内容重试不会重复发送。",
            ),
          );
          state(failures >= 100 ? "unavailable" : "reconnecting", reason);
          if (failures < 100)
            this.retry = setTimeout(
              () => {
                this.retry = undefined;
                void attempt();
              },
              Math.min(30000, 1000 * 2 ** Math.min(failures++, 5)) +
                Math.random() * 400,
            );
        }
      }
    };
    this.retryAction = () => void attempt();
    void attempt();
  }
  /** Retry only a scheduled attempt; never create a parallel session or resend a message. */
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
      writer = this.writer;
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
        .then(() => writer.write(bytes))
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
    this.transport?.close();
    this.transport = null;
    this.writer = null;
    this.output = Promise.resolve();
    this.rejectPending(new Error("咨询连接已关闭"));
    this.listener = undefined;
  }
}
