import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useRef,
  useState,
  type ReactNode,
} from "react";
import { Link, useLocation } from "react-router-dom";
import { Bell, MessageSquare, X } from "lucide-react";
import { useSession } from "../context";
import { chatAdmin, chatApi, chatTransport, chatRoute } from "../lib/chat-api";
import type {
  ChatList,
  ChatPresence,
  Conversation,
} from "../../../共享/chat-types";
import "../chat.css";
const empty: ChatList = { data: [], unread: 0, total: 0, next_offset: null };
const ChatContext = createContext<{
  list: ChatList;
  revision: number;
  error: string;
  presence: ChatPresence | null;
  presenceError: string;
  refresh: () => Promise<void>;
  setAccepting: (value: boolean) => Promise<void>;
  presenceBusy: boolean;
  connected: boolean;
  connectionError: string;
}>(null!);
export const useChat = () => useContext(ChatContext);
export function ChatProvider({ children }: { children: ReactNode }) {
  const { user, refresh: refreshSession } = useSession();
  const location = useLocation();
  const route = useRef(location);
  route.current = location;
  const [list, setList] = useState<ChatList>(empty),
    [revision, setRevision] = useState(0),
    [error, setError] = useState("");
  const [presence, setPresence] = useState<ChatPresence | null>(null),
    [presenceError, setPresenceError] = useState(""),
    [presenceBusy, setPresenceBusy] = useState(false);
  const [connectionError, setConnectionError] = useState("");
  const [connected, setConnected] = useState(false),
    [toast, setToast] = useState<Conversation | null>(null);
  const seen = useRef(new Map<string, string>()),
    initialized = useRef(false),
    inFlight = useRef(false),
    generation = useRef(0),
    presenceLock = useRef(false);
  const refresh = useCallback(async () => {
    if (!user || inFlight.current) return;
    const current = generation.current;
    inFlight.current = true;
    try {
      const next = await chatApi.list();
      if (current !== generation.current) return;
      const changed = next.data.find(
        (item) =>
          !item.last_message_mine &&
          item.unread > 0 &&
          seen.current.get(item.id) !== item.last_message_id,
      );
      if (
        initialized.current &&
        changed &&
        !(
          document.visibilityState === "visible" &&
          route.current.pathname === chatRoute &&
          new URLSearchParams(route.current.search).get("conversation") ===
            changed.id
        )
      ) {
        setToast(changed);
        if (
          "Notification" in window &&
          Notification.permission === "granted" &&
          document.visibilityState !== "visible"
        ) {
          try {
            const notification = new Notification("清叶 · 新消息", {
              body: "收到一条新的商品咨询消息，点击返回查看。",
              tag: `qingye-chat-${changed.id}`,
            });
            notification.onclick = () => {
              window.focus();
              notification.close();
            };
          } catch {
            /* Some mobile browsers require a service worker; keep in-site delivery active. */
          }
        }
      }
      next.data.forEach((item) =>
        seen.current.set(item.id, item.last_message_id),
      );
      initialized.current = true;
      setList(next);
      setError("");
      setRevision((v) => v + 1);
    } catch (e) {
      if (current !== generation.current) return;
      setError(e instanceof Error ? e.message : "消息读取失败，正在等待重试");
      if ((e as { status?: number }).status === 401) void refreshSession();
    } finally {
      if (current === generation.current) inFlight.current = false;
    }
  }, [user?.id, refreshSession]);
  const updatePresence = useCallback(
    async (accepting?: boolean) => {
      if (!chatAdmin || !user || presenceLock.current) return;
      const current = generation.current;
      presenceLock.current = true;
      setPresenceBusy(true);
      try {
        const next = await chatApi.presence(accepting, true);
        if (current === generation.current) {
          setPresence(next);
          setPresenceError("");
        }
      } catch (e) {
        if (current === generation.current)
          setPresenceError(e instanceof Error ? e.message : "接待状态更新失败");
      } finally {
        if (current === generation.current) {
          presenceLock.current = false;
          setPresenceBusy(false);
        }
      }
    },
    [user?.id],
  );
  useEffect(() => {
    generation.current++;
    inFlight.current = false;
    presenceLock.current = false;
    initialized.current = false;
    seen.current.clear();
    setList(empty);
    setToast(null);
    setPresence(null);
    setError("");
    setPresenceError("");
    setPresenceBusy(false);
    if (!user) return;
    void refresh();
    let scheduled: ReturnType<typeof setTimeout> | undefined;
    chatTransport.connect(
      chatApi.ticket,
      () => {
        if (scheduled) return;
        scheduled = setTimeout(() => {
          scheduled = undefined;
          void refresh();
        }, 150);
      },
      (state, message) => {
        setConnected(state === "connected");
        setConnectionError(message);
        if (state === "connected" && chatAdmin) void updatePresence();
      },
    );
    const poll = setInterval(() => void refresh(), 15000);
    const heartbeat = chatAdmin
      ? setInterval(() => void updatePresence(), 20000)
      : undefined;
    const visible = () => {
      if (document.visibilityState === "visible") {
        void refresh();
        if (chatAdmin) void updatePresence();
      }
    };
    document.addEventListener("visibilitychange", visible);
    return () => {
      generation.current++;
      chatTransport.disconnect();
      setConnected(false);
      clearInterval(poll);
      if (heartbeat) clearInterval(heartbeat);
      if (scheduled) clearTimeout(scheduled);
      document.removeEventListener("visibilitychange", visible);
    };
  }, [user?.id, refresh, updatePresence]);
  useEffect(() => {
    if (!toast) return;
    const timer = setTimeout(() => setToast(null), 7000);
    return () => clearTimeout(timer);
  }, [toast]);
  useEffect(() => {
    const title = document.title.replace(/^\(\d+\) /, "");
    document.title = list.unread ? `(${list.unread}) ${title}` : title;
    return () => {
      document.title = title;
    };
  }, [list.unread]);
  return (
    <ChatContext.Provider
      value={{
        list,
        revision,
        error,
        presence,
        presenceError,
        refresh,
        setAccepting: updatePresence,
        presenceBusy,
        connected,
        connectionError,
      }}
    >
      {children}
      {toast && (
        <div className="chat-toast" role="status">
          <MessageSquare size={19} />
          <Link
            to={`${chatRoute}?conversation=${encodeURIComponent(toast.id)}`}
            onClick={() => setToast(null)}
          >
            <strong>收到新消息</strong>
            <span>
              {chatAdmin ? toast.member_name : toast.admin_name || "平台客服"} ·{" "}
              {toast.product.title}
            </span>
          </Link>
          <button aria-label="关闭消息提醒" onClick={() => setToast(null)}>
            <X size={16} />
          </button>
        </div>
      )}
    </ChatContext.Provider>
  );
}
export function ChatNavLink() {
  const { list } = useChat();
  return (
    <Link
      className="chat-nav-link"
      to={chatRoute}
      aria-label={list.unread ? `消息，${list.unread} 条未读` : "消息"}
    >
      <MessageSquare size={20} />
      <span className="chat-nav-label">消息</span>
      {list.unread > 0 && <b>{list.unread > 99 ? "99+" : list.unread}</b>}
    </Link>
  );
}
export function DesktopNoticeButton() {
  const [note, setNote] = useState("");
  return (
    <div className="chat-notification-control">
      <button
        type="button"
        className="button-outline"
        onClick={async () => {
          if (!window.isSecureContext || !("Notification" in window)) {
            setNote(
              "当前 IP/HTTP 环境使用站内消息提醒；桌面提醒需 HTTPS 和浏览器支持。",
            );
            return;
          }
          try {
            const p = await Notification.requestPermission();
            setNote(
              p === "granted"
                ? "桌面提醒已开启，请保持网站打开。"
                : "未开启桌面提醒，站内未读提示仍然可用。",
            );
          } catch {
            setNote("浏览器未允许桌面提醒，站内提醒仍然可用。");
          }
        }}
      >
        <Bell size={15} />
        桌面提醒
      </button>
      {note && <p role="status">{note}</p>}
    </div>
  );
}
