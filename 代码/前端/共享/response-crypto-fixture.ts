/** Synthetic protocol peer for client tests; contains no production material. */
export const fixtureProtocol = "qy-r24-aesgcm-v1";
export const fixtureKey = Uint8Array.from(
  { length: 32 },
  (_, index) => index + 1,
);
export const fixtureBase64 = (bytes: Uint8Array) =>
  btoa(String.fromCharCode(...bytes))
    .replace(/\+/g, "-")
    .replace(/\//g, "_")
    .replace(/=+$/, "");
export function fixtureInit(
  surface = "public",
  overrides: Record<string, unknown> = {},
) {
  return new Response(
    JSON.stringify({
      data: {
        enabled: true,
        protocol: fixtureProtocol,
        surface,
        context_id: "context-1",
        key_id: "hour-1",
        key: fixtureBase64(fixtureKey),
        server_time: 1800000000,
        rotate_at: 1800003600,
        retain_until: 1800003720,
        media_access_plan: true,
        ...overrides,
      },
    }),
    { headers: { "Content-Type": "application/json" } },
  );
}
export async function fixtureEncrypted(
  input: string,
  init: RequestInit,
  value: unknown,
  options: {
    surface?: string;
    status?: number;
    contentType?: string;
    contentEncoding?: "identity" | "gzip";
    bytes?: Uint8Array<ArrayBuffer>;
  } = {},
) {
  const headers = new Headers(init.headers);
  const url = new URL(input, "https://qingye.test");
  const status = options.status ?? 200;
  const contentType = options.contentType ?? "application/json";
  const contentEncoding = options.contentEncoding ?? "identity";
  const aad = [
    "QY-R24-HTTP",
    "1",
    options.surface ?? "public",
    headers.get("X-QY-Context"),
    headers.get("X-QY-Key-Id"),
    headers.get("X-QY-Request-Id"),
    (init.method ?? "GET").toUpperCase(),
    url.pathname + url.search,
    String(status),
    contentType,
    contentEncoding,
  ].join("\n");
  const nonce = crypto.getRandomValues(new Uint8Array(12));
  const key = await crypto.subtle.importKey(
    "raw",
    fixtureKey,
    "AES-GCM",
    false,
    ["encrypt"],
  );
  const encrypted = await crypto.subtle.encrypt(
    {
      name: "AES-GCM",
      iv: nonce,
      additionalData: new TextEncoder().encode(aad),
    },
    key,
    options.bytes ?? new TextEncoder().encode(JSON.stringify(value)),
  );
  return new Response(encrypted, {
    status,
    headers: {
      "Content-Type": "application/vnd.qingye.encrypted",
      "X-QY-Protocol": fixtureProtocol,
      "X-QY-Context": headers.get("X-QY-Context")!,
      "X-QY-Key-Id": headers.get("X-QY-Key-Id")!,
      "X-QY-Request-Id": headers.get("X-QY-Request-Id")!,
      "X-QY-Nonce": fixtureBase64(nonce),
      "X-QY-Content-Type": contentType,
      "X-QY-Content-Encoding": contentEncoding,
    },
  });
}
export function fixtureLocalImage(
  init: RequestInit,
  contentType = "image/png",
) {
  const request = new Headers(init.headers);
  return new Response(new Uint8Array([137, 80, 78, 71, 0, 255]), {
    headers: {
      "Content-Type": contentType,
      "X-QY-Content-Type": contentType,
      "X-QY-Content-Encoding": "identity",
      "X-QY-Protocol": fixtureProtocol,
      "X-QY-Response-Mode": "local-image",
      "X-QY-Context": request.get("X-QY-Context")!,
      "X-QY-Key-Id": request.get("X-QY-Key-Id")!,
      "X-QY-Request-Id": request.get("X-QY-Request-Id")!,
    },
  });
}

export async function fixtureChat(
  value: unknown,
  options: {
    keyId?: string;
    channel?: string;
    frameId?: string;
    surface?: string;
  } = {},
) {
  const frame = {
    type: "encrypted",
    v: 1,
    context_id: "context-1",
    key_id: options.keyId ?? "hour-1",
    channel_id: options.channel ?? "channel-1",
    frame_id: options.frameId ?? "a".repeat(32),
    nonce: "",
    ciphertext: "",
  };
  const aad = [
    "QY-R24-CHAT",
    "1",
    options.surface ?? "public",
    frame.context_id,
    frame.key_id,
    frame.channel_id,
    frame.frame_id,
  ].join("\n");
  const nonce = crypto.getRandomValues(new Uint8Array(12));
  const key = await crypto.subtle.importKey(
    "raw",
    fixtureKey,
    "AES-GCM",
    false,
    ["encrypt"],
  );
  const encrypted = await crypto.subtle.encrypt(
    {
      name: "AES-GCM",
      iv: nonce,
      additionalData: new TextEncoder().encode(aad),
    },
    key,
    new TextEncoder().encode(JSON.stringify(value)),
  );
  return {
    ...frame,
    nonce: fixtureBase64(nonce),
    ciphertext: fixtureBase64(new Uint8Array(encrypted)),
  };
}
