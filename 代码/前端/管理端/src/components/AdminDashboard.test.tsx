import { afterEach, describe, expect, it, vi } from "vitest";
import { renderToStaticMarkup } from "react-dom/server";
import { MemoryRouter } from "react-router-dom";
import {
  AdminDashboard,
  DashboardContent,
  DashboardTrend,
} from "./AdminDashboard";
import { adminApi } from "../lib/admin-api";
import {
  dashboardChart,
  dashboardTime,
  readDashboard,
  type DashboardData,
  type DashboardDays,
  type RankingMetric,
} from "../lib/dashboard";

function fixture(
  days: DashboardDays = 7,
  by: RankingMetric = "views",
): DashboardData {
  const end = Date.parse("2026-11-14");
  const start = end - (days - 1) * 86_400_000;
  const trend = Array.from({ length: days }, (_, i) => ({
    date: new Date(start + i * 86_400_000).toISOString().slice(0, 10),
    views: i === days - 1 ? 12 : 0,
    favorites: i === 0 ? 2 : 0,
    consultations: i === 0 ? 1 : 0,
    new_members: i === days - 1 ? 3 : 0,
  }));
  return {
    generated_at: "2026-11-14T09:32:07Z",
    timezone: "Asia/Shanghai",
    range: {
      days,
      start_date: trend[0].date,
      end_date: trend[days - 1].date,
      start_at: `${trend[0].date}T00:00:00+08:00`,
      end_at: "2026-11-14T09:32:07Z",
    },
    products: {
      total: 89,
      visible: 30,
      sale: 40,
      rent: 49,
      trash: 6,
      statuses: {
        draft: 20,
        pending: 3,
        publishing: 2,
        published: 35,
        rejected: 5,
        rejected_final: 4,
        offline: 10,
        sold: 5,
        rented: 5,
      },
    },
    members: { total: 23, enabled: 21, disabled: 2, new_in_period: 3 },
    todos: {
      pending_products: 3,
      publishing_products: 2,
      pending_nicknames: 4,
      waiting_conversations: 5,
    },
    engagement: { views: 12, favorites: 2, consultations: 1 },
    trend,
    ranking: {
      by,
      limit: 10,
      items: [
        {
          id: "item/a",
          product_code: "QY-001",
          title: "<script>账号标题</script>",
          trade_type: "rent",
          rental_category: "week_month",
          status: "offline",
          views: 12,
          favorites: 2,
          consultations: 1,
        },
      ],
    },
  };
}
afterEach(() => vi.unstubAllGlobals());

describe("数据看板响应与时间范围", () => {
  it.each([7, 30] as const)(
    "读取 %s 个连续北京时间自然日，保留快照与期间数据",
    (days) => {
      const data = fixture(days, "consultations");
      expect(readDashboard(data, days, "consultations")).toBe(data);
      expect(data.products.total).toBe(89);
      expect(data.trend).toHaveLength(days);
    },
  );
  it("响应日期或排行与请求不符时拒绝，不能把旧数据标为新范围", () => {
    expect(() => readDashboard(fixture(7), 30, "views")).toThrow("数据不完整");
    expect(() => readDashboard(fixture(30, "favorites"), 30, "views")).toThrow(
      "数据不完整",
    );
  });
  it("缺失统计和负数不默认为零", () => {
    const missing = fixture() as unknown as Record<string, unknown>;
    delete missing.todos;
    expect(() => readDashboard(missing, 7, "views")).toThrow();
    const negative = fixture();
    negative.engagement.favorites = -1;
    expect(() => readDashboard(negative, 7, "views")).toThrow();
    const fractional = fixture();
    fractional.products.total = 1.5;
    expect(() => readDashboard(fractional, 7, "views")).toThrow();
  });
  it("拒绝缺日、重复日期、无效时区与超出上限的排行榜", () => {
    const gap = fixture();
    gap.trend.splice(2, 1);
    expect(() => readDashboard(gap, 7, "views")).toThrow();
    const repeated = fixture();
    repeated.trend[2].date = repeated.trend[1].date;
    expect(() => readDashboard(repeated, 7, "views")).toThrow();
    expect(() =>
      readDashboard({ ...fixture(), timezone: "UTC" }, 7, "views"),
    ).toThrow();
    const overflowing = fixture();
    overflowing.ranking.items = Array.from(
      { length: 11 },
      () => overflowing.ranking.items[0],
    );
    expect(() => readDashboard(overflowing, 7, "views")).toThrow();
  });
  it("更新时间始终显示北京时间，与浏览器本地时区无关", () => {
    expect(dashboardTime("2026-11-14T09:32:07Z")).toContain("17:32:07");
  });
});

