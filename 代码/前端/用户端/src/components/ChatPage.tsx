import { ProtectedImage } from '../../../共享/MediaSecurity';
import { useCallback, useEffect, useRef, useState, type FormEvent } from 'react';
import { useSearchParams } from 'react-router-dom';
import { ArrowLeft, ArrowDown, Check, MessageSquare, Send, Package, Smile } from 'lucide-react';
import { createRequestId } from '../lib/api';
import { useSession } from '../context';
import { chatAdmin, chatApi } from '../lib/chat-api';
import { DesktopNoticeButton, useChat } from './ChatNotifications';
import { ChatConnectionNotice } from './ChatConnectionNotice';
import {
  mergeMessages,
  nextChatPosition,
  type ChatListPosition,
  type ChatMessage,
  type ChatProduct,
  type Conversation,
} from '../../../共享/chat-types';
import '../chat.css';
import {
  AvatarEditor,
  ChatAvatar,
  ChatImageGrid,
  ChatLightbox,
  ImageDrafts,
  ImagePicker,
  useChatImages,
} from './ChatMedia';
import type { ChatImage } from '../../../共享/chat-types';
import { ChatOutbox } from '../../../共享/chat-outbox';

const emojis = [
  '😊',
  '😄',
  '🥰',
  '👍',
  '🙏',
  '🤝',
  '🎉',
  '❤️',
  '✨',
  '👀',
  '👌',
  '🤔',
  '😅',
  '😭',
  '🙌',
  '🌿',
];
const statusNames = { waiting: '等待接待', active: '咨询中', closed: '已结束' };
const units: Record<string, string> = {
  account: '元',
  hour: '元/小时',
  day: '元/日',
  week: '元/周',
  month: '元/月',
  unknown: '',
};
const errorText = (e: unknown) => (e instanceof Error ? e.message : '操作失败，请稍后重试。');
const date = (value: string) =>
  new Date(value).toLocaleString('zh-CN', {
    month: '2-digit',
    day: '2-digit',
    hour: '2-digit',
    minute: '2-digit',
  });
function ProductCard({ product }: { product: ChatProduct }) {
  const [failed, setFailed] = useState(false);
  const site = chatAdmin
    ? String(import.meta.env.VITE_PUBLIC_SITE_URL || '').replace(/\/$/, '')
    : '';
  return (
    <a
      className="chat-product-card"
      href={`${site}/accounts/${encodeURIComponent(product.id)}`}
      target="_blank"
      rel="noreferrer"
    >
      <span className="chat-product-cover">
        {product.cover_url && !failed ? (
          <ProtectedImage src={product.cover_url} onError={() => setFailed(true)} alt="咨询商品封面" />
        ) : (
          <Package size={22} />
        )}
      </span>
      <span>
        <small>咨询商品 · 报价以客服确认为准</small>
        <strong>{product.title}</strong>
        <b>
          {product.price !== null && product.unit !== 'unknown'
            ? `¥ ${Number(product.price).toLocaleString('zh-CN')} ${units[product.unit] || ''}`
            : '联系客服报价'}
        </b>
      </span>
    </a>
  );
}

