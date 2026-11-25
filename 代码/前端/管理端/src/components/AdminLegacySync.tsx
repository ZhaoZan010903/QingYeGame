import { useEffect, useRef, useState } from "react";
import {
  ArrowDownToLine,
  Clock3,
  Pencil,
  Plus,
  RefreshCw,
  RotateCcw,
  X,
} from "lucide-react";
import {
  formatRentalAmount,
  rentalQuoteLabel,
  rentalUnitLabels,
} from "../../../共享/rental-quotes";
import type { RentalQuote } from "../../../共享/types";
import { ApiError, createRequestId, request } from "../lib/api";
import {
  LegacySyncConfirm,
  LegacySyncProgress,
  type LegacySyncConfirmation,
  type SyncProgress,
} from "./LegacySyncControls";

type BatchStatus =
  | "running"
  | "blocked"
  | "failed"
  | "applied"
  | "no_change"
  | "rolled_back"
  | "superseded"
  | "cancelled";
type Summary = {
  created: number;
  updated: number;
  removed: number;
  restored: number;
  review: number;
  missing: number;
};
type Batch = {
  id: string;
  status: BatchStatus;
  planned_at: string | null;
  started_at: string;
  source_updated_at: string | null;
  finished_at: string | null;
  rollback_until: string | null;
  source_count: number;
  reason: string;
  review_kind: "" | "bulk_offline";
  can_continue: boolean;
  can_cancel: boolean;
  summary: Summary;
  progress: SyncProgress;
};
type Product = {
  title: string;
  category: string;
  status: string;
  price: string | null;
  unit: string;
  quotes: RentalQuote[];
  image_count: number;
};
type Change = {
  legacy_id: string;
  code: string;
  action: "create" | "update" | "restore" | "offline" | "hold" | "missing";
  before: Product | null;
  after: Product | null;
  images_changed: boolean;
  attributes_changed: boolean;
  description_changed: boolean;
  notices: string[] | null;
  blocking: boolean;
};
type Controls = { schedule_enabled: boolean; version: number };
type BatchList = {
  batches: Batch[];
  latest_batch: Batch | null;
  can_start: boolean;
  controls: Controls;
};
type Filter = "create" | "update" | "restore" | "offline" | "issues";
type Selection = { id: string; filter: Filter; pages: (string | null)[] };

const states: Record<BatchStatus, string> = {
  running: "同步中",
  blocked: "校验中止",
  failed: "同步失败",
  applied: "同步完成",
  no_change: "无变化",
  rolled_back: "已回滚",
  superseded: "已归档",
  cancelled: "已取消",
};
const productStates: Record<string, string> = {
  published: "已上架",
  offline: "已下架",
  sold: "已售出",
  rented: "出租中",
  draft: "草稿",
};
const groups = [
  { key: "created", action: "create", label: "新增", icon: Plus },
  { key: "updated", action: "update", label: "更新", icon: Pencil },
  { key: "restored", action: "restore", label: "恢复上架", icon: RotateCcw },
  { key: "removed", action: "offline", label: "下架", icon: ArrowDownToLine },
] as const;
const filterNames: Record<Filter, string> = {
  create: "新增商品",
  update: "更新商品",
  restore: "恢复上架",
  offline: "下架商品",
  issues: "商品提示",
};
const dateFormat = new Intl.DateTimeFormat("zh-CN", {
  timeZone: "Asia/Shanghai",
  dateStyle: "short",
  timeStyle: "medium",
});
const date = (value?: string | null) =>
  value ? dateFormat.format(new Date(value)) : "—";
const batchesURL = "/admin/legacy-sync/batches";
const batchURL = (id: string) => batchesURL + "/" + encodeURIComponent(id);
const query = (cursor: string | null, filter?: Filter) =>
  "?" +
  new URLSearchParams({
    limit: "15",
    ...(cursor ? { cursor } : {}),
    ...(filter ? { filter } : {}),
  });
const changed = (batch: Batch) =>
  groups.some(({ key }) => batch.summary[key] > 0);
const applied = (batch: Batch) =>
  batch.status === "applied" ||
  batch.status === "no_change" ||
  batch.status === "rolled_back";
const stopped = (batch: Batch) =>
  batch.status === "blocked" || batch.status === "failed";
const awaitingReview = (batch: Batch) =>
  batch.status === "blocked" &&
  batch.review_kind === "bulk_offline" &&
  batch.can_continue;