describe("数据看板真实请求", () => {
  it("使用明确的日期与排行参数，传递取消信号和管理员cookie", async () => {
    const fetch = vi
      .fn()
      .mockResolvedValue(
        new Response(JSON.stringify({ data: fixture(30, "favorites") })),
      );
    vi.stubGlobal("fetch", fetch);
    const controller = new AbortController();
    const data = await adminApi.dashboard(30, "favorites", controller.signal);
    expect(fetch.mock.calls[0][0]).toBe(
      "/api/v1/admin/dashboard?days=30&rank_by=favorites",
    );
    expect(fetch.mock.calls[0][1]).toMatchObject({
      method: "GET",
      credentials: "include",
      signal: controller.signal,
    });
    expect(data.ranking.by).toBe("favorites");
  });
  it("缺省只请求7天浏览排行，不拉商品分页或写入重算", async () => {
    const fetch = vi
      .fn()
      .mockResolvedValue(new Response(JSON.stringify({ data: fixture() })));
    vi.stubGlobal("fetch", fetch);
    await adminApi.dashboard();
    expect(fetch).toHaveBeenCalledTimes(1);
    expect(fetch.mock.calls[0][0]).toBe(
      "/api/v1/admin/dashboard?days=7&rank_by=views",
    );
    expect(fetch.mock.calls[0][1].method).toBe("GET");
  });
  it.each([401, 403, 500])(
    "%s 响应保持错误，不能显示伪造统计",
    async (status) => {
      vi.stubGlobal(
        "fetch",
        vi
          .fn()
          .mockResolvedValue(
            new Response(
              JSON.stringify({ message: "读取被拒绝", code: "DENIED" }),
              { status },
            ),
          ),
      );
      await expect(adminApi.dashboard()).rejects.toMatchObject({
        status,
        message: "读取被拒绝",
      });
    },
  );
  it("中断继续保留AbortError供请求代次处理", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn().mockRejectedValue(new DOMException("aborted", "AbortError")),
    );
    await expect(adminApi.dashboard()).rejects.toMatchObject({
      name: "AbortError",
    });
  });
  it("成功HTTP中的不完整数据同样显示可重试失败", async () => {
    vi.stubGlobal(
      "fetch",
      vi
        .fn()
        .mockResolvedValue(
          new Response(JSON.stringify({ data: { products: { total: 0 } } })),
        ),
    );
    await expect(adminApi.dashboard()).rejects.toThrow("看板数据不完整");
  });
});

