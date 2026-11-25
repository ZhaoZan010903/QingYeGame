import {
  useEffect,
  useRef,
  useState,
  useSyncExternalStore,
  type ImgHTMLAttributes,
  type SyntheticEvent,
} from "react";
import {
  accessSnapshot,
  closeMediaVerification,
  ensureMediaSession,
  inspectMediaFailure,
  isProtectedMedia,
  mediaVerificationComplete,
  mediaIdentityGeneration,
  openMediaVerification,
  queueMediaRetry,
  resetMediaIdentity,
  safetyRequest,
  subscribeAccess,
} from "./media-access-client";
import "./media-security.css";
import { responseCrypto } from "./response-crypto";
import {
  acquireMediaSlot,
  ExpiredImageLink,
  resolveMediaImage,
  type DeliveredImage,
  type ProductMediaScope,
} from "./media-delivery";

const placeholder =
  'data:image/svg+xml,%3Csvg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 640 360"%3E%3Crect width="640" height="360" fill="%238e8e9314"/%3E%3C/svg%3E';

type ProtectedImageProps = ImgHTMLAttributes<HTMLImageElement> &
  ProductMediaScope & { retainOnScroll?: boolean };

export function ProtectedImage(props: ProtectedImageProps) {
  const view = useSyncExternalStore(
    subscribeAccess,
    accessSnapshot,
    accessSnapshot,
  );
  const identityEpoch = mediaIdentityGeneration();
  // 换图、更新商品版本或切换登录身份时，释放卡片持有的上一张图片。
  const key = props.retainOnScroll
    ? JSON.stringify([
        props.src,
        props.productId,
        props.productVersion,
        identityEpoch,
      ])
    : undefined;
  return (
    <MediaImage key={key} {...props} view={view} identityEpoch={identityEpoch} />
  );
}

