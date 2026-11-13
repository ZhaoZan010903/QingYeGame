import React, { lazy, type ReactNode } from 'react';
import ReactDOM from 'react-dom/client';
import { BrowserRouter, Routes, Route, Navigate, useLocation, Link } from 'react-router-dom';
import { Providers, useSession } from './context';
import { MediaVerificationDialog } from '../../共享/MediaSecurity';
import { Layout } from './components/Layout';
import { StateView } from './components/shared';
import { HomePage } from './pages/HomePage';
import { ChatProvider } from './components/ChatNotifications';
const ChatPage = lazy(() => import('./components/ChatPage').then((m) => ({ default: m.ChatPage })));
import './style.css';
import './refinements.css';
import './enhancements.css';
import './app.css';
import './redesign.css';
import './home-entrance.css';
import './theme.css';
import './design-system.css';
import './hero-atmosphere.css';
import './storefront-backdrop.css';
// Keep the homepage available immediately; fetch other screens when their route is visited.
const CatalogPage = lazy(() =>
  import('./pages/CatalogPage').then((m) => ({ default: m.CatalogPage })),
);
const DetailPage = lazy(() =>
  import('./pages/DetailPage').then((m) => ({ default: m.DetailPage })),
);
const HelpPage = lazy(() => import('./pages/HelpPage').then((m) => ({ default: m.HelpPage })));
const AuthPage = lazy(() => import('./pages/AuthPage').then((m) => ({ default: m.AuthPage })));
const MemberPage = lazy(() =>
  import('./pages/MemberPages').then((m) => ({ default: m.MemberPage })),
);
const PublishPage = lazy(() =>
  import('./pages/PublishPage').then((m) => ({ default: m.PublishPage })),
);
function Protected({ children }: { children: ReactNode }) {
  const { user, loading, error, refresh } = useSession();
  const location = useLocation();
  return loading ? (
    <StateView loading />
  ) : error ? (
    <StateView error={error} onRetry={() => void refresh()} />
  ) : user ? (
    <>{children}</>
  ) : (
    <Navigate
      replace
      to={`/login?returnTo=${encodeURIComponent(location.pathname + location.search)}`}
    />
  );
}
function MediaSafetyHost() {
  const { user, loading } = useSession();
  const location = useLocation();
  return <MediaVerificationDialog identity={loading ? 'loading' : user?.id ?? 'anonymous'} routeKey={location.key} />;
}
class ErrorBoundary extends React.Component<{ children: ReactNode }, { error: boolean }> {
  state = { error: false };
  static getDerivedStateFromError() {
    return { error: true };
  }
  render() {
    return this.state.error ? (
      <div className="state-view">
        <h1>页面遇到了一点问题</h1>
        <p>请重新加载页面。已保存的草稿不会因此删除。</p>
        <button className="button-white" onClick={() => location.reload()}>
          重新加载
        </button>
      </div>
    ) : (
      this.props.children
    );
  }
}
ReactDOM.createRoot(document.getElementById('root')!).render(
  <React.StrictMode>
    <ErrorBoundary>
      <BrowserRouter>
        <Providers>
          <MediaSafetyHost />
          <ChatProvider>
            <Routes>
              <Route element={<Layout />}>
                <Route index element={<HomePage />} />
                <Route path="accounts" element={<CatalogPage />} />
                <Route path="accounts/:id" element={<DetailPage />} />
                <Route
                  path="messages"
                  element={
                    <Protected>
                      <ChatPage />
                    </Protected>
                  }
                />
                {['login', 'register', 'forgot-password'].map((path) => (
                  <Route key={path} path={path} element={<AuthPage />} />
                ))}
                <Route
                  path="publish"
                  element={
                    <Protected>
                      <PublishPage />
                    </Protected>
                  }
                />
                <Route
                  path="me/listings/:id/edit"
                  element={
                    <Protected>
                      <PublishPage />
                    </Protected>
                  }
                />
                <Route
                  path="me/*"
                  element={
                    <Protected>
                      <MemberPage />
                    </Protected>
                  }
                />
                <Route path="help/:slug" element={<HelpPage />} />
                <Route
                  path="*"
                  element={
                    <div className="state-view">
                      <h1>没有找到这个页面</h1>
                      <p>链接可能已经变更。</p>
                      <Link className="button-white" to="/">
                        返回首页
                      </Link>
                    </div>
                  }
                />
              </Route>
            </Routes>
          </ChatProvider>
        </Providers>
      </BrowserRouter>
    </ErrorBoundary>
  </React.StrictMode>,
);
