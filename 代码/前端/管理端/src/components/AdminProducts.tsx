import { ProtectedImage } from "../../../共享/MediaSecurity";
import { useEffect, useRef, useState, type FormEvent } from "react";
import { publicationInterrupted } from "../../../共享/upload-progress";
import { Link, useSearchParams } from "react-router-dom";
import {
  Plus,
  Search,
  SlidersHorizontal,
  RefreshCw,
  ChevronLeft,
  ChevronRight,
  ImageOff,
  X,
  Trash2,
  RotateCcw,
} from "lucide-react";
import { adminApi } from "../lib/admin-api";
import { useUnsavedChanges, publicSite } from "../lib/admin-ux";
import { adminProductPath } from "../lib/product-links";
import {
  pageItems,
  productTime,
  readProductListQuery,
  sources,
} from "../lib/product-list";
import type { Product, ProductPage } from "../types";
import { Price, StateView } from "./shared";
import { AdminProductEditor, statusNames } from "./AdminProductEditor";
import { GuideButton } from "./AdminGuide";
import { AdminTrashPurge, type PurgeTarget } from "./AdminTrashPurge";
import { AdminProductRecommendation } from "./AdminProductRecommendation";
import "../products.css";

type Action = "publish" | "offline" | "trash" | "restore";
const actionNames: Record<Action, string> = {
  publish: "确认上架",
  offline: "下架商品",
  trash: "移入回收站",
  restore: "恢复为草稿",
};
const actionNotes: Record<Action, string> = {
  publish:
    "系统会检查资料、图片和审核状态。上架成功后，用户可以在集市中看到该商品。",
  offline:
    "下架后不再公开展示。新商品图片在OSS保留5天，迁回暂存成功后再保留5天；到期清理，请及时处理。",
  trash:
    "商品会从正常列表和用户网站中移除。资料、图片与历史记录保留，可以在回收站恢复。",
  restore:
    "恢复后进入草稿，不会自动上架。请检查资料，会员商品仍需按原流程审核。",
};

