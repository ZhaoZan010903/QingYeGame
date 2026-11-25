export const adminProductPath = (id: string) =>
  `/products/${encodeURIComponent(id)}`;
export function productListReturn(value: unknown): string | undefined {
  return typeof value === "string" && /^\/(products|reviews)(\?|$)/.test(value)
    ? value
    : undefined;
}
export function chatProductPath(id: string, conversation?: string | null) {
  const params = new URLSearchParams({ from: "chat" });
  if (conversation) params.set("conversation", conversation);
  return adminProductPath(id) + "?" + params;
}

export function productDetailOrigin(state: unknown, search: string) {
  const list = productListReturn(state);
  if (list)
    return {
      href: list,
      label: list.startsWith("/reviews") ? "待审核商品" : "商品列表",
    };
  if (typeof state === "string" && (state === "/" || state.startsWith("/?"))) {
    return { href: state, label: "数据看板" };
  }
  const params = new URLSearchParams(search);
  if (params.get("from") === "chat") {
    const conversation = params.get("conversation");
    const href = conversation
      ? "/chat?" + new URLSearchParams({ conversation })
      : "/chat";
    return { href, label: "在线咨询" };
  }
  return { href: "/products", label: "商品列表" };
}
export function adminProductID(path: string): string | undefined {
  const match = /^(?:products|accounts)\/([^/]+)$/.exec(path);
  if (!match) return;
  try {
    return decodeURIComponent(match[1]);
  } catch {
    return;
  }
}
