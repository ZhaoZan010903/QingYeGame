import { useEffect, useState } from "react";
import {
  formatRentalAmount,
  rentalQuoteLabel,
  rentalUnitLabels,
} from "../../../共享/rental-quotes";
import type { RentalQuote } from "../../../共享/types";
import { request } from "../lib/api";

type Mode = "preview" | "automatic" | "paused";
type Controls = { mode: Mode; version: number };
type BatchStatus =
  | "running"
  | "ready"
  | "review"
  | "failed"
  | "applied"
  | "rolled_back"
  | "superseded";
type Batch = {
  id: string;
  status: BatchStatus;
  planned_at: string | null;
  started_at: string;
  source_updated_at: string | null;
  finished_at: string | null;
  applied_at: string | null;
  rolled_back_at: string | null;
  rollback_until: string | null;
  source_count: number;
  reason: string;
  summary: Partial<
    Record<"created" | "updated" | "removed" | "review" | "imaged", number>
  >;
};
type Product = {
  title: string;
  category: string;
  attributes: Record<string, unknown>;
  status: string;
  price: string | null;
  unit: string;
  quotes: RentalQuote[];
  image_count: number;
};
type Change = {
  legacy_id: string;
  code: string;
  action: "create" | "update" | "hold" | "missing";
  source_price: string;
  source_updated_at: string | null;
  before: Product | null;
  after: Product | null;
  images_changed: boolean;
  notices: string[];
};

const states: Record<BatchStatus, string> = {
  running: "读取中",
  ready: "待发布",
  review: "待核对",
  failed: "同步失败",
  applied: "已发布",
  rolled_back: "已回滚",
  superseded: "已替换",
};
const modes: Record<Mode, string> = {
  preview: "每天生成预览",
  automatic: "自动发布通过检查的批次",
  paused: "已暂停",
};
const productStates: Record<string, string> = {
  published: "已上架",
  offline: "已下架",
  sold: "已售出",
  rented: "出租中",
  draft: "草稿",
};
const actions: Record<Change["action"], string> = {
  create: "新增商品",
  update: "更新商品",
  hold: "待人工核对",
  missing: "旧站列表缺失",
};
const dateFormat = new Intl.DateTimeFormat("zh-CN", {
  timeZone: "Asia/Shanghai",
  dateStyle: "short",
  timeStyle: "medium",
});
const date = (value?: string | null) =>
  value ? dateFormat.format(new Date(value)) : "—";
const batchURL = (id: string) =>
  "/admin/legacy-sync/batches/" + encodeURIComponent(id);
const query = (cursor: string | null) =>
  "?" + new URLSearchParams({ limit: "15", ...(cursor ? { cursor } : {}) });

function ProductSummary({ product }: { product: Product | null }) {
  if (!product) return <span>—</span>;
  return (
    <>
      <strong>{product.title}</strong>
      <span>
        {productStates[product.status] ?? product.status} · {product.category} ·{" "}
        {product.image_count} 张图
      </span>
      {typeof product.attributes?.server === "string" && (
        <span>{product.attributes.server}</span>
      )}
      {product.quotes?.length ? (
        product.quotes.map((quote, index) => (
          <span key={index}>
            {rentalQuoteLabel(quote)} ¥{formatRentalAmount(quote.amount)}
            {quote.unit === "period" && quote.billing_unit
              ? "/" + rentalUnitLabels[quote.billing_unit]
              : ""}
          </span>
        ))
      ) : (
        <span>
          {product.price == null
            ? "报价待确认"
            : "¥" +
              formatRentalAmount(product.price) +
              (product.unit in rentalUnitLabels
                ? "/" +
                  rentalUnitLabels[
                    product.unit as keyof typeof rentalUnitLabels
                  ]
                : "")}
        </span>
      )}
    </>
  );
}

