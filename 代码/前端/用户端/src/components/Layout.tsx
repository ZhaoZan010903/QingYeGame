import { Suspense, useEffect, useLayoutEffect, useRef, useState } from 'react';
import {
  Link,
  NavLink,
  Outlet,
  useLocation,
  useNavigationType,
  useNavigate,
} from 'react-router-dom';
import { Search, UserRound, Plus, ArrowUpRight, ArrowRight } from 'lucide-react';
import { isDemo, api, environmentLabel } from '../lib/api';
import { useSession, useUi } from '../context';
import { Modal, StateView, Artwork } from './shared';
import { useLoad } from '../lib-use';
import { StorefrontBackdrop, ReadingProgress } from './AmbientBackdrop';
import { ChatNavLink } from './ChatNotifications';
import { ThemeToggle } from './ThemeToggle';
export function Brand() {
  return (
    <Link className="brand flex items-center" to="/" aria-label="清叶首页">
      <svg className="brand-symbol" viewBox="0 0 32 32">
        <path d="M23 6H11L5 16l6 10h12l5-10-5-10Z" />
        <path d="m11 19 5-9 5 9h-10Zm7 3 5 5" />
      </svg>
      <span className="brand-cn">清叶</span>
      <span className="brand-divider" />
      <span className="brand-en">QINGYE</span>
    </Link>
  );
}
function SearchDialog({ onClose }: { onClose: () => void }) {
  const location = useLocation();
  const [query, setQuery] = useState(() => new URLSearchParams(location.search).get('q') || '');
  const [deferred, setDeferred] = useState(query);
  const navigate = useNavigate();
  useEffect(() => {
    const t = setTimeout(() => setDeferred(query), 220);
    return () => clearTimeout(t);
  }, [query]);
  const results = useLoad(
    () => api.products(new URLSearchParams({ q: deferred, limit: '5' })),
    deferred,
  );
  return (
    <Modal title="找到你的下一份珍藏" onClose={onClose}>
      <form
        className="quick-search-form"
        onSubmit={(e) => {
          e.preventDefault();
          const params =
            location.pathname === '/accounts'
              ? new URLSearchParams(location.search)
              : new URLSearchParams();
          params.delete('cursor');
          params.delete('page');
          query.trim() ? params.set('q', query.trim()) : params.delete('q');
          navigate(`/accounts${params.size ? '?' + params.toString() : ''}`);
          onClose();
        }}
      >
        <Search />
        <input
          autoFocus
          type="search"
          placeholder="搜索编号、标题或区服"
          value={query}
          onChange={(e) => setQuery(e.target.value)}
          aria-label="搜索关键词"
        />
        <button className="button-white" type="submit">
          搜索
        </button>
      </form>
      <div className="quick-results">
        {results.loading || results.error ? (
          <StateView {...results} onRetry={results.retry} />
        ) : results.data?.items.length ? (
          results.data.items.map((p) => (
            <Link key={p.id} className="quick-result" to={`/accounts/${p.id}`} onClick={onClose}>
              <Artwork product={p} className="quick-image" />
              <span className="quick-info">
                <strong>{p.title}</strong>
                <small>
                  {p.code ?? p.id} · {p.server}
                </small>
              </span>
              <ArrowRight />
            </Link>
          ))
        ) : (
          <StateView empty="没有找到相关账号" />
        )}
      </div>
    </Modal>
  );
}
const positions = new Map<string, number>();
function ScrollRestoration() {
  const location = useLocation();
  const type = useNavigationType();
  const previous = useRef<{ key: string; pathname: string; hash: string } | null>(null);
  const activeKey = useRef(location.key);
  const restoring = useRef(false);
  const lastY = useRef(window.scrollY);
  useEffect(() => {
    const old = window.history.scrollRestoration;
    window.history.scrollRestoration = 'manual';
    const remember = () => {
      if (!restoring.current) {
        lastY.current = window.scrollY;
        positions.set(activeKey.current, window.scrollY);
      }
    };
    window.addEventListener('scroll', remember, { passive: true });
    return () => {
      window.removeEventListener('scroll', remember);
      window.history.scrollRestoration = old;
    };
  }, []);
  useLayoutEffect(() => {
    const prior = previous.current;
    const priorY = prior ? (positions.get(prior.key) ?? lastY.current) : 0;
    const samePath = prior?.pathname === location.pathname;
    const target = type === 'POP' ? (positions.get(location.key) ?? 0) : samePath ? priorY : 0;
    const hasSavedPosition = type === 'POP' && positions.has(location.key);
    const useAnchor = !!location.hash && prior?.hash !== location.hash && !hasSavedPosition;
    activeKey.current = location.key;
    previous.current = { key: location.key, pathname: location.pathname, hash: location.hash };
    restoring.current = true;
    let frame = 0;
    let finished = false;
    let deadline: ReturnType<typeof setTimeout> | undefined;
    const stop = (remember: boolean) => {
      if (finished) return;
      finished = true;
      cancelAnimationFrame(frame);
      observer.disconnect();
      if (deadline) clearTimeout(deadline);
      for (const event of ['wheel', 'touchstart', 'pointerdown', 'keydown'])
        window.removeEventListener(event, interrupt, true);
      restoring.current = false;
      if (remember) {
        lastY.current = window.scrollY;
        positions.set(location.key, window.scrollY);
      }
    };
    const interrupt = () => stop(true);
    const restore = () => {
      if (finished) return;
      if (useAnchor) {
        let id = location.hash.slice(1);
        try {
          id = decodeURIComponent(id);
        } catch {}
        const anchor = document.getElementById(id);
        if (anchor) {
          anchor.scrollIntoView({ behavior: 'instant', block: 'start' });
          stop(true);
        }
        return;
      }
      const maximum = Math.max(0, document.documentElement.scrollHeight - window.innerHeight);
      window.scrollTo({ top: Math.min(target, maximum), behavior: 'instant' });
      if (maximum >= target - 1 && !document.querySelector('#main .loader')) stop(true);
    };
    const schedule = () => {
      cancelAnimationFrame(frame);
      frame = requestAnimationFrame(restore);
    };
    const observer = new ResizeObserver(schedule);
    observer.observe(document.body);
    for (const event of ['wheel', 'touchstart', 'pointerdown', 'keydown'])
      window.addEventListener(event, interrupt, { capture: true, passive: true });
    // A slow request can grow the document after the first render. Never fight user scrolling.
    deadline = setTimeout(() => stop(true), 15000);
    schedule();
    return () => stop(false);
  }, [location.key, location.pathname, location.hash, type]);
  return null;
}
export function Layout() {
  const [search, setSearch] = useState(false);
  const { user } = useSession();
  const { contact } = useUi();
  const location = useLocation();
  const isCatalog = location.pathname === '/accounts';
  const isHome = location.pathname === '/';
  useLayoutEffect(() => setSearch(false), [location.key]);
  useLayoutEffect(() => {
    const key = (e: KeyboardEvent) => {
      if ((e.ctrlKey || e.metaKey) && e.key.toLowerCase() === 'k') {
        e.preventDefault();
        if (isCatalog) window.dispatchEvent(new Event('qingye:focus-catalog-search'));
        else setSearch((v) => !v);
      }
    };
    window.addEventListener('keydown', key);
    return () => window.removeEventListener('keydown', key);
  }, [isCatalog]);
  return (
    <>
      <ScrollRestoration />
      <ReadingProgress />
      <StorefrontBackdrop />
      <div className="nav-top-mask" aria-hidden="true" />
      <a className="skip-link" href="#main">
        跳到主要内容
      </a>
      <div className="nav-wrap">
        <header
          className={`site-nav mx-auto flex items-center justify-between ${isCatalog ? 'catalog-nav' : ''}`}
        >
          <Brand />
          <nav className="main-links" aria-label="主导航">
            <NavLink to="/" end>
              首页
            </NavLink>
            <NavLink to="/accounts">账号集市</NavLink>
          </nav>
          {isCatalog ? (
            <div
              id="catalog-search-dock"
              className="nav-search catalog-search-dock"
              aria-hidden="true"
            />
          ) : (
            <button
              className="nav-search flex items-center"
              aria-label="搜索账号"
              onClick={() => setSearch(true)}
            >
              <Search />
              <span>搜索编号、标题或区服</span>
              <kbd>⌘ K</kbd>
            </button>
          )}
          <div className="nav-actions flex items-center">
            <ChatNavLink />
            <ThemeToggle />
            <Link className="profile-button" to="/me" aria-label="个人中心">
              <UserRound />
            </Link>
            <Link className="button-white publish-button" to="/publish" aria-label="发布账号">
              <Plus />
              <span>发布资产</span>
            </Link>
          </div>
        </header>
      </div>
      <div id="catalog-search-layer" />
      {environmentLabel && !isHome && (
        <div className="demo-ribbon">
          <span className="status-dot" />
          {environmentLabel}
        </div>
      )}
      <main
        id="main"
        className={isHome ? 'home-main' : undefined}
        key={isCatalog ? 'catalog' : undefined}
      >
        <Suspense fallback={<StateView loading />}>
          <Outlet />
        </Suspense>
      </main>
      <footer className="site-footer page-width mx-auto">
        <div className="footer-top">
          <Brand />
          <p>让信任，成为每一次选择的底色。</p>
          <button className="text-button" onClick={() => contact()}>
            联系客服 <ArrowUpRight />
          </button>
        </div>
        <nav className="footer-links" aria-label="网站说明">
          {[
            ['about', '关于清叶'],
            ['process', '咨询流程'],
            ['publish', '上架说明'],
            ['terms', '服务协议'],
            ['privacy', '隐私说明'],
          ].map(([path, label]) => (
            <Link key={path} to={`/help/${path}`}>
              {label}
            </Link>
          ))}
        </nav>
        <div className="footer-bottom">
          <span className="mono">© {new Date().getFullYear()} QINGYE.</span>
          <span>
            {isDemo
              ? '概念图仅作风格展示，不代表真实账号或保障承诺。'
              : '账号资料以核实结果为准，具体服务请咨询平台客服。'}
          </span>
        </div>
      </footer>
      {search && <SearchDialog onClose={() => setSearch(false)} />}
    </>
  );
}
