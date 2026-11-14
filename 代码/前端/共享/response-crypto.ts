/** R24 only wraps server responses. Authentication and business request IDs stay unchanged. */
export const RESPONSE_PROTOCOL = "qy-r24-aesgcm-v1";
const ENCRYPTED_TYPE = "application/vnd.qingye.encrypted";
const MAX_BODY = 64 * 1024 * 1024 + 16;
type KeyEntry = { key: CryptoKey; retainUntil: number };
export type ResponsePolicy = {
  enabled: boolean;
  media_access_plan: boolean;
  surface?: "public" | "admin";
  context_id?: string;
  key_id?: string;
};
export type ResponseContext = ResponsePolicy & {
  generation: number;
  method: string;
  path: string;
  requestId: string;
  acceptsGzip: boolean;
  headers: Headers;
  key?: CryptoKey;
};
export type ChatCrypto = {
  enabled: boolean;
  context_id?: string;
  channel_id?: string;
};
export class ResponseCryptoError extends Error {
  constructor(
    message = "安全响应未能确认，请重试；已提交的操作不会自动重复。",
  ) {
    super(message);
    this.name = "ResponseCryptoError";
  }
}
function record(value: unknown): Record<string, unknown> {
  if (!value || typeof value !== "object" || Array.isArray(value))
    throw new ResponseCryptoError();
  return value as Record<string, unknown>;
}
function token(value: unknown): value is string {
  return typeof value === "string" && /^[a-zA-Z0-9_-]{1,160}$/.test(value);
}
function decode64(value: unknown, length?: number): Uint8Array<ArrayBuffer> {
  if (typeof value !== "string" || !/^[A-Za-z0-9_-]+$/.test(value))
    throw new ResponseCryptoError();
  const decoded = atob(value.replace(/-/g, "+").replace(/_/g, "/"));
  const bytes = Uint8Array.from(decoded, (ch) => ch.charCodeAt(0));
  if (length !== undefined && bytes.length !== length)
    throw new ResponseCryptoError();
  return bytes;
}
const utf8 = new TextEncoder();
const textDecoder = new TextDecoder("utf-8", { fatal: true });
async function boundedBody(
  response: Response,
  maximum = MAX_BODY,
): Promise<Uint8Array<ArrayBuffer>> {
  if (Number(response.headers.get("Content-Length")) > maximum) {
    await response.body?.cancel();
    throw new ResponseCryptoError("服务响应过大，请稍后重试。");
  }
  if (!response.body) return new Uint8Array();
  const reader = response.body.getReader();
  const parts: Uint8Array[] = [];
  let length = 0;
  try {
    for (;;) {
      const { done, value } = await reader.read();
      if (done) break;
      length += value.byteLength;
      if (length > maximum) {
        await reader.cancel();
        throw new ResponseCryptoError("服务响应过大，请稍后重试。");
      }
      parts.push(value);
    }
  } finally {
    reader.releaseLock();
  }
  const output = new Uint8Array(length);
  let offset = 0;
  for (const part of parts) {
    output.set(part, offset);
    offset += part.byteLength;
  }
  return output;
}

