import { useEffect, useState, type FormEvent } from "react";
import { adminApi, adminImageApi, type AdminRecord } from "../lib/admin-api";
import { ApiError, productFrom } from "../lib/api";
import { ImageUploader } from "./ImageUploader";
import { Price, StateView } from "./shared";
import type { Product } from "../types";

export const statusNames: Record<string, string> = {
  draft: "草稿",
  pending: "待审核",
  published: "已展示",
  rejected: "已退回",
  offline: "已下架",
};
const message = (error: unknown) =>
  error instanceof Error ? error.message : "保存失败，请重试。";
const units: Record<string, string> = {
  account: "元（账号总价）",
  hour: "元 / 小时",
  day: "元 / 日",
  week: "元 / 周",
  month: "元 / 月",
  unknown: "原站报价，单位待核对",
};
interface FormValues {
  title: string;
  category_id: string;
  trade_type: string;
  price_unit: string;
  price_amount: string;
  description: string;
  attributes: string;
}
const formFrom = (row: AdminRecord): FormValues => ({
  title: row.title ?? "",
  category_id: row.category_id ?? "",
  trade_type: row.trade_type ?? "sale",
  price_unit: row.price_unit ?? "account",
  price_amount: row.price_amount ?? "",
  description: row.description ?? "",
  attributes: JSON.stringify(row.attributes ?? {}, null, 2),
});