export function AdminLegacySync() {
  const [revision, setRevision] = useState(0);
  const [batches, setBatches] = useState<Batch[]>([]);
  const [controls, setControls] = useState<Controls | null>(null);
  const [batchPages, setBatchPages] = useState<(string | null)[]>([null]);
  const [nextBatch, setNextBatch] = useState<string | null>(null);
  const [selectedId, setSelectedId] = useState("");
  const [detail, setDetail] = useState<{
    batch: Batch;
    items: Change[];
  } | null>(null);
  const [detailPages, setDetailPages] = useState<(string | null)[]>([null]);
  const [nextDetail, setNextDetail] = useState<string | null>(null);
  const [loading, setLoading] = useState(true);
  const [detailLoading, setDetailLoading] = useState(false);
  const [pending, setPending] = useState(false);
  const [listError, setListError] = useState("");
  const [detailError, setDetailError] = useState("");
  const [feedback, setFeedback] = useState<{
    error: boolean;
    text: string;
  } | null>(null);

  const batchCursor = batchPages.at(-1) ?? null;
  const detailCursor = detailPages.at(-1) ?? null;
  useEffect(() => {
    const abort = new AbortController();
    setLoading(true);
    setListError("");
    request(
      "/admin/legacy-sync/batches" + query(batchCursor),
      "GET",
      undefined,
      abort.signal,
    )
      .then((response) => {
        if (abort.signal.aborted) return;
        setBatches(response.data.batches ?? []);
        setControls(response.data.controls);
        setNextBatch(response.next_cursor ?? null);
      })
      .catch((error: Error) => {
        if (!abort.signal.aborted) setListError(error.message);
      })
      .finally(() => {
        if (!abort.signal.aborted) setLoading(false);
      });
    return () => abort.abort();
  }, [revision, batchCursor]);

  useEffect(() => {
    if (!selectedId) return;
    const abort = new AbortController();
    setDetailLoading(true);
    setDetailError("");
    request(
      batchURL(selectedId) + query(detailCursor),
      "GET",
      undefined,
      abort.signal,
    )
      .then((response) => {
        if (abort.signal.aborted) return;
        setDetail(response.data);
        setNextDetail(response.next_cursor ?? null);
      })
      .catch((error: Error) => {
        if (!abort.signal.aborted) setDetailError(error.message);
      })
      .finally(() => {
        if (!abort.signal.aborted) setDetailLoading(false);
      });
    return () => abort.abort();
  }, [selectedId, detailCursor, revision]);

  async function mutate(
    path: string,
    method: string,
    body: unknown,
    message: string,
  ) {
    setPending(true);
    setFeedback(null);
    try {
      await request(path, method, body);
      setFeedback({ error: false, text: message });
    } catch (error) {
      setFeedback({
        error: true,
        text:
          error instanceof Error
            ? error.message
            : "操作失败，请刷新后核对批次状态。",
      });
    } finally {
      setPending(false);
      setRevision((value) => value + 1);
    }
  }

  function changeMode(mode: Mode) {
    if (!controls || pending || controls.mode === mode) return;
    if (
      mode === "automatic" &&
      !window.confirm(
        "开启后，每天中午 12:00 通过检查的批次会自动更新商品。确认开启？",
      )
    )
      return;
    void mutate(
      "/admin/legacy-sync/mode",
      "PUT",
      { mode, expected_version: controls.version },
      "已保存同步方式。",
    );
  }

  function changeBatch(operation: "apply" | "rollback", batch: Batch) {
    const message =
      operation === "apply"
        ? "确认发布该批次的可同步商品？新增 " +
          (batch.summary.created ?? 0) +
          " 件，更新 " +
          (batch.summary.updated ?? 0) +
          " 件。"
        : "确认整批回滚？本批新增商品会转为草稿，旧站定时同步也会暂停。";
    if (!pending && window.confirm(message))
      void mutate(
        batchURL(batch.id) + "/" + operation,
        "POST",
        { confirm: true },
        operation === "apply" ? "批次已发布。" : "批次已回滚，定时同步已暂停。",
      );
  }

  const active = detail?.batch;
  return (
    <section>
      <div className="page-heading">
        <div>
          <h1>旧站同步</h1>
          <p>每日北京时间 12:00 读取旧站商品。批次发布后可在 7 天内回滚。</p>
        </div>
        <button
          className="button-outline"
          onClick={() => setRevision((value) => value + 1)}
          disabled={loading || pending}
        >
          刷新记录
        </button>
      </div>
      <div className="admin-panel products-panel">
        <div className="admin-toolbar">
          <label>
            同步方式{" "}
            <select
              value={controls?.mode ?? "preview"}
              onChange={(event) => changeMode(event.target.value as Mode)}
              disabled={!controls || pending}
            >
              {Object.entries(modes).map(([key, label]) => (
                <option value={key} key={key}>
                  {label}
                </option>
              ))}
            </select>
          </label>
          <span className="muted">
            旧站列表缺失的商品保留当前状态，等待人工核对。
          </span>
        </div>
        {listError && (
          <p className="form-error" role="alert">
            {listError}
          </p>
        )}
        {feedback && (
          <p
            className={feedback.error ? "form-error" : undefined}
            role={feedback.error ? "alert" : "status"}
          >
            {feedback.text}
          </p>
        )}
        {loading ? (
          <p role="status">正在读取同步记录…</p>
        ) : (
          <div className="admin-table-wrap">
            <table className="admin-table">
              <thead>
                <tr>
                  <th>批次</th>
                  <th>计划时刻</th>
                  <th>开始 / 完成</th>
                  <th>生效时间</th>
                  <th>变更</th>
                  <th>状态</th>
                  <th>操作</th>
                </tr>
              </thead>
              <tbody>
                {batches.length === 0 && (
                  <tr>
                    <td colSpan={7}>还没有同步批次。</td>
                  </tr>
                )}
                {batches.map((batch) => (
                  <tr key={batch.id}>
                    <td className="mono" title={batch.id}>
                      {batch.id.slice(0, 8)}
                    </td>
                    <td>
                      {batch.planned_at ? date(batch.planned_at) : "手动预览"}
                    </td>
                    <td>
                      {date(batch.started_at)}
                      <span>{date(batch.finished_at)}</span>
                    </td>
                    <td>
                      {date(batch.applied_at)}
                      {batch.rolled_back_at && (
                        <span>回滚 {date(batch.rolled_back_at)}</span>
                      )}
                    </td>
                    <td>
                      新增 {batch.summary.created ?? 0} · 更新{" "}
                      {batch.summary.updated ?? 0}
                      <span>待核对 {batch.summary.review ?? 0}</span>
                    </td>
                    <td>
                      <span className="status-pill">
                        {states[batch.status]}
                      </span>
                    </td>
                    <td>
                      <button
                        className="button-outline"
                        disabled={pending}
                        onClick={() => {
                          if (selectedId === batch.id) return;
                          setSelectedId(batch.id);
                          setDetail(null);
                          setNextDetail(null);
                          setDetailPages([null]);
                        }}
                      >
                        查看明细
                      </button>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
        <div className="admin-actions">
          <button
            className="button-outline"
            disabled={batchPages.length < 2 || loading || pending}
            onClick={() => setBatchPages((pages) => pages.slice(0, -1))}
          >
            上一页
          </button>
          <button
            className="button-outline"
            disabled={!nextBatch || loading || pending}
            onClick={() => setBatchPages((pages) => [...pages, nextBatch])}
          >
            下一页
          </button>
        </div>
      </div>
      {selectedId && (
        <div
          className="admin-panel products-panel"
          style={{ marginTop: 20 }}
          aria-label="批次明细"
        >
          <div className="admin-toolbar">
            <div>
              <h2>
                批次 <span className="mono">{selectedId.slice(0, 8)}</span>
              </h2>
              {active && (
                <p>
                  旧站读到 {active.source_count} 件 · 源数据最新时间{" "}
                  {date(active.source_updated_at)}
                </p>
              )}
              {active?.reason && <p role="status">{active.reason}</p>}
            </div>
            {active && (
              <div className="admin-actions">
                {(active.status === "ready" || active.status === "review") &&
                  controls?.mode !== "paused" && (
                    <button
                      className="button-primary"
                      disabled={pending || detailLoading || !controls}
                      onClick={() => changeBatch("apply", active)}
                    >
                      发布本批次
                    </button>
                  )}
                {active.status === "applied" &&
                  active.rollback_until &&
                  new Date(active.rollback_until).getTime() > Date.now() && (
                    <button
                      className="button-outline"
                      disabled={pending || detailLoading}
                      onClick={() => changeBatch("rollback", active)}
                    >
                      回滚本批次
                    </button>
                  )}
              </div>
            )}
          </div>
          {active?.rollback_until && active.status === "applied" && (
            <p>可回滚至 {date(active.rollback_until)}。</p>
          )}
          {detailError && (
            <p className="form-error" role="alert">
              {detailError}
            </p>
          )}
          {detailLoading ? (
            <p role="status">正在读取变更…</p>
          ) : (
            detail && (
              <>
                <div className="admin-table-wrap">
                  <table className="admin-table">
                    <thead>
                      <tr>
                        <th>商品</th>
                        <th>同步前</th>
                        <th>同步后</th>
                        <th>核对事项</th>
                      </tr>
                    </thead>
                    <tbody>
                      {detail.items.length === 0 && (
                        <tr>
                          <td colSpan={4}>本次暂无商品差异。</td>
                        </tr>
                      )}
                      {detail.items.map((item) => (
                        <tr key={item.legacy_id}>
                          <td>
                            <strong className="mono">{item.code}</strong>
                            <span>{actions[item.action]}</span>
                          </td>
                          <td>
                            <ProductSummary product={item.before} />
                          </td>
                          <td>
                            <ProductSummary product={item.after} />
                            {item.images_changed && <span>图集有变更</span>}
                          </td>
                          <td>
                            {item.source_price && (
                              <span>旧站原报价数值 {item.source_price}</span>
                            )}
                            {item.notices.map((notice) => (
                              <span key={notice}>{notice}</span>
                            ))}
                            {item.source_updated_at && (
                              <span>
                                旧站更新 {date(item.source_updated_at)}
                              </span>
                            )}
                          </td>
                        </tr>
                      ))}
                    </tbody>
                  </table>
                </div>
                <div className="admin-actions">
                  <button
                    className="button-outline"
                    disabled={detailPages.length < 2 || pending}
                    onClick={() =>
                      setDetailPages((pages) => pages.slice(0, -1))
                    }
                  >
                    上一页
                  </button>
                  <button
                    className="button-outline"
                    disabled={!nextDetail || pending}
                    onClick={() =>
                      setDetailPages((pages) => [...pages, nextDetail])
                    }
                  >
                    下一页
                  </button>
                </div>
              </>
            )
          )}
        </div>
      )}
    </section>
  );
}
