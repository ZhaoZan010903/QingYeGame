import { useEffect, useRef, useState, type ReactNode } from "react";
import { Link, useLocation } from "react-router-dom";
import {
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
  RefreshCw,
  ShieldCheck,
  TrendingUp,
  UserRound,
  Users,
  X,
} from "lucide-react";
import { useChat } from "./ChatNotifications";
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

function WorkspaceMenu({
  active,
  unread,
  onClose,
}: {
  active?: View;
  unread: number;
  onClose: () => void;
}) {
  const ref = useRef<HTMLDialogElement>(null);
  const closeButton = useRef<HTMLButtonElement>(null);

  useEffect(() => {
    const node = ref.current;
    if (!node) return;
    const mobile = window.matchMedia("(max-width: 760px)");
    const resize = () => {
      if (!mobile.matches) node.close();
    };
    node.showModal();
    closeButton.current?.focus();
    mobile.addEventListener("change", resize);
    resize();
    return () => {
      mobile.removeEventListener("change", resize);
      node.close();
    };
  }, []);

  return (
    <dialog
      ref={ref}
      id="admin-drawer-navigation"
      className="admin-drawer fluent-flyout"
      aria-labelledby="admin-drawer-title"
      onClose={() => {
        // A StrictMode cleanup can queue a close event before reopening.
        if (!ref.current?.open) onClose();
      }}
      onClick={(event) => {
        if (event.target === event.currentTarget) ref.current?.close();
      }}
    >
      <div className="admin-drawer-body">
        <div className="admin-drawer-heading">
          <h2 id="admin-drawer-title">管理菜单</h2>
          <button
            ref={closeButton}
            className="admin-icon-button"
            type="button"
            aria-label="关闭管理菜单"
            onClick={() => ref.current?.close()}
          >
            <X size={20} aria-hidden="true" />
          </button>
        </div>
        <ManagementNav
          active={active}
          unread={unread}
          onNavigate={() => ref.current?.close()}
        />
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
  const [menuOpen, setMenuOpen] = useState(false);
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
            aria-expanded={menuOpen}
            aria-controls={menuOpen ? "admin-drawer-navigation" : undefined}
            onClick={() => setMenuOpen(true)}
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
        </div>
        {children}
      </main>
      {menuOpen && (
        <WorkspaceMenu
          active={active}
          unread={list.unread}
          onClose={() => setMenuOpen(false)}
        />
      )}
    </div>
  );
}