export function ProductListRow({
  product: p,
  trash,
  review,
  disabled,
  returnTo,
  edit,
  act,
  purge,
  recommend,
}: {
  product: Product;
  trash: boolean;
  review: boolean;
  disabled: boolean;
  returnTo: string;
  edit: () => void;
  act: (kind: Action) => void;
  purge: () => void;
  recommend?: () => void;
}) {
  const cover = p.images[0];
  const [failed, setFailed] = useState(false);
  useEffect(() => setFailed(false), [cover?.thumbnailUrl, cover?.url]);
  return (
    <tr>
      <td className="product-code-cell" data-label="商品编号">
        <span className="product-code mono">{p.code || p.id}</span>
      </td>
      <td className="product-cell" data-label="商品">
        <div className="product-summary">
          <div className="product-cover">
            {cover && !failed && (cover.thumbnailUrl || cover.url) ? (
              <ProtectedImage
                src={cover.thumbnailUrl || cover.url}
                productId={p.id}
                productVersion={p.version}
                alt=""
                loading="lazy"
                onError={() => setFailed(true)}
              />
            ) : (
              <ImageOff size={22} aria-label="暂无封面" />
            )}
          </div>
          <div className="product-summary-text">
            {trash ? (
              <strong className="product-list-title" title={p.title}>
                {p.title.trim() || "未填写标题"}
              </strong>
            ) : (
              <Link
                className="product-list-title"
                to={adminProductPath(p.id)}
                state={{ returnTo }}
                title={p.title}
              >
                {p.title.trim() || "未填写标题"}
              </Link>
            )}
            <div className="product-attributes">
              <span>{p.mode === "sale" ? "账号出售" : "账号租赁"}</span>
              <span>{p.server || "区服未填写"}</span>
              {p.attributeLabels?.gender && (
                <span>{p.attributeLabels.gender}</span>
              )}
            </div>
          </div>
        </div>
      </td>
      <td data-label="报价">
        <Price product={p} />
        {!!p.rentalQuotes?.length && (
          <small className="product-cell-note">
            含 {p.rentalQuotes.length} 档租期报价
          </small>
        )}
      </td>
      <td data-label="来源 / 状态">
        <div className="product-status">
          <strong
            className={`status-pill status-${trash ? "deleted" : p.status}`}
          >
            {trash
              ? "已删除"
              : publicationInterrupted(p.status, p.publicationInterrupted)
                ? "上架已中止"
                : statusNames[p.status]}
          </strong>
          <small>{sources[p.source || ""] || "来源未记录"}</small>
        </div>
      </td>
      <td className="product-count-cell" data-label="图片 / 咨询">
        <div className="product-counts">
          <span>
            图片 <b className="mono">{p.imageTotal}</b>
          </span>
          <span>
            咨询 <b className="mono">{p.consultationCount ?? 0}</b> 人
          </span>
        </div>
      </td>
      <td className="product-time-cell" data-label="时间记录">
        <dl className="product-times">
          <div>
            <dt>首次上架</dt>
            <dd className="mono">{productTime(p.publishedAt, "尚未上架")}</dd>
          </div>
          <div>
            <dt>最近更新</dt>
            <dd className="mono">{productTime(p.updatedAt)}</dd>
          </div>
          <div>
            <dt>{trash ? "删除时间" : "创建时间"}</dt>
            <dd className="mono">
              {productTime(trash ? p.deletedAt : p.createdAt)}
            </dd>
          </div>
        </dl>
      </td>
      <td data-label="操作">
        <div className="product-row-actions">
          {p.status === "rejected_final" ? (
            <>
              {!trash && (
                <Link to={adminProductPath(p.id)} state={{ returnTo }}>
                  查看审核记录
                </Link>
              )}
              <span className="subtle">最终驳回，不可编辑或恢复</span>
            </>
          ) : trash ? (
            <button
              className="button-outline"
              disabled={disabled}
              onClick={() => act("restore")}
            >
              <RotateCcw size={14} />
              恢复草稿
            </button>
          ) : (
            <button
              className="button-outline"
              disabled={disabled}
              onClick={edit}
            >
              {p.status === "pending" ? "查看并审核" : "编辑"}
            </button>
          )}
          {!trash && (
            <details className="product-more-actions">
              <summary>更多操作</summary>
              <div>
                {!review && ["published", "publishing"].includes(p.status) && (
                  <button disabled={disabled} onClick={() => act("offline")}>
                    {p.status === "publishing" ? "取消上架" : "下架"}
                  </button>
                )}
                {!review &&
                  (["offline", "sold", "rented"].includes(p.status) ||
                    (p.source !== "member" &&
                      ["draft", "rejected"].includes(p.status))) && (
                    <button disabled={disabled} onClick={() => act("publish")}>
                      上架
                    </button>
                  )}
                {p.isPubliclyVisible && publicSite && (
                  <a
                    href={`${publicSite}/accounts/${encodeURIComponent(p.id)}`}
                    target="_blank"
                    rel="noreferrer"
                  >
                    公开页 ↗
                  </a>
                )}
                {recommend && (
                  <button type="button" disabled={disabled} onClick={recommend}>
                    推荐加分{" "}
                    <b className="mono">{p.recommendationBoost ?? 0}</b>
                  </button>
                )}
                <button
                  className="product-delete"
                  disabled={disabled}
                  onClick={() => act("trash")}
                >
                  移入回收站
                </button>
              </div>
            </details>
          )}
          {trash && (
            <button
              className="product-delete product-purge"
              disabled={disabled}
              onClick={purge}
            >
              永久删除
            </button>
          )}
        </div>
      </td>
    </tr>
  );
}

