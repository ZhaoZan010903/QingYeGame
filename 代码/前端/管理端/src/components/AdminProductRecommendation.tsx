import { useEffect, useRef, useState, type FormEvent } from "react";
import { X } from "lucide-react";
import { ApiError } from "../lib/api";
import { adminApi } from "../lib/admin-api";
import {
  parseRecommendationBoost,
  readRecommendation,
  saveRecommendation,
  type RecommendationSnapshot,
} from "../lib/product-recommendation";
import type { Product } from "../types";

export function AdminProductRecommendation({
  product,
  onClose,
  onSaved,
}: {
  product: Product;
  onClose: () => void;
  onSaved: (snapshot: RecommendationSnapshot) => void;
}) {
  const dialog = useRef<HTMLDialogElement>(null);
  const [snapshot, setSnapshot] = useState<RecommendationSnapshot>({
    id: product.id,
    boost: product.recommendationBoost ?? 0,
    version: product.recommendationVersion ?? 1,
  });
  const [value, setValue] = useState(String(snapshot.boost));
  const [busy, setBusy] = useState(false);
  const [needsReload, setNeedsReload] = useState(false);
  const [error, setError] = useState("");
  const [notice, setNotice] = useState("");
  useEffect(() => {
    dialog.current?.showModal();
  }, []);

  async function save(event: FormEvent) {
    event.preventDefault();
    if (busy || needsReload) return;
    setError("");
    setNotice("");
    let boost: number;
    try {
      boost = parseRecommendationBoost(value);
    } catch (error) {
      setError((error as Error).message);
      return;
    }
    setBusy(true);
    try {
      const next = await saveRecommendation(
        snapshot.id,
        boost,
        snapshot.version,
      );
      setSnapshot(next);
      setValue(String(next.boost));
      onSaved(next);
      setNotice("推荐加分已保存，商品上架与审核状态保持不变。");
    } catch (error) {
      const knownRejection =
        error instanceof ApiError &&
        [400, 401, 403, 404, 409, 422, 429].includes(error.status);
      setNeedsReload(
        !knownRejection || (error instanceof ApiError && error.status === 409),
      );
      setError(
        !knownRejection
          ? "保存结果尚未确认。请重新读取当前设置，核对后再决定是否修改。"
          : error instanceof ApiError && error.status === 409
            ? "推荐设置已被其他操作更新。当前输入仍保留，请重新读取并核对最新设置。"
            : (error as Error).message,
      );
    } finally {
      setBusy(false);
    }
  }

  async function reload() {
    if (busy) return;
    setBusy(true);
    setError("");
    try {
      const next = readRecommendation(await adminApi.product(snapshot.id));
      setSnapshot(next);
      setValue(String(next.boost));
      setNeedsReload(false);
      onSaved(next);
      setNotice(`已读取当前推荐加分：${next.boost}。请核对后再决定是否修改。`);
    } catch (error) {
      setError((error as Error).message);
    } finally {
      setBusy(false);
    }
  }

  return (
    <dialog
      ref={dialog}
      className="product-action-dialog recommendation-dialog"
      aria-label="设置推荐加分"
      onCancel={(event) => {
        event.preventDefault();
        if (!busy) onClose();
      }}
    >
      <button
        type="button"
        className="product-dialog-close"
        aria-label="关闭推荐设置"
        disabled={busy}
        onClick={onClose}
      >
        <X size={20} />
      </button>
      <h2>设置推荐加分</h2>
      <p className="product-action-code mono">{product.code || product.id}</p>
      <p className="product-action-title">{product.title}</p>
      <form onSubmit={(event) => void save(event)}>
        <label>
          额外推荐加分（0–100）
          <input
            type="number"
            inputMode="numeric"
            min="0"
            max="100"
            step="1"
            required
            value={value}
            disabled={busy || needsReload}
            onChange={(event) => {
              setValue(event.target.value);
              setNotice("");
            }}
          />
        </label>
        <p>
          默认
          0，表示不额外加分。数值越高，精选、热门和为你推荐获得的加分越多；自然热度较高的商品仍可能排在前面。
        </p>
        <p>
          独立保存，立即参与推荐排序；不会改变价格、最新上架排序或手动轮播。
        </p>
        {error && (
          <p className="form-error" role="alert">
            {error}
          </p>
        )}
        {notice && (
          <p className="admin-success" role="status">
            {notice}
          </p>
        )}
        {needsReload && (
          <button
            type="button"
            className="button-outline"
            disabled={busy}
            onClick={() => void reload()}
          >
            重新读取当前设置
          </button>
        )}
        <div className="product-dialog-actions">
          <button
            type="button"
            className="button-outline"
            disabled={busy}
            onClick={onClose}
          >
            关闭
          </button>
          <button
            type="submit"
            className="button-white"
            disabled={busy || needsReload || value === String(snapshot.boost)}
          >
            {busy ? "处理中…" : "保存推荐加分"}
          </button>
        </div>
      </form>
    </dialog>
  );
}
