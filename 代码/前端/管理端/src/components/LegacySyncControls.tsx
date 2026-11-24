import { useEffect, useId, useRef } from "react";
import { X } from "lucide-react";

export type SyncProgress = {
  stage:
    | "read_source"
    | "verify_source"
    | "compare"
    | "prepare_media"
    | "commit"
    | "done";
  completed: number;
  total: number;
};

const steps = {
  read_source: { label: "读取旧站目录", unit: "页", from: 0, to: 22 },
  verify_source: { label: "复核旧站目录", unit: "页", from: 22, to: 38 },
  compare: { label: "比对商品差异", unit: "件", from: 38, to: 58 },
  prepare_media: { label: "准备商品图集", unit: "组", from: 58, to: 92 },
  commit: { label: "提交本批变更", unit: "", from: 95, to: 95 },
  done: { label: "同步完成", unit: "", from: 100, to: 100 },
} satisfies Record<
  SyncProgress["stage"],
  { label: string; unit: string; from: number; to: number }
>;

export function LegacySyncProgress({
  progress,
  running,
  failed,
}: {
  progress: SyncProgress;
  running: boolean;
  failed: boolean;
}) {
  const step = steps[progress.stage];
  const fraction =
    progress.total > 0 ? Math.min(progress.completed / progress.total, 1) : 0;
  const percent = Math.round(step.from + (step.to - step.from) * fraction);
  const count =
    progress.total > 0 && step.unit
      ? progress.completed + " / " + progress.total + " " + step.unit
      : "";
  return (
    <div className="legacy-sync-progress">
      <div className="legacy-sync-progress-caption">
        <strong aria-live="polite">
          {failed ? "中止于" + step.label : step.label}
        </strong>
        <span className="legacy-sync-numbers">
          {count && <span>{count}</span>}
          <b>{percent}%</b>
        </span>
      </div>
      <div
        className="legacy-sync-track"
        role="progressbar"
        aria-label="商品同步进度"
        aria-valuemin={0}
        aria-valuemax={100}
        aria-valuenow={percent}
        aria-valuetext={[step.label, count, percent + "%"]
          .filter(Boolean)
          .join("，")}
        data-running={running}
        data-failed={failed}
      >
        <span style={{ width: percent + "%" }} />
      </div>
    </div>
  );
}

export type LegacySyncConfirmation = {
  title: string;
  message: string;
  confirmLabel: string;
  confirm: () => void;
};

export function LegacySyncConfirm({
  confirmation,
  onClose,
}: {
  confirmation: LegacySyncConfirmation;
  onClose: () => void;
}) {
  const dialog = useRef<HTMLDialogElement>(null);
  const cancel = useRef<HTMLButtonElement>(null);
  const id = useId();
  useEffect(() => {
    const element = dialog.current;
    if (!element) return;
    element.showModal();
    cancel.current?.focus();
    return () => element.close();
  }, []);

  return (
    <dialog
      ref={dialog}
      className="product-action-dialog legacy-sync-dialog"
      aria-labelledby={id + "-title"}
      aria-describedby={id + "-description"}
      onCancel={(event) => {
        event.preventDefault();
        onClose();
      }}
    >
      <form
        onSubmit={(event) => {
          event.preventDefault();
          onClose();
          confirmation.confirm();
        }}
      >
        <button
          type="button"
          className="product-dialog-close"
          aria-label="关闭确认"
          onClick={onClose}
        >
          <X size={20} aria-hidden="true" />
        </button>
        <h2 id={id + "-title"}>{confirmation.title}</h2>
        <p id={id + "-description"}>{confirmation.message}</p>
        <div className="product-dialog-actions">
          <button
            ref={cancel}
            type="button"
            className="button-outline"
            onClick={onClose}
          >
            取消
          </button>
          <button type="submit" className="button-white">
            {confirmation.confirmLabel}
          </button>
        </div>
      </form>
    </dialog>
  );
}
