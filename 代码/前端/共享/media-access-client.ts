export type AccessState = {
  enabled: boolean;
  state: "allowed" | "challenge" | "limited" | "restricted";
  site_key?: string;
  retry_after?: number;
  code?: string;
};
type ViewState = {
  status: AccessState | null;
  open: boolean;
  revision: number;
};
let view: ViewState = { status: null, open: false, revision: 0 };
const listeners = new Set<() => void>();
let session: Promise<AccessState> | undefined;
let probe: Promise<AccessState> | undefined;
let generation = 0;
let lastProbe = 0;
let lastStatus: AccessState | undefined;
export const accessSnapshot = () => view;
export function subscribeAccess(fn: () => void) {
  listeners.add(fn);
  return () => {
    listeners.delete(fn);
  };
}
function publish(update: Partial<ViewState>) {
  view = { ...view, ...update };
  listeners.forEach((fn) => fn());
}
export function isProtectedMedia(src?: string): boolean {
  if (!src || typeof window === "undefined") return false;
  try {
    const url = new URL(src, window.location.href);
    return (
      url.origin === window.location.origin &&
      /^\/api\/v1\/media\/[a-zA-Z0-9-]+(?:\/(?:file|preview))?$/.test(
        url.pathname,
      )
    );
  } catch {
    return false;
  }
}
export async function safetyRequest<T>(
  path: string,
  body?: unknown,
  signal?: AbortSignal,
): Promise<T> {
  const timeout = AbortSignal.timeout(15000);
  const response = await fetch(`/api/v1/security/${path}`, {
    method: body === undefined ? "GET" : "POST",
    credentials: "same-origin",
    cache: "no-store",
    headers:
      body === undefined ? undefined : { "Content-Type": "application/json" },
    body: body === undefined ? undefined : JSON.stringify(body),
    signal: signal ? AbortSignal.any([signal, timeout]) : timeout,
  });
  const data = await response.json();
  if (!response.ok)
    throw new Error(data.message || "图片服务暂不可用，请稍后重试。");
  return data as T;
}
export function ensureMediaSession(): Promise<AccessState> {
  if (!session) {
    const current = generation;
    session = safetyRequest<AccessState>("media-access")
      .then((data) => {
        if (current !== generation) throw new Error("会话已变化");
        return data;
      })
      .catch((error) => {
        if (current === generation) session = undefined;
        throw error;
      });
  }
  return session;
}
export function inspectMediaFailure(): Promise<AccessState> {
  if (probe) return probe;
  if (lastStatus && Date.now() - lastProbe < 1000)
    return Promise.resolve(lastStatus);
  const current = generation;
  probe = safetyRequest<AccessState>("media-access")
    .then((data) => {
      if (current !== generation) throw new Error("会话已变化");
      lastStatus = data;
      lastProbe = Date.now();
      if (data.enabled && data.state !== "allowed") {
        publish({
          status: data,
          open:
            data.state === "challenge"
              ? view.open || view.status === null
              : view.open,
        });
      }
      return data;
    })
    .finally(() => {
      if (current === generation) probe = undefined;
    });
  return probe;
}
export function openMediaVerification() {
  publish({ open: true });
}
export function closeMediaVerification() {
  publish({ open: false });
}
export function mediaVerificationComplete() {
  lastStatus = undefined;
  lastProbe = 0;
  publish({ open: false, status: null, revision: view.revision + 1 });
}
export function resetMediaIdentity() {
  generation++;
  session = undefined;
  probe = undefined;
  lastProbe = 0;
  lastStatus = undefined;
  publish({ open: false, status: null, revision: view.revision + 1 });
}

// 恢复请求最多四个同时启动；完成或超时后释放，不在验证后重载整个图集。
let running = 0;
const queue: Array<{
  generation: number;
  start: (release: () => void) => void;
}> = [];
function drain() {
  while (running < 4 && queue.length) {
    const item = queue.shift()!;
    if (item.generation !== generation) continue;
    running++;
    let finished = false;
    const release = () => {
      if (finished) return;
      finished = true;
      clearTimeout(timer);
      running--;
      drain();
    };
    const timer = setTimeout(release, 15000);
    item.start(release);
  }
}
export function queueMediaRetry(start: (release: () => void) => void) {
  queue.push({ generation, start });
  drain();
}
