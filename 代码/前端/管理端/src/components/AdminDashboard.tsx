import { useEffect, useId, useRef, useState } from "react";
import { Link } from "react-router-dom";
import {
  ArrowRight,
  Package,
  ClipboardCheck,
  PanelsTopLeft,
  Headphones,
  Plus,
  BookOpen,
  TrendingUp,
  MessageSquare,
  Users,
  RefreshCw,
  Eye,
  Heart,
  Clock3,
} from "lucide-react";
import { adminApi } from "../lib/admin-api";
import {
  dashboardChart,
  dashboardDate,
  dashboardMetricNames,
  dashboardMetricNotes,
  dashboardNumber as number,
  dashboardStatusNames,
  dashboardTime,
  type DashboardData,
  type DashboardDays,
  type RankingMetric,
  type TrendMetric,
} from "../lib/dashboard";
import { adminProductPath } from "../lib/product-links";
import { GuideButton } from "./AdminGuide";
import { StateView } from "./shared";
import { publicSite } from "../lib/admin-ux";
import "../dashboard.css";

const metrics: TrendMetric[] = [
  "views",
  "favorites",
  "consultations",
  "new_members",
];
const metricIcons = {
  views: Eye,
  favorites: Heart,
  consultations: MessageSquare,
  new_members: Users,
};

