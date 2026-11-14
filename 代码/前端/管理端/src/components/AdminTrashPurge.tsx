import { useEffect, useRef, useState, type FormEvent } from "react";
import { AlertTriangle, RefreshCw, X } from "lucide-react";
import { adminApi } from "../lib/admin-api";
import { createRequestId } from "../lib/api";
import {
  purgeFailureKind,
  trashPreviewLimit,
  type TrashPreview,
  type PurgeResult,
  type SinglePurgeInput,
  type TrashPurgeInput,
} from "../lib/trash-purge";
import type { Product } from "../types";

export type PurgeTarget =
  { kind: "single"; product: Product } | { kind: "all" };
type Attempt =
  | { kind: "single"; id: string; input: SinglePurgeInput }
  | { kind: "all"; input: TrashPurgeInput };
const failureMessage = (error: unknown) =>
  error instanceof Error ? error.message : "操作失败，请重试。";

/** Wording is shared by the modal and the static rendering checks. */
export function PurgeScope({
  target,
  preview,
}: {
  target: PurgeTarget;
  preview: TrashPreview | null;
}) {
  return (
    <>
      {target.kind === "single" ? (
        <>
          <p className="product-action-code mono">
            {target.product.code || target.product.id}
          </p>
          <p className="product-action-title">
            {target.product.title.trim() || "未填写标题"}
          </p>
          <p>将永久删除这 1 个商品及其商品资料、商品快照，无法恢复为草稿。</p>
        </>
      ) : (
        <>
          <p>
            范围是<strong>整个回收站</strong>，不受当前搜索、筛选或页码限制。
          </p>
          {preview && (
            <div className="trash-purge-counts">
              <span>
                <strong>{preview.total.toLocaleString("zh-CN")}</strong> 个商品
              </span>
              <span>
                <strong>{preview.image_count.toLocaleString("zh-CN")}</strong>{" "}
              张关联及审核留存图片
              </span>
            </div>
          )}
          <p>
            提交前若回收站发生新增、恢复、删除或资料变化，需要重新预览并确认。
          </p>
        </>
      )}
      <div className="trash-purge-warning">
        <AlertTriangle size={20} aria-hidden="true" />
        <div>
          <strong>永久删除后不可恢复</strong>
          <p>
            商品独占图片会在后台清理，其他地方仍引用的图片会保留。用户聊天文字及必要操作记录保留，备份不会即时擦除。
          </p>
        </div>
      </div>
    </>
  );
}

