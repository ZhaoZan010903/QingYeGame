import React, { lazy, Suspense, type ReactNode, useState } from "react";
import ReactDOM from "react-dom/client";
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
import { MediaVerificationDialog } from '../../共享/MediaSecurity';
import { api } from "./lib/api";
import { StateView } from "./components/shared";
import { LoginPage } from "./pages/LoginPage";
import { ChatProvider, ChatNavLink } from "./components/ChatNotifications";
import "./style.css";
const AdminPage = lazy(() =>
  import("./pages/AdminPage").then((module) => ({ default: module.AdminPage })),
);
function Protected({ children }: { children: ReactNode }) {
  const { user, loading, error, refresh } = useSession();
  return loading ? (
    <StateView loading />
  ) : error ? (
    <StateView error={error} onRetry={() => void refresh()} />
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
    <>
      <MediaVerificationDialog identity={user?.id ?? 'anonymous'} routeKey={location.key} />
      <header className="admin-topbar">
        <Link className="admin-brand" to="/">
          <svg viewBox="0 0 33 32" fill="none" aria-hidden="true">
            <path d="M23 6H11L5 16l6 10h12l5-10-5-10Z" />
            <path d="m11 19 5-9 5 9h-10Zm7 3 5 5" />
          </svg>
          <strong>清叶</strong>
          <span>管理后台</span>
        </Link>
        {user && (
          <div className="admin-session">
            <ChatNavLink />
            <span className="environment-badge">
              {import.meta.env.VITE_ENVIRONMENT_LABEL || "管理环境"}
            </span>
            <Link className="topbar-help" to="/help">
              操作指南
            </Link>
            <span>{user.nickname}</span>
            <button
              className="button-outline"
              disabled={busy}
              onClick={() => void logout()}
            >
              退出管理台
            </button>
          </div>
        )}
      </header>
      {error && (
        <p className="form-error" role="alert">
          {error}
        </p>
      )}
      <main>
        <Suspense fallback={<StateView loading />}>
          <Routes>
            <Route path="login" element={<LoginPage />} />
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
      </main>
    </>
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
