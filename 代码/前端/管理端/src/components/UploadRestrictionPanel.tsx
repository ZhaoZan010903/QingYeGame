import { useEffect, useState } from "react";
import { adminApi, type AdminRecord } from "../lib/admin-api";

export function UploadRestrictionPanel({
  member,
  close,
}: {
  member: AdminRecord;
  close: () => void;
}) {
  const [current, setCurrent] = useState<AdminRecord | null>(null);
  const [blocked, setBlocked] = useState(false);
  const [reason, setReason] = useState("");
  const [purge, setPurge] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  useEffect(() => {
    let alive = true;
    setCurrent(null);
    setPurge(false);
    setError("");
    void adminApi
      .uploadRestriction(member.id)
      .then((value) => {
        if (alive) {
          setCurrent(value);
          setBlocked(!!value.blocked);
          setReason(value.reason ?? "");
        }
      })
      .catch((error) => {
        if (alive) setError(error.message);
      });
    return () => {
      alive = false;
    };
  }, [member.id]);
  async function save() {
    if (!current || !reason.trim()) {
      setError("请填写本次处置原因。");
      return;
    }
    if (
      purge &&
      !window.confirm(
        `确认已核实「${member.nickname}」恶意上传，并清理该账号的服务器商品暂存图片？此操作不可恢复；不涉及OSS已上架图片、历史迁移原图或其他会员。`,
      )
    )
      return;
    setBusy(true);
    setError("");
    try {
      await adminApi.saveUploadRestriction(member.id, {
        blocked,
        reason: reason.trim(),
        expected_version: current.version,
        purge_staging: blocked && purge,
      });
      close();
    } catch (error) {
      setError(
        error instanceof Error ? error.message : "保存失败，请重新读取状态",
      );
    } finally {
      setBusy(false);
    }
  }
  return (
    <section className="admin-actions-panel" aria-label="会员上传权限">
      <h2>{member.nickname} · 上传权限</h2>
      <p>
        单独限制图片上传，保留登录与咨询功能。共享网络或单次限流不作为恶意上传依据。
      </p>
      {!current && !error && <p role="status">正在读取…</p>}
      {current && (
        <>
          <label className="checkbox-option">
            <input
              type="checkbox"
              checked={blocked}
              disabled={busy}
              onChange={(event) => {
                setBlocked(event.target.checked);
                if (!event.target.checked) setPurge(false);
              }}
            />
            停用上传权限
          </label>
          <label className="form-field">
            处置原因
            <textarea
              value={reason}
              maxLength={500}
              disabled={busy}
              onChange={(event) => setReason(event.target.value)}
            />
          </label>
          {blocked && (
            <label className="checkbox-option">
              <input
                type="checkbox"
                checked={purge}
                disabled={busy}
                onChange={(event) => setPurge(event.target.checked)}
              />
              已核实恶意上传，同时清理该账号的服务器商品暂存图片
            </label>
          )}
          <button
            type="button"
            className="button-white"
            disabled={busy || !reason.trim()}
            onClick={() => void save()}
          >
            {busy ? "正在保存…" : "保存上传权限"}
          </button>
        </>
      )}
      {error && (
        <p className="form-error" role="alert">
          {error}
        </p>
      )}
      <button
        type="button"
        className="button-outline"
        disabled={busy}
        onClick={close}
      >
        关闭
      </button>
    </section>
  );
}
