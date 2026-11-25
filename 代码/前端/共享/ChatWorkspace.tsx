import { Button } from "@chatui/core/es/components/Button";
import { Input } from "@chatui/core/es/components/Input";
import { Navbar } from "@chatui/core/es/components/Navbar";
import {
  useCallback,
  useEffect,
  useRef,
  useState,
  type FormEvent,
  type ReactNode,
} from "react";
import {
  ArrowLeft,
  ArrowDown,
  MessageSquare,
  Send,
  Package,
  Smile,
  X,
} from "lucide-react";
import {
  isReadThrough,
  mergeMessages,
  nextChatPosition,
  quoteMessage,
  type ChatList,
  type ChatListPosition,
  type ChatMessage,
  type ChatPresence,
  type ChatQuote,
  type Conversation,
  type MessagePage,
  type ChatImage,
} from "./chat-types";
import {
  ChatAvatar,
  ChatLightbox,
  ImageDrafts,
  ImagePicker,
  useChatImages,
  type ChatMediaServices,
} from "./ChatMedia";
import { ChatOutbox } from "./chat-outbox";
import { chatDate as date, ChatThread, MessageQuote } from "./ChatThread";

type ChatDraft = { text: string; quote?: ChatQuote };
const emptyDraft: ChatDraft = { text: "" };

interface ChatWorkspaceProps {
  chatAdmin: boolean;
  selected: string;
  productId: string;
  navigate: (params: Record<string, string>, replace?: boolean) => void;
  productHref: (id: string) => string;
  user: { id: string; nickname: string } | null;
  notificationControl: ReactNode;
  connectionNotice: ReactNode;
  media: ChatMediaServices;
  api: {
    profile: () => Promise<{ avatar_url: string }>;
    start: (product: string) => Promise<string>;
    list: (position?: Exclude<ChatListPosition, null>) => Promise<ChatList>;
    detail: (id: string) => Promise<Conversation>;
    messages: (
      id: string,
      page: { before?: string; after?: string },
    ) => Promise<MessagePage>;
    send: (
      id: string,
      body: string,
      client: string,
      images: string[],
      replyTo?: string,
    ) => Promise<unknown>;
    read: (id: string, through: string) => Promise<unknown>;
    close: (id: string) => Promise<unknown>;
  };
  chat: {
    list: ChatList;
    revision: number;
    error: string;
    presence: ChatPresence | null;
    presenceError: string;
    refresh: () => Promise<void>;
    setAccepting: (value: boolean) => Promise<void>;
    presenceBusy: boolean;
    connected: boolean;
  };
}

const emojis = [
  "😊",
  "😄",
  "🥰",
  "👍",
  "🙏",
  "🤝",
  "🎉",
  "❤️",
  "✨",
  "👀",
  "👌",
  "🤔",
  "😅",
  "😭",
  "🙌",
  "🌿",
];
const statusNames = { waiting: "等待接待", active: "咨询中", closed: "已结束" };
const errorText = (e: unknown) =>
  e instanceof Error ? e.message : "操作失败，请稍后重试。";
