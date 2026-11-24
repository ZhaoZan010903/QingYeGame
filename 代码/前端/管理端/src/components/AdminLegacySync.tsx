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
  "running" | "blocked" | "failed" | "applied" | "rolled_back" | "superseded";
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
  notices: string[];
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
  rolled_back: "已回滚",
  superseded: "已归档",
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
  batch.status === "applied" || batch.status === "rolled_back";
const stopped = (batch: Batch) =>
  batch.status === "blocked" || batch.status === "failed";

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

function ChangeRow({ item }: { item: Change }) {
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
            需处理
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
      {item.notices.map((notice) => (
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
  const [pending, setPending] = useState<
    "start" | "schedule" | "rollback" | null
  >(null);
  const [confirmation, setConfirmation] = useState<LegacySyncConfirmation>();
  const [feedback, setFeedback] = useState<{ error: boolean; text: string }>();
  const work = useRef(false);
  const requestID = useRef("");
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
        if (value.latest_batch?.id === requestID.current) {
          requestID.current = "";
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
      if (kind === "start") {
        if (
          error instanceof ApiError &&
          error.status >= 400 &&
          error.status < 500
        ) {
          requestID.current = "";
        } else {
          setFeedback({
            error: true,
            text: "暂未收到本次启动结果。可再次点击，系统会核对同一条请求。",
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

  const start = () =>
    mutate("start", async () => {
      requestID.current ||= createRequestId();
      const response = await request(batchesURL, "POST", {
        request_id: requestID.current,
      });
      requestID.current = "";
      setSelection(null);
      setBatchPages([null]);
      setData(
        (previous) => previous && { ...previous, latest_batch: response.data },
      );
    });

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
        <button
          type="button"
          className="button-white legacy-sync-start"
          disabled={!data?.can_start || running || pending !== null}
          onClick={() => void start()}
        >
          <RefreshCw
            size={16}
            aria-hidden="true"
            className={running || pending === "start" ? "legacy-sync-spin" : ""}
          />
          {running ? "正在同步" : pending === "start" ? "正在启动" : "立即同步"}
        </button>
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

      <section className="legacy-sync-panel" aria-label="同步状态">
        <div className="legacy-sync-status-head">
          <div>
            <span>{running ? "当前任务" : "最近一次同步"}</span>
            <h2>
              {latest
                ? applied(latest) &&
                  latest.status === "applied" &&
                  !changed(latest)
                  ? "商品已是最新"
                  : states[latest.status]
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
          (running || stopped(latest) || latest.status === "applied") && (
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
        {latest && latest.summary.review > 0 && (
          <button
            type="button"
            className="button-outline legacy-sync-issues"
            aria-pressed={
              selectionID === latest.id && selectionFilter === "issues"
            }
            onClick={() => choose(latest, "issues")}
          >
            {latest.summary.review} 条商品提示
          </button>
        )}
        {data && !data.can_start && (
          <p className="legacy-sync-note">当前环境未启用商品同步服务。</p>
        )}
      </section>

      {selection && (
        <section
          className="legacy-sync-panel legacy-sync-detail"
          aria-labelledby="legacy-sync-detail-heading"
        >
          <div className="legacy-sync-status-head">
            <div>
              <h2
                id="legacy-sync-detail-heading"
                ref={detailHeading}
                tabIndex={-1}
              >
                {filterNames[selection.filter]}
              </h2>
              {detail && (
                <span>
                  {date(detail.batch.started_at)} ·{" "}
                  {states[detail.batch.status]}
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
                <ChangeRow item={item} key={item.legacy_id} />
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
          className="legacy-sync-panel legacy-sync-schedule"
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
        className="legacy-sync-panel"
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
                  {states[batch.status]}
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
                      提示 {batch.summary.review}
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
