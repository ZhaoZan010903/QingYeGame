import { useEffect, useId, useRef, useState } from "react";
import { Link, useLocation, useSearchParams } from "react-router-dom";
import {
  ArrowRight,
  Package,
  ClipboardCheck,
  PanelsTopLeft,
  Headphones,
  Plus,
  ListChecks,
  RefreshCcw,
  ChartNoAxesCombined,
  MessageSquare,
  Users,
  RefreshCw,
  Eye,
  Clock3,
  ChevronRight,
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
function DashboardRange({
  value,
  change,
}: {
  value: DashboardDays;
  change: (days: DashboardDays) => void;
}) {
  return (
    <div
      className="dashboard-range"
      role="group"
      aria-label="趋势及商品排行的统计时间范围"
    >
      {([7, 30] as const).map((days) => (
        <button
          key={days}
          type="button"
          aria-pressed={days === value}
          onClick={() => change(days)}
        >
          最近 {days} 天
        </button>
      ))}
    </div>
  );
}

export function DashboardTrend({
  data,
  metric,
  onMetric,
  onDays,
}: {
  data: DashboardData;
  metric: TrendMetric;
  onMetric: (value: TrendMetric) => void;
  onDays?: (days: DashboardDays) => void;
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
      className="dashboard-panel dashboard-trend fluent-card"
      aria-labelledby={`${id}-heading`}
    >
      <div className="dashboard-section-title">
        <div>
          <h2 id={`${id}-heading`}>每日趋势</h2>
          <p className="dashboard-note">
            最近 {data.range.days} 天 · {dashboardDate(data.range.start_date)} –{" "}
            {dashboardDate(data.range.end_date)}（北京）
          </p>
        </div>
        {onDays && <DashboardRange value={data.range.days} change={onDays} />}
      </div>
      <div className="dashboard-metric-tabs" role="group" aria-label="趋势指标">
        {metrics.map((key) => (
          <button
            key={key}
            type="button"
            aria-pressed={metric === key}
            onClick={() => onMetric(key)}
          >
            <span>{dashboardMetricNames[key]}</span>
            <strong>
              {number(
                key === "new_members"
                  ? data.members.new_in_period
                  : data.engagement[key],
              )}
            </strong>
          </button>
        ))}
      </div>
      <p className="dashboard-chart-summary">
        <span className="dashboard-chart-key">{label}</span>
        <span>
          单日最高 <strong>{number(chart.peak)}</strong>
        </span>
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
        <defs>
          <linearGradient
            id={`${id}-area`}
            x1="0"
            y1="0"
            x2="0"
            y2="1"
            gradientUnits="objectBoundingBox"
          >
            <stop offset="0%" stopColor="var(--accent)" stopOpacity=".33" />
            <stop offset="100%" stopColor="#30D158" stopOpacity=".03" />
          </linearGradient>
        </defs>
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
            fill={`url(#${id}-area)`}
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
      className="dashboard-panel dashboard-distributions fluent-card"
      aria-labelledby="dashboard-distribution-title"
    >
      <div className="dashboard-section-title">
        <div>
          <h2 id="dashboard-distribution-title">商品分布</h2>
          <p className="dashboard-note">当前库存 · 不含回收站</p>
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
        已上架商品须分类启用且当前版本已获批，才会公开展示。
      </p>
      <Link className="dashboard-inline-link" to="/products?trash=1">
        回收站 {number(data.products.trash)} 件 <ArrowRight size={14} />
      </Link>
    </section>
  );
}

function DashboardRanking({ data }: { data: DashboardData }) {
  const selected = data.ranking.by;
  const location = useLocation();
  return (
    <section
      className="dashboard-panel dashboard-ranking fluent-card"
      aria-labelledby="dashboard-ranking-title"
    >
      <div className="dashboard-section-title">
        <div>
          <h2 id="dashboard-ranking-title">商品互动排行</h2>
          <p className="dashboard-note">
            最近 {data.range.days} 天 · 按{dashboardMetricNames[selected]}
            排序，最多 10 件
          </p>
        </div>
        <Link className="dashboard-inline-link" to="/recommendations">
          首页热门规则 <ArrowRight size={14} />
        </Link>
      </div>
      {data.ranking.items.length === 0 ? (
        <div className="dashboard-ranking-empty" role="status">
          <ChartNoAxesCombined size={28} aria-hidden="true" />
          <p>本期暂无产生{dashboardMetricNames[selected]}的商品。</p>
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
                <Link
                  to={adminProductPath(item.id)}
                  state={{ returnTo: location.pathname + location.search }}
                  title={item.title}
                >
                  {item.title.trim() || "未填写标题"}
                </Link>
                <span>
                  <code>{item.product_code}</code> ·{" "}
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

function DashboardInventory({ data }: { data: DashboardData }) {
  return (
    <section className="dashboard-snapshot-grid" aria-label="当前库存和会员">
      <Link className="dashboard-snapshot fluent-card" to="/products">
        <div>
          <Package size={17} aria-hidden="true" />
          <h2>全部商品</h2>
          <ChevronRight size={16} aria-hidden="true" />
        </div>
        <strong>{number(data.products.total)}</strong>
        <p>在库商品，含 {number(data.products.statuses.draft)} 件草稿</p>
      </Link>
      <Link
        className="dashboard-snapshot fluent-card"
        to="/products?visibility=public"
      >
        <div>
          <Eye size={17} aria-hidden="true" />
          <h2>公开可见</h2>
          <ChevronRight size={16} aria-hidden="true" />
        </div>
        <strong>{number(data.products.visible)}</strong>
        <p>用户站可查看的在架商品</p>
      </Link>
      <Link className="dashboard-snapshot fluent-card" to="/users">
        <div>
          <Users size={17} aria-hidden="true" />
          <h2>会员总数</h2>
          <ChevronRight size={16} aria-hidden="true" />
        </div>
        <strong>{number(data.members.total)}</strong>
        <p>
          启用 {number(data.members.enabled)} · 停用{" "}
          {number(data.members.disabled)}
        </p>
      </Link>
    </section>
  );
}

function DashboardWork({ data }: { data: DashboardData }) {
  const {
    pending_products,
    pending_nicknames,
    waiting_conversations,
    publishing_products,
  } = data.todos;
  return (
    <section
      className="dashboard-panel dashboard-work fluent-card"
      aria-labelledby="dashboard-work-title"
    >
      <div className="dashboard-section-title">
        <div>
          <h2 id="dashboard-work-title">工作队列</h2>
          <p className="dashboard-note">当前状态 · 全部时间</p>
        </div>
        <ListChecks aria-hidden="true" size={20} />
      </div>
      <h3>待人工处理</h3>
      <ul className="dashboard-work-list">
        {[
          {
            name: "待审核商品",
            count: pending_products,
            to: "/reviews",
            icon: ClipboardCheck,
          },
          {
            name: "待审核昵称",
            count: pending_nicknames,
            to: "/nickname-reviews",
            icon: Users,
          },
        ].map(({ name, count, to, icon: Icon }) => (
          <li key={to}>
            <Link to={to}>
              <Icon size={16} aria-hidden="true" />
              <span>{name}</span>
              <strong data-active={count > 0}>{number(count)}</strong>
              <ChevronRight size={15} aria-hidden="true" />
            </Link>
          </li>
        ))}
      </ul>
      <h3>系统处理中</h3>
      <ul className="dashboard-work-list">
        <li>
          <Link to="/chat">
            <MessageSquare size={16} aria-hidden="true" />
            <span>
              全站等待分配<small>进入接待台</small>
            </span>
            <strong>{number(waiting_conversations)}</strong>
            <ChevronRight size={15} aria-hidden="true" />
          </Link>
        </li>
        <li>
          <Link to="/products?status=publishing">
            <Clock3 size={16} aria-hidden="true" />
            <span>商品上架中</span>
            <strong>{number(publishing_products)}</strong>
            <ChevronRight size={15} aria-hidden="true" />
          </Link>
        </li>
      </ul>
    </section>
  );
}

export function DashboardContent({
  data,
  metric,
  onMetric,
  days = data.range.days,
  onDays,
}: {
  data: DashboardData;
  metric: TrendMetric;
  onMetric: (value: TrendMetric) => void;
  days?: DashboardDays;
  onDays?: (days: DashboardDays) => void;
}) {
  const current = days === data.range.days;
  return (
    <div
      className="dashboard-content"
      data-days={days}
      data-rank-by={data.ranking.by}
    >
      <DashboardInventory data={data} />
      <div className="dashboard-analysis-grid">
        {current ? (
          <DashboardTrend
            data={data}
            metric={metric}
            onMetric={onMetric}
            onDays={onDays}
          />
        ) : (
          <section
            className="dashboard-panel dashboard-trend fluent-card"
            aria-label="每日趋势"
          >
            <div className="dashboard-section-title">
              <h2>每日趋势</h2>
              {onDays && <DashboardRange value={days} change={onDays} />}
            </div>
            <div className="dashboard-loading-period" role="status">
              正在读取最近 {days} 天的趋势和排行…
            </div>
          </section>
        )}
        <DashboardWork data={data} />
      </div>
      <div className="dashboard-analysis-grid">
        {current ? (
          <DashboardRanking data={data} />
        ) : (
          <section
            className="dashboard-panel dashboard-ranking fluent-card"
            aria-label="商品互动排行"
          >
            <h2>商品互动排行</h2>
            <p className="dashboard-note">等待所选期间数据</p>
          </section>
        )}
        <DashboardDistributions data={data} />
      </div>
      <details className="dashboard-definition fluent-card">
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
            icon: RefreshCcw,
            title: "旧站同步",
            desc: "核对目录更新与批次结果",
            to: "/legacy-sync",
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
          <Link
            className="dashboard-action fluent-card fluent-interactive"
            key={item.to}
            to={item.to}
          >
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
  const [params, setParams] = useSearchParams();
  const days: DashboardDays = params.get("days") === "30" ? 30 : 7;
  const metric =
    metrics.find((item) => item === params.get("metric")) ?? "views";
  const setFilter = (
    key: "days" | "metric",
    value: string,
    defaultValue: string,
  ) =>
    setParams(
      (previous) => {
        const next = new URLSearchParams(previous);
        if (value === defaultValue) next.delete(key);
        else next.set(key, value);
        return next;
      },
      { replace: true },
    );
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
      .dashboard(days, "views", controller.signal)
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
  }, [days, retry]);
  const refresh = () => setRetry((value) => value + 1);
  const changeDays = (next: DashboardDays) => {
    if (days === next) return;
    setError(undefined);
    setFilter("days", String(next), "7");
  };
  return (
    <div className="admin-dashboard">
      <div className="admin-heading">
        <div>
          <h1>数据看板</h1>
          <p className="dashboard-feedback" aria-live="polite">
            {loading && !data
              ? `正在读取最近 ${days} 天的数据…`
              : error
                ? "数据读取失败"
                : data
                  ? `数据截至 ${dashboardTime(data.generated_at)}（北京时间）`
                  : "准备读取数据…"}
          </p>
        </div>
        <div className="dashboard-heading-actions">
          <button
            className="button-outline dashboard-refresh"
            type="button"
            disabled={loading}
            onClick={refresh}
          >
            <RefreshCw size={16} aria-hidden="true" />
            {loading ? "正在更新" : "刷新数据"}
          </button>
          <Link className="button-white" to="/products?new=1">
            <Plus size={17} aria-hidden="true" />
            新建商品
          </Link>
        </div>
      </div>
      {data && !error ? (
        <div aria-busy={loading}>
          <DashboardContent
            data={data}
            metric={metric}
            onMetric={(value) => setFilter("metric", value, "views")}
            days={days}
            onDays={changeDays}
          />
        </div>
      ) : (
        <div
          className={`dashboard-panel dashboard-initial fluent-card${error ? " is-error" : ""}`}
        >
          <div className="dashboard-section-title">
            <h2>运营数据</h2>
            <DashboardRange value={days} change={changeDays} />
          </div>
          <StateView error={error} loading={!error} onRetry={refresh} />
        </div>
      )}
      <DashboardActions />
      {publicSite && (
        <div className="dashboard-foot">
          <a href={publicSite} target="_blank" rel="noreferrer">
            打开用户网站 ↗
          </a>
        </div>
      )}
    </div>
  );
}