export function ChatWorkspace({
  chatAdmin,
  selected,
  productId,
  navigate,
  productHref,
  user,
  notificationControl,
  connectionNotice,
  api,
  media,
  chat,
}: ChatWorkspaceProps) {
  const pageElement = useRef<HTMLElement>(null);
  useEffect(() => {
    const viewport = window.visualViewport;
    const resize = () =>
      pageElement.current?.style.setProperty(
        "--chat-visual-height",
        `${viewport?.height ?? window.innerHeight}px`,
      );
    resize();
    viewport?.addEventListener("resize", resize);
    return () => viewport?.removeEventListener("resize", resize);
  }, []);
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
  } = chat;
  const [conversation, setConversation] = useState<Conversation | null>(null),
    [messages, setMessages] = useState<ChatMessage[]>([]);
  const [before, setBefore] = useState<string | null>(null),
    [older, setOlder] = useState<Conversation[]>([]),
    [nextOffset, setNextOffset] = useState<ChatListPosition>(null);
  const expandedList = useRef(false),
    listBusy = useRef(false),
    listGeneration = useRef(0);
  const previousHead = useRef<Conversation[]>([]);
  const [emojiOpen, setEmojiOpen] = useState(false);
  const composer = useRef<HTMLInputElement | HTMLTextAreaElement>(null);
  const startRequest = useRef<{ key: string; promise: Promise<string> } | null>(
    null,
  );
  const [draft, setDraft] = useState<ChatDraft>(emptyDraft);
  const { text, quote } = draft;
  const [error, setError] = useState(""),
    [starting, setStarting] = useState(false),
    [startRetry, setStartRetry] = useState(0);
  const images = useChatImages(user?.id || "", selected, setError, media);
  const [myAvatar, setMyAvatar] = useState("");
  useEffect(() => {
    let active = true;
    setMyAvatar("");
    if (user?.id) {
      api
        .profile()
        .then((profile) => {
          if (active) setMyAvatar(profile.avatar_url);
        })
        .catch(() => {});
    }
    return () => {
      active = false;
    };
  }, [user?.id, api]);
  const [gallery, setGallery] = useState<{
    images: ChatImage[];
    index: number;
  } | null>(null);
  const sendLock = useRef(false);
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
    latestQueued = useRef(false),
    messageCache = useRef<ChatMessage[]>([]),
    lastRead = useRef("");
  const [initialRead, setInitialRead] = useState<string | null>(null);
  const tail = useRef<HTMLDivElement>(null);
  const drafts = useRef(new Map<string, ChatDraft>()),
    pending = useRef(new ChatOutbox()),
    scroll = useRef<HTMLDivElement>(null),
    bottom = useRef(true);
  currentID.current = selected;
  useEffect(() => {
    expandedList.current = false;
    previousHead.current = [];
    listBusy.current = false;
    listGeneration.current += 1;
    setOlder([]);
    setLoadingList(false);
    return () => {
      listGeneration.current += 1;
    };
  }, [user?.id]);
  useEffect(() => {
    // Live first-page refreshes must not rewind an already expanded list.
    if (!expandedList.current) setNextOffset(nextChatPosition(list));
    else {
      const previous = previousHead.current;
      setOlder((old) => [
        ...new Map(
          [...old, ...previous].map((item) => [item.id, item]),
        ).values(),
      ]);
    }
    previousHead.current = list.data;
  }, [list, user?.id]);
  useEffect(() => {
    if (!productId) {
      startRequest.current = null;
      return;
    }
    if (chatAdmin || !connected) return;
    let active = true;
    setStarting(true);
    setError("");
    const key = `${user?.id}:${productId}:${startRetry}`;
    if (startRequest.current?.key !== key)
      startRequest.current = { key, promise: api.start(productId) };
    startRequest.current.promise
      .then((id) => {
        if (active) {
          navigate({ conversation: id }, true);
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
    pending.current.clear();
    setOlder([]);
  }, [user?.id]);
  useEffect(() => {
    sequence.current++;
    loaded.current = false;
    inFlight.current = false;
    historyBusy.current = false;
    latestQueued.current = false;
    messageCache.current = [];
    lastRead.current = "";
    bottom.current = true;
    setGallery(null);
    setConversation(null);
    setMessages([]);
    setBefore(null);
    setInitialRead(null);
    setDraft(drafts.current.get(selected) ?? emptyDraft);
    setEmojiOpen(false);
    setError("");
    setAtBottom(true);
    setLoadingOlder(false);
    setLoading(!!selected);
    return () => {
      sequence.current++;
    };
  }, [selected, user?.id]);
  const latest = useCallback(
    async function fetchLatest() {
      if (!selected || currentID.current !== selected) return;
      if (inFlight.current || historyBusy.current) {
        latestQueued.current = true;
        return;
      }
      latestQueued.current = false;
      const current = sequence.current;
      inFlight.current = true;
      try {
        const detail = await api.detail(selected);
        if (current !== sequence.current) return;
        const cached = messageCache.current;
        // A receipt changes the conversation metadata without generating a new message.
        if (
          loaded.current &&
          (cached.at(-1)?.id ?? "0") === detail.last_message_id
        ) {
          setConversation(detail);
          setError("");
          return;
        }
        let page = await api.messages(
          selected,
          loaded.current && cached.length ? { after: cached.at(-1)!.id } : {},
        );
        const initial = !loaded.current;
        let batch = page.data,
          pages = 0;
        // Read forward without gaps when more than one page arrived while disconnected.
        while (page.after && pages++ < 9) {
          page = await api.messages(selected, { after: page.after });
          batch = mergeMessages(batch, page.data);
        }
        if (current !== sequence.current) return;
        setConversation(detail);
        if (initial) {
          setBefore(page.before);
        }
        if ((initial || !bottom.current) && detail.unread > 0)
          setInitialRead(detail.read_through ?? "0");
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
            latestQueued.current = false;
            messageCache.current = [];
          }
        }
      } finally {
        if (current === sequence.current) {
          inFlight.current = false;
          setLoading(false);
          if (latestQueued.current) void fetchLatest();
        }
      }
    },
    [selected, user?.id, api],
  );
  useEffect(() => {
    void latest();
  }, [latest, revision]);
  useEffect(() => {
    const node = scroll.current;
    if (!node) return;
    const observer = new ResizeObserver(() => {
      if (bottom.current) node.scrollTop = node.scrollHeight;
    });
    observer.observe(node);
    return () => observer.disconnect();
  }, [selected]);
  let latestIncoming: string | undefined;
  for (let index = messages.length - 1; index >= 0; index--) {
    if (!messages[index].mine) {
      latestIncoming = messages[index].id;
      break;
    }
  }
  const markRead = useCallback(() => {
    const node = scroll.current;
    const end = tail.current;
    if (
      !node ||
      !end ||
      !connected ||
      !selected ||
      !conversation ||
      !latestIncoming ||
      !loaded.current ||
      messageCache.current !== messages ||
      currentID.current !== selected ||
      !bottom.current ||
      document.visibilityState !== "visible" ||
      isReadThrough(latestIncoming, conversation.read_through) ||
      isReadThrough(latestIncoming, lastRead.current || undefined)
    )
      return;

    // The pinned tail must be visible in both the transcript and the actual viewport.
    const edge = end.getBoundingClientRect();
    const room = node.getBoundingClientRect();
    const viewport = window.visualViewport;
    const top = viewport?.offsetTop ?? 0;
    const bottomEdge = top + (viewport?.height ?? window.innerHeight);
    if (
      edge.top < Math.max(room.top, top) ||
      edge.bottom > Math.min(room.bottom, bottomEdge) + 1
    )
      return;

    const current = sequence.current;
    lastRead.current = latestIncoming;
    api.read(selected, latestIncoming).catch(() => {
      if (current === sequence.current && lastRead.current === latestIncoming)
        lastRead.current = "";
    });
  }, [api, selected, messages, conversation, latestIncoming, connected]);
  useEffect(() => {
    if (!tail.current || !atBottom || !conversation || !latestIncoming) return;
    let frame = 0;
    const check = () => {
      cancelAnimationFrame(frame);
      frame = requestAnimationFrame(markRead);
    };
    const observer = new IntersectionObserver(([entry]) => {
      if (entry?.isIntersecting) check();
    });
    observer.observe(tail.current);
    document.addEventListener("visibilitychange", check);
    check();
    return () => {
      observer.disconnect();
      document.removeEventListener("visibilitychange", check);
      cancelAnimationFrame(frame);
    };
  }, [markRead, atBottom, revision, conversation, latestIncoming]);

  async function loadOlder() {
    if (!before || inFlight.current || historyBusy.current) return;
    const current = sequence.current,
      height = scroll.current?.scrollHeight || 0,
      top = scroll.current?.scrollTop || 0;
    historyBusy.current = true;
    setLoadingOlder(true);
    try {
      const page = await api.messages(selected, { before });
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
        if (latestQueued.current) void latest();
      }
    }
  }
  async function loadList() {
    if (nextOffset === null || listBusy.current) return;
    listBusy.current = true;
    const generation = listGeneration.current;
    const head = list.data;
    setLoadingList(true);
    try {
      const page = await api.list(nextOffset);
      if (generation !== listGeneration.current) return;
      expandedList.current = true;
      setOlder((old) => [
        ...new Map(
          [...old, ...head, ...page.data].map((item) => [item.id, item]),
        ).values(),
      ]);
      setNextOffset(nextChatPosition(page));
    } catch (e) {
      if (generation === listGeneration.current) setError(errorText(e));
    } finally {
      if (generation === listGeneration.current) {
        listBusy.current = false;
        setLoadingList(false);
      }
    }
  }
  function updateDraft(next: ChatDraft) {
    drafts.current.set(selected, next);
    setDraft(next);
  }
  function selectReply(message: ChatMessage) {
    updateDraft({ ...draft, quote: quoteMessage(message) });
    composer.current?.focus();
  }
  function jumpToMessage(id: string) {
    const row = document.getElementById("chat-message-" + id);
    if (!row || !scroll.current?.contains(row)) return;
    row.focus({ preventScroll: true });
    const node = scroll.current;
    const offset =
      row.getBoundingClientRect().top - node.getBoundingClientRect().top;
    node.scrollTop +=
      offset - Math.max(12, (node.clientHeight - row.clientHeight) / 2);
  }
  function scrollToLatest() {
    if (scroll.current) scroll.current.scrollTop = scroll.current.scrollHeight;
    bottom.current = true;
    setAtBottom(true);
    markRead();
  }
  function insertText(value: string) {
    const node = composer.current;
    const start = node?.selectionStart ?? text.length,
      end = node?.selectionEnd ?? text.length;
    const next = text.slice(0, start) + value + text.slice(end);
    if (Array.from(next).length > 2000) return;
    updateDraft({ ...draft, text: next });
    setEmojiOpen(false);
    requestAnimationFrame(() => {
      node?.focus();
      node?.setSelectionRange(start + value.length, start + value.length);
    });
  }
  async function send(event: FormEvent) {
    event.preventDefault();
    const body = text.trim();
    if (
      (!body && !images.items.length) ||
      sendLock.current ||
      !images.ready ||
      !conversation ||
      conversation.status === "closed"
    )
      return;
    if (!connected) {
      setError("连接恢复后即可发送，输入内容已保留。");
      return;
    }
    if (Array.from(body).length > 2000) {
      setError("每条消息最多 2000 字。");
      return;
    }
    const input = pending.current.prepare(
      selected,
      body,
      media.createRequestId,
      images.items.map((image) => image.mediaID!),
      quote?.id,
    );
    const keys = images.items.map((image) => image.key);
    const generation = listGeneration.current;
    sendLock.current = true;
    setSending(true);
    setError("");
    try {
      await api.send(
        input.id,
        input.body,
        input.client,
        input.images,
        input.replyToID,
      );
      if (generation !== listGeneration.current) return;
      images.clear(input.id, keys);
      drafts.current.delete(input.id);
      if (currentID.current === input.id) {
        setDraft(emptyDraft);
        bottom.current = true;
        setAtBottom(true);
        await latest();
      }
      pending.current.acknowledge(input);
      void refresh();
    } catch (e) {
      if (
        generation === listGeneration.current &&
        currentID.current === input.id
      )
        setError(`${errorText(e)} 内容已保留，可以重新发送。`);
    } finally {
      sendLock.current = false;
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
      await api.close(selected);
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
  const unreadFrom =
    initialRead === null
      ? undefined
      : messages.find(
          (item) => !item.mine && !isReadThrough(item.id, initialRead),
        )?.id;
  const canCompose = !!conversation && conversation.status !== "closed";
  const canReply =
    canCompose && conversation?.peer_read_through !== undefined && !sending;
  const currentPresence = connected && !presenceError ? presence : null;
  return (
    <section
      ref={pageElement}
      aria-label={chatAdmin ? "在线咨询" : "我的消息"}
      className={`chat-page ${chatAdmin ? "chat-admin" : "chat-member"} ${selected ? "has-selection" : ""}`}
    >
      <div className="chat-page-tools" aria-label="消息通知设置">
        {notificationControl}
      </div>
      {!connected && connectionNotice}
      {chatAdmin && (
        <div className="chat-presence">
          <div>
            <span
              className={`chat-presence-dot ${currentPresence?.accepting ? "accepting" : ""}`}
              aria-hidden="true"
            />
            <strong>
              {!connected
                ? "接待连接未就绪"
                : !currentPresence
                  ? presenceError
                    ? "接待状态暂不可用"
                    : "正在读取接待状态"
                  : currentPresence.accepting
                    ? currentPresence.active >= currentPresence.capacity
                      ? "接待已满"
                      : "正在接待"
                    : "暂停接待新咨询"}
            </strong>
            <span>
              我的接待 {currentPresence?.active ?? "—"} /{" "}
              {currentPresence?.capacity ?? "—"}
              {" · "}全站待分配 {currentPresence?.waiting ?? "—"}
            </span>
          </div>
          <Button
            className="button-outline"
            disabled={presenceBusy || !currentPresence}
            onClick={() =>
              currentPresence && void setAccepting(!currentPresence.accepting)
            }
          >
            {presenceBusy
              ? "正在更新…"
              : currentPresence?.accepting
                ? "暂停接待"
                : "开始接待"}
          </Button>
          <small>
            开始接待后，系统会按空余名额分配咨询。暂停接待后仍可回复已有对话。
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
          <Button onClick={() => setStartRetry((v) => v + 1)}>重新连接</Button>
        </p>
      )}
      {gallery && (
        <ChatLightbox gallery={gallery} close={() => setGallery(null)} />
      )}
      <div className="chat-workspace">
        <aside className="chat-list">
          <div className="chat-list-heading">
            <strong>
              {chatAdmin ? "分配给我的咨询" : "全部咨询"}{" "}
              <span>{list.total}</span>
            </strong>
            <small>{connected ? "实时连接" : "连接未就绪"}</small>
          </div>
          {listError && (
            <p className="chat-error" role="alert">
              {listError}
              <Button onClick={() => void refresh()}>重试</Button>
            </p>
          )}
          {conversations.map((item) => (
            <Button
              key={item.id}
              aria-current={selected === item.id ? "true" : undefined}
              className={`conversation-item ${selected === item.id ? "active" : ""}`}
              onClick={() => navigate({ conversation: item.id })}
            >
              <ChatAvatar
                className="conversation-avatar"
                src={chatAdmin ? item.member_avatar : item.admin_avatar}
                name={chatAdmin ? item.member_name : item.admin_name || "清叶"}
              />
              <span className="conversation-copy">
                <span>
                  <strong>
                    {chatAdmin
                      ? item.member_name
                      : item.admin_name || "等待客服接待"}
                  </strong>
                  <time>{date(item.updated_at)}</time>
                </span>
                <b>
                  {item.product_count && item.product_count > 1
                    ? `已咨询 ${item.product_count} 个账号 · `
                    : ""}
                  {item.product.code || item.product.title}
                </b>
                <span>
                  <small>{item.last_message || statusNames[item.status]}</small>
                  {item.unread > 0 && (
                    <i title={`${item.unread} 条未读消息`}>
                      {item.unread > 99 ? "99+" : item.unread}
                    </i>
                  )}
                </span>
                <em>{statusNames[item.status]}</em>
              </span>
            </Button>
          ))}
          {!listError && !conversations.length && (
            <div className="chat-empty">
              <MessageSquare size={32} />
              <h3>{chatAdmin ? "还没有分配的咨询" : "还没有咨询记录"}</h3>
              <p>
                {chatAdmin
                  ? "开启接待后，系统会随机分配待接待的用户。"
                  : "从商品详情发起咨询。不同账号的咨询会归入同一对话。"}
              </p>
            </div>
          )}
          {nextOffset !== null && (
            <Button
              className="chat-load-more"
              disabled={loadingList}
              onClick={() => void loadList()}
            >
              {loadingList ? "正在读取…" : "更多咨询记录"}
            </Button>
          )}
        </aside>
        <div className="chat-room">
          {selected ? (
            <>
              <div className="chat-room-heading">
                <Button
                  className="chat-back"
                  aria-label="返回咨询列表"
                  onClick={() => navigate({})}
                >
                  <ArrowLeft size={19} aria-hidden="true" />
                </Button>
                <ChatAvatar
                  className="chat-room-avatar"
                  src={
                    chatAdmin
                      ? conversation?.member_avatar
                      : conversation?.admin_avatar
                  }
                  name={
                    (chatAdmin
                      ? conversation?.member_name
                      : conversation?.admin_name) || "清叶"
                  }
                />
                <Navbar
                  align="left"
                  className="chat-room-navbar"
                  title={
                    conversation
                      ? chatAdmin
                        ? conversation.member_name
                        : conversation.admin_name || "平台接待队列"
                      : "正在读取咨询…"
                  }
                  desc={
                    conversation && (
                      <span
                        className="chat-room-status"
                        data-status={conversation.status}
                      >
                        {statusNames[conversation.status]}
                      </span>
                    )
                  }
                />
                {canCompose && (
                  <Button
                    className="button-outline chat-close-room"
                    disabled={closing || sending}
                    onClick={() => void closeConversation()}
                  >
                    {closing ? "处理中…" : "结束咨询"}
                  </Button>
                )}
              </div>
              {conversation?.status === "waiting" && (
                <p className="chat-queue" role="status">
                  暂时没有空闲管理员，已进入等待队列。你可以先留言，有人接待时会收到提醒。
                </p>
              )}
              {conversation && (
                <div className="chat-context-bar">
                  <Package size={15} />
                  <span>
                    正在咨询 · <b>{conversation.product.code}</b>
                  </span>
                  <a
                    href={productHref(conversation.product.id)}
                    target="_blank"
                    rel="noreferrer"
                  >
                    查看商品 ↗
                  </a>
                </div>
              )}
              <div className="chat-history-shell">
                <div
                  className="chat-history"
                  role="log"
                  aria-label="聊天记录"
                  aria-busy={loading || loadingOlder}
                  ref={scroll}
                  onScroll={() => {
                    const node = scroll.current;
                    if (node) {
                      bottom.current =
                        node.scrollHeight - node.scrollTop - node.clientHeight <
                        65;
                      setAtBottom(bottom.current);
                      if (bottom.current) markRead();
                    }
                  }}
                >
                  {before && (
                    <Button
                      className="chat-load-more"
                      disabled={loadingOlder}
                      onClick={() => void loadOlder()}
                    >
                      {loadingOlder ? "正在加载…" : "查看更早的消息"}
                    </Button>
                  )}
                  {loading && (
                    <p className="chat-info" role="status">
                      正在读取聊天记录…
                    </p>
                  )}
                  <ChatThread
                    messages={messages}
                    unreadFrom={unreadFrom}
                    peerReadThrough={conversation?.peer_read_through}
                    nickname={user?.nickname}
                    myAvatar={myAvatar}
                    productHref={productHref}
                    onReply={canReply ? selectReply : undefined}
                    openImages={(images, index) =>
                      setGallery({ images, index })
                    }
                    onImageLoad={() => {
                      if (bottom.current) scrollToLatest();
                    }}
                    jumpToMessage={jumpToMessage}
                  />
                  <div
                    ref={tail}
                    className="chat-history-tail"
                    aria-hidden="true"
                  />
                </div>
                {!atBottom && (
                  <Button className="chat-newest" onClick={scrollToLatest}>
                    <ArrowDown size={14} aria-hidden="true" />
                    回到最新消息
                  </Button>
                )}
              </div>
              {error && !productId && (
                <div className="chat-error" role="alert">
                  {error}
                  <Button onClick={() => void latest()}>重新读取</Button>
                </div>
              )}
              <form className="chat-composer" onSubmit={send}>
                {conversation?.status === "closed" ? (
                  <div className="chat-closed">
                    本次咨询已结束，记录保留供查阅。
                    <a
                      href={productHref(conversation.product.id)}
                      target="_blank"
                      rel="noreferrer"
                    >
                      查看商品 ↗
                    </a>
                  </div>
                ) : (
                  <>
                    {quote && (
                      <div
                        className="chat-compose-quote"
                        aria-label="正在引用回复"
                      >
                        <MessageQuote quote={quote} />
                        <Button
                          className="chat-remove-quote"
                          aria-label="取消引用"
                          disabled={sending}
                          onClick={() => updateDraft({ text })}
                        >
                          <X size={17} aria-hidden="true" />
                        </Button>
                      </div>
                    )}
                    <ImageDrafts images={images} disabled={sending} />
                    <div className="chat-compose-tools">
                      <ImagePicker
                        images={images}
                        disabled={sending || !conversation}
                      />
                      <Button
                        type="button"
                        aria-label="插入表情"
                        aria-expanded={emojiOpen}
                        disabled={sending || !conversation}
                        onClick={() => setEmojiOpen((v) => !v)}
                      >
                        <Smile size={19} />
                        <span>表情</span>
                      </Button>
                      <small>{Array.from(text).length} / 2000</small>
                    </div>
                    {emojiOpen && (
                      <div
                        className="chat-emoji-picker"
                        role="group"
                        aria-label="选择表情"
                      >
                        {emojis.map((emoji) => (
                          <Button
                            key={emoji}
                            type="button"
                            aria-label={`插入 ${emoji}`}
                            onClick={() => insertText(emoji)}
                          >
                            {emoji}
                          </Button>
                        ))}
                      </div>
                    )}
                    {!chatAdmin &&
                      !text &&
                      !quote &&
                      !images.items.length &&
                      conversation && (
                        <div
                          className="chat-quick-questions"
                          aria-label="快捷咨询"
                        >
                          {[
                            "这个账号现在可以租 / 购买吗？",
                            "想了解具体报价和交付方式。",
                            "可以再介绍一下账号配置吗？",
                          ].map((question) => (
                            <Button
                              type="button"
                              key={question}
                              onClick={() => insertText(question)}
                            >
                              {question}
                            </Button>
                          ))}
                        </div>
                      )}
                    <Input
                      multiline
                      rows={2}
                      showCount={false}
                      variant="filled"
                      ref={composer}
                      aria-label="输入咨询消息"
                      placeholder={
                        chatAdmin
                          ? "输入回复内容…"
                          : "想问价格、租期，还是账号配置？"
                      }
                      value={text}
                      maxLength={4000}
                      disabled={sending || !conversation}
                      onPaste={(e) => {
                        const files = Array.from(e.clipboardData.files).filter(
                          (file) => file.type.startsWith("image/"),
                        );
                        if (files.length) {
                          e.preventDefault();
                          images.add(files);
                        }
                      }}
                      onChange={(value) =>
                        updateDraft({ ...draft, text: value })
                      }
                      onKeyDown={(e) => {
                        if (
                          e.key === "Enter" &&
                          !e.shiftKey &&
                          !e.nativeEvent.isComposing &&
                          window.matchMedia("(pointer: fine)").matches
                        ) {
                          e.preventDefault();
                          void send(e);
                        }
                      }}
                    />
                    <div className="chat-compose-footer">
                      <span>Enter 发送 · Shift + Enter 换行</span>
                      <Button
                        type="submit"
                        className="button-white"
                        disabled={
                          sending ||
                          (!text.trim() && !images.items.length) ||
                          !images.ready ||
                          !conversation ||
                          !connected
                        }
                      >
                        <Send size={15} />
                        {sending ? "发送中…" : "发送"}
                      </Button>
                    </div>
                  </>
                )}
              </form>
            </>
          ) : (
            <div className="chat-empty chat-room-empty">
              <MessageSquare size={42} />
              <h2>选择一个咨询，开始沟通</h2>
              <p>同一账号的咨询集中在这里，切换商品也不会丢失上下文。</p>
              {chatAdmin && <p>点击上方“开始接待”，即可接收用户咨询。</p>}
            </div>
          )}
        </div>
      </div>
    </section>
  );
}