export function AdminTrashPurge({
  target,
  onClose,
  onSuccess,
  onBusyChange,
}: {
  target: PurgeTarget;
  onClose: (refresh?: boolean) => void;
  onSuccess: (result: PurgeResult) => void;
  onBusyChange: (value: boolean) => void;
}) {
  const dialog = useRef<HTMLDialogElement>(null);
  const confirmationField = useRef<HTMLInputElement>(null);
  const [preview, setPreview] = useState<TrashPreview | null>(null);
  const [previewLoading, setPreviewLoading] = useState(target.kind === "all");
  const [previewError, setPreviewError] = useState("");
  const [previewRevision, setPreviewRevision] = useState(0);
  const [confirmation, setConfirmation] = useState("");
  const [reason, setReason] = useState("");
  const [error, setError] = useState("");
  const [failure, setFailure] = useState<ReturnType<
    typeof purgeFailureKind
  > | null>(null);
  const [busy, setBusy] = useState(false);
  const lock = useRef(false);
  const mounted = useRef(false);
  const attempt = useRef<Attempt | null>(null);
  const previewGeneration = useRef(0);
  const requiredText = target.kind === "all" ? "清空回收站" : "永久删除";
  const limitError = preview ? trashPreviewLimit(preview) : "";
  const ready =
    target.kind === "single" ||
    (!!preview &&
      preview.total > 0 &&
      !previewLoading &&
      !previewError &&
      !limitError);
  const frozen = busy || failure === "unknown" || failure === "refresh";
  const canSubmit =
    !busy &&
    !previewLoading &&
    (failure === "unknown" ||
      (ready &&
        failure !== "refresh" &&
        confirmation === requiredText &&
        !!reason.trim()));

  useEffect(() => {
    mounted.current = true;
    const previousFocus = document.activeElement;
    const element = dialog.current;
    element?.showModal();
    return () => {
      mounted.current = false;
      element?.close();
      if (previousFocus instanceof HTMLElement && previousFocus.isConnected)
        previousFocus.focus();
    };
  }, []);
  useEffect(() => {
    if (target.kind !== "all") return;
    const controller = new AbortController();
    const generation = ++previewGeneration.current;
    setPreview(null);
    setPreviewLoading(true);
    setPreviewError("");
    setConfirmation("");
    setError("");
    setFailure(null);
    attempt.current = null;
    adminApi
      .trashPreview(controller.signal)
      .then((result) => {
        if (
          !controller.signal.aborted &&
          previewGeneration.current === generation
        )
          setPreview(result);
      })
      .catch((error: unknown) => {
        if (
          !controller.signal.aborted &&
          previewGeneration.current === generation
        )
          setPreviewError(failureMessage(error));
      })
      .finally(() => {
        if (
          !controller.signal.aborted &&
          previewGeneration.current === generation
        )
          setPreviewLoading(false);
      });
    return () => {
      controller.abort();
      previewGeneration.current++;
    };
  }, [target, previewRevision]);

  function close() {
    if (!lock.current) onClose(failure === "unknown" || failure === "refresh");
  }
  async function submit(event: FormEvent) {
    event.preventDefault();
    if (lock.current || !canSubmit) return;
    lock.current = true;
    setBusy(true);
    onBusyChange(true);
    setError("");
    try {
      if (!attempt.current) {
        const common = { reason: reason.trim(), request_id: createRequestId() };
        attempt.current =
          target.kind === "single"
            ? {
                kind: "single",
                id: target.product.id,
                input: {
                  ...common,
                  expected_version: target.product.version,
                  confirmation: "永久删除",
                },
              }
            : {
                kind: "all",
                input: {
                  ...common,
                  snapshot: preview!.snapshot,
                  confirmation: "清空回收站",
                },
              };
      }
      // Preserve this exact attempt after an ambiguous response; a retry must not widen its scope.
      const current = attempt.current;
      const result =
        current.kind === "single"
          ? await adminApi.purgeProduct(current.id, current.input)
          : await adminApi.purgeTrash(current.input);
      if (mounted.current) onSuccess(result);
    } catch (error: unknown) {
      if (!mounted.current) return;
      const kind = purgeFailureKind(error);
      setFailure(kind);
      setError(failureMessage(error));
      if (kind !== "unknown") attempt.current = null;
    } finally {
      lock.current = false;
      if (mounted.current) {
        setBusy(false);
        onBusyChange(false);
      }
    }
  }
  return (
    <dialog
      ref={dialog}
      className="product-action-dialog trash-purge-dialog"
      aria-labelledby="trash-purge-heading"
      onCancel={(event) => {
        event.preventDefault();
        close();
      }}
    >
      <form onSubmit={submit}>
        <button
          className="product-dialog-close"
          type="button"
          aria-label="关闭永久删除确认"
          disabled={busy}
          onClick={close}
        >
          <X size={20} />
        </button>
        <h2 id="trash-purge-heading">
          {target.kind === "all" ? "清空回收站" : "永久删除商品"}
        </h2>
        <PurgeScope target={target} preview={preview} />
        {previewLoading && <p role="status">正在读取整个回收站的预览数量…</p>}
        {previewError && (
          <div className="trash-purge-error" role="alert">
            <p>{previewError}</p>
            <button
              type="button"
              className="button-outline"
              onClick={() => setPreviewRevision((value) => value + 1)}
            >
              <RefreshCw size={14} />
              重新预览
            </button>
          </div>
        )}
        {preview?.total === 0 && !previewLoading && (
          <p className="trash-purge-empty" role="status">
            回收站为空，没有可永久删除的商品。
          </p>
        )}
        {limitError && (
          <p className="form-error" role="alert">
            {limitError}
          </p>
        )}
        {ready && (
          <>
            <label>
              操作原因
              <textarea
                required
                disabled={frozen}
                rows={3}
                maxLength={1000}
                value={reason}
                onChange={(event) =>
                  setReason(
                    Array.from(event.target.value).slice(0, 500).join(""),
                  )
                }
                placeholder="填写本次永久删除的原因"
              />
              <small>最多 500 个字符。</small>
            </label>
            <label>
              请输入“{requiredText}”确认
              <input
                ref={confirmationField}
                required
                disabled={frozen}
                autoComplete="off"
                value={confirmation}
                onChange={(event) => setConfirmation(event.target.value)}
                aria-label="永久删除确认文字"
                placeholder={requiredText}
              />
            </label>
          </>
        )}
        {error && (
          <div className="trash-purge-error" role="alert">
            <p>{error}</p>
            {failure === "unknown" && (
              <p>
                结果尚未确认，服务器可能已处理。重试会继续同一次操作，不会扩大删除范围。确认文字和原因已锁定。
              </p>
            )}
            {failure === "refresh" && (
              <p>
                {target.kind === "all"
                  ? "回收站已变化，本次确认已失效。请重新预览数量并输入确认文字。"
                  : "商品状态或版本已变化，本次确认已失效。请关闭并刷新列表后重新选择。"}
              </p>
            )}
          </div>
        )}
        <div className="product-dialog-actions">
          <button
            className="button-outline"
            type="button"
            disabled={busy}
            onClick={close}
          >
            {failure === "unknown" || failure === "refresh"
              ? "关闭并刷新列表"
              : "取消"}
          </button>
          {failure === "refresh" && target.kind === "all" ? (
            <button
              type="button"
              className="button-outline"
              onClick={() => setPreviewRevision((value) => value + 1)}
            >
              重新预览
            </button>
          ) : (
            <button className="product-danger-button" disabled={!canSubmit}>
              {busy
                ? "正在永久删除…"
                : failure === "unknown"
                  ? "重试本次删除"
                  : target.kind === "all"
                    ? "确认清空回收站"
                    : "确认永久删除"}
            </button>
          )}
        </div>
      </form>
    </dialog>
  );
}
