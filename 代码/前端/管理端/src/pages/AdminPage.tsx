import { lazy, Suspense, useEffect } from "react";
import { Link, useParams, useLocation } from "react-router-dom";
import { useSession } from "../context";
import { canManage } from "../lib/admin-api";
import { AdminProducts } from "../components/AdminProducts";
import { AdminResources, type Resource } from "../components/AdminResources";
import {
  AdminRecommendations,
  AdminUsers,
} from "../components/AdminOperations";
import { AdminDashboard } from "../components/AdminDashboard";
import { AdminAdministrators } from "../components/AdminAdministrators";
import { AdminProductView } from "../components/AdminProductView";
import { adminProductID } from "../lib/product-links";
import { AdminWorkspace } from "../components/AdminWorkspace";
import { AdminHelp } from "../components/AdminGuide";
import { AdminMediaSecurity } from "../components/AdminMediaSecurity";
import { AdminLegacySync } from "../components/AdminLegacySync";
import { DictionarySettings } from "../components/DictionarySettings";
import {
  AdminNicknameReviews,
  AdminAvatarLibrary,
} from "../components/AdminProfiles";
import "../admin.css";
import { StateView } from "../components/shared";

const ChatPage = lazy(() =>
  import("../components/ChatPage").then((module) => ({
    default: module.ChatPage,
  })),
);

export function AdminGate({ children }: { children: React.ReactNode }) {
  const { user } = useSession();
  return canManage(user) ? (
    <>{children}</>
  ) : (
    <main id="admin-content" tabIndex={-1} className="state-view">
      <h1>需要管理员权限</h1>
      <p>请使用已开通的管理员账户登录。</p>
      <Link className="button-white" to="/login">
        返回管理员登录
      </Link>
    </main>
  );
}
export function AdminPage() {
  const location = useLocation();
  useEffect(() => {
    window.scrollTo({ top: 0, behavior: "instant" });
  }, [location.pathname, location.search]);
  const { "*": path = "" } = useParams();
  const resources: Resource[] = ["contacts", "pages", "categories", "settings"];
  const tab = path.replace(/\/$/, "");
  const detailID = adminProductID(tab);
  return (
    <AdminGate>
      <AdminWorkspace tab={tab} detail={Boolean(detailID)}>
        {tab === "" ? (
          <AdminDashboard />
        ) : tab === "chat" ? (
          <Suspense fallback={<StateView loading />}>
            <ChatPage />
          </Suspense>
        ) : detailID ? (
          <AdminProductView key={detailID} id={detailID} />
        ) : tab === "security/media-access" ? (
          <AdminMediaSecurity />
        ) : tab === "legacy-sync" ? (
          <AdminLegacySync />
        ) : tab === "help" ? (
          <AdminHelp />
        ) : tab === "products" || tab === "reviews" ? (
          <AdminProducts key={tab} review={tab === "reviews"} />
        ) : tab === "users" ? (
          <AdminUsers />
        ) : tab === "administrators" ? (
          <AdminAdministrators />
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
              返回数据看板
            </Link>
          </div>
        )}
      </AdminWorkspace>
    </AdminGate>
  );
}