const batchLabel = (batch: Batch) =>
  awaitingReview(batch) ? "待确认批量下架" : states[batch.status];
const issueLabel = (batch: Batch) =>
  batch.review_kind === "bulk_offline" ? "拟下架清单" : "商品提示";

type BatchOperation = "start" | "schedule" | "rollback" | "continue" | "cancel";
export function BatchReviewActions({
  batch,
  canStart,
  pending,
  expanded,
  showItems,
  review,
}: {
  batch: Batch;
  canStart: boolean;
  pending: BatchOperation | null;
  expanded: boolean;
  showItems: () => void;
  review: (action: "continue" | "cancel") => void;
}) {
  const canContinue = canStart && awaitingReview(batch);
  if (!batch.summary.review && !canContinue && !batch.can_cancel) return null;
  return (
    <div className="legacy-sync-review-actions">
      {batch.summary.review > 0 && (
        <button
          type="button"
          className="button-outline legacy-sync-issues"
          aria-expanded={expanded}
          onClick={showItems}
        >
          {issueLabel(batch)}（{batch.summary.review}）
        </button>
      )}
      {canContinue && (
        <button
          type="button"
          className="button-white"
          disabled={pending !== null}
          onClick={() => review("continue")}
        >
          {pending === "continue" ? "正在提交…" : "确认并继续同步"}
        </button>
      )}
      {batch.can_cancel && (
        <button
          type="button"
          className="button-outline"
          disabled={pending !== null}
          onClick={() => review("cancel")}
        >
          {pending === "cancel" ? "正在取消…" : "取消本批"}
        </button>
      )}
    </div>
  );
}

function price(product: Product) {
  if (product.quotes?.length) {
    return product.quotes
      .map((quote) =>
        [
          rentalQuoteLabel(quote),
          "¥" + formatRentalAmount(quote.amount),
          quote.unit === "period" && quote.billing_unit
            ? rentalUnitLabels[quote.billing_unit]
            : "",
        ]
          .filter(Boolean)
          .join(" "),
      )
      .join(" · ");
  }
  if (product.price == null) return "联系客服";
  const unit = rentalUnitLabels[product.unit as keyof typeof rentalUnitLabels];
  return "¥" + formatRentalAmount(product.price) + (unit ? " / " + unit : "");
}

function ChangeRow({
  item,
  bulkReview,
}: {
  item: Change;
  bulkReview: boolean;
}) {
  const { before, after } = item;
  const fields =
    before && after
      ? [
          {
            label: "状态",
            before: productStates[before.status] ?? before.status,
            after: productStates[after.status] ?? after.status,
          },
          { label: "标题", before: before.title, after: after.title },
          { label: "报价", before: price(before), after: price(after) },
        ].filter((field) => field.before !== field.after)
      : [];
  return (
    <li className="legacy-sync-change">
      <div>
        <b className="legacy-sync-numbers">{item.code || item.legacy_id}</b>
        {item.blocking && (
          <span className="legacy-sync-status" data-status="blocked">
            {bulkReview ? "拟下架" : "需处理"}
          </span>
        )}
      </div>
      {!before && after && (
        <p>
          {after.title} · {price(after)} · {after.image_count} 张图
        </p>
      )}
      {fields.map((field) => (
        <div className="legacy-sync-field" key={field.label}>
          <span>{field.label}</span>
          <del>{field.before}</del>
          <span aria-label="变更为">→</span>
          <b>{field.after}</b>
        </div>
      ))}
      {before && after && item.images_changed && (
        <p>
          图集{" "}
          {before.image_count === after.image_count
            ? "已更换 · " + after.image_count + " 张"
            : before.image_count + " 张 → " + after.image_count + " 张"}
        </p>
      )}
      {item.attributes_changed && <p>商品属性已更新</p>}
      {item.description_changed && <p>商品说明已更新</p>}
      {item.notices?.map((notice) => (
        <p className="legacy-sync-note" key={notice}>
          {notice}
        </p>
      ))}
    </li>
  );
}

function Pagination({
  page,
  next,
  disabled,
  turn,
}: {
  page: number;
  next: string | null;
  disabled: boolean;
  turn: (next: boolean) => void;
}) {
  if (page === 1 && !next) return null;
  return (
    <div className="legacy-sync-pagination" aria-label="分页">
      <button
        className="button-outline"
        disabled={disabled || page === 1}
        onClick={() => turn(false)}
      >
        上一页
      </button>
      <span className="legacy-sync-numbers">第 {page} 页</span>
      <button
        className="button-outline"
        disabled={disabled || !next}
        onClick={() => turn(true)}
      >
        下一页
      </button>
    </div>
  );
}

