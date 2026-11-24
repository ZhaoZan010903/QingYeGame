import { useSearchParams } from "react-router-dom";
import { createRequestId, request } from "../lib/api";
import { chatAdmin, chatApi } from "../lib/chat-api";
import { useSession } from "../context";
import { DesktopNoticeButton, useChat } from "./ChatNotifications";
import { ChatConnectionNotice } from "./ChatConnectionNotice";
import { ChatWorkspace } from "../../../共享/ChatWorkspace";
import { ChatProductCard as ProductCard } from "../../../共享/ChatThread";
import type { ChatProduct } from "../../../共享/chat-types";
import "../chat.css";
import { adminProductPath } from "../lib/product-links";

export function ChatProductCard({ product }: { product: ChatProduct }) {
  return <ProductCard product={product} href={adminProductPath(product.id)} />;
}
export function ChatPage() {
  const { user } = useSession();
  const chat = useChat();
  const [params, setParams] = useSearchParams();
  return (
    <ChatWorkspace
      chatAdmin={chatAdmin}
      user={user}
      chat={chat}
      api={chatApi}
      media={{ request, createRequestId }}
      selected={params.get("conversation") || ""}
      productId={params.get("product") || ""}
      navigate={(next, replace) => setParams(next, { replace })}
      productHref={adminProductPath}
      notificationControl={<DesktopNoticeButton />}
      connectionNotice={<ChatConnectionNotice message={chat.connectionError} />}
    />
  );
}
