import { useEffect, useRef, useState, type FormEvent } from "react";
import {
  Link,
  NavLink,
  useParams,
  useSearchParams,
  useLocation,
} from "react-router-dom";
import {
  LayoutDashboard,
  MessageSquare,
  Package,
  ClipboardCheck,
  PanelsTopLeft,
  TrendingUp,
  Headphones,
  ListFilter,
  Users,
  FileText,
  CircleHelp,
  ArrowUpRight,
  Plus,
} from "lucide-react";
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
import { AdminDashboard } from "../components/AdminDashboard";
import { AdminProductView } from "../components/AdminProductView";
import { adminProductID } from "../lib/product-links";
import { ChatPage } from "../components/ChatPage";
import { useChat } from "../components/ChatNotifications";
import { AdminHelp, GuideButton } from "../components/AdminGuide";
import { DictionarySettings } from "../components/DictionarySettings";
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
function Products({ review = false }: { review?: boolean }) {
  const [params, setParams] = useSearchParams();
  const requestSequence = useRef(0);
  const [page, setPage] = useState<ProductPage>({
    items: [],
    total: 0,
    nextCursor: null,
  });
  const status = review ? "pending" : (params.get("status") ?? "");
  const [q, setQ] = useState("");
  const [query, setQuery] = useState("");
  const [version, setVersion] = useState(0);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<unknown>();
  const [actionID, setActionID] = useState("");
  async function changeListing(id: string, kind: "publish" | "offline") {
    const reason =
      kind === "offline"
        ? window.prompt("填写下架原因（会记录到商品操作历史）")
        : "";
    if (kind === "offline" && !reason?.trim()) return;
    if (
      kind === "publish" &&
      !window.confirm("确认重新上架？系统会检查资料、图片及审核版本。")
    )
      return;
    setActionID(id);
    setError(undefined);
    try {
      const latest = await adminApi.product(id);
      await adminApi.action(id, kind === "publish" ? "publish" : "status", {
        expected_version: latest.version,
        ...(kind === "offline" ? { action: kind, reason: reason!.trim() } : {}),
      });
      setVersion((v) => v + 1);
    } catch (e) {
      setError(e);
    } finally {
      setActionID("");
    }
  }
  const selected =
    params.get("edit") || (params.get("new") === "1" ? "new" : null);
  function setSelected(id: string | null) {
    const next = new URLSearchParams(params);
    next.delete("new");
    next.delete("edit");
    if (id === "new") next.set("new", "1");
    else if (id) next.set("edit", id);
    setParams(next);
  }
  function setStatus(value: string) {
    const next = new URLSearchParams(params);
    if (value) next.set("status", value);
    else next.delete("status");
    setParams(next);
  }
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
      {selected !== null ? (
        <AdminProductEditor
          key={selected}
          id={selected === "new" ? undefined : selected}
          onClose={() => setSelected(null)}
          onSaved={() => setVersion((v) => v + 1)}
        />
      ) : (
        <>
          <div className="admin-heading">
            <div>
              <span className="eyebrow">
                {review ? "会员投稿" : "商品维护"}
              </span>
              <h1>{review ? "待审核商品" : "商品管理"}</h1>
              <p>
                {review
                  ? "检查资料和图片，再决定通过上架或退回修改。"
                  : "查找商品，修改资料、管理图片或调整展示状态。"}
              </p>
            </div>
            <div className="heading-actions">
              <GuideButton kind={review ? "review" : "product"} />
              {!review && (
                <button
                  className="button-white"
                  disabled={selected !== null}
                  onClick={() => setSelected("new")}
                >
                  <Plus size={17} />
                  上架新商品
                </button>
              )}
            </div>
          </div>
          {!review && (
            <div className="status-tabs" aria-label="按状态筛选">
              <button
                className={!status ? "active" : ""}
                onClick={() => setStatus("")}
              >
                全部商品
              </button>
              {Object.entries(statusNames).map(([key, label]) => (
                <button
                  className={key === status ? "active" : ""}
                  key={key}
                  onClick={() => setStatus(key)}
                >
                  {label}
                </button>
              ))}
            </div>
          )}
          <section className="panel products-panel">
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
              <span className="muted">
                {loading ? "正在读取…" : `共 ${page.total} 个商品`}
              </span>
            </div>
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
                        <th>咨询人数</th>
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
                            <strong
                              className={`status-pill status-${p.status}`}
                            >
                              {statusNames[p.status]}
                            </strong>
                          </td>
                          <td>{p.consultationCount ?? 0}</td>
                          <td>
                            <button
                              className="button-outline"
                              disabled={selected !== null || loading}
                              onClick={() => setSelected(p.id)}
                            >
                              {review ? "查看并审核" : "编辑商品"}
                            </button>
                            {!review && p.status === "published" && (
                              <button
                                className="text-button"
                                disabled={!!actionID || loading}
                                onClick={() =>
                                  void changeListing(p.id, "offline")
                                }
                              >
                                {actionID === p.id ? "处理中…" : "下架"}
                              </button>
                            )}
                            {!review &&
                              ["offline", "sold", "rented"].includes(
                                p.status,
                              ) && (
                                <button
                                  className="text-button"
                                  disabled={!!actionID || loading}
                                  onClick={() =>
                                    void changeListing(p.id, "publish")
                                  }
                                >
                                  {actionID === p.id ? "处理中…" : "重新上架"}
                                </button>
                              )}
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
                  <p className="admin-empty">
                    {review
                      ? "待审核商品已处理完毕。新投稿会出现在这里。"
                      : "没有符合条件的商品，试试其他关键词或状态。"}
                  </p>
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
          </section>
        </>
      )}
    </>
  );
}
export function AdminPage() {
  const { list: chats } = useChat();
  const location = useLocation();
  useEffect(() => {
    window.scrollTo({ top: 0, behavior: "instant" });
  }, [location.pathname, location.search]);
  const { "*": path = "" } = useParams();
  const resources: Resource[] = ["contacts", "pages", "categories", "settings"];
  const tab = path.replace(/\/$/, "");
  const detailID = adminProductID(tab);
  const groups = [
    {
      label: "日常工作",
      items: [
        ["", "工作台", LayoutDashboard],
        ["products", "商品管理", Package],
        ["reviews", "待审核", ClipboardCheck],
        ["chat", "在线咨询", MessageSquare],
      ],
    },
    {
      label: "网站运营",
      items: [
        ["settings", "首页装修", PanelsTopLeft],
        ["recommendations", "热门榜单", TrendingUp],
        ["contacts", "客服与二维码", Headphones],
        ["categories", "区服与账号字典", ListFilter],
        ["pages", "说明与协议", FileText],
        ["users", "会员管理", Users],
      ],
    },
    { label: "使用帮助", items: [["help", "操作演示", CircleHelp]] },
  ] as const;
  return (
    <AdminGate>
      <div className="admin-layout">
        <aside className="admin-sidebar">
          <nav aria-label="管理导航">
            {groups.map((group) => (
              <div className="nav-group" key={group.label}>
                <p>{group.label}</p>
                {group.items.map(([href, name, Icon]) => (
                  <NavLink key={href} end to={`/${href}`}>
                    <Icon size={18} />
                    <span>{name}</span>
                    {href === "chat" && chats.unread > 0 && (
                      <b className="chat-unread-nav">
                        {chats.unread > 99 ? "99+" : chats.unread}
                      </b>
                    )}
                  </NavLink>
                ))}
              </div>
            ))}
          </nav>
          {publicSite && (
            <a
              className="sidebar-site"
              href={publicSite}
              target="_blank"
              rel="noreferrer"
            >
              <span>
                查看用户网站<small>保存后，到前台检查效果</small>
              </span>
              <ArrowUpRight size={18} />
            </a>
          )}
        </aside>
        <section className="admin-shell">
          {tab === "" ? (
            <AdminDashboard />
          ) : tab === "chat" ? (
            <ChatPage />
          ) : detailID ? (
            <AdminProductView key={detailID} id={detailID} />
          ) : tab === "help" ? (
            <AdminHelp />
          ) : tab === "products" || tab === "reviews" ? (
            <Products key={tab} review={tab === "reviews"} />
          ) : tab === "users" ? (
            <AdminUsers />
          ) : tab === "recommendations" ? (
            <AdminRecommendations />
          ) : tab === "categories" ? (
            <DictionarySettings />
          ) : resources.includes(tab as Resource) ? (
            <AdminResources key={tab} resource={tab as Resource} />
          ) : (
            <div className="state-view">
              <h1>没有找到这个管理页面</h1>
              <Link className="button-white" to="/">
                返回工作台
              </Link>
            </div>
          )}
        </section>
      </div>
    </AdminGate>
  );
}