function MediaImage({
  src,
  onError,
  onLoad,
  productId,
  productVersion,
  retainOnScroll = false,
  view,
  identityEpoch,
  ...props
}: ProtectedImageProps & {
  view: ReturnType<typeof accessSnapshot>;
  identityEpoch: number;
}) {
  const protectedSource = isProtectedMedia(src);
  const [ready, setReady] = useState(!protectedSource);
  const [pending, setPending] = useState(false);
  const [attempt, setAttempt] = useState(0);
  const [delivered, setDelivered] = useState<DeliveredImage>();
  const [visible, setVisible] = useState(
    !retainOnScroll && props.loading !== "lazy",
  );
  const [startedDelivery, setStartedDelivery] = useState(false);
  const shouldLoad = visible || (retainOnScroll && startedDelivery);
  const image = useRef<HTMLImageElement>(null);
  const release = useRef<(() => void) | null>(null);
  const retries = useRef(0);
  const ossRefreshes = useRef(0);
  const deliveryRelease = useRef<(() => void) | null>(null);
  const current = useRef(0);
  const timer = useRef<ReturnType<typeof setTimeout> | undefined>(undefined);
  const seen = useRef(view.revision);
  useEffect(() => {
    const element = image.current;
    if (
      !protectedSource ||
      !element ||
      typeof IntersectionObserver === "undefined"
    ) {
      setVisible(true);
      return;
    }
    const observer = new IntersectionObserver(
      (entries) => {
        setVisible(entries.some((entry) => entry.isIntersecting));
      },
      { rootMargin: "200px" },
    );
    observer.observe(element);
    return () => observer.disconnect();
  }, [src, protectedSource]);
  useEffect(() => {
    const version = ++current.current;
    setPending(false);
    setAttempt(0);
    retries.current = 0;
    ossRefreshes.current = 0;
    setReady(!protectedSource);
    if (protectedSource)
      void ensureMediaSession()
        .catch(() => undefined)
        .then(() => {
          if (version === current.current) setReady(true);
        });
    return () => {
      current.current++;
      clearTimeout(timer.current);
      release.current?.();
    };
  }, [src, protectedSource, identityEpoch, productId, productVersion]);
  useEffect(() => {
    if (!protectedSource || !src || !ready || !shouldLoad || pending) {
      setDelivered(undefined);
      return;
    }
    const controller = new AbortController();
    const version = current.current;
    let disposed = false;
    let loaded: DeliveredImage | undefined;
    let releaseSlot: (() => void) | undefined;
    let started = false;
    let timeout: ReturnType<typeof setTimeout> | undefined;
    setDelivered(undefined);
    const start = () => {
      if (started || disposed) return;
      started = true;
      void (async () => {
        releaseSlot = await acquireMediaSlot(controller.signal);
        if (disposed) {
          releaseSlot();
          return;
        }
        // 排队时滑出视口可取消。拿到下载槽后由卡片持有，滚动不再中断读取。
        if (retainOnScroll) setStartedDelivery(true);
        timeout = setTimeout(() => {
          controller.abort(new DOMException("图片读取超时", "TimeoutError"));
          releaseSlot?.();
          if (!disposed && version === current.current && loaded)
            image.current?.dispatchEvent(new Event("error"));
        }, 20000);
        deliveryRelease.current = () => {
          clearTimeout(timeout);
          releaseSlot?.();
        };
        loaded = await resolveMediaImage(src, controller.signal, {
          productId,
          productVersion,
        });
        if (disposed || version !== current.current) {
          loaded.dispose();
          return;
        }
        if (loaded.mode === "oss" && loaded.expiresAt! <= performance.now()) {
          clearTimeout(timeout);
          releaseSlot();
          if (ossRefreshes.current < 1) {
            ossRefreshes.current++;
            setAttempt((value) => value + 1);
            return;
          }
          throw new Error("图片访问凭据已过期，请稍后重试。");
        }
        setDelivered(loaded);
      })().catch((error) => {
        clearTimeout(timeout);
        releaseSlot?.();
        if (
          !disposed &&
          version === current.current &&
          error instanceof ExpiredImageLink &&
          ossRefreshes.current < 1
        ) {
          ossRefreshes.current++;
          setAttempt((value) => value + 1);
          return;
        }
        if (!disposed && version === current.current)
          image.current?.dispatchEvent(new Event("error"));
      });
    };
    start();
    return () => {
      disposed = true;
      controller.abort();
      clearTimeout(timeout);
      releaseSlot?.();
      loaded?.dispose();
      deliveryRelease.current = null;
    };
  }, [
    src,
    protectedSource,
    ready,
    attempt,
    shouldLoad,
    retainOnScroll,
    pending,
    identityEpoch,
    productId,
    productVersion,
  ]);
  function retry() {
    if (!pending || retries.current >= 3) return;
    clearTimeout(timer.current);
    const version = current.current;
    queueMediaRetry((done) => {
      if (version !== current.current) {
        done();
        return;
      }
      release.current = done;
      retries.current++;
      setPending(false);
      setAttempt((value) => value + 1);
    });
  }
  useEffect(() => {
    if (!pending) {
      seen.current = view.revision;
      return;
    }
    if (seen.current === view.revision) return;
    const element = image.current;
    if (!element) return;
    const resume = () => {
      seen.current = view.revision;
      retries.current = 0;
      retry();
    };
    const rect = element.getBoundingClientRect();
    if (rect.bottom >= -100 && rect.top <= window.innerHeight + 100) {
      resume();
      return;
    }
    const observer = new IntersectionObserver(
      (entries) => {
        if (entries.some((entry) => entry.isIntersecting)) {
          observer.disconnect();
          resume();
        }
      },
      { rootMargin: "100px" },
    );
    observer.observe(element);
    return () => observer.disconnect();
  }, [view.revision, pending]);
  async function failed(event: SyntheticEvent<HTMLImageElement>) {
    deliveryRelease.current?.();
    release.current?.();
    release.current = null;
    delivered?.invalidate?.();
    if (retainOnScroll) setStartedDelivery(false);
    if (
      protectedSource &&
      (delivered?.mode === "oss" || delivered?.mode === "oss-blob") &&
      ossRefreshes.current < 1
    ) {
      ossRefreshes.current++;
      setAttempt((value) => value + 1);
      return;
    }
    if (!protectedSource || pending) {
      if (!pending) onError?.(event);
      return;
    }
    // React会在原事件派发后清空currentTarget，异步探测不能把空目标传回上传预览。
    const callbackEvent: SyntheticEvent<HTMLImageElement> = {
      ...event,
      currentTarget: event.currentTarget,
      preventDefault: () => event.preventDefault(),
      stopPropagation: () => event.stopPropagation(),
      isDefaultPrevented: () => event.isDefaultPrevented(),
      isPropagationStopped: () => event.isPropagationStopped(),
      persist: () => event.persist(),
    };
    const version = current.current;
    try {
      const state = await inspectMediaFailure();
      if (version !== current.current) return;
      if (!state.enabled || state.state === "allowed") {
        onError?.(callbackEvent);
        return;
      }
      setPending(true);
      if (state.state === "limited" && retries.current < 3) {
        timer.current = setTimeout(
          () => {
            if (version !== current.current) return;
            queueMediaRetry((done) => {
              if (version !== current.current) {
                done();
                return;
              }
              release.current = done;
              retries.current++;
              setPending(false);
              setAttempt((value) => value + 1);
            });
          },
          Math.max(1000, Math.min(60000, (state.retry_after ?? 3) * 1000)) +
            Math.random() * 500,
        );
      }
    } catch {
      if (version === current.current) onError?.(callbackEvent);
    }
  }
  let effective = protectedSource ? delivered?.url : src;
  if (protectedSource && delivered?.mode === "legacy" && attempt && effective) {
    const url = new URL(effective, window.location.href);
    url.searchParams.set("_media_retry", String(attempt));
    effective = url.pathname + url.search;
  }
  return (
    <img
      {...props}
      ref={image}
      loading={retainOnScroll && protectedSource && shouldLoad ? "eager" : props.loading}
      src={pending ? placeholder : ready ? effective : undefined}
      alt={pending ? "图片访问需要确认，请使用页面提示继续" : props.alt}
      data-media-state={pending ? "waiting" : undefined}
      onError={failed}
      onLoad={(event) => {
        if (pending) return;
        deliveryRelease.current?.();
        release.current?.();
        release.current = null;
        onLoad?.(event);
      }}
    />
  );
}

