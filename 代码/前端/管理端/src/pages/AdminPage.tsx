import { useEffect } from "react";
import { Link, NavLink, useParams, useLocation } from "react-router-dom";
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
} from "lucide-react";
import { useSession } from "../context";
import { canManage } from "../lib/admin-api";
import { AdminProducts } from "../components/AdminProducts";
import { AdminResources, type Resource } from "../components/AdminResources";
import {
  AdminRecommendations,
  AdminUsers,
} from "../components/AdminOperations";
import { AdminDashboard } from "../components/AdminDashboard";
import { AdminProductView } from "../components/AdminProductView";
import { adminProductID } from "../lib/product-links";
import { ChatPage } from "../components/ChatPage";
import { useChat } from "../components/ChatNotifications";
import { AdminHelp } from "../components/AdminGuide";
import { AdminMediaSecurity } from "../components/AdminMediaSecurity";
import { DictionarySettings } from "../components/DictionarySettings";
import {
  AdminNicknameReviews,
  AdminAvatarLibrary,
} from "../components/AdminProfiles";
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
        ["nickname-reviews", "昵称审核", ClipboardCheck],
        ["avatar-library", "头像库", Users],
        ["security/media-access", "图片访问保护", CircleHelp],
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
          ) : tab === "security/media-access" ? (
            <AdminMediaSecurity />
          ) : tab === "help" ? (
            <AdminHelp />
          ) : tab === "products" || tab === "reviews" ? (
            <AdminProducts key={tab} review={tab === "reviews"} />
          ) : tab === "users" ? (
            <AdminUsers />
          ) : tab === "nickname-reviews" ? (
            <AdminNicknameReviews />
          ) : tab === "avatar-library" ? (
            <AdminAvatarLibrary />
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
