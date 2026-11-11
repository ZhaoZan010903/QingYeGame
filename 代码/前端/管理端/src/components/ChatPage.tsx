import {
  useCallback,
  useEffect,
  useRef,
  useState,
  type FormEvent,
} from "react";
import { useSearchParams } from "react-router-dom";
import {
  ArrowLeft,
  ArrowDown,
  Check,
  MessageSquare,
  Send,
  Package,
} from "lucide-react";
import { createRequestId } from "../lib/api";
import { useSession } from "../context";
import { chatAdmin, chatApi } from "../lib/chat-api";
import { DesktopNoticeButton, useChat } from "./ChatNotifications";
import { ChatConnectionNotice } from "./ChatConnectionNotice";
import {
  mergeMessages,
  type ChatMessage,
  type ChatProduct,
  type Conversation,
} from "../../../共享/chat-types";
import "../chat.css";

const statusNames = { waiting: "等待接待", active: "咨询中", closed: "已结束" };
const units: Record<string, string> = {
  account: "元",
  hour: "元/小时",
  day: "元/日",
  week: "元/周",
  month: "元/月",
  unknown: "",
};
const errorText = (e: unknown) =>
  e instanceof Error ? e.message : "操作失败，请稍后重试。";
const date = (value: string) =>
  new Date(value).toLocaleString("zh-CN", {
    month: "2-digit",
    day: "2-digit",
    hour: "2-digit",
    minute: "2-digit",
  });
function ProductCard({ product }: { product: ChatProduct }) {
  const [failed, setFailed] = useState(false);
  const site = chatAdmin
    ? String(import.meta.env.VITE_PUBLIC_SITE_URL || "").replace(/\/$/, "")
    : "";
  return (
    <a
      className="chat-product-card"
      href={`${site}/accounts/${encodeURIComponent(product.id)}`}
      target="_blank"
      rel="noreferrer"
    >
      <span className="chat-product-cover">
        {product.cover_url && !failed ? (
          <img
            src={product.cover_url}
            onError={() => setFailed(true)}
            alt="咨询商品封面"
          />
        ) : (
          <Package size={22} />
        )}
      </span>
      <span>
        <small>正在咨询的商品 · 发起时的资料</small>
        <strong>{product.title}</strong>
        <b>
          {product.price !== null && product.unit !== "unknown"
            ? `¥ ${Number(product.price).toLocaleString("zh-CN")} ${units[product.unit] || ""}`
            : "联系客服报价"}
        </b>
      </span>
    </a>
  );
}