describe("趋势图与看板展示", () => {
  it("全零、单点及空数组不会产生NaN或除零坐标", () => {
    for (const trend of [
      [],
      [
        {
          date: "2026-11-14",
          views: 0,
          favorites: 0,
          consultations: 0,
          new_members: 0,
        },
      ],
    ]) {
      const chart = dashboardChart(trend, "views");
      expect(chart.total).toBe(0);
      expect(chart.ceiling).toBe(1);
      expect(chart.line).not.toMatch(/NaN|Infinity/);
      expect(
        chart.points.every(
          (point) => Number.isFinite(point.x) && point.y === 196,
        ),
      ).toBe(true);
    }
  });
  it("峰值不溢出绘图区，汇总只使用所选指标", () => {
    const data = fixture();
    data.trend[2].favorites = 1_234;
    const chart = dashboardChart(data.trend, "favorites");
    expect(chart.total).toBe(1_236);
    expect(chart.peak).toBe(1_234);
    expect(chart.ceiling).toBeGreaterThanOrEqual(chart.peak);
    expect(chart.points.every((point) => point.y >= 24 && point.y <= 196)).toBe(
      true,
    );
    expect(chart.points[0].x).toBe(54);
    expect(chart.points.at(-1)?.x).toBe(686);
  });
  it("图表带文本摘要和键盘可展开的数据明细，不依赖悬停读数", () => {
    const html = renderToStaticMarkup(
      <DashboardTrend
        data={fixture()}
        metric="favorites"
        onMetric={() => {}}
      />,
    );
    expect(html).toContain('role="img"');
    expect(html).toContain("首次有效收藏每日趋势");
    expect(html).toContain("<summary>查看每日数据明细</summary>");
    expect(html).toContain('tabindex="0"');
    expect(html).toContain("2026-11-08");
    expect(html).toContain("2026-11-14");
    expect(html).toContain('aria-pressed="true">首次有效收藏');
  });
  it.each([254, 324, 710])(
    "%s像素绘图区保持完整日期和真实数值，轴标签预留空间",
    (width) => {
      const data = fixture(30);
      data.trend[2].views = 123_456_789;
      const chart = dashboardChart(data.trend, "views", width);
      expect(chart.points[0].x).toBe(chart.left);
      expect(chart.points.at(-1)?.x).toBe(width - 24);
      expect(
        chart.points.every(
          (point) => point.x >= chart.left && point.x <= width - 24,
        ),
      ).toBe(true);
      expect(chart.left).toBeGreaterThan(54);
      expect(chart.points).toHaveLength(30);
      expect(chart.total).toBe(123_456_801);
      expect(chart.points[2].value).toBe(123_456_789);
    },
  );
  it("快照、期间、待办跳转和下架商品排行使用实际数据，标题转义", () => {
    const html = renderToStaticMarkup(
      <MemoryRouter>
        <DashboardContent
          data={fixture(30, "consultations")}
          metric="views"
          onMetric={() => {}}
        />
      </MemoryRouter>,
    );
    expect(html).toContain('data-days="30" data-rank-by="consultations"');
    expect(html).toContain("当前快照 · 不随日期筛选");
    expect(html).toContain("最近 30 天");
    expect(html).toContain('href="/nickname-reviews"');
    expect(html).toContain('href="/reviews"');
    expect(html).toContain("待审核商品");
    expect(html).toContain("待审核昵称");
    expect(html).toContain("当前全部待审核数量，不受 7／30 天选择影响");
    expect(html).toContain('href="/products?status=publishing"');
    expect(html).toContain('href="/products?trash=1"');
    expect(html).toContain('href="/products/item%2Fa"');
    expect(html).toContain("周月租号 · 已下架");
    expect(html).toContain("&lt;script&gt;账号标题&lt;/script&gt;");
    expect(html).not.toContain("<script>");
    expect(html).toContain("当前未删除商品在所选期间的互动排行");
    expect(html).not.toContain("转化率");
  });
  it("没有互动时显示空态并保留每日零值", () => {
    const data = fixture();
    data.trend.forEach((row) => {
      row.views = 0;
    });
    data.engagement.views = 0;
    data.ranking.items = [];
    const html = renderToStaticMarkup(
      <MemoryRouter>
        <DashboardContent data={data} metric="views" onMetric={() => {}} />
      </MemoryRouter>,
    );
    expect(html).toContain("这段时间暂无去重商品浏览记录");
    expect(html).toContain("这段时间暂无符合条件的互动商品");
    expect(html).not.toContain("NaN");
    expect(html).not.toContain("Infinity");
  });
  it("首次加载不会渲染假数字，快捷操作仍可用", () => {
    const html = renderToStaticMarkup(
      <MemoryRouter>
        <AdminDashboard />
      </MemoryRouter>,
    );
    expect(html).toContain("正在读取最近 7 天的数据");
    expect(html).not.toContain('class="dashboard-content"');
    for (const target of [
      "/products?new=1",
      "/reviews",
      "/chat",
      "/settings",
      "/products",
    ])
      expect(html).toContain(`href="${target}"`);
  });
});
