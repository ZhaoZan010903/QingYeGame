import { useCallback, useLayoutEffect, useRef } from "react";

type ConfirmOptions = {
  confirmLabel?: string;
  signal?: AbortSignal;
};

let activeDialog: HTMLDialogElement | null = null;

export function confirmAdmin(
  message: string,
  { confirmLabel = "确认", signal }: ConfirmOptions = {},
): Promise<boolean> {
  // Two handlers must never share an approval for different operations.
  if (activeDialog || signal?.aborted) return Promise.resolve(false);
  const dialog = document.createElement("dialog");
  activeDialog = dialog;
  const trigger = document.activeElement;
  dialog.className = "admin-confirm-dialog";
  dialog.setAttribute("aria-labelledby", "admin-confirm-title");
  dialog.setAttribute("aria-describedby", "admin-confirm-message");
  const title = document.createElement("h2");
  title.id = "admin-confirm-title";
  title.textContent = "确认操作";
  const description = document.createElement("p");
  description.id = "admin-confirm-message";
  description.textContent = message;
  const actions = document.createElement("div");
  actions.className = "admin-confirm-actions";
  const cancel = document.createElement("button");
  cancel.type = "button";
  cancel.className = "button-outline";
  cancel.textContent = "取消";
  cancel.autofocus = true;
  const confirm = document.createElement("button");
  confirm.type = "button";
  confirm.className = "button-white";
  confirm.textContent = confirmLabel;
  actions.append(cancel, confirm);
  dialog.append(title, description, actions);
  document.body.append(dialog);

  return new Promise<boolean>((resolve) => {
    function finish(approved: boolean) {
      if (activeDialog !== dialog) return;
      activeDialog = null;
      signal?.removeEventListener("abort", decline);
      if (dialog.open) dialog.close();
      dialog.remove();
      if (trigger instanceof HTMLElement && trigger.isConnected)
        trigger.focus({ preventScroll: true });
      resolve(approved);
    }
    function decline() {
      finish(false);
    }
    cancel.addEventListener("click", decline);
    confirm.addEventListener("click", () => finish(true));
    dialog.addEventListener("cancel", (event) => {
      event.preventDefault();
      decline();
    });
    dialog.addEventListener("close", decline);
    signal?.addEventListener("abort", decline, { once: true });
    try {
      dialog.showModal();
      cancel.focus({ preventScroll: true });
    } catch {
      decline();
    }
  });
}

// Leaving an editor, or switching its record, cancels its pending decision.
export function useAdminConfirm(scope?: string) {
  const owner = useRef<AbortController | null>(null);
  useLayoutEffect(() => {
    const controller = new AbortController();
    owner.current = controller;
    return () => {
      controller.abort();
      owner.current = null;
    };
  }, [scope]);
  return useCallback((message: string, confirmLabel: string) => {
    if (!owner.current) return Promise.resolve(false);
    return confirmAdmin(message, {
      confirmLabel,
      signal: owner.current.signal,
    });
  }, []);
}