export function AdminProductEditor({
  id,
  onClose,
  onSaved,
}: {
  id?: string;
  onClose: () => void;
  onSaved: () => void;
}) {
  const [row, setRow] = useState<AdminRecord | null>(null);
  const [form, setForm] = useState<FormValues>(formFrom({}));
  const [categories, setCategories] = useState<AdminRecord[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");
  const [conflict, setConflict] = useState(false);
  const [busy, setBusy] = useState(false);
  const [imageBusy, setImageBusy] = useState(false);
  const [imagePending, setImagePending] = useState(false);
  const [dirty, setDirty] = useState(false);
  const [reason, setReason] = useState("");
  const [rank, setRank] = useState("");
  const [notice, setNotice] = useState("");
  const product = row ? productFrom(row) : null;
  const locked = busy || imageBusy;
  function accept(value: AdminRecord) {
    setRow(value);
    setForm(formFrom(value));
    setRank(value.manual_rank == null ? "" : String(value.manual_rank));
    setDirty(false);
    setConflict(false);
  }
  async function reload() {
    setLoading(true);
    setError("");
    try {
      const [list, value] = await Promise.all([
        adminApi.list("categories"),
        (row?.id ?? id)
          ? adminApi.product(row?.id ?? id!)
          : Promise.resolve(null),
      ]);
      setCategories(list);
      if (value) accept(value);
      else
        setForm(
          formFrom({
            category_id: list.find(
              (c) =>
                c.enabled &&
                (c.trade_type === "all" || c.trade_type === "sale"),
            )?.id,
          }),
        );
    } catch (e) {
      setError(message(e));
    } finally {
      setLoading(false);
    }
  }
  useEffect(() => {
    void reload();
  }, [id]);
  useEffect(() => {
    if (!dirty && !imagePending && !imageBusy) return;
    const warn = (event: BeforeUnloadEvent) => {
      event.preventDefault();
    };
    window.addEventListener("beforeunload", warn);
    return () => window.removeEventListener("beforeunload", warn);
  }, [dirty, imagePending, imageBusy]);
  function change(key: keyof FormValues, value: string) {
    setForm((old) => ({ ...old, [key]: value }));
    setDirty(true);
    setNotice("");
  }
  function fail(e: unknown) {
    setError(message(e));
    setConflict(e instanceof ApiError && e.status === 409);
  }
  async function save(event?: FormEvent): Promise<Product> {
    event?.preventDefault();
    setError("");
    setBusy(true);
    try {
      let attributes: unknown;
      try {
        attributes = JSON.parse(form.attributes);
      } catch {
        throw new Error("账号属性不是有效的 JSON，请检查括号和引号。");
      }
      if (
        !attributes ||
        typeof attributes !== "object" ||
        Array.isArray(attributes)
      )
        throw new Error("账号属性必须为对象。");
      const input = {
        ...form,
        attributes,
        price_amount:
          form.price_unit === "unknown" || form.price_amount === ""
            ? null
            : form.price_amount,
      };
      const value = await adminApi.saveProduct(input, row?.id, row?.version);
      accept(value);
      setNotice("已保存为草稿。完成资料和图片后可提交审核或发布。");
      onSaved();
      return productFrom(value);
    } catch (e) {
      fail(e);
      throw e;
    } finally {
      setBusy(false);
    }
  }
  async function action(
    kind: "approve" | "reject" | "submit" | "offline" | "publish",
  ) {
    if (!row) return;
    setError("");
    setNotice("");
    if (dirty || imagePending) {
      setError("请先保存资料并处理完图片队列，再进行状态操作。");
      return;
    }
    if ((kind === "reject" || kind === "offline") && !reason.trim()) {
      setError("请填写退回或下架原因。");
      return;
    }
    setBusy(true);
    try {
      const endpoint =
        kind === "approve" || kind === "reject"
          ? "review"
          : kind === "publish"
            ? "publish"
            : "status";
      const body = {
        expected_version: row.version,
        ...(endpoint === "review"
          ? { decision: kind, reason }
          : endpoint === "status"
            ? { action: kind, reason }
            : {}),
      };
      accept(await adminApi.action(row.id, endpoint, body));
      setReason("");
      setNotice("商品状态已更新。");
      onSaved();
    } catch (e) {
      fail(e);
    } finally {
      setBusy(false);
    }
  }
  async function feature() {
    if (!row) return;
    setError("");
    setBusy(true);
    try {
      await adminApi.featured(row.id, rank === "" ? null : Number(rank));
      setNotice(rank === "" ? "已取消人工精选。" : "精选顺序已保存。");
      onSaved();
    } catch (e) {
      fail(e);
    } finally {
      setBusy(false);
    }
  }
  function close() {
    if (imageBusy) return;
    if (
      (dirty || imagePending) &&
      !window.confirm("还有未保存的资料或图片队列，确认关闭编辑？")
    )
      return;
    onClose();
  }
  return (
    <section className="admin-editor panel" aria-label="商品编辑">
      <div className="admin-heading">
        <div>
          <h2>{id ? "编辑商品" : "新建平台商品"}</h2>
          <p>
            {row
              ? `${row.product_code ?? row.id} · ${statusNames[row.status]} · 版本 ${row.version}`
              : "先保存草稿，再追加图片。"}
          </p>
        </div>
        <button
          type="button"
          className="button-outline"
          onClick={close}
          disabled={imageBusy}
        >
          关闭编辑
        </button>
      </div>
      {loading ? (
        <StateView loading />
      ) : (
        <>
          {product?.unit === "unknown" && (
            <div className="admin-notice">
              <strong>原站迁移资料</strong>
              <Price product={product} />
              <p>
                保留原价与原分类「{product.legacyCategory || "未注明"}
                」。核对报价单位后才能填写新报价；保存其他资料不会覆盖原站报价。
              </p>
            </div>
          )}
          {error && (
            <div className="form-error" role="alert">
              {error}
              {conflict && (
                <button
                  className="text-button"
                  onClick={() => {
                    if (window.confirm("重新加载会替换尚未保存的编辑，继续？"))
                      void reload();
                  }}
                >
                  重新加载最新版本
                </button>
              )}
            </div>
          )}
          {notice && (
            <p className="admin-success" role="status">
              {notice}
            </p>
          )}
          <form
            onSubmit={(e) => void save(e).catch(() => {})}
            className="member-form"
          >
            <fieldset disabled={locked}>
              <label className="form-field">
                商品标题
                <input
                  required
                  maxLength={row?.source === "legacy" ? 1000 : 100}
                  value={form.title}
                  onChange={(e) => change("title", e.target.value)}
                />
              </label>
              {row?.source === "legacy" &&
                Array.from(row.title ?? "").length > 100 && (
                  <p className="admin-warning">
                    原站长标题可原样保留。若修改标题，请精简至 100
                    字以内；详细内容可保留在商品介绍。
                  </p>
                )}
              <div className="admin-grid">
                <label className="form-field">
                  交易类型
                  <select
                    value={form.trade_type}
                    onChange={(e) => {
                      const mode = e.target.value;
                      setForm((old) => ({
                        ...old,
                        trade_type: mode,
                        category_id:
                          categories.find(
                            (c) =>
                              c.enabled &&
                              (c.trade_type === "all" || c.trade_type === mode),
                          )?.id ?? "",
                        price_unit: mode === "sale" ? "account" : "day",
                      }));
                      setDirty(true);
                    }}
                  >
                    <option value="sale">账号出售</option>
                    <option value="rent">账号租赁</option>
                  </select>
                </label>
                <label className="form-field">
                  分类
                  <select
                    required
                    value={form.category_id}
                    onChange={(e) => change("category_id", e.target.value)}
                  >
                    <option value="">选择分类</option>
                    {categories
                      .filter(
                        (c) =>
                          c.trade_type === "all" ||
                          c.trade_type === form.trade_type,
                      )
                      .map((c) => (
                        <option key={c.id} value={c.id} disabled={!c.enabled}>
                          {c.name}
                          {!c.enabled ? "（已停用）" : ""}
                        </option>
                      ))}
                  </select>
                </label>
                <label className="form-field">
                  报价单位
                  <select
                    value={form.price_unit}
                    onChange={(e) => change("price_unit", e.target.value)}
                  >
                    {Object.entries(units)
                      .filter(([key]) =>
                        key === "unknown"
                          ? row?.price_unit === "unknown"
                          : form.trade_type === "sale"
                            ? key === "account"
                            : key !== "account",
                      )
                      .map(([key, label]) => (
                        <option key={key} value={key}>
                          {label}
                        </option>
                      ))}
                  </select>
                </label>
                <label className="form-field">
                  核对后报价（元）
                  <input
                    type="number"
                    min="0"
                    step="0.01"
                    disabled={form.price_unit === "unknown"}
                    value={form.price_amount}
                    placeholder="留空表示联系客服"
                    onChange={(e) => change("price_amount", e.target.value)}
                  />
                </label>
              </div>
              <label className="form-field">
                商品介绍
                <textarea
                  rows={7}
                  value={form.description}
                  onChange={(e) => change("description", e.target.value)}
                />
              </label>
              <details className="admin-advanced">
                <summary>账号属性与迁移资料</summary>
                <p>
                  保留已有字段。分类筛选项以分类配置中的编码为准；只修改已核实的值。
                </p>
                <label className="form-field">
                  属性内容（JSON 对象）
                  <textarea
                    className="mono"
                    rows={10}
                    value={form.attributes}
                    onChange={(e) => change("attributes", e.target.value)}
                    spellCheck={false}
                  />
                </label>
              </details>
              {row?.status === "published" && (
                <p className="admin-warning">
                  保存资料或修改图片后，商品会停止公开并转为草稿。
                </p>
              )}
              <button className="button-white" type="submit">
                {busy ? "处理中…" : "保存为草稿"}
              </button>
            </fieldset>
          </form>
          <h3>商品图集</h3>
          <ImageUploader
            product={product}
            transport={adminImageApi}
            ensureDraft={() =>
              dirty || !product ? save() : Promise.resolve(product)
            }
            onChange={(next) => {
              setRow((old) => ({
                ...old,
                id: next.id,
                status: next.status,
                version: next.version,
                image_total: next.imageTotal,
                images: next.images,
              }));
              onSaved();
            }}
            onBusyChange={setImageBusy}
            onPendingChange={setImagePending}
            disabled={busy}
          />
          {row && (
            <section className="admin-actions-panel" aria-label="审核与展示">
              <h3>审核与展示</h3>
              {row.review_note && <p>最近审核说明：{row.review_note}</p>}
              <label className="form-field">
                操作原因
                <input
                  value={reason}
                  onChange={(e) => setReason(e.target.value)}
                  placeholder="退回或下架时必填"
                  maxLength={500}
                  disabled={locked}
                />
              </label>
              <div className="admin-actions">
                {row.status === "pending" && (
                  <>
                    <button
                      className="button-white"
                      disabled={locked}
                      onClick={() => void action("approve")}
                    >
                      审核通过并展示
                    </button>
                    <button
                      className="button-outline"
                      disabled={locked}
                      onClick={() => void action("reject")}
                    >
                      退回修改
                    </button>
                  </>
                )}
                {["draft", "rejected", "offline"].includes(row.status) && (
                  <>
                    <button
                      className="button-outline"
                      disabled={locked}
                      onClick={() => void action("submit")}
                    >
                      提交审核
                    </button>
                    {row.source !== "member" && (
                      <button
                        className="button-white"
                        disabled={locked}
                        onClick={() => void action("publish")}
                      >
                        发布平台商品
                      </button>
                    )}
                  </>
                )}
                {row.status === "published" && (
                  <button
                    className="button-outline"
                    disabled={locked}
                    onClick={() => void action("offline")}
                  >
                    下架商品
                  </button>
                )}
              </div>
              {row.status === "published" && (
                <div className="admin-feature">
                  <label className="form-field">
                    人工精选顺序
                    <input
                      type="number"
                      min="1"
                      step="1"
                      value={rank}
                      onChange={(e) => setRank(e.target.value)}
                      placeholder="留空取消精选"
                    />
                  </label>
                  <button
                    className="button-outline"
                    disabled={
                      locked ||
                      (rank !== "" &&
                        (!Number.isInteger(Number(rank)) || Number(rank) < 1))
                    }
                    onClick={() => void feature()}
                  >
                    保存精选
                  </button>
                </div>
              )}
            </section>
          )}
        </>
      )}
    </section>
  );
}
