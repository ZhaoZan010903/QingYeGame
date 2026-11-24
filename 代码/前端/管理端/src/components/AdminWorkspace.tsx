import { useEffect, useRef, useState, type ReactNode } from "react";
import { Link, useLocation } from "react-router-dom";
import {
  ArrowRight,
  ChevronRight,
  CircleHelp,
  ClipboardCheck,
  FileText,
  Headphones,
  LayoutDashboard,
  ListFilter,
  Menu,
  MessageSquare,
  Package,
  PanelsTopLeft,
  Plus,
  RefreshCw,
  Settings2,
  ShieldCheck,
  Trash2,
  TrendingUp,
  UserRound,
  Users,
  X,
  type LucideIcon,
} from "lucide-react";
import { useChat } from "./ChatNotifications";
import { GuideButton, guides, type DemoKind } from "./AdminGuide";
import { productListReturn } from "../lib/product-links";
import "../workspace.css";

const groups = [
  {
    name: "日常工作",
    links: [
      ["", "数据看板", LayoutDashboard],
      ["products", "商品管理", Package],
      ["legacy-sync", "旧站同步", RefreshCw],
      ["reviews", "待审核", ClipboardCheck],
      ["chat", "在线咨询", MessageSquare],
    ],
  },
  {
    name: "网站运营",
    links: [
      ["settings", "首页装修", PanelsTopLeft],
      ["recommendations", "热门榜单", TrendingUp],
      ["contacts", "客服与二维码", Headphones],
      ["categories", "区服与账号字典", ListFilter],
      ["pages", "说明与协议", FileText],
    ],
  },
  {
    name: "账户与安全",
    links: [
      ["users", "会员管理", Users],
      ["administrators", "管理员管理", ShieldCheck],
      ["nickname-reviews", "昵称审核", ClipboardCheck],
      ["avatar-library", "头像库", UserRound],
      ["security/media-access", "图片访问保护", ShieldCheck],
    ],
  },
  {
    name: "使用帮助",
    links: [["help", "操作演示", CircleHelp]],
  },
] as const;

type View = (typeof groups)[number]["links"][number][0];
type Shortcut = readonly [to: string, label: string, icon: LucideIcon];

const workflows: Record<View, readonly Shortcut[]> = {
  "": [
    ["/legacy-sync", "同步旧站商品", RefreshCw],
    ["/products?trash=1", "查看商品回收站", Trash2],
    ["/security/media-access", "查看图片访问保护", ShieldCheck],
  ],
  products: [
    ["/products?new=1", "录入新商品", Plus],
    ["/products?trash=1", "商品回收站", Trash2],
    ["/reviews", "审核会员投稿", ClipboardCheck],
  ],
  "legacy-sync": [
    ["/products?source=legacy", "查看旧站迁入商品", Package],
    [
      "/products?source=legacy&status=offline",
      "查看迁入的已下架商品",
      ListFilter,
    ],
  ],
  reviews: [
    ["/products?status=published", "查看已上架商品", Package],
    ["/products?status=rejected", "查看已退回商品", ListFilter],
  ],
  chat: [
    ["/products", "查找咨询商品", Package],
    ["/contacts", "维护客服入口", Headphones],
  ],
  settings: [
    ["/recommendations", "管理热门排序", TrendingUp],
    ["/contacts", "维护客服入口", Headphones],
  ],
  recommendations: [
    ["/products?status=published", "查看可见商品", Package],
    ["/settings", "调整首页展示", PanelsTopLeft],
  ],
  contacts: [
    ["/chat", "进入在线咨询", MessageSquare],
    ["/settings", "查看首页装修", PanelsTopLeft],
  ],
  categories: [
    ["/products?new=1", "查看录入表单", Plus],
    ["/products", "查看商品列表", Package],
  ],
  pages: [
    ["/settings", "查看首页装修", PanelsTopLeft],
    ["/contacts", "维护客服入口", Headphones],
  ],
  users: [
    ["/nickname-reviews", "审核会员昵称", ClipboardCheck],
    ["/avatar-library", "维护会员头像库", UserRound],
  ],
  administrators: [
    ["/users", "查看会员管理", Users],
    ["/security/media-access", "查看图片访问保护", ShieldCheck],
  ],
  "nickname-reviews": [
    ["/users", "查看会员列表", Users],
    ["/avatar-library", "维护会员头像库", UserRound],
  ],
  "avatar-library": [
    ["/users", "查看会员列表", Users],
    ["/nickname-reviews", "审核会员昵称", ClipboardCheck],
  ],
  "security/media-access": [
    ["/products", "查看商品图集", Package],
    ["/administrators", "查看管理员", Users],
  ],
  help: [
    ["/products", "返回商品管理", Package],
    ["/", "返回数据看板", LayoutDashboard],
  ],
};

