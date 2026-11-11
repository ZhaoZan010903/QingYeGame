import { useEffect, useRef, useState } from "react";
import { adminApi } from "../lib/admin-api";

export function PrivateCredentials({ id }: { id: string }) {
  const [value, setValue] = useState<{
    qq_account: string;
    qq_password: string;
  } | null>(null);
  const [busy, setBusy] = useState(false),
    [error, setError] = useState("");
  const generation = useRef(0);
  useEffect(() => {
    generation.current++;
    setValue(null);
    setBusy(false);
    setError("");
    return () => {
      generation.current++;
    };
  }, [id]);
  useEffect(() => {
    if (!value) return;
    const timer = setTimeout(() => setValue(null), 30000);
    return () => clearTimeout(timer);
  }, [value]);
  async function reveal() {
    const current = generation.current;
    setBusy(true);
    setError("");
    try {
      const result = await adminApi.privateCredentials(id);
      if (current === generation.current) setValue(result);
    } catch (e) {
      if (current === generation.current)
        setError(e instanceof Error ? e.message : "私密资料读取失败");
    } finally {
      if (current === generation.current) setBusy(false);
    }
  }
  return (
    <section className="subform-card private-credentials">
      <h3>账号私密资料</h3>
      <p className="field-hint">
        仅限授权管理员核验，查看会被审计，30 秒后自动隐藏。
      </p>
      {value ? (
        <>
          <dl>
            <dt>QQ 账号</dt>
            <dd>{value.qq_account}</dd>
            <dt>QQ 密码</dt>
            <dd>{value.qq_password}</dd>
          </dl>
          <button className="button-outline" onClick={() => setValue(null)}>
            立即隐藏
          </button>
        </>
      ) : (
        <button
          className="button-outline"
          disabled={busy}
          onClick={() => void reveal()}
        >
          {busy ? "读取中…" : "查看私密资料"}
        </button>
      )}
      {error && (
        <p className="form-error" role="alert">
          {error}
        </p>
      )}
    </section>
  );
}
