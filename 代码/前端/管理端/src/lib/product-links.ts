export const adminProductPath = (id: string) =>
  `/products/${encodeURIComponent(id)}`;
export function productListReturn(value: unknown): string | undefined {
  return typeof value === "string" && /^\/(products|reviews)(\?|$)/.test(value)
    ? value
    : undefined;
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