const demonstrations: Partial<Record<View, DemoKind>> = {
  products: "product",
  reviews: "review",
  chat: "chat",
  settings: "home",
  contacts: "contact",
};
function ManagementNav({
  active,
  unread,
  onNavigate,
}: {
  active?: View;
  unread: number;
  onNavigate?: () => void;
}) {
  return (
    <nav aria-label="管理导航" className="admin-navigation">
      {groups.map((group) => (
        <div className="nav-group" key={group.name}>
          <p>{group.name}</p>
          {group.links.map(([route, title, Icon]) => (
            <Link
              aria-current={active === route ? "page" : undefined}
              key={route}
              to={"/" + route}
              onClick={onNavigate}
            >
              <Icon size={18} aria-hidden="true" />
              <span>{title}</span>
              {route === "chat" && unread > 0 && (
                <b className="chat-unread-nav">
                  {unread > 99 ? "99+" : unread}
                </b>
              )}
            </Link>
          ))}
        </div>
      ))}
    </nav>
  );
}

function WorkspaceContext({
  view,
  parentHref,
  onNavigate,
}: {
  view?: View;
  parentHref?: string;
  onNavigate?: () => void;
}) {
  const related: readonly Shortcut[] = parentHref
    ? [
        [
          parentHref,
          view === "reviews"
            ? "返回待审核列表"
            : view === "chat"
              ? "返回在线咨询"
              : "返回商品列表",
          view === "chat" ? MessageSquare : Package,
        ],
        view === "products"
          ? ["/reviews", "查看待审核商品", ClipboardCheck]
          : ["/products", "查看商品列表", Package],
      ]
    : workflows[view ?? ""];
  const guide = view === undefined ? undefined : demonstrations[view];
  return (
    <>
      <section className="admin-context-card fluent-card" aria-label="快捷操作">
        <h2>快捷操作</h2>
        <nav className="admin-context-links" aria-label="快捷操作">
          {related.map(([to, title, Icon]) => (
            <Link key={to} to={to} onClick={onNavigate}>
              <Icon size={16} aria-hidden="true" />
              <span>{title}</span>
              <ArrowRight size={14} aria-hidden="true" />
            </Link>
          ))}
        </nav>
      </section>
      {guide ? (
        <section className="admin-context-card fluent-card">
          <h2>{guides[guide].title}</h2>
          <GuideButton kind={guide} label="看操作演示" />
        </section>
      ) : view !== "help" ? (
        <section className="admin-context-card fluent-card">
          <Link className="admin-context-help" to="/help" onClick={onNavigate}>
            <CircleHelp size={17} aria-hidden="true" />
            操作演示
            <ArrowRight size={14} aria-hidden="true" />
          </Link>
        </section>
      ) : null}
    </>
  );
}

type Drawer = "navigation" | "context";
const drawerTitles: Record<Drawer, string> = {
  navigation: "管理菜单",
  context: "相关操作",
};