export class ResponseCryptoClient {
  private policy: ResponsePolicy | undefined;
  private current: Promise<ResponsePolicy> | undefined;
  private keys = new Map<string, KeyEntry>();
  private keyLoads = new Map<string, Promise<void>>();
  private rotateAt = 0;
  private generation = 0;
  private identity: string | undefined;
  private channel: BroadcastChannel | undefined;
  private watchingIdentity = false;
  private identityListeners = new Set<() => void>();
  constructor(
    private options: {
      base?: string;
      origin?: string;
      fetch?: typeof fetch;
      now?: () => number;
    } = {},
  ) {}
  private now() {
    return this.options.now?.() ?? performance.now();
  }
  private origin() {
    return (
      this.options.origin ??
      globalThis.location?.origin ??
      "https://qingye.invalid"
    );
  }
  private base() {
    return (this.options.base ?? "/api/v1").replace(/\/$/, "");
  }
  private fetchRaw(input: RequestInfo | URL, init?: RequestInit) {
    return (this.options.fetch ?? globalThis.fetch)(input, init);
  }
  private url(input: string) {
    const url = new URL(input, this.origin());
    if (url.origin !== this.origin() || url.username || url.password)
      throw new ResponseCryptoError("接口地址不属于当前站点。");
    return url;
  }
  private watchIdentity() {
    if (this.watchingIdentity || typeof window === "undefined") return;
    this.watchingIdentity = true;
    const changed = () => {
      this.reset();
      this.emitIdentity();
    };
    if (typeof BroadcastChannel !== "undefined") {
      this.channel = new BroadcastChannel("qingye-response-identity-v1");
      this.channel.onmessage = (event) => {
        if (event.data?.type === "identity-changed") changed();
      };
    } else if (typeof window.addEventListener === "function") {
      window.addEventListener("storage", (event) => {
        if (event.key === "qingye-identity-signal" && event.newValue) changed();
      });
    }
  }
  subscribeIdentity(listener: () => void) {
    this.watchIdentity();
    this.identityListeners.add(listener);
    return () => {
      this.identityListeners.delete(listener);
    };
  }
  private emitIdentity() {
    this.identityListeners.forEach((listener) => listener());
  }
  /** No keys or user data cross tabs; the server independently rechecks every new context. */
  reset(notify = false, announceLocal = true) {
    this.generation++;
    this.policy = undefined;
    this.current = undefined;
    this.keys.clear();
    this.keyLoads.clear();
    this.rotateAt = 0;
    this.identity = undefined;
    if (notify) {
      this.watchIdentity();
      if (announceLocal) this.emitIdentity();
      if (this.channel) this.channel.postMessage({ type: "identity-changed" });
      else if (typeof window !== "undefined") {
        try {
          window.localStorage.setItem(
            "qingye-identity-signal",
            crypto.randomUUID(),
          );
          window.localStorage.removeItem("qingye-identity-signal");
        } catch {
          /* Storage can be unavailable in private browsing. No key is persisted. */
        }
      }
    }
  }
  setIdentity(identity: string) {
    if (this.identity !== undefined && this.identity !== identity) this.reset();
    this.identity = identity;
  }
  epoch() {
    return this.generation;
  }
  private assertGeneration(generation: number) {
    if (generation !== this.generation)
      throw new DOMException("登录状态已变更", "AbortError");
  }
  private async initialize(keyId?: string): Promise<ResponsePolicy> {
    const generation = this.generation;
    const target = `${this.base()}/response-crypto/init${keyId ? `?key_id=${encodeURIComponent(keyId)}` : ""}`;
    this.url(target);
    const signal = AbortSignal.timeout(15000);
    const fetchInit = () => {
      this.assertGeneration(generation);
      return this.fetchRaw(target, {
        credentials: "include",
        cache: "no-store",
        redirect: "error",
        headers: { Accept: "application/json", "X-QY-Init": "1" },
        signal,
      });
    };
    // Same-origin tabs serialize the first Set-Cookie exchange, never exchanging key material.
    const response =
      typeof navigator !== "undefined" && navigator.locks?.request
        ? await navigator.locks.request(
            "qingye-response-init-v1",
            { signal },
            fetchInit,
          )
        : await fetchInit();
    if (!response.ok)
      throw new ResponseCryptoError("安全连接初始化失败，请稍后重试。");
    const data = record(record(await response.json()).data);
    this.assertGeneration(generation);
    if (
      data.protocol !== RESPONSE_PROTOCOL ||
      typeof data.enabled !== "boolean" ||
      typeof data.media_access_plan !== "boolean"
    )
      throw new ResponseCryptoError();
    if (!data.enabled) {
      if (keyId || this.policy?.enabled)
        throw new ResponseCryptoError("安全连接配置已变化，请重新加载页面。");
      return { enabled: false, media_access_plan: data.media_access_plan };
    }
    if (
      !["public", "admin"].includes(String(data.surface)) ||
      !token(data.context_id) ||
      !token(data.key_id)
    )
      throw new ResponseCryptoError();
    if (
      keyId &&
      (data.key_id !== keyId ||
        data.context_id !== this.policy?.context_id ||
        data.surface !== this.policy?.surface)
    )
      throw new ResponseCryptoError();
    if (this.policy?.enabled && data.context_id !== this.policy.context_id) {
      this.reset();
      this.emitIdentity();
      throw new ResponseCryptoError("登录状态已变化，请刷新页面后重试。");
    }
    const serverTime = Number(
      data.server_time ?? Date.parse(response.headers.get("Date") ?? "") / 1000,
    );
    const rotate = Number(data.rotate_at),
      retain = Number(data.retain_until);
    if (
      ![serverTime, rotate, retain].every(Number.isFinite) ||
      retain <= serverTime ||
      retain - serverTime > 7320
    )
      throw new ResponseCryptoError();
    if (!globalThis.crypto?.subtle)
      throw new ResponseCryptoError("请使用新版浏览器通过 HTTPS 访问。");
    const raw = decode64(data.key, 32);
    let key: CryptoKey;
    try {
      key = await crypto.subtle.importKey("raw", raw, "AES-GCM", false, [
        "decrypt",
      ]);
    } finally {
      raw.fill(0);
    }
    this.assertGeneration(generation);
    this.keys.set(data.key_id, {
      key,
      retainUntil: this.now() + (retain - serverTime) * 1000,
    });
    for (const [id, entry] of this.keys)
      if (entry.retainUntil <= this.now()) this.keys.delete(id);
    while (this.keys.size > 4) this.keys.delete(this.keys.keys().next().value!);
    if (!keyId)
      this.rotateAt = this.now() + Math.max(0, rotate - serverTime) * 1000;
    return {
      enabled: true,
      media_access_plan: data.media_access_plan,
      surface: data.surface as "public" | "admin",
      context_id: data.context_id,
      key_id: data.key_id,
    };
  }
  ensure(): Promise<ResponsePolicy> {
    this.watchIdentity();
    if (this.current) return this.current;
    if (this.policy && (!this.policy.enabled || this.now() < this.rotateAt))
      return Promise.resolve(this.policy);
    const generation = this.generation;
    this.current = this.initialize()
      .then((policy) => {
        this.assertGeneration(generation);
        this.policy = policy;
        return policy;
      })
      .finally(() => {
        if (generation === this.generation) this.current = undefined;
      });
    return this.current;
  }
  async prepare(
    input: string,
    method = "GET",
    headers?: HeadersInit,
  ): Promise<ResponseContext> {
    const url = this.url(input);
    const policy = await this.ensure();
    const requestId = crypto.randomUUID();
    const acceptsGzip = typeof DecompressionStream === "function";
    const output = new Headers(headers);
    const key = policy.key_id ? this.keys.get(policy.key_id)?.key : undefined;
    if (policy.enabled) {
      if (!key) throw new ResponseCryptoError();
      output.set("X-QY-Protocol", RESPONSE_PROTOCOL);
      output.set("X-QY-Context", policy.context_id!);
      output.set("X-QY-Key-Id", policy.key_id!);
      output.set("X-QY-Request-Id", requestId);
      if (acceptsGzip) output.set("X-QY-Accept-Encoding", "gzip");
      else output.delete("X-QY-Accept-Encoding");
    }
    return {
      ...policy,
      generation: this.generation,
      method: method.toUpperCase(),
      path: url.pathname + url.search,
      requestId,
      acceptsGzip,
      headers: output,
      key,
    };
  }
  async decode(
    response: Response,
    context: ResponseContext,
  ): Promise<Response> {
    this.assertGeneration(context.generation);
    if (response.headers.get("X-QY-Protocol-Error") === "1") {
      const data = record(await response.json());
      if (
        response.ok ||
        typeof data.code !== "string" ||
        typeof data.message !== "string" ||
        Object.keys(data).some((key) => !["code", "message"].includes(key))
      )
        throw new ResponseCryptoError();
      this.reset();
      if (data.code === "RESPONSE_CONTEXT_CHANGED") this.emitIdentity();
      return new Response(
        JSON.stringify({ code: data.code, message: data.message }),
        {
          status: response.status,
          headers: { "Content-Type": "application/json" },
        },
      );
    }
    if (!context.enabled) {
      if (response.headers.get("Content-Type")?.startsWith(ENCRYPTED_TYPE))
        throw new ResponseCryptoError();
      return response;
    }
    if (context.method === "HEAD" || [204, 304].includes(response.status))
      return response;
    const headers = response.headers;
    const contentType = headers.get("X-QY-Content-Type");
    const contentEncoding = headers.get("X-QY-Content-Encoding");
    const bindingMatches =
      headers.get("X-QY-Protocol") === RESPONSE_PROTOCOL &&
      headers.get("X-QY-Context") === context.context_id &&
      headers.get("X-QY-Key-Id") === context.key_id &&
      headers.get("X-QY-Request-Id") === context.requestId;
    if (headers.get("X-QY-Response-Mode") === "local-image") {
      const path = context.path.split("?")[0];
      if (
        !bindingMatches ||
        context.method !== "GET" ||
        response.status !== 200 ||
        !/^\/api\/v1\/media\/[a-zA-Z0-9-]+(?:\/(?:file|preview))?$/.test(
          path,
        ) ||
        !contentType ||
        !["image/jpeg", "image/png", "image/webp", "image/gif"].includes(
          contentType,
        ) ||
        headers.get("Content-Type") !== contentType ||
        contentEncoding !== "identity" ||
        headers.has("X-QY-Nonce")
      )
        throw new ResponseCryptoError();
      const maximum = MAX_BODY - 16;
      if (Number(headers.get("Content-Length")) > maximum) {
        await response.body?.cancel();
        throw new ResponseCryptoError("服务响应过大，请稍后重试。");
      }
      let received = 0;
      const stream = response.body?.pipeThrough(
        new TransformStream<Uint8Array, Uint8Array>({
          transform: (chunk, controller) => {
            this.assertGeneration(context.generation);
            received += chunk.byteLength;
            if (received > maximum)
              throw new ResponseCryptoError("服务响应过大，请稍后重试。");
            controller.enqueue(chunk);
          },
        }),
      );
      return new Response(stream, { status: response.status, headers });
    }
    if (
      !bindingMatches ||
      headers.get("Content-Type") !== ENCRYPTED_TYPE ||
      !contentType ||
      /[\r\n]/.test(contentType) ||
      contentType.length > 200 ||
      !["identity", "gzip"].includes(contentEncoding ?? "")
    )
      throw new ResponseCryptoError();
    if (contentEncoding === "gzip") {
      const publicPath =
        /^\/api\/v1\/(?:categories|home|products(?:\/[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}(?:\/images)?)?)$/.test(
          context.path.split("?")[0],
        );
      if (
        !context.acceptsGzip ||
        context.surface !== "public" ||
        context.method !== "GET" ||
        response.status !== 200 ||
        !publicPath ||
        contentType.split(";")[0].trim() !== "application/json"
      )
        throw new ResponseCryptoError();
    }
    const bytes = await boundedBody(response);
    if (bytes.byteLength < 16 || bytes.byteLength > MAX_BODY)
      throw new ResponseCryptoError();
    const aad = [
      "QY-R24-HTTP",
      "1",
      context.surface!,
      context.context_id!,
      context.key_id!,
      context.requestId,
      context.method,
      context.path,
      String(response.status),
      contentType,
      contentEncoding!,
    ].join("\n");
    let plaintext: ArrayBuffer;
    try {
      plaintext = await crypto.subtle.decrypt(
        {
          name: "AES-GCM",
          iv: decode64(headers.get("X-QY-Nonce"), 12),
          additionalData: utf8.encode(aad),
          tagLength: 128,
        },
        context.key!,
        bytes,
      );
    } catch {
      throw new ResponseCryptoError();
    }
    this.assertGeneration(context.generation);
    if (contentEncoding === "gzip") {
      try {
        const compressed = new Response(plaintext).body!;
        const decoded = await boundedBody(
          new Response(compressed.pipeThrough(new DecompressionStream("gzip"))),
          4 * 1024 * 1024,
        );
        plaintext = decoded.buffer;
      } catch {
        throw new ResponseCryptoError("服务响应解压失败，请稍后重试。");
      }
      this.assertGeneration(context.generation);
    }
    const decodedHeaders = new Headers(headers);
    decodedHeaders.set("Content-Type", contentType);
    decodedHeaders.delete("Content-Length");
    decodedHeaders.delete("Content-Encoding");
    return new Response(plaintext, {
      status: response.status,
      statusText: response.statusText,
      headers: decodedHeaders,
    });
  }
  async fetch(input: string, init: RequestInit = {}): Promise<Response> {
    const context = await this.prepare(input, init.method, init.headers);
    if (init.signal?.aborted) throw init.signal.reason;
    const response = await this.fetchRaw(input, {
      ...init,
      headers: context.headers,
      cache: "no-store",
      redirect: "error",
    });
    return this.decode(response, context);
  }
  async decodeXHR(
    xhr: XMLHttpRequest,
    context: ResponseContext,
  ): Promise<Response> {
    const headers = new Headers();
    for (const line of xhr
      .getAllResponseHeaders()
      .trim()
      .split(/[\r\n]+/)) {
      const index = line.indexOf(":");
      if (index > 0)
        headers.append(
          line.slice(0, index).trim(),
          line.slice(index + 1).trim(),
        );
    }
    const body = [204, 304].includes(xhr.status)
      ? null
      : (xhr.response as ArrayBuffer);
    return this.decode(
      new Response(body, { status: xhr.status, headers }),
      context,
    );
  }
  async validateChat(config: ChatCrypto | undefined): Promise<ChatCrypto> {
    const policy = await this.ensure();
    if (
      !config ||
      typeof config.enabled !== "boolean" ||
      config.enabled !== policy.enabled
    )
      throw new ResponseCryptoError("咨询安全协议不匹配，请重新连接。");
    if (
      config.enabled &&
      (config.context_id !== policy.context_id || !token(config.channel_id))
    )
      throw new ResponseCryptoError();
    return config;
  }
  async decodeChat(
    value: unknown,
    config: ChatCrypto,
    seen: Set<string>,
  ): Promise<unknown> {
    if (!config.enabled) return value;
    const frame = record(value),
      generation = this.generation;
    if (
      frame.type !== "encrypted" ||
      frame.v !== 1 ||
      frame.context_id !== config.context_id ||
      frame.context_id !== this.policy?.context_id ||
      frame.channel_id !== config.channel_id ||
      !token(frame.key_id) ||
      typeof frame.frame_id !== "string" ||
      !/^[a-f0-9]{32}$/.test(frame.frame_id) ||
      seen.has(frame.frame_id)
    )
      throw new ResponseCryptoError();
    let entry = this.keys.get(frame.key_id);
    if (!entry || entry.retainUntil <= this.now()) {
      let pending = this.keyLoads.get(frame.key_id);
      if (!pending) {
        const keyId = frame.key_id;
        pending = this.initialize(keyId)
          .then(() => {})
          .finally(() => {
            if (generation === this.generation) this.keyLoads.delete(keyId);
          });
        this.keyLoads.set(keyId, pending);
      }
      await pending;
      this.assertGeneration(generation);
      entry = this.keys.get(frame.key_id);
    }
    if (!entry) throw new ResponseCryptoError();
    const bytes = decode64(frame.ciphertext);
    if (bytes.length < 16 || bytes.length > 1048576 + 16)
      throw new ResponseCryptoError();
    const aad = [
      "QY-R24-CHAT",
      "1",
      this.policy!.surface!,
      config.context_id!,
      frame.key_id,
      config.channel_id!,
      frame.frame_id,
    ].join("\n");
    let decrypted: ArrayBuffer;
    try {
      decrypted = await crypto.subtle.decrypt(
        {
          name: "AES-GCM",
          iv: decode64(frame.nonce, 12),
          additionalData: utf8.encode(aad),
          tagLength: 128,
        },
        entry.key,
        bytes,
      );
    } catch {
      throw new ResponseCryptoError();
    }
    this.assertGeneration(generation);
    seen.add(frame.frame_id);
    if (seen.size > 4096) seen.delete(seen.values().next().value!);
    return JSON.parse(textDecoder.decode(decrypted));
  }
}

// Each app is a separate bundle and HTTPS origin; no key state is shared between surfaces.
export const responseCrypto = new ResponseCryptoClient({
  base: import.meta.env.VITE_API_BASE_URL || "/api/v1",
});
