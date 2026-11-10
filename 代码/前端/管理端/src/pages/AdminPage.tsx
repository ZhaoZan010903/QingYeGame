import { useEffect, useRef, useState, type FormEvent } from "react";
import { Link, NavLink, useParams } from "react-router-dom";
import { useSession } from "../context";
import { adminApi, canManage } from "../lib/admin-api";
import { Price, StateView } from "../components/shared";
import {
  AdminProductEditor,
  statusNames,
} from "../components/AdminProductEditor";
import { AdminResources, type Resource } from "../components/AdminResources";
import {
  AdminRecommendations,
  AdminUsers,
} from "../components/AdminOperations";
import type { ProductPage } from "../types";
import "../admin.css";
const publicSite = String(import.meta.env.VITE_PUBLIC_SITE_URL ?? "").replace(
  /\/$/,
  "",
);

export function AdminGate({ children }: { children: React.ReactNode }) {
  const { user } = useSession();
  return canManage(user) ? (
    <>{children}</>
  ) : (
    <div className="state-view">
      <h1>需要管理员权限</h1>
      <p>请使用已开通的管理员账户登录。</p>
      <Link className="button-white" to="/login">
        返回管理员登录
      </Link>
    </div>
  );
}
function Products() {
  const requestSequence = useRef(0);
  const [page, setPage] = useState<ProductPage>({
    items: [],
    total: 0,
    nextCursor: null,
  });
  const [status, setStatus] = useState("");
  const [q, setQ] = useState("");
  const [query, setQuery] = useState("");
  const [version, setVersion] = useState(0);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<unknown>();
  const [selected, setSelected] = useState<string | null>(null);
  async function load(more = false) {
    const sequence = ++requestSequence.current;
    setLoading(true);
    setError(undefined);
    try {
      const params = new URLSearchParams({ limit: "20" });
      if (status) params.set("status", status);
      if (query) params.set("q", query);
      if (more && page.nextCursor) params.set("cursor", page.nextCursor);
      const result = await adminApi.products(params);
      if (sequence !== requestSequence.current) return;
      setPage((old) => ({
        ...result,
        items: more
          ? [
              ...old.items,
              ...result.items.filter(
                (p) => !old.items.some((item) => item.id === p.id),
              ),
            ]
          : result.items,
      }));
    } catch (e) {
      if (sequence === requestSequence.current) setError(e);
    } finally {
      if (sequence === requestSequence.current) setLoading(false);
    }
  }
  useEffect(() => {
    void load();
    return () => {
      requestSequence.current++;
    };
  }, [status, query, version]);
  function search(event: FormEvent) {
    event.preventDefault();
    setQuery(q.trim());
  }
  return (
    <>
      <div className="admin-heading">
        <div>
          <h2>商品管理</h2>
          <p>共 {page.total} 个商品 · 编辑展示中的商品会转为草稿</p>
        </div>
        <button
          className="button-white"
          disabled={selected !== null}
          onClick={() => setSelected("new")}
        >
          新建平台商品
        </button>
      </div>
      <div className="admin-toolbar">
        <form onSubmit={search}>
          <input
            aria-label="搜索管理商品"
            placeholder="商品编号或标题"
            value={q}
            onChange={(e) => setQ(e.target.value)}
          />
          <button type="submit" className="button-outline">
            搜索
          </button>
        </form>
        <label>
          状态
          <select
            aria-label="商品状态"
            value={status}
            onChange={(e) => setStatus(e.target.value)}
          >
            <option value="">全部状态</option>
            {Object.entries(statusNames).map(([key, label]) => (
              <option key={key} value={key}>
                {label}
              </option>
            ))}
          </select>
        </label>
      </div>
      {selected !== null && (
        <AdminProductEditor
          key={selected}
          id={selected === "new" ? undefined : selected}
          onClose={() => setSelected(null)}
          onSaved={() => setVersion((v) => v + 1)}
        />
      )}
      {error ? (
        <StateView error={error} onRetry={() => void load()} />
      ) : (
        <>
          <div className="admin-table-wrap">
            <table className="admin-table">
              <thead>
                <tr>
                  <th>商品</th>
                  <th>报价</th>
                  <th>来源 / 状态</th>
                  <th>管理</th>
                </tr>
              </thead>
              <tbody>
                {page.items.map((p) => (
                  <tr key={p.id}>
                    <td>
                      <strong>{p.title}</strong>
                      <span>{p.code ?? p.id}</span>
                    </td>
                    <td>
                      <Price product={p} />
                    </td>
                    <td>
                      <span>
                        {p.source === "member"
                          ? "会员投稿"
                          : p.source === "legacy"
                            ? "旧站迁移"
                            : "平台录入"}
                      </span>
                      <strong>{statusNames[p.status]}</strong>
                    </td>
                    <td>
                      <button
                        className="button-outline"
                        disabled={selected !== null || loading}
                        onClick={() => setSelected(p.id)}
                      >
                        查看 / 编辑
                      </button>
                      {p.status === "published" && publicSite && (
                        <a
                          target="_blank"
                          rel="noreferrer"
                          href={`${publicSite}/accounts/${p.id}`}
                        >
                          查看公开页 ↗
                        </a>
                      )}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
          {loading && (
            <p role="status" className="muted">
              正在读取商品…
            </p>
          )}
          {!loading && !page.items.length && (
            <p className="admin-empty">没有符合条件的商品。</p>
          )}
          {page.nextCursor && (
            <button
              className="button-outline"
              disabled={loading}
              onClick={() => void load(true)}
            >
              加载更多
            </button>
          )}
        </>
      )}
    </>
  );
}
export function AdminPage() {
  const { "*": path = "" } = useParams();
  const resources: Resource[] = ["contacts", "pages", "categories", "settings"];
  const tab = resources.includes(path as Resource)
    ? (path as Resource)
    : path === "users" || path === "recommendations"
      ? path
      : "products";
  return (
    <AdminGate>
      <section className="admin-shell page-width mx-auto">
        <header className="admin-heading">
          <div>
            <h1>清叶管理台</h1>
            <p>商品审核、内容维护与展示配置</p>
          </div>
          {publicSite && (
            <a
              className="button-outline"
              target="_blank"
              rel="noreferrer"
              href={publicSite}
            >
              查看用户站 ↗
            </a>
          )}
        </header>
        <nav className="admin-nav" aria-label="管理导航">
          {[
            ["", "商品管理"],
            ["contacts", "客服配置"],
            ["pages", "说明页面"],
            ["categories", "分类筛选"],
            ["settings", "首页配置"],
            ["users", "会员管理"],
            ["recommendations", "推荐规则"],
          ].map(([href, name]) => (
            <NavLink key={href} end to={`/${href}`}>
              {name}
            </NavLink>
          ))}
        </nav>
        {tab === "products" ? (
          <Products />
        ) : tab === "users" ? (
          <AdminUsers />
        ) : tab === "recommendations" ? (
          <AdminRecommendations />
        ) : (
          <AdminResources key={tab} resource={tab} />
        )}
      </section>
    </AdminGate>
  );
}