export function AdminProducts({ review = false }: { review?: boolean }) {
  const [params, setParams] = useSearchParams();
  const {
    page: pageNumber,
    size,
    query: requestParams,
    trash,
  } = readProductListQuery(params, review);
  const requestKey = requestParams.toString();
  const [result, setResult] = useState<{
    query: string;
    revision: number;
    page?: ProductPage;
    error?: unknown;
  } | null>(null);
  const [q, setQ] = useState(params.get("q") || "");
  const [filtersOpen, setFiltersOpen] = useState(false);
  const advancedCount = [
    params.get("source"),
    params.get("trade_type"),
    params.get("sort") && params.get("sort") !== "updated",
  ].filter(Boolean).length;
  const [revision, setRevision] = useState(0);
  const current =
    result?.query === requestKey && result.revision === revision
      ? result
      : null;
  const data = current?.page;
  const loading = !current;
  const error = current?.error;
  const [notice, setNotice] = useState("");
  const [action, setAction] = useState<{
    product: Product;
    kind: Action;
  } | null>(null);
  const [reason, setReason] = useState("");
  const [rentalCategory, setRentalCategory] = useState("");
  const [actionError, setActionError] = useState("");
  const [busy, setBusy] = useState(false);
  const [purgeTarget, setPurgeTarget] = useState<PurgeTarget | null>(null);
  const [recommendationProduct, setRecommendationProduct] =
    useState<Product | null>(null);
  const dialog = useRef<HTMLDialogElement>(null);
  const jump = useRef<HTMLInputElement>(null);
  useUnsavedChanges(false, busy);
  const selected =
    params.get("edit") || (params.get("new") === "1" ? "new" : null);
  const cancelPublication =
    action?.kind === "offline" && action.product.status === "publishing";
  const actionLabel =
    action && (cancelPublication ? "取消上架" : actionNames[action.kind]);
  const totalPages = Math.max(1, Math.ceil((data?.total ?? 0) / size));
  const status = requestParams.get("status") || "";
  const publicOnly = requestParams.get("visibility") === "public";
  useEffect(() => setQ(params.get("q") || ""), [params.get("q")]);
  useEffect(() => {
    setPurgeTarget(null);
    setAction(null);
    setRecommendationProduct(null);
  }, [requestKey, selected]);
  useEffect(() => {
    const controller = new AbortController();
    void adminApi
      .products(new URLSearchParams(requestKey), controller.signal)
      .then((page) => {
        if (controller.signal.aborted) return;
        const last = Math.max(1, Math.ceil(page.total / size));
        if (pageNumber > last) {
          setParams(
            (previous) => {
              const next = new URLSearchParams(previous);
              next.set("page", String(last));
              return next;
            },
            { replace: true },
          );
          return;
        }
        setResult({ query: requestKey, revision, page });
      })
      .catch((e) => {
        if (!controller.signal.aborted)
          setResult({ query: requestKey, revision, error: e });
      });
    return () => {
      controller.abort();
    };
  }, [requestKey, revision]);
  useEffect(() => {
    if (action) {
      setReason("");
      setRentalCategory(action.product.rentalCategory ?? "");
      setActionError("");
      dialog.current?.showModal();
    } else dialog.current?.close();
  }, [action]);
  function update(values: Record<string, string>, reset = true) {
    const next = new URLSearchParams(params);
    if (reset) next.delete("page");
    for (const [key, value] of Object.entries(values))
      value ? next.set(key, value) : next.delete(key);
    setParams(next);
    setNotice("");
  }
  function select(id: string | null) {
    update(
      { edit: id && id !== "new" ? id : "", new: id === "new" ? "1" : "" },
      false,
    );
  }
  function go(page: number) {
    update({ page: String(Math.min(totalPages, Math.max(1, page))) }, false);
  }
  function switchScope(scope: string) {
    update({
      status: scope === "public" || scope === "trash" ? "" : scope,
      visibility: scope === "public" ? "public" : "",
      trash: scope === "trash" ? "1" : "",
    });
  }
  async function perform(event: FormEvent) {
    event.preventDefault();
    if (!action || busy) return;
    setBusy(true);
    setActionError("");
    const { product: p, kind } = action;
    try {
      if (kind === "publish" && p.mode === "rent" && !rentalCategory)
        throw new Error("请先选择租赁分类：时日或周月。");
      // Use the version the operator saw; never silently overwrite a concurrent edit.
      if (kind === "trash")
        await adminApi.trashProduct(p.id, p.version, reason.trim());
      else if (kind === "restore")
        await adminApi.restoreProduct(p.id, p.version);
      else
        await adminApi.action(p.id, kind === "publish" ? "publish" : "status", {
          expected_version: p.version,
          ...(kind === "publish" && p.mode === "rent"
            ? { rental_category: rentalCategory }
            : {}),
          ...(kind === "offline"
            ? { action: "offline", reason: reason.trim() }
            : {}),
        });
      setNotice(
        `${p.code || p.id}：${kind === "trash" ? "已移入回收站" : kind === "restore" ? "已恢复为草稿" : cancelPublication ? "已取消上架" : kind === "offline" ? "已下架" : "已提交上架"}`,
      );
      setAction(null);
      setRevision((v) => v + 1);
    } catch (e) {
      setActionError(e instanceof Error ? e.message : "操作失败，请重试。");
    } finally {
      setBusy(false);
    }
  }
  return (
    <>
      {selected ? (
        <AdminProductEditor
          key={selected}
          id={selected === "new" ? undefined : selected}
          onClose={() => select(null)}
          onSaved={() => setRevision((v) => v + 1)}
        />
      ) : (
        <section className="product-management">
          <div className="admin-heading">
            <div>
              <h1>{review ? "待审核商品" : "商品管理"}</h1>
              {review && <p>核对商品资料与图片后，可通过审核或退回修改。</p>}
            </div>
            <div className="heading-actions">
              <GuideButton kind={review ? "review" : "product"} />
              {!review && (
                <button className="button-white" onClick={() => select("new")}>
                  <Plus size={17} />
                  新增商品
                </button>
              )}
            </div>
          </div>
          {!review && (
            <>
              <label className="product-mobile-scope">
                商品范围
                <select
                  value={trash ? "trash" : publicOnly ? "public" : status}
                  onChange={(event) => switchScope(event.target.value)}
                >
                  <option value="">全部商品</option>
                  <option value="public">公开可见</option>
                  {Object.entries(statusNames).map(([value, label]) => (
                    <option key={value} value={value}>
                      {label}
                    </option>
                  ))}
                  <option value="trash">回收站</option>
                </select>
              </label>
              <div
                className="product-list-tabs"
                role="group"
                aria-label="按商品状态筛选"
              >
                <button
                  aria-pressed={!status && !trash && !publicOnly}
                  onClick={() => switchScope("")}
                >
                  全部商品
                </button>
                <button
                  aria-pressed={publicOnly}
                  onClick={() => switchScope("public")}
                >
                  公开可见
                </button>
                {Object.entries(statusNames).map(([value, label]) => (
                  <button
                    key={value}
                    aria-pressed={status === value && !trash}
                    onClick={() => switchScope(value)}
                  >
                    {label}
                  </button>
                ))}
                <button
                  className="trash-tab"
                  aria-pressed={trash}
                  onClick={() => switchScope("trash")}
                >
                  <Trash2 size={15} />
                  回收站
                </button>
              </div>
            </>
          )}
          <div className="panel product-list-panel">
            <form
              className="product-list-filters"
              onSubmit={(e) => {
                e.preventDefault();
                update({ q: q.trim() });
              }}
            >
              <label className="product-search">
                <Search size={17} />
                <input
                  aria-label="搜索商品编号或标题"
                  placeholder="搜索商品编号或标题"
                  value={q}
                  maxLength={200}
                  onChange={(e) => setQ(e.target.value)}
                />
              </label>
              <button className="button-white" type="submit">
                搜索
              </button>
              <button
                className="product-filter-toggle button-outline"
                type="button"
                aria-controls="product-list-options"
                aria-expanded={filtersOpen}
                onClick={() => setFiltersOpen(!filtersOpen)}
              >
                <SlidersHorizontal size={16} aria-hidden="true" />
                筛选{advancedCount > 0 ? ` (${advancedCount})` : ""}
              </button>
              <div
                className="product-list-options"
                id="product-list-options"
                data-open={filtersOpen}
              >
                <label>
                  交易类型
                  <select
                    value={params.get("trade_type") || ""}
                    onChange={(e) => update({ trade_type: e.target.value })}
                  >
                    <option value="">全部类型</option>
                    <option value="sale">账号出售</option>
                    <option value="rent">账号租赁</option>
                  </select>
                </label>
                <label>
                  商品来源
                  <select
                    value={params.get("source") || ""}
                    onChange={(e) => update({ source: e.target.value })}
                  >
                    <option value="">全部来源</option>
                    {Object.entries(sources).map(([value, label]) => (
                      <option key={value} value={value}>
                        {label}
                      </option>
                    ))}
                  </select>
                </label>
                <label>
                  排序
                  <select
                    value={params.get("sort") || "updated"}
                    onChange={(e) => update({ sort: e.target.value })}
                  >
                    <option value="updated">最近更新优先</option>
                    <option value="published">首次上架时间倒序</option>
                    <option value="created">创建时间倒序</option>
                  </select>
                </label>
                <button
                  className="text-button"
                  type="button"
                  onClick={() => {
                    setQ("");
                    update({ q: "", source: "", trade_type: "", sort: "" });
                  }}
                >
                  重置查询条件
                </button>
              </div>
            </form>
            <div className="product-list-meta">
              <span>
                {data ? (
                  <>
                    共 <b className="mono">{data.total}</b> 个商品
                  </>
                ) : null}
                {data && data.total > 0 && (
                  <>
                    {" "}
                    · 本页{" "}
                    <b className="mono">
                      {(pageNumber - 1) * size + 1}–
                      {Math.min(pageNumber * size, data.total)}
                    </b>{" "}
                    条
                  </>
                )}
                {publicOnly && (
                  <span className="product-scope-note">
                    用户端可见 · 分类已启用且当前版本已审核上架
                  </span>
                )}
              </span>
              <button
                type="button"
                disabled={loading}
                onClick={() => setRevision((v) => v + 1)}
              >
                <RefreshCw size={14} />
                刷新列表
              </button>
            </div>
            {trash && (
              <div className="product-trash-tools">
                <div>
                  <p>
                    这里保留已删除商品的资料和图片。恢复后进入草稿，请检查后再上架。
                  </p>
                  <small>
                    永久删除不可恢复；清空范围是整个回收站，需先核对服务器预览。
                  </small>
                </div>
                <button
                  type="button"
                  className="product-danger-outline"
                  disabled={busy || loading}
                  onClick={() => setPurgeTarget({ kind: "all" })}
                >
                  <Trash2 size={15} />
                  清空回收站
                </button>
              </div>
            )}
            {notice && (
              <p className="admin-success" role="status">
                {notice}
              </p>
            )}
            {error ? (
              <StateView
                error={error}
                onRetry={() => setRevision((v) => v + 1)}
              />
            ) : (
              <>
                <div
                  className="admin-table-wrap product-table-scroll"
                  aria-busy={loading}
                >
                  <table className="admin-table product-table">
                    <caption className="sr-only">
                      {review
                        ? "待审核商品"
                        : trash
                          ? "商品回收站"
                          : "商品管理"}
                      列表
                    </caption>
                    <thead>
                      <tr>
                        <th scope="col">商品编号</th>
                        <th>商品信息</th>
                        <th>报价</th>
                        <th>来源 / 状态</th>
                        <th>图片 / 咨询</th>
                        <th>
                          时间记录 <small>北京时间</small>
                        </th>
                        <th>操作</th>
                      </tr>
                    </thead>
                    <tbody>
                      {data?.items.map((p) => (
                        <ProductListRow
                          returnTo={`${review ? "/reviews" : "/products"}?${params}`}
                          key={p.id}
                          product={p}
                          trash={trash}
                          review={review}
                          disabled={loading || busy}
                          edit={() => select(p.id)}
                          recommend={() => setRecommendationProduct(p)}
                          act={(kind) => setAction({ product: p, kind })}
                          purge={() =>
                            setPurgeTarget({ kind: "single", product: p })
                          }
                        />
                      ))}
                    </tbody>
                  </table>
                </div>
                {loading && (
                  <p className="product-list-loading" role="status">
                    正在读取第 {pageNumber} 页…
                  </p>
                )}
                {data && !data.items.length && (
                  <StateView
                    empty={
                      trash
                        ? "回收站中没有符合当前条件的商品。清空预览将检查整个回收站。"
                        : review &&
                            !params.get("q") &&
                            !params.get("source") &&
                            !params.get("trade_type")
                          ? "当前没有待审核商品。"
                          : "没有符合条件的商品，请调整搜索或筛选条件。"
                    }
                  />
                )}
              </>
            )}
            {data && data.total > 0 && (
              <nav className="product-pagination" aria-label="商品列表分页">
                <label>
                  每页
                  <select
                    aria-label="每页条数"
                    disabled={loading}
                    value={size}
                    onChange={(e) => update({ limit: e.target.value })}
                  >
                    <option value="20">20 条</option>
                    <option value="50">50 条</option>
                  </select>
                </label>
                <div className="product-page-buttons">
                  <button
                    aria-label="上一页"
                    disabled={loading || !!error || pageNumber <= 1}
                    onClick={() => go(pageNumber - 1)}
                  >
                    <ChevronLeft size={16} />
                  </button>
                  {pageItems(pageNumber, totalPages).map((n) =>
                    typeof n === "string" ? (
                      <span key={n}>…</span>
                    ) : (
                      <button
                        key={n}
                        aria-label={`第 ${n} 页`}
                        aria-current={n === pageNumber ? "page" : undefined}
                        disabled={loading || !!error}
                        onClick={() => go(n)}
                      >
                        {n}
                      </button>
                    ),
                  )}
                  <button
                    aria-label="下一页"
                    disabled={loading || !!error || pageNumber >= totalPages}
                    onClick={() => go(pageNumber + 1)}
                  >
                    <ChevronRight size={16} />
                  </button>
                </div>
                <form
                  onSubmit={(e) => {
                    e.preventDefault();
                    const n = Number(jump.current?.value);
                    if (Number.isSafeInteger(n) && n > 0) {
                      go(n);
                      if (jump.current) jump.current.value = "";
                    }
                  }}
                >
                  <span>
                    共 <b className="mono">{totalPages}</b> 页，前往
                  </span>
                  <input
                    ref={jump}
                    aria-label="跳转页码"
                    type="number"
                    min="1"
                    max={totalPages}
                    inputMode="numeric"
                  />
                  <button disabled={loading || !!error}>跳转</button>
                </form>
              </nav>
            )}
          </div>
        </section>
      )}
      {purgeTarget && trash && !selected && (
        <AdminTrashPurge
          target={purgeTarget}
          onBusyChange={setBusy}
          onClose={(refresh) => {
            setPurgeTarget(null);
            if (refresh) {
              setRevision((value) => value + 1);
            }
          }}
          onSuccess={(result) => {
            setPurgeTarget(null);
            setBusy(false);
            setNotice(
              `已永久删除 ${result.purged_count} 个商品。独占图片正在后台清理，其他引用及必要记录保留。`,
            );
            setRevision((value) => value + 1);
          }}
        />
      )}
      {recommendationProduct && (
        <AdminProductRecommendation
          key={recommendationProduct.id}
          product={recommendationProduct}
          onClose={() => setRecommendationProduct(null)}
          onSaved={() => setRevision((value) => value + 1)}
        />
      )}
      <dialog
        ref={dialog}
        className="product-action-dialog"
        aria-labelledby="product-action-heading"
        onCancel={(e) => {
          if (busy) e.preventDefault();
          else setAction(null);
        }}
      >
        {action && (
          <form onSubmit={perform}>
            <button
              className="product-dialog-close"
              aria-label="关闭操作确认"
              type="button"
              disabled={busy}
              onClick={() => setAction(null)}
            >
              <X size={20} />
            </button>
            <h2 id="product-action-heading">{actionLabel}</h2>
            <p className="product-action-code mono">
              {action.product.code || action.product.id}
            </p>
            <p className="product-action-title">
              {action.product.title.trim() || "未填写标题"}
            </p>
            <p>
              {cancelPublication
                ? "停止本次上架流程，商品转为已下架。"
                : actionNotes[action.kind]}
            </p>
            {action.kind === "publish" && action.product.mode === "rent" && (
              <label>
                租赁标签
                <select
                  required
                  aria-label="租赁标签"
                  value={rentalCategory}
                  onChange={(e) => setRentalCategory(e.target.value)}
                >
                  <option value="">请选择租赁标签</option>
                  <option value="hour_day">时日</option>
                  <option value="week_month">周月</option>
                </select>
              </label>
            )}
            {["trash", "offline"].includes(action.kind) && (
              <label>
                操作原因
                <textarea
                  autoFocus
                  required
                  maxLength={500}
                  rows={3}
                  placeholder="填写原因，便于后续追溯"
                  value={reason}
                  onChange={(e) => setReason(e.target.value)}
                />
              </label>
            )}
            {actionError && (
              <p className="form-error" role="alert">
                {actionError}
              </p>
            )}
            <div className="product-dialog-actions">
              <button
                className="button-outline"
                type="button"
                disabled={busy}
                onClick={() => setAction(null)}
              >
                取消
              </button>
              <button
                className={
                  action.kind === "trash"
                    ? "product-danger-button"
                    : "button-white"
                }
                disabled={
                  busy ||
                  (action.kind === "publish" &&
                    action.product.mode === "rent" &&
                    !rentalCategory) ||
                  (["trash", "offline"].includes(action.kind) && !reason.trim())
                }
              >
                {busy ? "正在处理…" : actionLabel}
              </button>
            </div>
          </form>
        )}
      </dialog>
    </>
  );
}
