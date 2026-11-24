import React, { lazy, Suspense, type ReactNode, useState } from "react";
import ReactDOM from "react-dom/client";
import { ArrowUp, ArrowUpRight, LogOut } from "lucide-react";
import {
  createBrowserRouter,
  RouterProvider,
  Routes,
  Route,
  Navigate,
  Link,
  useNavigate,
  useLocation,
} from "react-router-dom";
import { Providers, useSession } from "./context";
import { MediaVerificationDialog } from "../../共享/MediaSecurity";
import { api } from "./lib/api";
import { publicSite } from "./lib/admin-ux";
import { StateView } from "./components/shared";
import { LoginPage } from "./pages/LoginPage";
import { ChatProvider, ChatNavLink } from "./components/ChatNotifications";
import "./style.css";
const AdminPage = lazy(() =>
  import("./pages/AdminPage").then((module) => ({ default: module.AdminPage })),
);
function Protected({ children }: { children: ReactNode }) {
  const { user, loading, error, refresh } = useSession();
  return loading || error ? (
    <main id="admin-content" tabIndex={-1}>
      <StateView
        loading={loading}
        error={error}
        onRetry={() => void refresh()}
      />
    </main>
  ) : user?.role === "admin" ? (
    <>{children}</>
  ) : (
    <Navigate replace to="/login" />
  );
}
function Shell() {
  const { user, refresh } = useSession();
  const navigate = useNavigate();
  const location = useLocation();
  const [error, setError] = useState(""),
    [busy, setBusy] = useState(false);
  async function logout() {
    if (
      !window.dispatchEvent(
        new Event("qingye-before-logout", { cancelable: true }),
      )
    )
      return;
    setBusy(true);
    setError("");
    try {
      await api.logout();
      await refresh();
      navigate("/login");
    } catch (e) {
      setError(e instanceof Error ? e.message : "退出失败，请重试。");
    } finally {
      setBusy(false);
    }
  }
  return (
    <div className="admin-app">
      <MediaVerificationDialog
        identity={user?.id ?? "anonymous"}
        routeKey={location.key}
      />
      <a className="admin-skip-link" href="#admin-content">
        跳到主要内容
      </a>
      <header className="admin-topbar">
        <div className="admin-topbar-inner">
          <Link className="admin-brand" to="/">
            <span className="admin-brand-mark" aria-hidden="true">
              <svg viewBox="0 0 33 32" fill="none">
                <path d="M23 6H11L5 16l6 10h12l5-10-5-10Z" />
                <path d="m11 19 5-9 5 9h-10Zm7 3 5 5" />
              </svg>
            </span>
            <strong>清叶</strong>
            <span className="admin-brand-label">管理后台</span>
          </Link>
          {user && (
            <div className="admin-session">
              <span className="environment-badge">
                {import.meta.env.VITE_ENVIRONMENT_LABEL || "管理环境"}
              </span>
              <ChatNavLink />
              <Link className="topbar-help" to="/help">
                操作指南
              </Link>
              <span className="admin-account" title={user.nickname}>
                <span className="admin-account-mark" aria-hidden="true">
                  {Array.from(user.nickname?.trim() ?? "")[0] || "管"}
                </span>
                <span className="admin-account-name">{user.nickname}</span>
              </span>
              <button
                className="button-outline admin-logout"
                type="button"
                aria-label="退出管理台"
                disabled={busy}
                onClick={() => void logout()}
              >
                <LogOut size={16} aria-hidden="true" />
                <span>退出管理台</span>
              </button>
            </div>
          )}
        </div>
      </header>
      {error && (
        <p className="form-error" role="alert">
          {error}
        </p>
      )}
      <div className="admin-app-content">
        <Suspense
          fallback={
            <main id="admin-content" tabIndex={-1}>
              <StateView loading />
            </main>
          }
        >
          <Routes>
            <Route
              path="login"
              element={
                <main className="admin-auth" id="admin-content" tabIndex={-1}>
                  <LoginPage />
                </main>
              }
            />
            <Route
              path="*"
              element={
                <Protected>
                  <AdminPage />
                </Protected>
              }
            />
          </Routes>
        </Suspense>
      </div>
      {user?.role === "admin" && (
        <footer className="admin-footer">
          <div className="admin-footer-inner">
            <nav aria-label="页脚导航">
              <Link to="/help">操作演示</Link>
              {publicSite && (
                <a href={publicSite} target="_blank" rel="noreferrer">
                  查看用户网站 <ArrowUpRight size={14} aria-hidden="true" />
                </a>
              )}
            </nav>
            <button
              className="text-button"
              type="button"
              onClick={() => window.scrollTo({ top: 0 })}
            >
              <ArrowUp size={15} aria-hidden="true" /> 回到顶部
            </button>
          </div>
        </footer>
      )}
    </div>
  );
}
class ErrorBoundary extends React.Component<
  { children: ReactNode },
  { failed: boolean }
> {
  state = { failed: false };
  static getDerivedStateFromError() {
    return { failed: true };
  }
  render() {
    return this.state.failed ? (
      <div className="state-view">
        <h1>管理页面加载失败</h1>
        <button className="button-outline" onClick={() => location.reload()}>
          重新加载
        </button>
      </div>
    ) : (
      this.props.children
    );
  }
}
const router = createBrowserRouter([
  {
    path: "*",
    element: (
      <Providers>
        <ChatProvider>
          <Shell />
        </ChatProvider>
      </Providers>
    ),
  },
]);
ReactDOM.createRoot(document.getElementById("root")!).render(
  <React.StrictMode>
    <ErrorBoundary>
      <RouterProvider router={router} />
    </ErrorBoundary>
  </React.StrictMode>,
);