export function ChatPage() {
  const { user } = useSession();
  const {
    list,
    revision,
    refresh,
    error: listError,
    presence,
    presenceError,
    setAccepting,
    presenceBusy,
    connected,
    connectionError,
  } = useChat();
  const [params, setParams] = useSearchParams();
  const selected = params.get("conversation") || "",
    productId = params.get("product") || "";
  const [conversation, setConversation] = useState<Conversation | null>(null),
    [messages, setMessages] = useState<ChatMessage[]>([]);
  const [before, setBefore] = useState<string | null>(null),
    [older, setOlder] = useState<Conversation[]>([]),
    [nextOffset, setNextOffset] = useState<number | null>(null);
  const [text, setText] = useState(""),
    [error, setError] = useState(""),
    [starting, setStarting] = useState(false),
    [startRetry, setStartRetry] = useState(0);
  const [loading, setLoading] = useState(false),
    [loadingOlder, setLoadingOlder] = useState(false),
    [loadingList, setLoadingList] = useState(false),
    [sending, setSending] = useState(false),
    [closing, setClosing] = useState(false),
    [atBottom, setAtBottom] = useState(true);
  const sequence = useRef(0),
    currentID = useRef(selected),
    loaded = useRef(false),
    inFlight = useRef(false),
    historyBusy = useRef(false),
    messageCache = useRef<ChatMessage[]>([]),
    lastRead = useRef("");
  const drafts = useRef(new Map<string, string>()),
    pending = useRef<{ id: string; body: string; client: string } | null>(null),
    scroll = useRef<HTMLDivElement>(null),
    bottom = useRef(true);
  currentID.current = selected;
  useEffect(() => {
    setNextOffset(list.next_offset);
  }, [list.next_offset]);
  useEffect(() => {
    if (!productId || chatAdmin || !connected) return;
    let active = true;
    setStarting(true);
    setError("");
    chatApi
      .start(productId)
      .then((id) => {
        if (active) {
          setParams({ conversation: id }, { replace: true });
          void refresh();
        }
      })
      .catch((e) => {
        if (active) setError(errorText(e));
      })
      .finally(() => {
        if (active) setStarting(false);
      });
    return () => {
      active = false;
    };
  }, [productId, connected, startRetry, user?.id]);
  useEffect(() => {
    drafts.current.clear();
    pending.current = null;
    setOlder([]);
  }, [user?.id]);
  useEffect(() => {
    sequence.current++;
    loaded.current = false;
    inFlight.current = false;
    historyBusy.current = false;
    messageCache.current = [];
    lastRead.current = "";
    bottom.current = true;
    setConversation(null);
    setMessages([]);
    setBefore(null);
    setText(drafts.current.get(selected) || "");
    setError("");
    setAtBottom(true);
    setLoadingOlder(false);
    setLoading(!!selected);
    return () => {
      sequence.current++;
    };
  }, [selected, user?.id]);
  const latest = useCallback(async () => {
    if (!selected || inFlight.current || historyBusy.current) return;
    const current = sequence.current;
    inFlight.current = true;
    try {
      const detail = await chatApi.detail(selected);
      const cached = messageCache.current;
      let page = await chatApi.messages(
        selected,
        loaded.current && cached.length ? { after: cached.at(-1)!.id } : {},
      );
      const initial = !loaded.current;
      let batch = page.data,
        pages = 0;
      // Read forward without gaps when more than one page arrived while disconnected.
      while (page.after && pages++ < 9) {
        page = await chatApi.messages(selected, { after: page.after });
        batch = mergeMessages(batch, page.data);
      }
      if (current !== sequence.current) return;
      setConversation(detail);
      if (initial) setBefore(page.before);
      const next = initial ? batch : mergeMessages(cached, batch);
      messageCache.current = next;
      setMessages(next);
      loaded.current = true;
      setError("");
      if (bottom.current)
        requestAnimationFrame(() => {
          if (current === sequence.current && scroll.current)
            scroll.current.scrollTop = scroll.current.scrollHeight;
        });
    } catch (e) {
      if (current === sequence.current) {
        setError(errorText(e));
        if ((e as { status?: number }).status === 404) {
          setConversation(null);
          setMessages([]);
          messageCache.current = [];
        }
      }
    } finally {
      if (current === sequence.current) {
        inFlight.current = false;
        setLoading(false);
      }
    }
  }, [selected, user?.id]);
  useEffect(() => {
    void latest();
  }, [latest, revision]);
  const markRead = useCallback(() => {
    const last = messages.at(-1)?.id;
    if (
      !connected ||
      !loaded.current ||
      messageCache.current !== messages ||
      !selected ||
      !last ||
      !atBottom ||
      document.visibilityState !== "visible" ||
      lastRead.current === last
    )
      return;
    lastRead.current = last;
    chatApi.read(selected, last).catch(() => {
      if (currentID.current === selected) lastRead.current = "";
    });
  }, [selected, messages, atBottom, connected]);
  useEffect(() => {
    markRead();
    const check = () => markRead();
    document.addEventListener("visibilitychange", check);
    return () => document.removeEventListener("visibilitychange", check);
  }, [markRead, revision]);
  async function loadOlder() {
    if (!before || inFlight.current || historyBusy.current) return;
    const current = sequence.current,
      height = scroll.current?.scrollHeight || 0,
      top = scroll.current?.scrollTop || 0;
    historyBusy.current = true;
    setLoadingOlder(true);
    try {
      const page = await chatApi.messages(selected, { before });
      if (current !== sequence.current) return;
      const next = mergeMessages(page.data, messageCache.current);
      messageCache.current = next;
      setMessages(next);
      setBefore(page.before);
      bottom.current = false;
      setAtBottom(false);
      requestAnimationFrame(() => {
        if (current === sequence.current && scroll.current)
          scroll.current.scrollTop = top + scroll.current.scrollHeight - height;
      });
    } catch (e) {
      if (current === sequence.current) setError(errorText(e));
    } finally {
      if (current === sequence.current) {
        historyBusy.current = false;
        setLoadingOlder(false);
      }
    }
  }
  async function loadList() {
    if (nextOffset === null || loadingList) return;
    setLoadingList(true);
    try {
      const page = await chatApi.list(nextOffset);
      setOlder((old) => [
        ...new Map(
          [...old, ...page.data].map((item) => [item.id, item]),
        ).values(),
      ]);
      setNextOffset(page.next_offset);
    } catch (e) {
      setError(errorText(e));
    } finally {
      setLoadingList(false);
    }
  }
  async function send(event: FormEvent) {
    event.preventDefault();
    const body = text.trim();
    if (!body || sending || !conversation || conversation.status === "closed")
      return;
    if (Array.from(body).length > 2000) {
      setError("每条消息最多 2000 字。");
      return;
    }
    if (
      !pending.current ||
      pending.current.body !== body ||
      pending.current.id !== selected
    )
      pending.current = { id: selected, body, client: createRequestId() };
    const input = pending.current;
    setSending(true);
    setError("");
    try {
      await chatApi.send(input.id, input.body, input.client);
      drafts.current.delete(input.id);
      if (currentID.current === input.id) {
        setText("");
        bottom.current = true;
        setAtBottom(true);
        await latest();
      }
      pending.current = null;
      void refresh();
    } catch (e) {
      if (currentID.current === input.id)
        setError(`${errorText(e)} 内容已保留，可以重新发送。`);
    } finally {
      setSending(false);
    }
  }
  async function closeConversation() {
    if (
      !window.confirm(
        "结束这次咨询？聊天记录会保留。如需再次咨询，可从商品页重新发起。",
      )
    )
      return;
    setClosing(true);
    try {
      await chatApi.close(selected);
      await latest();
      void refresh();
    } catch (e) {
      setError(errorText(e));
    } finally {
      setClosing(false);
    }
  }
  const conversations = [
    ...list.data,
    ...older.filter((item) => !list.data.some((row) => row.id === item.id)),
  ];
  return (
    <section
      className={`chat-page ${chatAdmin ? "chat-admin" : "chat-member"} ${selected ? "has-selection" : ""}`}
    >
      <header className="chat-page-heading">
        <div>
          <span className="chat-eyebrow">
            {chatAdmin ? "接待工作区" : "我的消息"}
          </span>
          <h1>{chatAdmin ? "在线咨询" : "与清叶聊一聊"}</h1>
          <p>
            {chatAdmin
              ? "查看商品资料，直接回复咨询用户。"
              : "商品和聊天放在一起，沟通更清楚。"}
          </p>
        </div>
        <DesktopNoticeButton />
      </header>
      {!connected && <ChatConnectionNotice message={connectionError} />}
      {chatAdmin && (
        <div className="chat-presence">
          <div>
            <span
              className={`chat-presence-dot ${presence?.accepting ? "accepting" : ""}`}
            />
            <strong>
              {presence?.accepting
                ? presence.active >= presence.capacity
                  ? "接待已满"
                  : "正在接待"
                : "暂停接待新咨询"}
            </strong>
            <span>
              当前 {presence?.active ?? "—"} / {presence?.capacity ?? 3} 个咨询
              · 等待分配 {presence?.waiting ?? "—"} 个
            </span>
          </div>
          <button
            className="button-outline"
            disabled={presenceBusy || !connected}
            onClick={() => void setAccepting(!presence?.accepting)}
          >
            {presenceBusy
              ? "正在更新…"
              : presence?.accepting
                ? "暂停接待"
                : "开始接待"}
          </button>
          <small>
            保持后台打开即保持在线。暂停只停止接收新咨询，已有对话仍可回复。
          </small>
          {presenceError && (
            <p className="chat-error" role="alert">
              {presenceError}
            </p>
          )}
        </div>
      )}
      {starting && (
        <p className="chat-info" role="status">
          正在连接商品咨询…
        </p>
      )}
      {productId && error && (
        <p className="chat-error" role="alert">
          {error}
          <button onClick={() => setStartRetry((v) => v + 1)}>重新连接</button>
        </p>
      )}
      <div className="chat-workspace">
        <aside className="chat-list">
          <div className="chat-list-heading">
            <strong>
              全部咨询 <span>{list.total}</span>
            </strong>
            <small>{connected ? "实时连接" : "连接未就绪"}</small>
          </div>
          {listError && (
            <p className="chat-error" role="alert">
              {listError}
              <button onClick={() => void refresh()}>重试</button>
            </p>
          )}
          {conversations.map((item) => (
            <button
              key={item.id}
              className={`conversation-item ${selected === item.id ? "active" : ""}`}
              onClick={() => setParams({ conversation: item.id })}
            >
              <span className="conversation-avatar">
                {(chatAdmin
                  ? item.member_name
                  : item.admin_name || "清叶"
                ).slice(0, 1)}
              </span>
              <span className="conversation-copy">
                <span>
                  <strong>
                    {chatAdmin
                      ? item.member_name
                      : item.admin_name || "等待客服接待"}
                  </strong>
                  <time>{date(item.updated_at)}</time>
                </span>
                <b>{item.product.title}</b>
                <span>
                  <small>{item.last_message || statusNames[item.status]}</small>
                  {item.unread > 0 && (
                    <i>{item.unread > 99 ? "99+" : item.unread}</i>
                  )}
                </span>
                <em>{statusNames[item.status]}</em>
              </span>
            </button>
          ))}
          {!listError && !conversations.length && (
            <div className="chat-empty">
              <MessageSquare size={32} />
              <h3>{chatAdmin ? "还没有分配的咨询" : "还没有咨询记录"}</h3>
              <p>
                {chatAdmin
                  ? "开启接待后，系统会随机分配待接待的用户。"
                  : "在商品详情点击“咨询这个账号”即可开始。"}
              </p>
            </div>
          )}
          {nextOffset !== null && (
            <button
              className="chat-load-more"
              disabled={loadingList}
              onClick={() => void loadList()}
            >
              {loadingList ? "正在读取…" : "更多咨询记录"}
            </button>
          )}
        </aside>
        <div className="chat-room">
          {selected ? (
            <>
              <header className="chat-room-heading">
                <button
                  className="chat-back"
                  aria-label="返回咨询列表"
                  onClick={() => setParams({})}
                >
                  <ArrowLeft size={19} />
                </button>
                <div>
                  <strong>
                    {conversation
                      ? chatAdmin
                        ? conversation.member_name
                        : conversation.admin_name || "平台接待队列"
                      : "正在读取咨询…"}
                  </strong>
                  <small>
                    {conversation ? statusNames[conversation.status] : ""}
                    {conversation?.status === "active"
                      ? " · 由平台管理员为你服务"
                      : ""}
                  </small>
                </div>
                {conversation?.status !== "closed" && conversation && (
                  <button
                    className="button-outline"
                    disabled={closing || sending}
                    onClick={() => void closeConversation()}
                  >
                    {closing ? "处理中…" : "结束咨询"}
                  </button>
                )}
              </header>
              {conversation?.status === "waiting" && (
                <p className="chat-queue" role="status">
                  暂时没有空闲管理员，已进入等待队列。你可以先留言，有人接待时会收到提醒。
                </p>
              )}
              <div
                className="chat-history"
                ref={scroll}
                onScroll={() => {
                  const node = scroll.current;
                  if (node) {
                    bottom.current =
                      node.scrollHeight - node.scrollTop - node.clientHeight <
                      65;
                    setAtBottom(bottom.current);
                  }
                }}
              >
                {before && (
                  <button
                    className="chat-load-more"
                    disabled={loadingOlder}
                    onClick={() => void loadOlder()}
                  >
                    {loadingOlder ? "正在加载…" : "查看更早的消息"}
                  </button>
                )}
                {loading && (
                  <p className="chat-info" role="status">
                    正在读取聊天记录…
                  </p>
                )}
                {messages.map((item) => (
                  <div
                    key={item.id}
                    className={`chat-message ${item.mine ? "mine" : ""}`}
                  >
                    <small>
                      {item.mine
                        ? "我"
                        : item.sender_role === "admin"
                          ? "平台客服"
                          : conversation?.member_name}{" "}
                      · {date(item.created_at)}
                    </small>
                    {item.kind === "product" && conversation ? (
                      <ProductCard product={conversation.product} />
                    ) : (
                      <p>{item.body}</p>
                    )}
                    {item.mine && item.kind === "text" && (
                      <span className="chat-sent">
                        <Check size={11} />
                        已发送
                      </span>
                    )}
                  </div>
                ))}
              </div>
              {!atBottom && (
                <button
                  className="chat-newest"
                  onClick={() => {
                    if (scroll.current)
                      scroll.current.scrollTop = scroll.current.scrollHeight;
                    bottom.current = true;
                    setAtBottom(true);
                  }}
                >
                  <ArrowDown size={14} />
                  回到最新消息
                </button>
              )}
              {error && !productId && (
                <div className="chat-error" role="alert">
                  {error}
                  <button onClick={() => void latest()}>重新读取</button>
                </div>
              )}
              <form className="chat-composer" onSubmit={send}>
                {conversation?.status === "closed" ? (
                  <div className="chat-closed">
                    本次咨询已结束，记录保留供查阅。
                    <a
                      href={`${chatAdmin ? String(import.meta.env.VITE_PUBLIC_SITE_URL || "").replace(/\/$/, "") : ""}/accounts/${conversation.product.id}`}
                      target="_blank"
                      rel="noreferrer"
                    >
                      查看商品 ↗
                    </a>
                  </div>
                ) : (
                  <>
                    <textarea
                      aria-label="输入咨询消息"
                      placeholder={
                        chatAdmin ? "输入回复内容…" : "说说你想了解的商品资料…"
                      }
                      value={text}
                      maxLength={4000}
                      disabled={sending || !conversation}
                      onChange={(e) => {
                        setText(e.target.value);
                        drafts.current.set(selected, e.target.value);
                      }}
                      onKeyDown={(e) => {
                        if (
                          e.key === "Enter" &&
                          !e.shiftKey &&
                          !e.nativeEvent.isComposing
                        ) {
                          e.preventDefault();
                          void send(e);
                        }
                      }}
                    />
                    <div>
                      <span>Enter 发送 · Shift + Enter 换行</span>
                      <button
                        type="submit"
                        className="button-white"
                        disabled={
                          sending || !text.trim() || !conversation || !connected
                        }
                      >
                        <Send size={15} />
                        {sending ? "发送中…" : "发送"}
                      </button>
                    </div>
                  </>
                )}
              </form>
            </>
          ) : (
            <div className="chat-empty chat-room-empty">
              <MessageSquare size={42} />
              <h2>选择一个咨询，开始沟通</h2>
              <p>商品资料、历史消息都会保留在对应对话中。</p>
              {chatAdmin && <p>点击上方“开始接待”，即可接收用户咨询。</p>}
            </div>
          )}
        </div>
      </div>
    </section>
  );
}
