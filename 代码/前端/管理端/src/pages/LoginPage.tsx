import { useState, type FormEvent } from "react";
import { Navigate, useNavigate } from "react-router-dom";
import { ShieldCheck } from "lucide-react";
import { api } from "../lib/api";
import { useSession } from "../context";
import { StateView } from "../components/shared";
export function LoginPage() {
  const { user, loading, refresh, loginNotice } = useSession();
  const navigate = useNavigate();
  const notice =
    loginNotice &&
    {
      "password-changed": "密码已修改，请使用新密码重新登录。",
      "password-uncertain": "登录已失效；如果上次修改已完成，请用新密码登录。",
      "session-expired": "登录已失效，请重新登录。",
    }[loginNotice];
  const [phone, setPhone] = useState(""),
    [password, setPassword] = useState(""),
    [error, setError] = useState(""),
    [busy, setBusy] = useState(false);
  async function submit(event: FormEvent) {
    event.preventDefault();
    setBusy(true);
    setError("");
    try {
      await api.login(phone, password);
      await refresh();
      navigate("/", { replace: true });
    } catch (e) {
      setError(e instanceof Error ? e.message : "登录失败，请重试。");
    } finally {
      setBusy(false);
    }
  }
  if (loading) return <StateView loading />;
  if (user?.role === "admin") return <Navigate replace to="/" />;
  return (
    <section className="admin-login panel">
      <span className="admin-login-icon" aria-hidden="true">
        <ShieldCheck size={24} />
      </span>
      <h1>管理员登录</h1>
      <p className="muted">使用已开通的管理员账户进入独立管理环境。</p>
      {notice && (
        <p className="admin-login-notice" role="status">
          {notice}
        </p>
      )}
      <form className="member-form" onSubmit={submit}>
        <fieldset disabled={busy}>
          <label className="form-field">
            管理员账号
            <input
              type="text"
              autoComplete="username"
              placeholder="账号名或手机号"
              value={phone}
              onChange={(e) => setPhone(e.target.value)}
              maxLength={32}
              autoCapitalize="none"
              spellCheck={false}
              required
            />
          </label>
          <label className="form-field">
            密码
            <input
              type="password"
              autoComplete="current-password"
              value={password}
              onChange={(e) => setPassword(e.target.value)}
              required
              maxLength={72}
            />
          </label>
          {error && (
            <p className="form-error" role="alert">
              {error}
            </p>
          )}
          <button className="button-white" type="submit">
            {busy ? "正在验证…" : "登录管理台"}
          </button>
        </fieldset>
      </form>
    </section>
  );
}
