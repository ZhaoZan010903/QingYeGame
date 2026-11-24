import { Check, ChevronDown, X } from "lucide-react";
import { useEffect, useId, useRef, useState, type KeyboardEvent } from "react";

const modes = {
  preview: "先预览，核对后发布",
  automatic: "通过检查后自动发布",
  paused: "暂停同步",
};
export type LegacySyncMode = keyof typeof modes;
const options = Object.keys(modes) as LegacySyncMode[];

export function LegacySyncModePicker({
  value,
  disabled,
  onChange,
}: {
  value: LegacySyncMode;
  disabled: boolean;
  onChange: (mode: LegacySyncMode) => void;
}) {
  const [open, setOpen] = useState(false);
  const wrapper = useRef<HTMLDivElement>(null);
  const button = useRef<HTMLButtonElement>(null);
  const menuItems = useRef<(HTMLButtonElement | null)[]>([]);
  const first = useRef(0);
  const id = useId();

  useEffect(() => {
    if (!open || disabled) return;
    menuItems.current[first.current]?.focus();
    function closeOutside(event: PointerEvent) {
      if (
        event.target instanceof Node &&
        !wrapper.current?.contains(event.target)
      )
        setOpen(false);
    }
    document.addEventListener("pointerdown", closeOutside);
    return () => document.removeEventListener("pointerdown", closeOutside);
  }, [open, disabled]);

  function show(index: number) {
    first.current = index;
    setOpen(true);
  }

  function close() {
    setOpen(false);
    button.current?.focus();
  }

  function move(event: KeyboardEvent) {
    if (event.key === "Escape") {
      event.preventDefault();
      close();
      return;
    }
    if (!["ArrowDown", "ArrowUp", "Home", "End"].includes(event.key)) return;
    event.preventDefault();
    const current = menuItems.current.indexOf(
      document.activeElement as HTMLButtonElement,
    );
    const last = options.length - 1;
    const next =
      event.key === "Home"
        ? 0
        : event.key === "End"
          ? last
          : (current + options.length + (event.key === "ArrowUp" ? -1 : 1)) %
            options.length;
    menuItems.current[next]?.focus();
  }

  return (
    <div
      className="legacy-sync-mode"
      ref={wrapper}
      onBlur={(event) => {
        if (!event.currentTarget.contains(event.relatedTarget)) setOpen(false);
      }}
    >
      <span id={id + "-label"}>同步方式</span>
      <button
        type="button"
        ref={button}
        className="button-outline legacy-sync-mode-button"
        aria-labelledby={id + "-label " + id + "-value"}
        aria-haspopup="menu"
        aria-expanded={open && !disabled}
        aria-controls={open && !disabled ? id + "-menu" : undefined}
        disabled={disabled}
        onClick={() => (open ? setOpen(false) : show(options.indexOf(value)))}
        onKeyDown={(event) => {
          if (event.key === "ArrowDown" || event.key === "ArrowUp") {
            event.preventDefault();
            if (open) {
              menuItems.current[
                event.key === "ArrowUp" ? options.length - 1 : 0
              ]?.focus();
            } else {
              show(event.key === "ArrowUp" ? options.length - 1 : 0);
            }
          }
        }}
      >
        <span id={id + "-value"}>{modes[value]}</span>
        <ChevronDown size={16} aria-hidden="true" />
      </button>
      {open && !disabled && (
        <div
          id={id + "-menu"}
          className="legacy-sync-mode-menu"
          role="menu"
          aria-labelledby={id + "-label"}
          onKeyDown={move}
        >
          {options.map((mode, index) => (
            <button
              key={mode}
              ref={(node) => {
                menuItems.current[index] = node;
              }}
              type="button"
              role="menuitemradio"
              tabIndex={-1}
              aria-checked={value === mode}
              onClick={() => {
                close();
                if (value !== mode) onChange(mode);
              }}
            >
              {modes[mode]}
              {value === mode && <Check size={16} aria-hidden="true" />}
            </button>
          ))}
        </div>
      )}
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
