import { useEffect, useRef, useState } from "react";
import { adminApi, type AdminRecord } from "../lib/admin-api";
import { publicationProgress } from "../../../共享/upload-progress";
import "../../../共享/upload-progress.css";

/** Count only verified images; a completed transfer alone is not a completed publication. */
export function MediaSyncPanel({
  productId,
  onStateChange,
}: {
  productId: string;
  onStateChange?: (state: AdminRecord) => void;
}) {
  const [state, setState] = useState<AdminRecord | null>(null);
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const [loadingStatus, setLoadingStatus] = useState(false);
  const [refresh, setRefresh] = useState(0);
  const callback = useRef(onStateChange);
  callback.current = onStateChange;
  const lastProduct = useRef(productId);
  useEffect(() => {
    let alive = true;
    let loading = false;
    let timer: ReturnType<typeof setTimeout>;
    let last: AdminRecord | null = null;
    if (lastProduct.current !== productId) {
      lastProduct.current = productId;
      setState(null);
    }
    async function load() {
      if (!alive || loading) return;
      loading = true;
      setLoadingStatus(true);
      clearTimeout(timer);
      try {
        const next = await adminApi.mediaSync(productId);
        if (!alive) return;
        setState(next);
        setError("");
        if (
          next.status !== last?.status ||
          next.version !== last?.version ||
          next.interrupted !== last?.interrupted
        )
          callback.current?.(next);
        last = next;
      } catch (error) {
        if (alive)
          setError(
            error instanceof Error ? error.message : "无法读取图片准备状态",
          );
      } finally {
        loading = false;
        if (alive) setLoadingStatus(false);
        // Hidden pages stop polling; coming back refreshes immediately, without overlapping requests.
        if (
          alive &&
          document.visibilityState === "visible" &&
          (!last || last.status === "publishing")
        )
          timer = setTimeout(load, last?.interrupted ? 10000 : 2000);
      }
    }
    const visible = () => {
      clearTimeout(timer);
      if (document.visibilityState === "visible") void load();
    };
    document.addEventListener("visibilitychange", visible);
    if (document.visibilityState === "visible") void load();
    return () => {
      alive = false;
      clearTimeout(timer);
      document.removeEventListener("visibilitychange", visible);
    };
  }, [productId, refresh]);

  async function retry() {
    if (
      !state ||
      state.interrupted ||
      state.status !== "publishing" ||
      !Number.isInteger(state.version)
    )
      return;
    setBusy(true);
    try {
      await adminApi.retryMediaSync(productId, state.version);
      setRefresh((value) => value + 1);
    } catch (error) {
      setError(error instanceof Error ? error.message : "重试未提交");
    } finally {
      setBusy(false);
    }
  }
  const percent = publicationProgress(
    Number(state?.ready ?? 0),
    Number(state?.total ?? 0),
  );
  const interrupted = state?.interrupted === true;
  return (
    <section className="location-note" aria-label="图片上架准备">
      <strong>{interrupted ? "上架已中止" : "图片上架准备"}</strong>
      <p>
        {interrupted
          ? "部分图片已被清理或停止使用。请取消上架，整理有效图片后再提交。"
          : "正在将图片从 ECS 同步至 OSS。全部图片校验完成后，商品才会公开展示；离开此页不影响后台处理。"}
      </p>
      {state ? (
        <>
          <div className="media-sync-progress" data-interrupted={interrupted}>
            <div className="upload-progress-heading">
              <strong>
                {interrupted
                  ? "已停止"
                  : percent === 100 && state.status === "publishing"
                    ? "校验完成，正在完成上架"
                    : "已同步并校验"}
              </strong>
              <span>{percent}%</span>
            </div>
            <progress
              value={percent}
              max={100}
              aria-label="ECS 至 OSS 图片同步校验进度"
            />
            <div className="upload-progress-details">
              <span>
                已校验 {state.ready} / {state.total} 张
              </span>
              <span>失败 {state.failed}</span>
            </div>
            <small>
              {state.total > 0
                ? "进度按校验成功的图片计数，单张图片传输时进度会短暂停留。"
                : "正在确认图片状态，是否上架以商品状态为准。"}
            </small>
          </div>
          {state.error && <p role="status">最近原因：{state.error}</p>}
          {state.next_retry && !interrupted && (
            <p>
              下次重试：{new Date(state.next_retry).toLocaleString("zh-CN")}
            </p>
          )}
          {!!state.failed && !interrupted && state.status === "publishing" && (
            <button
              type="button"
              className="button-outline"
              disabled={busy}
              onClick={() => void retry()}
            >
              重试图片准备
            </button>
          )}
        </>
      ) : (
        <p role="status">正在读取同步进度…</p>
      )}
      {error && (
        <p className="form-error" role="alert">
          {error}
        </p>
      )}
      <button
        type="button"
        className="text-button"
        disabled={busy || loadingStatus}
        onClick={() => setRefresh((value) => value + 1)}
      >
        刷新状态
      </button>
    </section>
  );
}