type WidgetAPI = {
  render(element: HTMLElement, options: Record<string, unknown>): string;
  remove(id: string): void;
};
declare global {
  interface Window {
    turnstile?: WidgetAPI;
  }
}
let scriptReady: Promise<WidgetAPI> | undefined;
export function loadWidget(): Promise<WidgetAPI> {
  if (window.turnstile) return Promise.resolve(window.turnstile);
  if (!scriptReady)
    scriptReady = new Promise<WidgetAPI>((resolve, reject) => {
      const script = document.createElement("script");
      script.src =
        "https://challenges.cloudflare.com/turnstile/v0/api.js?render=explicit";
      script.async = true;
      const timeout = setTimeout(() => {
        script.remove();
        reject(new Error("验证服务加载较慢，请稍后重试。"));
      }, 12000);
      script.onload = () => {
        clearTimeout(timeout);
        if (window.turnstile) resolve(window.turnstile);
        else reject(new Error("验证服务暂不可用。"));
      };
      script.onerror = () => {
        clearTimeout(timeout);
        script.remove();
        reject(new Error("验证服务暂不可用，请检查网络后重试。"));
      };
      document.head.appendChild(script);
    }).catch((error) => {
      scriptReady = undefined;
      throw error;
    });
  return scriptReady;
}
export function MediaVerificationDialog({
  identity,
  routeKey,
}: {
  identity: string;
  routeKey?: string;
}) {
  const view = useSyncExternalStore(
    subscribeAccess,
    accessSnapshot,
    accessSnapshot,
  );
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const [round, setRound] = useState(0);
  const container = useRef<HTMLDivElement>(null);
  const dialog = useRef<HTMLDivElement>(null);
  useEffect(() => {
    if (identity !== "loading") responseCrypto.setIdentity(identity);
    resetMediaIdentity();
    return () => resetMediaIdentity();
  }, [identity]);
  useEffect(() => {
    closeMediaVerification();
  }, [routeKey]);
  useEffect(() => {
    if (!view.open || view.status?.state !== "challenge") return;
    const controller = new AbortController();
    let widget: string | undefined;
    let api: WidgetAPI | undefined;
    let active = true;
    setError("");
    setBusy(true);
    void Promise.all([
      loadWidget(),
      safetyRequest<{ data: { id: string; action: string }; site_key: string }>(
        "challenges",
        {},
        controller.signal,
      ),
    ])
      .then(([instance, challenge]) => {
        if (!active || !container.current) return;
        api = instance;
        setBusy(false);
        widget = api.render(container.current, {
          sitekey: challenge.site_key,
          action: challenge.data.action,
          cData: challenge.data.id,
          theme: document.documentElement.classList.contains("dark")
            ? "dark"
            : "light",
          size: "flexible",
          language: "zh-cn",
          retry: "never",
          "refresh-expired": "manual",
          "refresh-timeout": "manual",
          callback: async (token: string) => {
            if (!active) return;
            setBusy(true);
            setError("");
            try {
              await safetyRequest(
                "challenges/verify",
                { id: challenge.data.id, token },
                controller.signal,
              );
              if (active) mediaVerificationComplete();
            } catch (failure) {
              if (active) {
                setBusy(false);
                setError(
                  failure instanceof Error
                    ? failure.message
                    : "验证未完成，请重试。",
                );
              }
            }
          },
          "error-callback": () => {
            if (active) {
              setBusy(false);
              setError("验证暂未完成，请重试。");
            }
            return true;
          },
          "expired-callback": () => {
            if (active) {
              setBusy(false);
              setError("验证已过期，请重新验证。");
            }
          },
          "timeout-callback": () => {
            if (active) {
              setBusy(false);
              setError("验证超时，请重新验证。");
            }
          },
        });
      })
      .catch((failure) => {
        if (active) {
          setBusy(false);
          setError(
            failure instanceof Error ? failure.message : "验证服务暂不可用。",
          );
        }
      });
    return () => {
      active = false;
      controller.abort();
      if (widget && api) api.remove(widget);
    };
  }, [view.open, view.status?.state, round]);
  useEffect(() => {
    if (!view.open) return;
    const previous = document.activeElement as HTMLElement | null;
    const overflow = document.body.style.overflow;
    document.body.style.overflow = "hidden";
    dialog.current?.focus();
    const key = (event: KeyboardEvent) => {
      if (event.key === "Escape") closeMediaVerification();
      if (event.key !== "Tab") return;
      const focusable = dialog.current?.querySelectorAll<HTMLElement>(
        'button:not(:disabled),iframe,[tabindex="0"]',
      );
      if (!focusable?.length) return;
      const first = focusable[0],
        last = focusable[focusable.length - 1];
      if (
        event.shiftKey &&
        (document.activeElement === first ||
          document.activeElement === dialog.current)
      ) {
        event.preventDefault();
        last.focus();
      } else if (!event.shiftKey && document.activeElement === last) {
        event.preventDefault();
        first.focus();
      }
    };
    document.addEventListener("keydown", key);
    return () => {
      document.removeEventListener("keydown", key);
      document.body.style.overflow = overflow;
      previous?.focus();
    };
  }, [view.open]);
  if (!view.status) return null;
  const challenge = view.status.state === "challenge";
  async function retryImages() {
    try {
      const state = await inspectMediaFailure();
      if (state.state === "challenge") openMediaVerification();
      else if (state.state === "allowed") mediaVerificationComplete();
    } catch {
      setError("图片服务暂不可用，请稍后重试。");
    }
  }
  return (
    <>
      {!view.open && (
        <aside className="read-safety-notice" aria-live="polite">
          <span>
            {challenge
              ? "完成安全验证后，继续查看图片。"
              : "图片请求较多，稍后可以继续查看。"}
          </span>
          <button
            onClick={() => {
              if (challenge) openMediaVerification();
              else void retryImages();
            }}
          >
            {challenge ? "继续验证" : "重试图片"}
          </button>
        </aside>
      )}
      {view.open && (
        <div className="read-safety-overlay" onClick={closeMediaVerification}>
          <div
            ref={dialog}
            className="read-safety-dialog"
            role="dialog"
            aria-modal="true"
            aria-labelledby="read-safety-title"
            tabIndex={-1}
            onClick={(event) => event.stopPropagation()}
          >
            <button
              className="read-safety-close"
              aria-label="暂时关闭验证"
              onClick={closeMediaVerification}
            >
              ×
            </button>
            <span className="read-safety-label">清叶 · 访问保护</span>
            <h2 id="read-safety-title">确认后继续浏览</h2>
            <p>
              当前图片请求较多，请完成一次安全验证。你的浏览位置和已加载图片会保留。
            </p>
            <div ref={container} className="read-safety-widget" />
            {busy && <p role="status">正在确认，请稍候…</p>}
            {error && (
              <p className="read-safety-error" role="alert">
                {error}
              </p>
            )}
            <div className="read-safety-actions">
              <button onClick={closeMediaVerification}>稍后再说</button>
              {error && (
                <button
                  disabled={busy}
                  onClick={() => setRound((value) => value + 1)}
                >
                  重新验证
                </button>
              )}
            </div>
            <small>验证仅用于访问保护，不会改变账号或商品权限。</small>
          </div>
        </div>
      )}
    </>
  );
}
