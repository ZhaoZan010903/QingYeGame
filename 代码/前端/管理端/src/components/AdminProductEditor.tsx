import { useEffect, useState, type FormEvent } from "react";
import { adminApi, adminImageApi, type AdminRecord } from "../lib/admin-api";
import { ApiError, productFrom } from "../lib/api";
import { ImageUploader } from "./ImageUploader";
import { Price, StateView } from "./shared";
import type { Product } from "../types";
import {
  ArrowLeft,
  ArrowRight,
  Check,
  Image,
  PackageCheck,
} from "lucide-react";
import { GuideButton } from "./AdminGuide";
import {
  parseFilters,
  publicSite,
  unitLabels,
  updateAttribute,
  useUnsavedChanges,
  type EditableGroup,
} from "../lib/admin-ux";

export const statusNames: Record<string, string> = {
  draft: "草稿",
  pending: "待审核",
  published: "已上架",
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
  const [step, setStep] = useState(0);
  useEffect(() => {
    window.scrollTo({ top: 0, behavior: "instant" });
  }, [step]);
  const product = row ? productFrom(row) : null;
  const locked = busy || imageBusy;
  const rankDirty =
    !!row && rank !== (row.manual_rank == null ? "" : String(row.manual_rank));
  useUnsavedChanges(dirty || imagePending || rankDirty, locked);
  const category = categories.find((c) => c.id === form.category_id);
  let groups: EditableGroup[] = [],
    attributes: Record<string, unknown> = {};
  try {
    groups = parseFilters(JSON.stringify(category?.filter_config ?? []));
    attributes = JSON.parse(form.attributes);
  } catch {
    /* The original value remains available in advanced fields. */
  }
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
    if (
      row?.status === "published" &&
      !window.confirm(
        "保存后，这个商品会暂时停止公开并变成草稿。检查完成后需要重新上架。确认保存？",
      )
    )
      throw new Error("已取消保存");
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
      setNotice("资料已保存为草稿。下一步整理图片，确认后再上架。");
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
    const confirmations = {
      approve: "审核通过后，这个版本会立即公开展示。确认资料与图片已经核对？",
      publish: "上架后，用户可以看到商品和图片。确认公开展示？",
      reject: "确认退回给投稿人修改？填写的原因会作为审核说明。",
      offline: "下架后，用户将无法查看这个商品。确认下架？",
      submit: "确认资料和图片已准备好，提交审核？",
    };
    if (!window.confirm(confirmations[kind])) return;
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
      setNotice(
        kind === "approve" || kind === "publish"
          ? "已上架，用户可以查看这个商品。"
          : kind === "reject"
            ? "已退回修改，审核原因已保存。"
            : kind === "offline"
              ? "商品已下架，前台不再展示。"
              : "已提交，等待审核。",
      );
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
      setRow((old) =>
        old ? { ...old, manual_rank: rank === "" ? null : Number(rank) } : old,
      );
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
    onClose();
  }
  return (
    <section className="admin-editor" aria-label="商品编辑">
      <div className="admin-heading">
        <div>
          <span className="eyebrow">商品维护</span>
          <h1>{id ? "编辑与管理商品" : "上架一个新商品"}</h1>
          <p>
            {row
              ? `${row.product_code ?? row.id} · ${statusNames[row.status]} · 版本 ${row.version}`
              : "先保存草稿，再追加图片。"}
          </p>
        </div>
        <div className="heading-actions">
          <GuideButton kind="product" />
          <button
            type="button"
            className="button-outline"
            onClick={close}
            disabled={locked}
          >
            <ArrowLeft size={16} />
            返回列表
          </button>
        </div>
      </div>
      <ol className="stepper editor-stepper">
        {["填写商品资料", "整理商品图片", "检查并上架"].map((name, i) => (
          <li className={step === i ? "active" : ""} key={name}>
            <button type="button" onClick={() => setStep(i)}>
              <span>{i + 1}</span>
              {name}
            </button>
          </li>
        ))}
      </ol>
      {loading ? (
        <StateView loading />
      ) : id && !row ? (
        <StateView
          error={new Error(error || "商品读取失败")}
          onRetry={() => void reload()}
        />
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
          <div hidden={step !== 0} className="editor-step-body panel">
            <div className="location-note">
              <strong>第 1 步 · 填写用户会看到的资料</strong>
              <span>
                显示位置：商品列表卡片 + 商品详情。先保存为草稿，不会立即上架。
              </span>
            </div>
            <form
              onSubmit={(e) => void save(e).catch(() => {})}
              className="member-form"
            >
              <fieldset disabled={locked}>
                <label className="form-field">
                  商品标题
                  <input
                    required
                    placeholder="例如：QQ 炫舞 · 华东区 · 稀有套装账号"
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
                        const target = categories.find(
                          (c) =>
                            c.enabled &&
                            (c.trade_type === "all" || c.trade_type === mode),
                        );
                        setForm((old) => ({
                          ...old,
                          trade_type: mode,
                          category_id: target?.id ?? "",
                          price_unit:
                            mode === "sale"
                              ? "account"
                              : (target?.price_units?.find(
                                  (u: string) =>
                                    !["account", "unknown"].includes(u),
                                ) ?? "day"),
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
                      onChange={(e) => {
                        change("category_id", e.target.value);
                        const target = categories.find(
                          (c) => c.id === e.target.value,
                        );
                        if (
                          target?.price_units?.length &&
                          !target.price_units.includes(form.price_unit)
                        )
                          change(
                            "price_unit",
                            target.price_units.find(
                              (u: string) => u !== "unknown",
                            ) ?? "unknown",
                          );
                      }}
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
                            : category?.price_units?.length &&
                                !category.price_units.includes(key)
                              ? false
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
                    商品报价（元）
                    <input
                      type="number"
                      min="0"
                      step="0.01"
                      disabled={form.price_unit === "unknown"}
                      value={form.price_amount}
                      placeholder="留空表示联系客服"
                      onChange={(e) => change("price_amount", e.target.value)}
                    />
                    <small>
                      留空显示“联系客服”。单位由左侧选择，不自动换算租期。
                    </small>
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
                <div className="admin-grid">
                  {groups
                    .filter((group) => group.enabled !== false)
                    .map((group) => {
                      const existing = attributes?.[group.key];
                      const selected = Array.isArray(existing)
                        ? existing.map(String)
                        : existing
                          ? [String(existing)]
                          : [];
                      return (
                        <div key={group.key} className="form-field">
                          <span>{group.label}</span>
                          {group.multiple ? (
                            <div className="attribute-options">
                              {group.options.map((option) => (
                                <label
                                  className="agreement-check"
                                  key={option.value}
                                >
                                  <input
                                    type="checkbox"
                                    checked={selected.includes(option.value)}
                                    disabled={
                                      option.enabled === false &&
                                      !selected.includes(option.value)
                                    }
                                    onChange={(e) => {
                                      try {
                                        change(
                                          "attributes",
                                          updateAttribute(
                                            form.attributes,
                                            group.key,
                                            e.target.checked
                                              ? [...selected, option.value]
                                              : selected.filter(
                                                  (v) => v !== option.value,
                                                ),
                                          ),
                                        );
                                      } catch (e) {
                                        fail(e);
                                      }
                                    }}
                                  />
                                  {option.label}
                                  {option.enabled === false ? "（已停用）" : ""}
                                </label>
                              ))}
                            </div>
                          ) : (
                            <select
                              aria-label={group.label}
                              value={selected[0] ?? ""}
                              onChange={(e) => {
                                try {
                                  change(
                                    "attributes",
                                    updateAttribute(
                                      form.attributes,
                                      group.key,
                                      e.target.value,
                                    ),
                                  );
                                } catch (e) {
                                  fail(e);
                                }
                              }}
                            >
                              <option value="">暂不填写</option>
                              {group.options.map((option) => (
                                <option
                                  value={option.value}
                                  key={option.value}
                                  disabled={option.enabled === false}
                                >
                                  {option.label}
                                </option>
                              ))}
                            </select>
                          )}
                          {selected.some(
                            (v) =>
                              !group.options.some(
                                (o) => o.value === v && o.enabled !== false,
                              ),
                          ) && (
                            <small className="danger-text">
                              有历史或停用选项，请核对高级资料后再保存。
                            </small>
                          )}
                        </div>
                      );
                    })}
                </div>
                <details className="admin-advanced">
                  <summary>高级资料（原始属性与迁移内容）</summary>
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
                <div className="save-bar">
                  <span>
                    {dirty ? "资料有修改，尚未保存" : "保存草稿后再检查图片"}
                  </span>
                  <button className="button-white" type="submit">
                    {busy ? "处理中…" : "保存为草稿"}
                  </button>
                  <button
                    type="button"
                    className="button-outline"
                    onClick={() => setStep(1)}
                  >
                    下一步：图片 <ArrowRight size={16} />
                  </button>
                </div>
              </fieldset>
            </form>
          </div>
          <div hidden={step !== 1} className="editor-step-body panel">
            <div className="location-note">
              <strong>第 2 步 · 整理商品图片</strong>
              <span>
                第一张是商品封面，其余在详情页按顺序展示。上传、删除和排序会立即保存；已上架商品会转为草稿。
              </span>
            </div>
            <h3>
              <Image size={18} />
              商品图集
            </h3>
            {dirty && product && (
              <p className="admin-warning">
                商品资料有修改，请先返回第 1 步保存，再调整图片。
              </p>
            )}
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
              disabled={busy || (!!product && dirty)}
            />
            <div className="save-bar">
              <button
                type="button"
                className="button-outline"
                onClick={() => setStep(0)}
              >
                上一步：资料
              </button>
              <button
                type="button"
                className="button-white"
                onClick={() => setStep(2)}
              >
                下一步：检查并上架 <ArrowRight size={16} />
              </button>
            </div>
          </div>
          <div hidden={step !== 2} className="editor-step-body panel">
            <div className="location-note">
              <strong>第 3 步 · 检查后再公开</strong>
              <span>确认标题、报价和图片无误，再选择上架或提交审核。</span>
            </div>
            <div className="product-review-card">
              <div className="review-cover">
                {product?.images[0]?.url ? (
                  <img
                    src={
                      product.images[0].thumbnailUrl || product.images[0].url
                    }
                    alt="当前商品封面"
                  />
                ) : (
                  <PackageCheck size={38} />
                )}
              </div>
              <div>
                <span className="pill">
                  {row ? statusNames[row.status] : "尚未保存"}
                </span>
                <h2>{form.title || "尚未填写商品标题"}</h2>
                <p>
                  {category?.name || "尚未选择分类"} ·{" "}
                  {unitLabels[form.price_unit]}
                </p>
                <strong>
                  {form.price_amount === ""
                    ? "联系客服"
                    : `¥ ${form.price_amount}`}
                </strong>
                <p>当前已保存 {product?.imageTotal ?? 0} 张图片</p>
              </div>
            </div>
            <ul className="review-checklist">
              <li>
                <Check size={16} />
                核对标题、分类和报价单位
              </li>
              <li>
                <Check size={16} />
                在“整理商品图片”中查看全部图片
              </li>
              <li>
                <Check size={16} />
                会员投稿还需核对资料是否完整、清楚
              </li>
            </ul>
            {(dirty || imagePending) && (
              <p className="admin-warning">
                还有资料或图片尚未保存，请返回前两步处理后再操作。
              </p>
            )}
            {!row && (
              <div className="inline-help">
                <p>请先保存商品资料，再上传图片。</p>
                <button
                  type="button"
                  className="button-white"
                  onClick={() => setStep(0)}
                >
                  返回填写资料
                </button>
              </div>
            )}
            {row && (
              <section className="admin-actions-panel" aria-label="审核与展示">
                <h3>
                  {row.status === "pending"
                    ? "审核这个投稿"
                    : row.status === "published"
                      ? "当前商品已上架"
                      : "选择下一步操作"}
                </h3>
                {row.status === "published" && publicSite && (
                  <a
                    className="button-outline"
                    href={`${publicSite}/accounts/${row.id}`}
                    target="_blank"
                    rel="noreferrer"
                  >
                    查看公开商品页 ↗
                  </a>
                )}
                {row.review_note && <p>最近审核说明：{row.review_note}</p>}
                {(row.status === "pending" || row.status === "published") && (
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
                )}
                <div className="admin-actions">
                  {row.status === "pending" && (
                    <>
                      <button
                        className="button-white"
                        disabled={locked}
                        onClick={() => void action("approve")}
                      >
                        审核通过并上架
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
                      {row.source === "member" && (
                        <button
                          className="button-outline"
                          disabled={locked}
                          onClick={() => void action("submit")}
                        >
                          提交审核
                        </button>
                      )}
                      {row.source !== "member" && (
                        <button
                          className="button-white"
                          disabled={locked}
                          onClick={() => void action("publish")}
                        >
                          确认上架
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
                      首页轮播 · 精选顺序
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
                      保存轮播顺序
                    </button>
                  </div>
                )}
                {row.status === "published" && (
                  <p className="field-hint">
                    数字越小越靠前，留空取消精选。轮播位置有限，实际显示以首页为准；不改变自动热门排序。
                  </p>
                )}
              </section>
            )}
          </div>
        </>
      )}
    </section>
  );
}
