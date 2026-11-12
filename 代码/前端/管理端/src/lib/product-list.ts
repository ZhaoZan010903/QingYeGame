export const sources: Record<string, string> = {
  platform: "平台录入",
  member: "会员投稿",
  legacy: "旧站迁移",
};
export function readProductListQuery(params: URLSearchParams, review = false) {
  const size = params.get("limit") === "50" ? 50 : 20;
  const raw = Number(params.get("page") || 1);
  const page =
    Number.isSafeInteger(raw) && raw > 0
      ? Math.min(raw, Math.floor(1000000 / size) + 1)
      : 1;
  const query = new URLSearchParams({
    limit: String(size),
    page: String(page),
  });
  for (const key of ["q", "source", "trade_type", "sort"]) {
    if (params.get(key)) query.set(key, params.get(key)!);
  }
  if (review) query.set("status", "pending");
  else if (params.get("trash") === "1") query.set("trash", "1");
  else if (params.get("status")) query.set("status", params.get("status")!);
  return { page, size, query, trash: !review && params.get("trash") === "1" };
}
export function pageItems(
  current: number,
  total: number,
): Array<number | string> {
  const pages = [...new Set([1, current - 1, current, current + 1, total])]
    .filter((n) => n >= 1 && n <= total)
    .sort((a, b) => a - b);
  return pages.flatMap((n, i) =>
    i && n - pages[i - 1] > 1 ? [`gap-${n}`, n] : [n],
  );
}
export function productTime(value?: string | null, empty = "—") {
  if (!value) return empty;
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return empty;
  return new Intl.DateTimeFormat("zh-CN", {
    timeZone: "Asia/Shanghai",
    year: "numeric",
    month: "2-digit",
    day: "2-digit",
    hour: "2-digit",
    minute: "2-digit",
    hourCycle: "h23",
  })
    .format(date)
    .replaceAll("/", "-");
}