export function DashboardTrend({
  data,
  metric,
  onMetric,
}: {
  data: DashboardData;
  metric: TrendMetric;
  onMetric: (value: TrendMetric) => void;
}) {
  const chartElement = useRef<SVGSVGElement>(null);
  const [chartWidth, setChartWidth] = useState(710);
  useEffect(() => {
    const element = chartElement.current;
    if (!element) return;
    // Match SVG units to screen pixels so axes remain readable at mobile widths.
    const measure = () =>
      setChartWidth(
        Math.max(220, Math.round(element.getBoundingClientRect().width)),
      );
    measure();
    const observer =
      typeof ResizeObserver === "undefined"
        ? null
        : new ResizeObserver(measure);
    observer?.observe(element);
    window.addEventListener("resize", measure);
    return () => {
      observer?.disconnect();
      window.removeEventListener("resize", measure);
    };
  }, []);
  const chart = dashboardChart(data.trend, metric, chartWidth);
  const id = useId();
  const label = dashboardMetricNames[metric];
  const ticks = [...new Set([0, Math.round(chart.ceiling / 2), chart.ceiling])];
  const dateIndexes = [
    ...new Set([
      0,
      Math.floor((data.trend.length - 1) / 2),
      data.trend.length - 1,
    ]),
  ];
  return (
    <section
      className="dashboard-panel dashboard-trend"
      aria-labelledby={`${id}-heading`}
    >
      <div className="dashboard-section-title">
        <div>
          <span className="dashboard-overline">
            最近 {data.range.days} 天 · 北京时间
          </span>
          <h2 id={`${id}-heading`}>每日趋势</h2>
        </div>
        <span className="dashboard-period">
          {data.range.start_date} — {data.range.end_date}
        </span>
      </div>
      <div className="dashboard-metric-tabs" role="group" aria-label="趋势指标">
        {metrics.map((key) => (
          <button
            key={key}
            type="button"
            aria-pressed={metric === key}
            onClick={() => onMetric(key)}
          >
            {dashboardMetricNames[key]}
          </button>
        ))}
      </div>
      <p className="dashboard-chart-summary">
        {label}
        <strong>{number(chart.total)}</strong>
        <span>期间合计 · 单日最高 {number(chart.peak)}</span>
      </p>
      <svg
        ref={chartElement}
        className="dashboard-chart"
        viewBox={`0 0 ${chartWidth} 230`}
        role="img"
        aria-labelledby={`${id}-title ${id}-desc`}
      >
        <title id={`${id}-title`}>{`${label}每日趋势`}</title>
        <desc id={`${id}-desc`}>
          {data.range.start_date} 至 {data.range.end_date}，合计{" "}
          {number(chart.total)}，单日最高 {number(chart.peak)}
          。精确每日数值见下方数据明细。
        </desc>
        {ticks.map((value) => {
          const y = 196 - (value / chart.ceiling) * 172;
          return (
            <g key={value}>
              <line
                x1={chart.left}
                y1={y}
                x2={chart.right}
                y2={y}
                className="dashboard-chart-grid"
              />
              <text x={chart.left - 10} y={y + 4} textAnchor="end">
                {number(value)}
              </text>
            </g>
          );
        })}
        {chart.total > 0 && (
          <path
            d={`${chart.line} L${chart.right},196 L${chart.left},196 Z`}
            className="dashboard-chart-area"
          />
        )}
        <path d={chart.line} className="dashboard-chart-line" />
        {chart.points.map((point) => (
          <circle
            key={point.date}
            cx={point.x}
            cy={point.y}
            r={data.range.days === 7 ? 3.5 : 2.4}
            className="dashboard-chart-point"
          >
            <title>{`${point.date}：${number(point.value)}`}</title>
          </circle>
        ))}
        {dateIndexes.map((index) => (
          <text
            key={index}
            x={chart.points[index].x}
            y="222"
            textAnchor={
              index === 0
                ? "start"
                : index === data.trend.length - 1
                  ? "end"
                  : "middle"
            }
          >
            {dashboardDate(data.trend[index].date)}
          </text>
        ))}
      </svg>
      {chart.total === 0 && (
        <p className="dashboard-empty-note">这段时间暂无{label}记录。</p>
      )}
      <p className="dashboard-note">{dashboardMetricNotes[metric]}</p>
      <details className="dashboard-data-details">
        <summary>查看每日数据明细</summary>
        <div
          className="dashboard-table-scroll"
          tabIndex={0}
          role="region"
          aria-label="每日统计数据，可横向滚动"
        >
          <table>
            <caption>
              最近 {data.range.days} 天每日数据，今日统计截至本次更新时间
            </caption>
            <thead>
              <tr>
                <th scope="col">日期（北京）</th>
                {metrics.map((key) => (
                  <th scope="col" key={key}>
                    {dashboardMetricNames[key]}
                  </th>
                ))}
              </tr>
            </thead>
            <tbody>
              {data.trend.map((row) => (
                <tr key={row.date}>
                  <th scope="row">{row.date}</th>
                  {metrics.map((key) => (
                    <td key={key}>{number(row[key])}</td>
                  ))}
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      </details>
    </section>
  );
}

function DashboardDistributions({ data }: { data: DashboardData }) {
  const statuses = Object.entries(dashboardStatusNames) as Array<
    [keyof typeof dashboardStatusNames, string]
  >;
  const max = Math.max(1, ...Object.values(data.products.statuses));
  return (
    <section
      className="dashboard-panel dashboard-distributions"
      aria-labelledby="dashboard-distribution-title"
    >
      <div className="dashboard-section-title">
        <div>
          <span className="dashboard-overline">当前快照 · 不随日期筛选</span>
          <h2 id="dashboard-distribution-title">商品分布</h2>
        </div>
        <Package size={20} aria-hidden="true" />
      </div>
      <div className="dashboard-trade-split">
        <Link to="/products?trade_type=sale">
          <span>账号出售</span>
          <strong>{number(data.products.sale)}</strong>
        </Link>
        <Link to="/products?trade_type=rent">
          <span>账号租赁</span>
          <strong>{number(data.products.rent)}</strong>
        </Link>
      </div>
      <h3>商品状态</h3>
      <ul className="dashboard-status-bars">
        {statuses.map(([status, label]) => (
          <li key={status}>
            <Link
              to={
                status === "pending" ? "/reviews" : `/products?status=${status}`
              }
            >
              <span>{label}</span>
              <span className="dashboard-bar-track" aria-hidden="true">
                <i
                  style={{
                    width: `${(data.products.statuses[status] / max) * 100}%`,
                  }}
                />
              </span>
              <strong>{number(data.products.statuses[status])}</strong>
            </Link>
          </li>
        ))}
      </ul>
      <p className="dashboard-note">
        商品总量与分布不含回收站。已上架为商品状态，公开可见还须审核版本一致、分类启用。
      </p>
      <Link className="dashboard-inline-link" to="/products?trash=1">
        回收站 {number(data.products.trash)} 件 <ArrowRight size={14} />
      </Link>
    </section>
  );
}

function DashboardRanking({ data }: { data: DashboardData }) {
  const selected = data.ranking.by;
  return (
    <section
      className="dashboard-panel dashboard-ranking"
      aria-labelledby="dashboard-ranking-title"
    >
      <div className="dashboard-section-title">
        <div>
          <span className="dashboard-overline">
            最近 {data.range.days} 天 · 按{dashboardMetricNames[selected]}排序
          </span>
          <h2 id="dashboard-ranking-title">商品互动排行 TOP 10</h2>
        </div>
        <Link className="dashboard-inline-link" to="/recommendations">
          推荐规则 <ArrowRight size={14} />
        </Link>
      </div>
      <p className="dashboard-note">
        当前未删除商品在所选期间的互动排行，包含草稿和已下架商品；仅列出所选指标大于
        0 的商品。首页热门榜另按推荐规则加权计算。
      </p>
      {data.ranking.items.length === 0 ? (
        <div className="dashboard-ranking-empty" role="status">
          <TrendingUp size={26} aria-hidden="true" />
          <p>这段时间暂无符合条件的互动商品。</p>
          <span>有新的浏览、收藏或咨询记录后，可刷新查看。</span>
        </div>
      ) : (
        <ol className="dashboard-ranking-list">
          {data.ranking.items.map((item, index) => (
            <li key={item.id}>
              <span
                className={`dashboard-rank-position ${index < 3 ? "leading" : ""}`}
              >
                {String(index + 1).padStart(2, "0")}
              </span>
              <div className="dashboard-rank-product">
                <Link to={adminProductPath(item.id)} title={item.title}>
                  {item.title.trim() || "未填写标题"}
                </Link>
                <span>
                  {item.product_code} ·{" "}
                  {item.trade_type === "sale"
                    ? "账号出售"
                    : item.rental_category === "hour_day"
                      ? "时日租号"
                      : item.rental_category === "week_month"
                        ? "周月租号"
                        : "账号租赁"}{" "}
                  · {dashboardStatusNames[item.status]}
                </span>
              </div>
              <dl className="dashboard-rank-counts">
                {(
                  ["views", "favorites", "consultations"] as RankingMetric[]
                ).map((key) => (
                  <div key={key} className={selected === key ? "selected" : ""}>
                    <dt>
                      {key === "views"
                        ? "浏览"
                        : key === "favorites"
                          ? "收藏"
                          : "咨询"}
                    </dt>
                    <dd>{number(item[key])}</dd>
                  </div>
                ))}
              </dl>
            </li>
          ))}
        </ol>
      )}
    </section>
  );
}

export function DashboardContent({
  data,
  metric,
  onMetric,
}: {
  data: DashboardData;
  metric: TrendMetric;
  onMetric: (value: TrendMetric) => void;
}) {
  const snapshots = [
    {
      label: "全部商品",
      value: data.products.total,
      note: "不含回收站商品",
      icon: Package,
      to: "/products",
    },
    {
      label: "公开可见",
      value: data.products.visible,
      note: "管理已上架商品",
      icon: Eye,
      to: "/products?status=published",
    },
    {
      label: "会员总数",
      value: data.members.total,
      note: `启用 ${number(data.members.enabled)} · 停用 ${number(data.members.disabled)}`,
      icon: Users,
      to: "/users",
    },
    {
      label: "未发布草稿",
      value: data.products.statuses.draft,
      note: "继续填写或准备上架",
      icon: BookOpen,
      to: "/products?status=draft",
    },
  ];
  const todos = [
    {
      label: "商品待审核",
      value: data.todos.pending_products,
      to: "/reviews",
      icon: ClipboardCheck,
    },
    {
      label: "昵称待审核",
      value: data.todos.pending_nicknames,
      to: "/nickname-reviews",
      icon: Users,
    },
    {
      label: "等待接待",
      value: data.todos.waiting_conversations,
      to: "/chat",
      icon: MessageSquare,
    },
    {
      label: "商品上架中",
      value: data.todos.publishing_products,
      to: "/products?status=publishing",
      icon: Clock3,
    },
  ];
  return (
    <div
      className="dashboard-content"
      data-days={data.range.days}
      data-rank-by={data.ranking.by}
    >
      <section aria-labelledby="dashboard-current-title">
        <div className="dashboard-section-title dashboard-section-heading">
          <h2 id="dashboard-current-title">当前总览</h2>
          <span className="dashboard-note">
            截至 {dashboardTime(data.generated_at)} · 北京时间
          </span>
        </div>
        <div className="dashboard-snapshot-grid">
          {snapshots.map((item) => (
            <Link className="dashboard-snapshot" key={item.label} to={item.to}>
              <div>
                <span>{item.label}</span>
                <item.icon size={19} aria-hidden="true" />
              </div>
              <strong>{number(item.value)}</strong>
              <p>
                {item.note}
                <ArrowRight size={14} aria-hidden="true" />
              </p>
            </Link>
          ))}
        </div>
      </section>
      <section className="dashboard-todos" aria-label="当前待办">
        <span className="dashboard-todos-heading">待办事项</span>
        {todos.map((item) => (
          <Link
            key={item.label}
            to={item.to}
            className={item.value > 0 ? "has-todo" : ""}
          >
            <item.icon size={17} aria-hidden="true" />
            <span>{item.label}</span>
            <strong>{number(item.value)}</strong>
            <ArrowRight size={14} aria-hidden="true" />
          </Link>
        ))}
      </section>
      <section aria-labelledby="dashboard-period-title">
        <div className="dashboard-section-title dashboard-section-heading">
          <h2 id="dashboard-period-title">最近 {data.range.days} 天</h2>
          <span className="dashboard-note">含今天，统计至本次更新时间</span>
        </div>
        <div className="dashboard-period-grid">
          {metrics.map((key) => {
            const Icon = metricIcons[key];
            return (
              <div className="dashboard-period-stat" key={key}>
                <Icon size={18} aria-hidden="true" />
                <span>{dashboardMetricNames[key]}</span>
                <strong>
                  {number(
                    key === "new_members"
                      ? data.members.new_in_period
                      : data.engagement[key],
                  )}
                </strong>
              </div>
            );
          })}
        </div>
      </section>
      <div className="dashboard-analysis-grid">
        <DashboardTrend data={data} metric={metric} onMetric={onMetric} />
        <DashboardDistributions data={data} />
      </div>
      <DashboardRanking data={data} />
      <details className="dashboard-definition">
        <summary>查看统计口径</summary>
        <ul>
          {metrics.map((key) => (
            <li key={key}>
              <strong>{dashboardMetricNames[key]}：</strong>
              {dashboardMetricNotes[key]}
            </li>
          ))}
          <li>
            收藏和咨询仅计入目前启用的普通会员，排除商品本人。相关关系失效后，历史日期的有效数量可能随之变化。
          </li>
          <li>
            会员和商品总量为当前状态；“已售出”“出租中”来自人工商品状态，不表示支付订单或收入。
          </li>
          <li>
            互动统计和排行均排除当前已删除商品，包含未删除的草稿、已下架等状态；今日尚未结束。
          </li>
        </ul>
      </details>
    </div>
  );
}

function DashboardActions() {
  return (
    <section
      className="dashboard-actions"
      aria-labelledby="dashboard-actions-title"
    >
      <div className="dashboard-section-title">
        <h2 id="dashboard-actions-title">常用操作</h2>
        <Link className="dashboard-inline-link" to="/help">
          操作指南 <ArrowRight size={14} />
        </Link>
      </div>
      <div className="dashboard-action-grid">
        {[
          {
            icon: Plus,
            title: "上架新商品",
            desc: "填写资料与图片",
            to: "/products?new=1",
          },
          {
            icon: ClipboardCheck,
            title: "审核会员投稿",
            desc: "检查并处理待审核商品",
            to: "/reviews",
          },
          {
            icon: MessageSquare,
            title: "接待在线咨询",
            desc: "查看商品卡片并回复用户",
            to: "/chat",
          },
          {
            icon: Package,
            title: "管理商品",
            desc: "维护资料、报价和图片",
            to: "/products",
          },
          {
            icon: PanelsTopLeft,
            title: "装修首页",
            desc: "修改文案和展示内容",
            to: "/settings",
          },
          {
            icon: Headphones,
            title: "客服与二维码",
            desc: "维护联系入口与服务时间",
            to: "/contacts",
          },
        ].map((item) => (
          <Link className="dashboard-action" key={item.to} to={item.to}>
            <item.icon size={20} aria-hidden="true" />
            <span>
              <strong>{item.title}</strong>
              <small>{item.desc}</small>
            </span>
            <ArrowRight size={15} aria-hidden="true" />
          </Link>
        ))}
      </div>
      <div className="dashboard-help">
        <span>第一次上架？查看三步操作演示。</span>
        <GuideButton kind="product" label="查看演示" />
      </div>
    </section>
  );
}

export function AdminDashboard() {
  const [days, setDays] = useState<DashboardDays>(7);
  const [rankBy, setRankBy] = useState<RankingMetric>("views");
  const [metric, setMetric] = useState<TrendMetric>("views");
  const [data, setData] = useState<DashboardData | null>(null);
  const [error, setError] = useState<unknown>();
  const [loading, setLoading] = useState(true);
  const [retry, setRetry] = useState(0);
  const generation = useRef(0);
  useEffect(() => {
    const requestId = ++generation.current;
    const controller = new AbortController();
    setLoading(true);
    setError(undefined);
    adminApi
      .dashboard(days, rankBy, controller.signal)
      .then((result) => {
        if (!controller.signal.aborted && generation.current === requestId)
          setData(result);
      })
      .catch((failure: unknown) => {
        if (!controller.signal.aborted && generation.current === requestId) {
          // In particular, an expired session must not reveal the prior snapshot on retry.
          setData(null);
          setError(failure);
        }
      })
      .finally(() => {
        if (!controller.signal.aborted && generation.current === requestId)
          setLoading(false);
      });
    return () => {
      controller.abort();
      generation.current++;
    };
  }, [days, rankBy, retry]);
  const current =
    data?.range.days === days && data.ranking.by === rankBy ? data : null;
  const refresh = () => setRetry((value) => value + 1);
  return (
    <div className="admin-dashboard">
      <div className="admin-heading">
        <div>
          <span className="eyebrow">清叶运营工作台</span>
          <h1>数据看板</h1>
          <p>查看商品与会员动态，及时处理今天的待办。</p>
        </div>
        <Link className="button-white" to="/products?new=1">
          <Plus size={17} />
          上架新商品
        </Link>
      </div>
      <div className="dashboard-toolbar">
        <div className="dashboard-range" role="group" aria-label="统计时间范围">
          {([7, 30] as DashboardDays[]).map((value) => (
            <button
              type="button"
              key={value}
              aria-pressed={days === value}
              onClick={() => {
                if (days === value) return;
                setDays(value);
                setError(undefined);
              }}
            >
              最近 {value} 天
            </button>
          ))}
        </div>
        <label className="dashboard-rank-select">
          <span>热门排行依据</span>
          <select
            value={rankBy}
            onChange={(event) => {
              setRankBy(event.target.value as RankingMetric);
              setError(undefined);
            }}
          >
            <option value="views">去重商品浏览</option>
            <option value="favorites">首次有效收藏</option>
            <option value="consultations">首次商品咨询</option>
          </select>
        </label>
        <button
          className="button-outline dashboard-refresh"
          type="button"
          disabled={loading}
          onClick={refresh}
        >
          <RefreshCw size={15} aria-hidden="true" />
          {loading ? "正在更新" : "刷新数据"}
        </button>
      </div>
      <div className="dashboard-feedback" aria-live="polite">
        {loading
          ? `正在读取最近 ${days} 天的数据…`
          : error
            ? "本次数据读取失败，请重试。"
            : current
              ? `已更新 ${dashboardTime(current.generated_at)}（北京时间）`
              : "准备读取数据…"}
      </div>
      {error ? (
        <StateView error={error} onRetry={refresh} />
      ) : current ? (
        <div aria-busy={loading}>
          <DashboardContent
            data={current}
            metric={metric}
            onMetric={setMetric}
          />
        </div>
      ) : (
        <div className="dashboard-loading">
          <StateView loading />
        </div>
      )}
      <DashboardActions />
      <div className="dashboard-foot">
        <span>当前数据来自测试站。</span>
        {publicSite && (
          <a href={publicSite} target="_blank" rel="noreferrer">
            打开用户网站 ↗
          </a>
        )}
      </div>
    </div>
  );
}
