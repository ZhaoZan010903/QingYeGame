import { Button } from "@chatui/core/es/components/Button";
import { Bubble } from "@chatui/core/es/components/Bubble";
import { Divider } from "@chatui/core/es/components/Divider";
import { Quote } from "@chatui/core/es/components/Quote";
import {
  ArrowUpRight,
  Check,
  CheckCheck,
  MessageSquareReply,
  Package,
} from "lucide-react";
import { useState } from "react";
import { ProtectedImage } from "./MediaSecurity";
import { ChatAvatar, ChatImageGrid } from "./ChatMedia";
import { chatProductPrice } from "./chat-product-price";
import {
  isReadThrough,
  type ChatImage,
  type ChatMessage,
  type ChatProduct,
  type ChatQuote,
} from "./chat-types";

const dateFormat = new Intl.DateTimeFormat("zh-CN", {
  month: "2-digit",
  day: "2-digit",
  hour: "2-digit",
  minute: "2-digit",
});
const timeFormat = new Intl.DateTimeFormat("zh-CN", {
  hour: "2-digit",
  minute: "2-digit",
});
export const chatDate = (value: string) => dateFormat.format(new Date(value));

export function ChatProductCard({
  product,
  href,
}: {
  product: ChatProduct;
  href: string;
}) {
  const [failedURL, setFailedURL] = useState("");
  return (
    <a
      className="chat-product-card"
      href={href}
      target="_blank"
      rel="noreferrer"
    >
      <span className="chat-product-cover">
        {product.cover_url && product.cover_url !== failedURL ? (
          <ProtectedImage
            src={product.cover_url}
            onError={() => setFailedURL(product.cover_url)}
            alt="咨询商品封面"
          />
        ) : (
          <Package size={22} aria-hidden="true" />
        )}
      </span>
      <span className="chat-product-copy">
        <small>咨询商品</small>
        <strong>{product.title}</strong>
        <b>{chatProductPrice(product)}</b>
        <small>报价以客服确认为准</small>
      </span>
      <ArrowUpRight
        className="chat-product-open"
        size={15}
        aria-hidden="true"
      />
    </a>
  );
}

function quoteTitle(quote: ChatQuote): string {
  return quote.mine
    ? "我"
    : quote.sender_name || (quote.sender_role === "admin" ? "客服" : "会员");
}

function quoteExcerpt(quote: ChatQuote): string {
  if (quote.kind === "image")
    return quote.excerpt ? `[图片] ${quote.excerpt}` : "[图片]";
  if (quote.kind === "product") return quote.excerpt || "[咨询商品]";
  return quote.excerpt;
}

export function MessageQuote({
  quote,
  jump,
}: {
  quote: ChatQuote;
  jump?: () => void;
}) {
  const excerpt = quoteExcerpt(quote);
  return (
    <Quote className="chat-quote" author={quoteTitle(quote)}>
      {jump ? (
        <Button
          className="chat-quote-jump"
          aria-label={`查看原消息：${excerpt}`}
          onClick={jump}
        >
          <span>{excerpt}</span>
          <ArrowUpRight size={13} aria-hidden="true" />
        </Button>
      ) : (
        <span>{excerpt}</span>
      )}
    </Quote>
  );
}

export function ChatThread({
  messages,
  unreadFrom,
  peerReadThrough,
  nickname,
  myAvatar,
  productHref,
  onReply,
  openImages,
  onImageLoad,
  jumpToMessage,
}: {
  messages: ChatMessage[];
  unreadFrom: string | undefined;
  peerReadThrough?: string;
  nickname?: string;
  myAvatar: string;
  productHref: (id: string) => string;
  onReply?: (message: ChatMessage) => void;
  openImages: (images: ChatImage[], index: number) => void;
  onImageLoad: () => void;
  jumpToMessage: (id: string) => void;
}) {
  const available = new Set(messages.map(({ id }) => id));
  return messages.map((item, index) => {
    const sender = item.mine
      ? "我"
      : item.sender_name || (item.sender_role === "admin" ? "客服" : "会员");
    const read = item.mine && isReadThrough(item.id, peerReadThrough);
    const status =
      peerReadThrough === undefined ? "已发送" : read ? "已读" : "未读";
    const time = new Date(item.created_at);
    const replyID = item.reply?.id;
    return (
      <div key={item.id} className="chat-message-group">
        {(index === 0 ||
          time.getTime() - Date.parse(messages[index - 1].created_at) >
            300000) && (
          <Divider className="chat-time-divider">
            <time dateTime={item.created_at}>{chatDate(item.created_at)}</time>
          </Divider>
        )}
        {item.id === unreadFrom && (
          <Divider className="chat-unread-divider">未读消息</Divider>
        )}
        <article
          id={`chat-message-${item.id}`}
          tabIndex={-1}
          className={`chat-message${item.mine ? " mine" : ""}`}
        >
          <ChatAvatar
            src={item.mine ? myAvatar : item.avatar_url}
            name={item.mine ? nickname || "我" : sender}
          />
          <div className="chat-message-content">
            <small>
              {sender}
              {item.sender_role === "admin" && sender !== "客服" && (
                <em>客服</em>
              )}
            </small>
            {item.kind === "product" && item.product ? (
              <ChatProductCard
                product={item.product}
                href={productHref(item.product.id)}
              />
            ) : (
              <Bubble className="Bubble chat-bubble">
                {item.reply && (
                  <MessageQuote
                    quote={item.reply}
                    jump={
                      replyID && available.has(replyID)
                        ? () => jumpToMessage(replyID)
                        : undefined
                    }
                  />
                )}
                {!!item.images?.length && (
                  <ChatImageGrid
                    images={item.images}
                    open={openImages}
                    loaded={onImageLoad}
                  />
                )}
                {item.body && <p>{item.body}</p>}
              </Bubble>
            )}
            <div className="chat-message-meta">
              <time dateTime={item.created_at}>{timeFormat.format(time)}</time>
              {item.mine && (
                <span
                  className="chat-sent"
                  data-status={read ? "read" : "sent"}
                  title={
                    peerReadThrough === undefined ? undefined : "对方" + status
                  }
                >
                  {read ? (
                    <CheckCheck size={13} aria-hidden="true" />
                  ) : (
                    <Check size={12} aria-hidden="true" />
                  )}
                  {status}
                </span>
              )}
              {onReply && (
                <Button
                  className="chat-reply-action"
                  title="引用回复"
                  onClick={() => onReply(item)}
                >
                  <MessageSquareReply size={14} aria-hidden="true" /> 回复
                </Button>
              )}
            </div>
          </div>
        </article>
      </div>
    );
  });
}