function WorkspaceDrawer({
  kind,
  children,
  onClose,
}: {
  kind: Drawer;
  children: (close: () => void) => ReactNode;
  onClose: () => void;
}) {
  const ref = useRef<HTMLDialogElement>(null);
  const closeButton = useRef<HTMLButtonElement>(null);
  const close = () => ref.current?.close();
  useEffect(() => {
    const node = ref.current;
    if (!node) return;
    const maxWidth = kind === "navigation" ? 760 : 1250;
    const responsive = window.matchMedia("(max-width: " + maxWidth + "px)");
    node.showModal();
    closeButton.current?.focus();
    const resize = () => {
      if (!responsive.matches) node.close();
    };
    responsive.addEventListener("change", resize);
    resize();
    return () => {
      responsive.removeEventListener("change", resize);
      node.close();
    };
  }, [kind]);
  return (
    <dialog
      ref={ref}
      id={"admin-drawer-" + kind}
      className="admin-drawer fluent-flyout"
      data-side={kind === "navigation" ? "left" : "right"}
      aria-labelledby="admin-drawer-title"
      onClose={() => {
        // StrictMode can close and reopen a dialog before its close event runs.
        if (!ref.current?.open) onClose();
      }}
      onClick={(event) => {
        if (event.target === event.currentTarget) close();
      }}
    >
      <div className="admin-drawer-body">
        <div className="admin-drawer-heading">
          <h2 id="admin-drawer-title">{drawerTitles[kind]}</h2>
          <button
            ref={closeButton}
            className="admin-icon-button"
            type="button"
            aria-label={"关闭" + drawerTitles[kind]}
            onClick={close}
          >
            <X size={20} aria-hidden="true" />
          </button>
        </div>
        {children(close)}
      </div>
    </dialog>
  );
}

export function AdminWorkspace({
  tab,
  detail,
  children,
}: {
  tab: string;
  detail: boolean;
  children: ReactNode;
}) {
  const { list } = useChat();
  const { state } = useLocation();
  const [drawer, setDrawer] = useState<Drawer | null>(null);
  const parentHref = detail
    ? (productListReturn(state?.returnTo) ?? "/chat")
    : undefined;
  const route = parentHref ? parentHref.slice(1).split("?")[0] : tab;
  const current = groups
    .flatMap((g) => [...g.links])
    .find(([key]) => key === route);
  const active = current?.[0];
  const label = current?.[1] ?? "管理页面";
  return (
    <div
      className="admin-layout"
      data-view={
        detail
          ? "product-detail"
          : active === ""
            ? "dashboard"
            : (active ?? "unknown")
      }
    >
      <aside className="admin-sidebar" aria-label="管理模块">
        <ManagementNav active={active} unread={list.unread} />
      </aside>
      <main id="admin-content" tabIndex={-1} className="admin-shell">
        <div className="admin-commandbar">
          <button
            className="admin-icon-button admin-menu-trigger"
            type="button"
            aria-label="打开管理菜单"
            aria-haspopup="dialog"
            aria-expanded={drawer === "navigation"}
            aria-controls={
              drawer === "navigation" ? "admin-drawer-navigation" : undefined
            }
            onClick={() => setDrawer("navigation")}
          >
            <Menu size={20} aria-hidden="true" />
            <span>菜单</span>
          </button>
          <nav className="admin-breadcrumb" aria-label="当前位置">
            {active !== "" && (
              <>
                <Link to="/">数据看板</Link>
                <ChevronRight size={14} aria-hidden="true" />
              </>
            )}
            {detail ? (
              <>
                <Link to={parentHref ?? "/products"}>{label}</Link>
                <ChevronRight size={14} aria-hidden="true" />
                <span aria-current="page">商品详情</span>
              </>
            ) : (
              <span aria-current="page">
                {active === "" ? "工作台" : label}
              </span>
            )}
          </nav>
          <button
            className="admin-icon-button admin-context-trigger"
            type="button"
            aria-label="打开相关操作"
            aria-haspopup="dialog"
            aria-expanded={drawer === "context"}
            aria-controls={
              drawer === "context" ? "admin-drawer-context" : undefined
            }
            onClick={() => setDrawer("context")}
          >
            <Settings2 size={18} aria-hidden="true" />
            <span>相关操作</span>
          </button>
        </div>
        {children}
      </main>
      <aside className="admin-context" aria-label="相关操作">
        <WorkspaceContext view={active} parentHref={parentHref} />
      </aside>
      {drawer && (
        <WorkspaceDrawer kind={drawer} onClose={() => setDrawer(null)}>
          {(close) =>
            drawer === "navigation" ? (
              <ManagementNav
                active={active}
                unread={list.unread}
                onNavigate={close}
              />
            ) : (
              <WorkspaceContext
                view={active}
                parentHref={parentHref}
                onNavigate={close}
              />
            )
          }
        </WorkspaceDrawer>
      )}
    </div>
  );
}