export function AdminLegacySync() {
  const [data, setData] = useState<BatchList | null>(null);
  const [batchPages, setBatchPages] = useState<(string | null)[]>([null]);
  const [nextBatch, setNextBatch] = useState<string | null>(null);
  const [selection, setSelection] = useState<Selection | null>(null);
  const [detail, setDetail] = useState<{
    batch: Batch;
    items: Change[];
    next: string | null;
  } | null>(null);
  const [detailError, setDetailError] = useState("");
  const [listError, setListError] = useState("");
  const [revision, setRevision] = useState(0);
  const [pending, setPending] = useState<BatchOperation | null>(null);
  const [confirmation, setConfirmation] = useState<LegacySyncConfirmation>();
  const [feedback, setFeedback] = useState<{ error: boolean; text: string }>();
  const work = useRef(false);
  const launchRequest = useRef<{ key: string; id: string } | null>(null);
  const detailHeading = useRef<HTMLHeadingElement>(null);
  const latest = data?.latest_batch ?? null;
  const running = latest?.status === "running";
  const batchCursor = batchPages.at(-1) ?? null;
  const detailCursor = selection?.pages.at(-1) ?? null;
  const selectionID = selection?.id;
  const selectionFilter = selection?.filter;
  const detailStatus = latest?.id === selectionID ? latest?.status : undefined;

  useEffect(() => {
    const abort = new AbortController();
    let timer: number;
    const refresh = async () => {
      let interval = 6_000;
      try {
        const response = await request(
          batchesURL + query(batchCursor),
          "GET",
          undefined,
          abort.signal,
        );
        if (abort.signal.aborted) return;
        const value: BatchList = response.data;
        setData(value);
        setNextBatch(response.next_cursor ?? null);
        setListError("");
        if (
          launchRequest.current &&
          value.latest_batch?.id === launchRequest.current.id
        ) {
          launchRequest.current = null;
          setFeedback(undefined);
        }
        if (value.latest_batch?.status === "running") interval = 1_600;
      } catch (error) {
        if (abort.signal.aborted) return;
        setListError(
          error instanceof Error ? error.message : "批次记录暂不可用",
        );
        interval = 2_500;
      } finally {
        if (!abort.signal.aborted) timer = window.setTimeout(refresh, interval);
      }
    };
    void refresh();
    return () => {
      abort.abort();
      window.clearTimeout(timer);
    };
  }, [batchCursor, revision]);

  useEffect(() => {
    if (!selectionID || !selectionFilter) return;
    const abort = new AbortController();
    setDetail(null);
    setDetailError("");
    request(
      batchURL(selectionID) + query(detailCursor, selectionFilter),
      "GET",
      undefined,
      abort.signal,
    )
      .then((response) => {
        if (!abort.signal.aborted)
          setDetail({ ...response.data, next: response.next_cursor ?? null });
      })
      .catch((error) => {
        if (!abort.signal.aborted)
          setDetailError(
            error instanceof Error ? error.message : "商品变更暂不可用",
          );
      });
    return () => abort.abort();
  }, [selectionID, selectionFilter, detailCursor, detailStatus, revision]);

  useEffect(() => {
    if (!selectionID) return;
    const heading = detailHeading.current;
    heading?.focus({ preventScroll: true });
    heading?.scrollIntoView({ block: "nearest" });
  }, [selectionID, selectionFilter]);

  async function mutate(
    kind: NonNullable<typeof pending>,
    task: () => Promise<void>,
  ) {
    if (work.current) return;
    work.current = true;
    setPending(kind);
    setFeedback(undefined);
    try {
      await task();
    } catch (error) {
      if (kind === "start" || kind === "continue") {
        if (
          error instanceof ApiError &&
          error.status >= 400 &&
          error.status < 500
        ) {
          launchRequest.current = null;
        } else {
          setFeedback({
            error: true,
            text: "暂未收到启动结果，正在刷新同步记录。若状态未更新，可再次执行同一操作。",
          });
          return;
        }
      }
      setFeedback({
        error: true,
        text: error instanceof Error ? error.message : "操作未完成，请稍后重试",
      });
    } finally {
      work.current = false;
      setPending(null);
      setRevision((value) => value + 1);
    }
  }

  const start = (batch?: Batch) =>
    mutate(batch ? "continue" : "start", async () => {
      const path = batch ? batchURL(batch.id) + "/continue" : batchesURL;
      if (launchRequest.current?.key !== path) {
        launchRequest.current = { key: path, id: createRequestId() };
      }
      const response = await request(path, "POST", {
        request_id: launchRequest.current.id,
        ...(batch ? { confirm: true } : {}),
      });
      launchRequest.current = null;
      setSelection(null);
      setBatchPages([null]);
      setData(
        (previous) => previous && { ...previous, latest_batch: response.data },
      );
    });

  function review(batch: Batch, action: "continue" | "cancel") {
    const count = batch.summary.review;
    if (action === "continue") {
      setConfirmation({
        title: `确认继续同步这 ${count} 件拟下架商品？`,
        message: `系统会重新核对 ${date(batch.started_at)} 这批的旧站目录和拟下架清单。核对通过后，这 ${count} 件商品将与本次其他变更一起生效。`,
        confirmLabel: "确认清单并继续",
        dismissLabel: "返回核对",
        confirm: () => void start(batch),
      });
      return;
    }
    setConfirmation({
      title: "取消这一批同步？",
      message: `取消 ${date(batch.started_at)} 的未生效批次后，商品保持现状。历史记录保留，可另行发起同步。`,
      confirmLabel: "确认取消本批",
      dismissLabel: "保留本批",
      confirm: () =>
        void mutate("cancel", async () => {
          const response = await request(
            batchURL(batch.id) + "/cancel",
            "POST",
            { confirm: true },
          );
          const cancelled: Batch = response.data;
          setData(
            (previous) =>
              previous && {
                ...previous,
                latest_batch:
                  previous.latest_batch?.id === cancelled.id
                    ? cancelled
                    : previous.latest_batch,
                batches: previous.batches.map((row) =>
                  row.id === cancelled.id ? cancelled : row,
                ),
              },
          );
          setFeedback({ error: false, text: "已取消本批，商品保持现状。" });
        }),
    });
  }

  function choose(batch: Batch, filter: Filter) {
    setSelection((previous) =>
      previous?.id === batch.id && previous.filter === filter
        ? null
        : { id: batch.id, filter, pages: [null] },
    );
  }

  function changeSchedule(controls: Controls) {
    const enabled = !controls.schedule_enabled;
    const save = () =>
      void mutate("schedule", async () => {
        await request("/admin/legacy-sync/schedule", "PUT", {
          enabled,
          expected_version: controls.version,
        });
        setFeedback({
          error: false,
          text: enabled ? "已允许服务器定时执行。" : "已暂停定时同步。",
        });
      });
    if (enabled) {
      setConfirmation({
        title: "恢复定时同步",
        message:
          "服务器定时器触发后，系统会自动同步旧站商品。通常在北京时间每天 12:00 执行。",
        confirmLabel: "恢复定时同步",
        confirm: save,
      });
    } else {
      save();
    }
  }

  function rollback(batch: Batch) {
    setConfirmation({
      title: "回滚这一批同步？",
      message:
        "系统会核对商品和图片是否有后续修改。核对通过后整批恢复，并暂停定时同步。",
      confirmLabel: "确认回滚",
      confirm: () =>
        void mutate("rollback", async () => {
          await request(batchURL(batch.id) + "/rollback", "POST", {
            confirm: true,
          });
          setFeedback({ error: false, text: "已整批回滚，定时同步已暂停。" });
        }),
    });
  }

  return (
    <section className="admin-workspace legacy-sync-page">
      <header className="admin-heading">
        <div>
          <h1>旧站商品同步</h1>
          <p>根据旧站目录核对商品，新上架和下架的商品随同一批次生效。</p>
        </div>
        {(!latest || !awaitingReview(latest) || !data?.can_start) && (
          <button
            type="button"
            className="button-white legacy-sync-start"
            disabled={!data?.can_start || running || pending !== null}
            onClick={() => void start()}
          >
            <RefreshCw size={16} aria-hidden="true" />
            {running
              ? "正在同步"
              : pending === "start"
                ? "正在启动"
                : latest && stopped(latest)
                  ? "重新同步"
                  : "立即同步"}
          </button>
        )}
      </header>

      {confirmation && (
        <LegacySyncConfirm
          confirmation={confirmation}
          onClose={() => setConfirmation(undefined)}
        />
      )}
      {feedback && (
        <p
          className={feedback.error ? "form-error" : "legacy-sync-note"}
          role="status"
        >
          {feedback.text}
        </p>
      )}
      {listError && (
        <p className="form-error" role="status">
          {data ? "进度暂未刷新，正在重连。" : listError + " 正在重试。"}
        </p>
      )}

      <section className="legacy-sync-panel fluent-card" aria-label="同步状态">
        <div className="legacy-sync-status-head">
          <div>
            <span>{running ? "当前任务" : "最近一次同步"}</span>
            <h2>
              {latest
                ? latest.status === "applied" &&
                  !changed(latest) &&
                  !latest.summary.review
                  ? "商品已是最新"
                  : batchLabel(latest)
                : data
                  ? "暂无同步记录"
                  : "正在读取状态"}
            </h2>
          </div>
          {latest && (
            <div className="legacy-sync-run-info">
              <span>{latest.planned_at ? "定时触发" : "手动触发"}</span>
              <time dateTime={latest.finished_at ?? latest.started_at}>
                {date(latest.finished_at ?? latest.started_at)}
              </time>
            </div>
          )}
        </div>
        {latest &&
          (running ||
            stopped(latest) ||
            latest.status === "applied" ||
            latest.status === "no_change") && (
            <LegacySyncProgress
              progress={latest.progress}
              running={running}
              failed={stopped(latest)}
            />
          )}
        {latest?.reason && (
          <p
            className={
              stopped(latest) ? "legacy-sync-stop" : "legacy-sync-note"
            }
            role={stopped(latest) ? "status" : undefined}
          >
            {latest.reason}
            {stopped(latest) && "。本批商品变更未生效。"}
          </p>
        )}
        {latest && applied(latest) && (
          <div className="legacy-sync-totals">
            {groups.map(({ key, action, label, icon: Icon }) => (
              <button
                type="button"
                key={key}
                disabled={!latest.summary[key]}
                aria-pressed={
                  selectionID === latest.id && selectionFilter === action
                }
                onClick={() => choose(latest, action)}
              >
                <span>
                  <Icon size={15} aria-hidden="true" />
                  {label}
                </span>
                <b className="legacy-sync-numbers">
                  {latest.summary[key] ?? 0}
                </b>
              </button>
            ))}
          </div>
        )}
        {latest && applied(latest) && latest.source_count > 0 && (
          <p className="legacy-sync-note">
            读取旧站 {latest.source_count} 件商品
            {latest.source_updated_at &&
              " · 旧站最新更新 " + date(latest.source_updated_at)}
          </p>
        )}
        {latest && (
          <BatchReviewActions
            batch={latest}
            canStart={Boolean(data?.can_start)}
            pending={pending}
            expanded={selectionID === latest.id && selectionFilter === "issues"}
            showItems={() => choose(latest, "issues")}
            review={(action) => review(latest, action)}
          />
        )}
        {latest?.status === "blocked" &&
          latest.review_kind === "bulk_offline" &&
          !latest.can_continue && (
            <p className="legacy-sync-note">
              这份清单已无法继续使用。重新同步可按当前旧站目录生成新的结果。
            </p>
          )}
        {data && !data.can_start && (
          <p className="legacy-sync-note">当前环境未启用商品同步服务。</p>
        )}
      </section>

      {selection && (
        <section
          className="legacy-sync-panel legacy-sync-detail fluent-card"
          aria-labelledby="legacy-sync-detail-heading"
        >
          <div className="legacy-sync-status-head">
            <div>
              <h2
                id="legacy-sync-detail-heading"
                ref={detailHeading}
                tabIndex={-1}
              >
                {selection.filter === "issues" && detail
                  ? issueLabel(detail.batch)
                  : filterNames[selection.filter]}
              </h2>
              {detail && (
                <span>
                  {date(detail.batch.started_at)} · {batchLabel(detail.batch)}
                </span>
              )}
            </div>
            <button
              type="button"
              className="button-outline legacy-sync-close"
              aria-label="收起商品变更"
              onClick={() => setSelection(null)}
            >
              <X size={17} aria-hidden="true" />
            </button>
          </div>
          {detailError ? (
            <p className="form-error">{detailError}</p>
          ) : !detail ? (
            <p className="legacy-sync-note" role="status">
              正在读取商品变更…
            </p>
          ) : detail.items.length > 0 ? (
            <ul className="legacy-sync-changes">
              {detail.items.map((item) => (
                <ChangeRow
                  item={item}
                  bulkReview={detail.batch.review_kind === "bulk_offline"}
                  key={item.legacy_id}
                />
              ))}
            </ul>
          ) : (
            <p className="legacy-sync-note">这一批没有此类变更。</p>
          )}
          <Pagination
            page={selection.pages.length}
            next={detail?.next ?? null}
            disabled={!detail}
            turn={(forward) =>
              setSelection({
                ...selection,
                pages:
                  forward && detail?.next
                    ? [...selection.pages, detail.next]
                    : selection.pages.slice(0, -1),
              })
            }
          />
        </section>
      )}

      {data && (
        <section
          className="legacy-sync-panel legacy-sync-schedule fluent-card"
          aria-label="定时同步"
        >
          <Clock3 size={19} aria-hidden="true" />
          <div>
            <h2>定时同步</h2>
            <p>服务器定时器 · 每天 12:00（北京时间）</p>
          </div>
          <span
            className="legacy-sync-status"
            data-status={
              data.controls.schedule_enabled ? "applied" : "superseded"
            }
          >
            {data.controls.schedule_enabled ? "已允许执行" : "已暂停"}
          </span>
          <button
            type="button"
            className="button-outline"
            disabled={pending !== null || !data.can_start}
            onClick={() => changeSchedule(data.controls)}
          >
            {data.controls.schedule_enabled ? "暂停定时" : "恢复定时"}
          </button>
        </section>
      )}

      <section
        className="legacy-sync-panel fluent-card"
        aria-labelledby="legacy-sync-history"
      >
        <div className="legacy-sync-status-head">
          <h2 id="legacy-sync-history">同步记录</h2>
          <span>北京时间</span>
        </div>
        {data?.batches.length ? (
          <ul className="legacy-sync-history">
            {data.batches.map((batch) => (
              <li key={batch.id}>
                <div className="legacy-sync-record">
                  <b>
                    <time dateTime={batch.started_at}>
                      {date(batch.started_at)}
                    </time>
                  </b>
                  <span>
                    {batch.planned_at ? "定时" : "手动"}
                    {batch.source_count > 0 &&
                      " · 旧站 " + batch.source_count + " 件"}
                  </span>
                </div>
                <span className="legacy-sync-status" data-status={batch.status}>
                  {batchLabel(batch)}
                </span>
                <div className="legacy-sync-history-actions">
                  {applied(batch) &&
                    groups
                      .filter(({ key }) => batch.summary[key] > 0)
                      .map(({ key, action, label }) => (
                        <button
                          type="button"
                          className="button-outline"
                          key={key}
                          aria-pressed={
                            selectionID === batch.id &&
                            selectionFilter === action
                          }
                          onClick={() => choose(batch, action)}
                        >
                          {label} {batch.summary[key]}
                        </button>
                      ))}
                  {batch.summary.review > 0 && (
                    <button
                      type="button"
                      className="button-outline"
                      aria-pressed={
                        selectionID === batch.id && selectionFilter === "issues"
                      }
                      onClick={() => choose(batch, "issues")}
                    >
                      {issueLabel(batch)} {batch.summary.review}
                    </button>
                  )}
                  {batch.id !== latest?.id && batch.can_cancel && (
                    <button
                      type="button"
                      className="button-outline"
                      disabled={pending !== null}
                      onClick={() => review(batch, "cancel")}
                    >
                      取消本批
                    </button>
                  )}
                  {batch.status === "applied" &&
                    changed(batch) &&
                    batch.rollback_until &&
                    Date.parse(batch.rollback_until) > Date.now() && (
                      <button
                        type="button"
                        className="button-outline"
                        disabled={running || pending !== null}
                        title={"可回滚至 " + date(batch.rollback_until)}
                        onClick={() => rollback(batch)}
                      >
                        回滚
                      </button>
                    )}
                </div>
                {batch.reason && (
                  <p className="legacy-sync-note">{batch.reason}</p>
                )}
              </li>
            ))}
          </ul>
        ) : (
          <p className="legacy-sync-note">
            {data ? "暂无同步记录。" : "正在读取同步记录…"}
          </p>
        )}
        <Pagination
          page={batchPages.length}
          next={nextBatch}
          disabled={!data}
          turn={(forward) =>
            setBatchPages(
              forward && nextBatch
                ? [...batchPages, nextBatch]
                : batchPages.slice(0, -1),
            )
          }
        />
      </section>
    </section>
  );
}
