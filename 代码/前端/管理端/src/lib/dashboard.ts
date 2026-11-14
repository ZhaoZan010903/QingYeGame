import type { ProductStatus, RentalCategory, TradeType } from "../types";

export type DashboardDays = 7 | 30;
export type RankingMetric = "views" | "favorites" | "consultations";
export type TrendMetric = RankingMetric | "new_members";
export interface DashboardTrend {
  date: string;
  views: number;
  favorites: number;
  consultations: number;
  new_members: number;
}
export interface DashboardData {
  generated_at: string;
  timezone: "Asia/Shanghai";
  range: {
    days: DashboardDays;
    start_date: string;
    end_date: string;
    start_at: string;
    end_at: string;
  };
  products: {
    total: number;
    visible: number;
    sale: number;
    rent: number;
    trash: number;
    statuses: Record<ProductStatus, number>;
  };
  members: {
    total: number;
    enabled: number;
    disabled: number;
    new_in_period: number;
  };
  todos: {
    pending_products: number;
    publishing_products: number;
    pending_nicknames: number;
    waiting_conversations: number;
  };
  engagement: Record<RankingMetric, number>;
  trend: DashboardTrend[];
  ranking: {
    by: RankingMetric;
    limit: 10;
    items: Array<{
      id: string;
      product_code: string;
      title: string;
      trade_type: TradeType;
      rental_category: RentalCategory | "" | null;
      status: ProductStatus;
      views: number;
      favorites: number;
      consultations: number;
    }>;
  };
}

export const dashboardStatusNames: Record<ProductStatus, string> = {
  draft: "草稿",
  pending: "待审核",
  publishing: "上架中",
  published: "已上架",
  rejected: "退回修改",
  rejected_final: "最终驳回",
  offline: "已下架",
  sold: "已售出",
  rented: "出租中",
};
export const dashboardMetricNames: Record<TrendMetric, string> = {
  views: "去重商品浏览",
  favorites: "首次有效收藏",
  consultations: "首次商品咨询",
  new_members: "新增会员",
};
export const dashboardMetricNotes: Record<TrendMetric, string> = {
  views: "同一访客对同一商品每天计 1 次，跨商品、跨日分别统计。",
  favorites:
    "期间首次收藏且目前仍有效的会员与商品关系；取消后重新收藏不重复新增。",
  consultations:
    "期间首次咨询且目前仍有效的会员与商品关系；重复咨询同一商品不重复新增。",
  new_members: "所选日期内注册的普通会员，包含目前已停用的会员。",
};

const record = (value: unknown): value is Record<string, unknown> =>
  !!value && typeof value === "object" && !Array.isArray(value);
const count = (value: unknown) =>
  Number.isSafeInteger(value) && Number(value) >= 0;
const counts = (value: unknown, keys: string[]) =>
  record(value) && keys.every((key) => count(value[key]));
const validDate = (value: unknown): value is string =>
  typeof value === "string" &&
  /^\d{4}-\d{2}-\d{2}$/.test(value) &&
  Number.isFinite(Date.parse(value)) &&
  new Date(value).toISOString().slice(0, 10) === value;
const timestamp = (value: unknown) =>
  typeof value === "string" &&
  /(?:Z|[+-]\d{2}:\d{2})$/.test(value) &&
  Number.isFinite(Date.parse(value));

/** Reject incomplete responses instead of silently presenting missing facts as zero. */
export function readDashboard(
  value: unknown,
  days: DashboardDays,
  by: RankingMetric,
): DashboardData {
  const invalid = () => {
    throw new Error("看板数据不完整，请刷新重试。");
  };
  if (!record(value)) return invalid();
  const { range, products, members, todos, engagement, trend, ranking } = value;
  if (
    !timestamp(value.generated_at) ||
    value.timezone !== "Asia/Shanghai" ||
    !record(range) ||
    range.days !== days ||
    !validDate(range.start_date) ||
    !validDate(range.end_date) ||
    !timestamp(range.start_at) ||
    !timestamp(range.end_at) ||
    !counts(products, ["total", "visible", "sale", "rent", "trash"]) ||
    !record(products) ||
    !counts(products.statuses, Object.keys(dashboardStatusNames)) ||
    !counts(members, ["total", "enabled", "disabled", "new_in_period"]) ||
    !counts(todos, [
      "pending_products",
      "publishing_products",
      "pending_nicknames",
      "waiting_conversations",
    ]) ||
    !counts(engagement, ["views", "favorites", "consultations"]) ||
    !Array.isArray(trend) ||
    trend.length !== days ||
    !record(ranking) ||
    ranking.by !== by ||
    ranking.limit !== 10 ||
    !Array.isArray(ranking.items) ||
    ranking.items.length > 10
  )
    return invalid();
  const firstDay = Date.parse(range.start_date);
  if (
    Date.parse(range.end_date) - firstDay !== (days - 1) * 86_400_000 ||
    !trend.every(
      (row, i) =>
        record(row) &&
        row.date ===
          new Date(firstDay + i * 86_400_000).toISOString().slice(0, 10) &&
        counts(row, ["views", "favorites", "consultations", "new_members"]),
    ) ||
    !ranking.items.every(
      (row) =>
        record(row) &&
        typeof row.id === "string" &&
        row.id !== "" &&
        typeof row.product_code === "string" &&
        typeof row.title === "string" &&
        (row.trade_type === "sale" || row.trade_type === "rent") &&
        typeof row.status === "string" &&
        Object.hasOwn(dashboardStatusNames, row.status) &&
        counts(row, ["views", "favorites", "consultations"]),
    )
  )
    return invalid();
  return value as unknown as DashboardData;
}

export function dashboardChart(
  rows: DashboardTrend[],
  metric: TrendMetric,
  width = 710,
) {
  const max = Math.max(1, ...rows.map((row) => row[metric]));
  const magnitude = 10 ** Math.floor(Math.log10(max));
  const ceiling = Math.ceil(max / magnitude) * magnitude;
  const left = Math.max(54, ceiling.toLocaleString("zh-CN").length * 7 + 10);
  const right = width - 24;
  const points = rows.map((row, i) => ({
    x:
      rows.length === 1
        ? (left + right) / 2
        : left + (i * (right - left)) / Math.max(1, rows.length - 1),
    y: 196 - (row[metric] / ceiling) * 172,
    date: row.date,
    value: row[metric],
  }));
  return {
    left,
    right,
    ceiling,
    points,
    line: points
      .map(
        (point, i) =>
          `${i === 0 ? "M" : "L"}${point.x.toFixed(2)},${point.y.toFixed(2)}`,
      )
      .join(" "),
    total: rows.reduce((total, row) => total + row[metric], 0),
    peak: rows.reduce((peak, row) => Math.max(peak, row[metric]), 0),
  };
}
export const dashboardNumber = (value: number) => value.toLocaleString("zh-CN");
export const dashboardDate = (value: string) =>
  value.slice(5).replace("-", "/");
export const dashboardTime = (value: string) =>
  new Intl.DateTimeFormat("zh-CN", {
    timeZone: "Asia/Shanghai",
    month: "2-digit",
    day: "2-digit",
    hour: "2-digit",
    minute: "2-digit",
    second: "2-digit",
    hour12: false,
  }).format(new Date(value));