export function ChatPage() {
  const pageElement = useRef<HTMLElement>(null);
  useEffect(() => {
    const viewport = window.visualViewport;
    const resize = () =>
      pageElement.current?.style.setProperty(
        '--chat-visual-height',
        `${viewport?.height ?? window.innerHeight}px`,
      );
    resize();
    viewport?.addEventListener('resize', resize);
    return () => viewport?.removeEventListener('resize', resize);
  }, []);
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
  const selected = params.get('conversation') || '',
    productId = params.get('product') || '';
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
  const composer = useRef<HTMLTextAreaElement>(null);
  const startRequest = useRef<{ key: string; promise: Promise<string> } | null>(null);
  const [text, setText] = useState(''),
    [error, setError] = useState(''),
    [starting, setStarting] = useState(false),
    [startRetry, setStartRetry] = useState(0);
  const images = useChatImages(user?.id || '', selected, setError);
  const [myAvatar, setMyAvatar] = useState('');
  const [gallery, setGallery] = useState<{ images: ChatImage[]; index: number } | null>(null);
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
    messageCache = useRef<ChatMessage[]>([]),
    lastRead = useRef('');
  const drafts = useRef(new Map<string, string>()),
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
        ...new Map([...old, ...previous].map((item) => [item.id, item])).values(),
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
    setError('');
    const key = `${user?.id}:${productId}:${startRetry}`;
    if (startRequest.current?.key !== key)
      startRequest.current = { key, promise: chatApi.start(productId) };
    startRequest.current.promise
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
    pending.current.clear();
    setOlder([]);
  }, [user?.id]);
  useEffect(() => {
    sequence.current++;
    loaded.current = false;
    inFlight.current = false;
    historyBusy.current = false;
    messageCache.current = [];
    lastRead.current = '';
    bottom.current = true;
    setGallery(null);
    setConversation(null);
    setMessages([]);
    setBefore(null);
    setText(drafts.current.get(selected) || '');
    setError('');
    setAtBottom(true);
    setLoadingOlder(false);
    setLoading(!!selected);
    return () => {
      sequence.current++;
    };
  }, [selected, user?.id]);
  const latest = useCallback(async () => {
    if (!selected || currentID.current !== selected || inFlight.current || historyBusy.current)
      return;
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
      setError('');
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
      document.visibilityState !== 'visible' ||
      lastRead.current === last
    )
      return;
    lastRead.current = last;
    chatApi.read(selected, last).catch(() => {
      if (currentID.current === selected) lastRead.current = '';
    });
  }, [selected, messages, atBottom, connected]);
  useEffect(() => {
    markRead();
    const check = () => markRead();
    document.addEventListener('visibilitychange', check);
    return () => document.removeEventListener('visibilitychange', check);
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
    if (nextOffset === null || listBusy.current) return;
    listBusy.current = true;
    const generation = listGeneration.current;
    const head = list.data;
    setLoadingList(true);
    try {
      const page = await chatApi.list(nextOffset);
      if (generation !== listGeneration.current) return;
      expandedList.current = true;
      setOlder((old) => [
        ...new Map([...old, ...head, ...page.data].map((item) => [item.id, item])).values(),
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
  function insertText(value: string) {
    const node = composer.current;
    const start = node?.selectionStart ?? text.length,
      end = node?.selectionEnd ?? text.length;
    const next = text.slice(0, start) + value + text.slice(end);
    if (Array.from(next).length > 2000) return;
    setText(next);
    drafts.current.set(selected, next);
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
      conversation.status === 'closed'
    )
      return;
    if (!connected) {
      setError('连接恢复后即可发送，输入内容已保留。');
      return;
    }
    if (Array.from(body).length > 2000) {
      setError('每条消息最多 2000 字。');
      return;
    }
    const input = pending.current.prepare(
      selected,
      body,
      createRequestId,
      images.items.map((image) => image.mediaID!),
    );
    const keys = images.items.map((image) => image.key);
    const generation = listGeneration.current;
    sendLock.current = true;
    setSending(true);
    setError('');
    try {
      await chatApi.send(input.id, input.body, input.client, input.images);
      if (generation !== listGeneration.current) return;
      images.clear(input.id, keys);
      drafts.current.delete(input.id);
      if (currentID.current === input.id) {
        setText('');
        bottom.current = true;
        setAtBottom(true);
        await latest();
      }
      pending.current.acknowledge(input);
      void refresh();
    } catch (e) {
      if (generation === listGeneration.current && currentID.current === input.id)
        setError(`${errorText(e)} 内容已保留，可以重新发送。`);
    } finally {
      sendLock.current = false;
      setSending(false);
    }
  }
  async function closeConversation() {
    if (!window.confirm('结束这次咨询？聊天记录会保留。如需再次咨询，可从商品页重新发起。')) return;
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
      ref={pageElement}
      className={`chat-page ${chatAdmin ? 'chat-admin' : 'chat-member'} ${selected ? 'has-selection' : ''}`}
    >
      <header className="chat-page-heading">
        <div>
          <span className="chat-eyebrow">{chatAdmin ? '接待工作区' : '我的消息'}</span>
          <h1>{chatAdmin ? '在线咨询' : '与清叶聊一聊'}</h1>
          <p>
            {chatAdmin
              ? '查看商品资料，直接回复咨询用户。'
              : '价格、租期、账号资料，在同一个对话中慢慢聊。'}
          </p>
        </div>
        <DesktopNoticeButton />
      </header>
      {!connected && <ChatConnectionNotice message={connectionError} />}
      {chatAdmin && (
        <div className="chat-presence">
          <div>
            <span className={`chat-presence-dot ${presence?.accepting ? 'accepting' : ''}`} />
            <strong>
              {presence?.accepting
                ? presence.active >= presence.capacity
                  ? '接待已满'
                  : '正在接待'
                : '暂停接待新咨询'}
            </strong>
            <span>
              当前 {presence?.active ?? '—'} / {presence?.capacity ?? 3} 个咨询 · 等待分配{' '}
              {presence?.waiting ?? '—'} 个
            </span>
          </div>
          <button
            className="button-outline"
            disabled={presenceBusy || !connected}
            onClick={() => void setAccepting(!presence?.accepting)}
          >
            {presenceBusy ? '正在更新…' : presence?.accepting ? '暂停接待' : '开始接待'}
          </button>
          <small>保持后台打开即保持在线。暂停只停止接收新咨询，已有对话仍可回复。</small>
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
      {gallery && <ChatLightbox gallery={gallery} close={() => setGallery(null)} />}
      <div className="chat-workspace">
        <aside className="chat-list">
          <AvatarEditor identity={user?.id || ''} onChange={setMyAvatar} />
          <div className="chat-list-heading">
            <strong>
              全部咨询 <span>{list.total}</span>
            </strong>
            <small>{connected ? '实时连接' : '连接未就绪'}</small>
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
              className={`conversation-item ${selected === item.id ? 'active' : ''}`}
              onClick={() => setParams({ conversation: item.id })}
            >
              <ChatAvatar
                className="conversation-avatar"
                src={chatAdmin ? item.member_avatar : item.admin_avatar}
                name={chatAdmin ? item.member_name : item.admin_name || '清叶'}
              />
              <span className="conversation-copy">
                <span>
                  <strong>
                    {chatAdmin ? item.member_name : item.admin_name || '等待客服接待'}
                  </strong>
                  <time>{date(item.updated_at)}</time>
                </span>
                <b>
                  {item.product_count && item.product_count > 1
                    ? `已咨询 ${item.product_count} 个账号 · `
                    : ''}
                  {item.product.code || item.product.title}
                </b>
                <span>
                  <small>{item.last_message || statusNames[item.status]}</small>
                  {item.unread > 0 && <i>{item.unread > 99 ? '99+' : item.unread}</i>}
                </span>
                <em>{statusNames[item.status]}</em>
              </span>
            </button>
          ))}
          {!listError && !conversations.length && (
            <div className="chat-empty">
              <MessageSquare size={32} />
              <h3>{chatAdmin ? '还没有分配的咨询' : '还没有咨询记录'}</h3>
              <p>
                {chatAdmin
                  ? '开启接待后，系统会随机分配待接待的用户。'
                  : '从商品详情发起咨询。不同账号的咨询会归入同一对话。'}
              </p>
            </div>
          )}
          {nextOffset !== null && (
            <button
              className="chat-load-more"
              disabled={loadingList}
              onClick={() => void loadList()}
            >
              {loadingList ? '正在读取…' : '更多咨询记录'}
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
                        : conversation.admin_name || '平台接待队列'
                      : '正在读取咨询…'}
                  </strong>
                  <small>
                    {conversation ? statusNames[conversation.status] : ''}
                    {conversation?.status === 'active'
                      ? chatAdmin
                        ? ' · 当前由你接待'
                        : ' · 由平台管理员为你服务'
                      : ''}
                  </small>
                </div>
                {conversation?.status !== 'closed' && conversation && (
                  <button
                    className="button-outline"
                    disabled={closing || sending}
                    onClick={() => void closeConversation()}
                  >
                    {closing ? '处理中…' : '结束咨询'}
                  </button>
                )}
              </header>
              {conversation?.status === 'waiting' && (
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
                    href={
                      chatAdmin
                        ? '/products/' + encodeURIComponent(conversation.product.id)
                        : '/accounts/' + encodeURIComponent(conversation.product.id)
                    }
                    target="_blank"
                    rel="noreferrer"
                  >
                    查看商品 ↗
                  </a>
                </div>
              )}
              <div
                className="chat-history"
                ref={scroll}
                onScroll={() => {
                  const node = scroll.current;
                  if (node) {
                    bottom.current = node.scrollHeight - node.scrollTop - node.clientHeight < 65;
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
                    {loadingOlder ? '正在加载…' : '查看更早的消息'}
                  </button>
                )}
                {loading && (
                  <p className="chat-info" role="status">
                    正在读取聊天记录…
                  </p>
                )}
                {messages.map((item, index) => (
                  <div key={item.id} className="chat-message-group">
                    {(index === 0 ||
                      new Date(item.created_at).getTime() -
                        new Date(messages[index - 1].created_at).getTime() >
                        300000) && (
                      <div className="chat-time-divider">
                        <time>{date(item.created_at)}</time>
                      </div>
                    )}
                    <div className={`chat-message ${item.mine ? 'mine' : ''}`}>
                      <ChatAvatar
                        src={item.mine ? myAvatar : item.avatar_url}
                        name={
                          item.mine
                            ? user?.nickname || '我'
                            : item.sender_name ||
                              (item.sender_role === 'admin'
                                ? '客服'
                                : conversation?.member_name || '会员')
                        }
                      />
                      <div className="chat-message-content">
                        <small>
                          {item.mine
                            ? '我'
                            : item.sender_name ||
                              (item.sender_role === 'admin'
                                ? '平台客服'
                                : conversation?.member_name)}
                          {item.sender_role === 'admin' && <em>客服</em>}
                        </small>
                        {item.kind === 'product' && item.product ? (
                          <ProductCard product={item.product} />
                        ) : (
                          <div className="chat-bubble">
                            {!!item.images?.length && (
                              <ChatImageGrid
                                images={item.images}
                                open={(images, index) => setGallery({ images, index })}
                                loaded={() => {
                                  if (bottom.current && scroll.current)
                                    scroll.current.scrollTop = scroll.current.scrollHeight;
                                }}
                              />
                            )}
                            {item.body && <p>{item.body}</p>}
                          </div>
                        )}
                        <span className="chat-message-meta">
                          <time>
                            {new Date(item.created_at).toLocaleTimeString('zh-CN', {
                              hour: '2-digit',
                              minute: '2-digit',
                            })}
                          </time>
                          {item.mine && (
                            <span className="chat-sent">
                              <Check size={11} />
                              已发送
                            </span>
                          )}
                        </span>
                      </div>
                    </div>
                  </div>
                ))}
              </div>
              {!atBottom && (
                <button
                  className="chat-newest"
                  onClick={() => {
                    if (scroll.current) scroll.current.scrollTop = scroll.current.scrollHeight;
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
                {conversation?.status === 'closed' ? (
                  <div className="chat-closed">
                    本次咨询已结束，记录保留供查阅。
                    <a
                      href={`${chatAdmin ? String(import.meta.env.VITE_PUBLIC_SITE_URL || '').replace(/\/$/, '') : ''}/accounts/${conversation.product.id}`}
                      target="_blank"
                      rel="noreferrer"
                    >
                      查看商品 ↗
                    </a>
                  </div>
                ) : (
                  <>
                    <ImageDrafts images={images} disabled={sending} />
                    <div className="chat-compose-tools">
                      <ImagePicker images={images} disabled={sending || !conversation} />
                      <button
                        type="button"
                        aria-label="插入表情"
                        aria-expanded={emojiOpen}
                        disabled={sending || !conversation}
                        onClick={() => setEmojiOpen((v) => !v)}
                      >
                        <Smile size={19} />
                        <span>表情</span>
                      </button>
                      <small>{Array.from(text).length} / 2000</small>
                    </div>
                    {emojiOpen && (
                      <div className="chat-emoji-picker" role="group" aria-label="选择表情">
                        {emojis.map((emoji) => (
                          <button
                            key={emoji}
                            type="button"
                            aria-label={`插入 ${emoji}`}
                            onClick={() => insertText(emoji)}
                          >
                            {emoji}
                          </button>
                        ))}
                      </div>
                    )}
                    {!text && !images.items.length && conversation && (
                      <div className="chat-quick-questions" aria-label="快捷咨询">
                        {[
                          '这个账号现在可以租 / 购买吗？',
                          '想了解具体报价和交付方式。',
                          '可以再介绍一下账号配置吗？',
                        ].map((question) => (
                          <button type="button" key={question} onClick={() => insertText(question)}>
                            {question}
                          </button>
                        ))}
                      </div>
                    )}
                    <textarea
                      ref={composer}
                      aria-label="输入咨询消息"
                      placeholder={chatAdmin ? '输入回复内容…' : '想问价格、租期，还是账号配置？'}
                      value={text}
                      maxLength={4000}
                      disabled={sending || !conversation}
                      onPaste={(e) => {
                        const files = Array.from(e.clipboardData.files).filter((file) =>
                          file.type.startsWith('image/'),
                        );
                        if (files.length) {
                          e.preventDefault();
                          images.add(files);
                        }
                      }}
                      onChange={(e) => {
                        setText(e.target.value);
                        drafts.current.set(selected, e.target.value);
                      }}
                      onKeyDown={(e) => {
                        if (
                          e.key === 'Enter' &&
                          !e.shiftKey &&
                          !e.nativeEvent.isComposing &&
                          window.matchMedia('(pointer: fine)').matches
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
                          sending ||
                          (!text.trim() && !images.items.length) ||
                          !images.ready ||
                          !conversation ||
                          !connected
                        }
                      >
                        <Send size={15} />
                        {sending ? '发送中…' : '发送'}
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
              <p>同一账号的咨询集中在这里，切换商品也不会丢失上下文。</p>
              {chatAdmin && <p>点击上方“开始接待”，即可接收用户咨询。</p>}
            </div>
          )}
        </div>
      </div>
    </section>
  );
}
