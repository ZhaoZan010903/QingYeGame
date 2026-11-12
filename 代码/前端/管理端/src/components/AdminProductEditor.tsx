import { MediaSyncPanel } from "./MediaSyncPanel";
import { publicationInterrupted } from "../../../共享/upload-progress";
import {
  canAcceptFreshProduct,
  mergeMediaSyncDisplay,
  editorRecordFromProduct,
} from "../lib/media-sync-state";
import { useEffect, useRef, useState, type FormEvent } from "react";
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
import { Link } from "react-router-dom";
import {
  buildRentalQuotes,
  customTagsForEditing,
  parseCustomTags,
} from "../lib/product-input";
import { fixedProductFields, type DictionaryGroup } from "../lib/dictionaries";
import {
  publicSite,
  unitLabels,
  updateAttribute,
  useUnsavedChanges,
  type EditableGroup,
} from "../lib/admin-ux";

export const statusNames: Record<string, string> = {
  draft: "草稿",
  pending: "待审核",
  publishing: "上架中",
  rejected_final: "最终驳回",
  published: "已上架",
  rejected: "已退回",
  offline: "已下架",
  sold: "已售出",
  rented: "出租中",
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
  const rowRef = useRef(row);
  rowRef.current = row;
  const aliveRef = useRef(true);
  useEffect(() => {
    aliveRef.current = true;
    return () => {
      aliveRef.current = false;
    };
  }, []);
  const [form, setForm] = useState<FormValues>(formFrom({}));
  const [categories, setCategories] = useState<AdminRecord[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");
  const [conflict, setConflict] = useState(false);
  const [busy, setBusy] = useState(false);
  const [imageBusy, setImageBusy] = useState(false);
  const [imagePending, setImagePending] = useState(false);
  const [dirty, setDirty] = useState(false);
  const dirtyRef = useRef(dirty);
  dirtyRef.current = dirty;
  const [reason, setReason] = useState("");
  const [notice, setNotice] = useState("");
  const [step, setStep] = useState(0);
  const [privateAccount, setPrivateAccount] = useState("");
  const [privatePassword, setPrivatePassword] = useState("");
  const [privateVisible, setPrivateVisible] = useState(false);
  const [tags, setTags] = useState("");
  const [quotes, setQuotes] = useState<Record<string, string>>({});
  const [dictionaries, setDictionaries] = useState<DictionaryGroup[]>([]);
  const formFields = {
    title: { label: "账号标题", placeholder: "填写账号特点与标题" },
    description: {
      label: "账号介绍",
      placeholder: "介绍账号特点与交易说明，私密资料请填写专用区域",
    },
    custom_tags: {
      label: "自定义标签",
      placeholder: "例如：典藏、可议价",
      required: false,
      enabled: true,
    },
    private_credentials: {
      label: "账号交接资料",
      placeholder: "仅供授权管理员核验，不公开展示。",
      required: false,
      enabled: true,
    },
  };
  const [showPreview, setShowPreview] = useState(false);
  useEffect(() => {
    window.scrollTo({ top: 0, behavior: "instant" });
  }, [step]);
  const product = row ? productFrom(row) : null;
  const statusLabel = row
    ? publicationInterrupted(row.status, row.publication_interrupted)
      ? "上架已中止"
      : statusNames[row.status]
    : "尚未保存";
  const locked = busy || imageBusy;
  const writeLocked = locked || conflict;
  useUnsavedChanges(dirty || imagePending, locked);
  const category = categories.find((c) => c.id === form.category_id);
  const allowedUnits: string[] = category?.price_units?.length
    ? category.price_units
    : form.trade_type === "rent"
      ? ["hour", "day", "week", "month"]
      : ["account"];
  const unsupportedPrimary =
    form.price_unit !== "unknown" && !allowedUnits.includes(form.price_unit);
  const unsupportedQuotes = Object.entries(quotes).filter(
    ([unit]) => unit !== form.price_unit && !allowedUnits.includes(unit),
  );
  let groups: EditableGroup[] = [],
    attributes: Record<string, unknown> = {};
  try {
    groups = fixedProductFields.map((field) => ({
      ...field,
      type: "select",
      multiple: false,
      enabled: true,
      options: (
        dictionaries.find((group) => group.key === field.key)?.options ?? []
      ).map((option) => ({ ...option })),
    }));
    attributes = JSON.parse(form.attributes);
  } catch {
    /* Preserve malformed legacy data for server validation instead of replacing it. */
  }
  function accept(value: AdminRecord) {
    rowRef.current = value;
    setRow(value);
    setForm(formFrom(value));
    dirtyRef.current = false;
    setDirty(false);
    setConflict(false);
    setPrivateAccount("");
    setPrivatePassword("");
    setPrivateVisible(false);
    setTags(customTagsForEditing(value.attributes));
    setQuotes(
      Object.fromEntries(
        (value.rental_quotes ?? []).map(
          (q: { unit: string; amount: string }) => [q.unit, String(q.amount)],
        ),
      ),
    );
  }
  async function receiveMediaState(next: AdminRecord) {
    const current = rowRef.current;
    if (!current) return;
    const merged = mergeMediaSyncDisplay(
      {
        ...current,
        status: String(current.status),
        version: Number(current.version),
      },
      {
        status: String(next.status),
        version: Number(next.version),
        interrupted: next.interrupted,
      },
      dirtyRef.current,
    );
    if (!merged.requiresReload) {
      setRow(merged.record);
      if (next.interrupted !== current.publication_interrupted) onSaved();
      return;
    }
    if (dirtyRef.current) {
      setRow(merged.record);
      setConflict(true);
      setError(merged.message);
      return;
    }
    const expected = {
      id: String(current.id),
      version: Number(current.version),
    };
    setBusy(true);
    try {
      const fresh = await adminApi.product(expected.id);
      if (!aliveRef.current) return;
      const now = rowRef.current;
      if (
        !canAcceptFreshProduct(
          expected,
          now ? { id: String(now.id), version: Number(now.version) } : null,
          dirtyRef.current,
        )
      ) {
        if (now?.id === expected.id && dirtyRef.current) {
          setConflict(true);
          setError("商品状态已更新，未保存编辑仍保留，请重新加载最新版本。");
        }
        return;
      }
      accept(fresh);
      setNotice(
        fresh.status === "published"
          ? "图片准备完成，商品已上架。"
          : "商品状态已更新。",
      );
      onSaved();
    } catch (error) {
      if (aliveRef.current && rowRef.current?.id === expected.id) {
        setConflict(true);
        setError("商品状态已更新，但未能读取完整资料，请重新加载最新版本。");
      }
    } finally {
      if (aliveRef.current && rowRef.current?.id === expected.id)
        setBusy(false);
    }
  }
  async function reload() {
    setLoading(true);
    setError("");
    try {
      const [list, value, sharedDictionaries] = await Promise.all([
        adminApi.list("categories"),
        (row?.id ?? id)
          ? adminApi.product(row?.id ?? id!)
          : Promise.resolve(null),
        adminApi.dictionaries(),
      ]);
      setDictionaries(sharedDictionaries);
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
    if (conflict || row?.status === "publishing")
      throw new Error("请先完成上架或重新加载最新版本，再编辑商品。");
    if (row?.status === "rejected_final")
      throw new Error("该商品已最终驳回，不能编辑或恢复上架。");
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
        attributes: {
          ...attributes,
          game: "QQ炫舞",
          custom_tags: parseCustomTags(
            tags,
            row?.attributes?.custom_tags ?? [],
          ),
        },
        rental_quotes: buildRentalQuotes(
          form.trade_type,
          form.price_unit,
          form.price_amount,
          quotes,
          allowedUnits,
        ),
        ...(privateAccount || privatePassword
          ? {
              private_credentials: {
                qq_account: privateAccount.trim(),
                qq_password: privatePassword,
              },
            }
          : {}),
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
    kind:
      | "approve"
      | "reject"
      | "reject_final"
      | "submit"
      | "offline"
      | "publish"
      | "sold"
      | "rented",
  ) {
    if (!row) return;
    if (conflict) {
      setError("请先重新加载最新版本，再继续操作。");
      return;
    }
    setError("");
    setNotice("");
    if (dirty || imagePending) {
      setError("请先保存资料并处理完图片队列，再进行状态操作。");
      return;
    }
    if (
      ["reject", "reject_final", "offline", "sold", "rented"].includes(kind) &&
      !reason.trim()
    ) {
      setError("请填写退回或下架原因。");
      return;
    }
    const confirmations = {
      approve:
        "审核通过后将准备图片，准备完成后公开展示。确认资料与图片已经核对？",
      publish: "确认资料和图片无误？图片同步并校验完成后，商品将公开展示。",
      reject: "确认退回给投稿人修改？填写的原因会作为审核说明。",
      reject_final:
        "最终驳回后会清理该商品暂存图片，不能继续原稿。确认最终驳回？",
      offline: "下架后，用户将无法查看这个商品。确认下架？",
      submit: "确认资料和图片已准备好，提交审核？",
      sold: "确认标记为已售出？商品将停止公开展示。",
      rented: "确认标记为出租中？商品将停止公开展示。",
    };
    if (!window.confirm(confirmations[kind])) return;
    setBusy(true);
    try {
      const endpoint = ["approve", "reject", "reject_final"].includes(kind)
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
          ? "已提交上架，图片准备完成后公开展示。"
          : kind === "reject_final"
            ? "已最终驳回，暂存图片将按规则清理。"
            : kind === "reject"
              ? "已退回修改，审核原因已保存。"
              : kind === "offline"
                ? "商品已下架，前台不再展示。"
                : kind === "sold" || kind === "rented"
                  ? `商品已标记为${statusNames[kind]}。`
                  : "已提交，等待审核。",
      );
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
              ? `${row.product_code ?? row.id} · ${statusLabel} · 版本 ${row.version}`
              : "先保存草稿，再追加图片。"}
          </p>
        </div>
        <div className="heading-actions">
          {row?.status !== "rejected_final" && (
            <button
              type="button"
              className="button-outline"
              onClick={() => setShowPreview(!showPreview)}
            >
              {showPreview ? "收起预览" : "上架预览"}
            </button>
          )}
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
          {row?.status === "rejected_final" && (
            <p className="admin-notice">
              该商品已最终驳回，不能编辑或恢复上架。
            </p>
          )}
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
          {showPreview && (
            <section
              className="panel admin-listing-preview"
              aria-label="上架效果预览"
            >
              <span className="eyebrow">当前编辑内容预览 · 尚未发布</span>
              {product?.images[0]?.url && (
                <img src={product.images[0].url} alt="商品封面预览" />
              )}
              <div>
                <small>
                  {row?.product_code ||
                    `${form.trade_type === "sale" ? "QYCH" : "QYZH"} · 保存后自动生成`}
                </small>
                <h2>{form.title || "商品标题"}</h2>
                <p>{form.description || "商品介绍"}</p>
                <div className="preview-tags">
                  {tags
                    .split(/[,，\n]/)
                    .map((t) => t.trim())
                    .filter(Boolean)
                    .map((t) => (
                      <span key={t}>{t}</span>
                    ))}
                </div>
                <strong>
                  {form.price_amount
                    ? `¥ ${form.price_amount} ${unitLabels[form.price_unit] || ""}`
                    : "联系客服报价"}
                </strong>
                {form.trade_type === "rent" && (
                  <div className="preview-tags">
                    {Object.entries(quotes).map(([unit, amount]) => (
                      <span key={unit}>
                        {amount || "—"} {unitLabels[unit]}
                      </span>
                    ))}
                  </div>
                )}
                <small>QQ 账号和密码不会出现在公开预览中。</small>
              </div>
            </section>
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
              <fieldset
                disabled={
                  writeLocked ||
                  ["publishing", "rejected_final"].includes(row?.status)
                }
              >
                <label className="form-field">
                  {formFields.title.label}
                  <input
                    required
                    placeholder={formFields.title.placeholder}
                    maxLength={Math.max(
                      1000,
                      row?.source === "legacy"
                        ? Array.from(row.title ?? "").length
                        : 1000,
                    )}
                    value={form.title}
                    onChange={(e) => change("title", e.target.value)}
                  />
                </label>
                <p className="field-hint">
                  QQ 炫舞 · 商品编号：
                  {row?.product_code ||
                    `${form.trade_type === "sale" ? "QYCH" : "QYZH"}，保存后按历史编号自动顺延`}
                </p>
                {row?.source === "legacy" &&
                  Array.from(row.title ?? "").length > 1000 && (
                    <p className="admin-warning">
                      原站长标题可原样保留。若修改标题，请精简至 1000
                      字以内；详细内容可保留在商品介绍。
                    </p>
                  )}
                <div className="admin-grid">
                  <label className="form-field">
                    交易类型
                    <select
                      value={form.trade_type}
                      disabled={!!row}
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
                    {row && <small>租售类型已固定，如需更换请新建资产。</small>}
                  </label>
                  <label className="form-field">
                    交易分类
                    <select
                      required
                      value={form.category_id}
                      onChange={(e) => change("category_id", e.target.value)}
                    >
                      <option value="">选择交易分类</option>
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
                      onChange={(e) => {
                        const unit = e.target.value;
                        setQuotes((old) =>
                          form.trade_type === "rent" &&
                          form.price_amount &&
                          form.price_unit !== "unknown"
                            ? { ...old, [form.price_unit]: form.price_amount }
                            : old,
                        );
                        setForm((old) => ({
                          ...old,
                          price_unit: unit,
                          price_amount: quotes[unit] ?? "",
                        }));
                        setDirty(true);
                      }}
                    >
                      {unsupportedPrimary && (
                        <option value={form.price_unit} disabled>
                          {units[form.price_unit]}（当前分类不支持）
                        </option>
                      )}
                      {Object.entries(units)
                        .filter(([key]) =>
                          key === "unknown"
                            ? row?.price_unit === "unknown"
                            : !allowedUnits.includes(key)
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
                {form.trade_type === "rent" && (
                  <section className="subform-card">
                    <h3>更多租期报价</h3>
                    {unsupportedPrimary && (
                      <p className="admin-warning">
                        当前分类不支持原主报价单位，金额仍保留。请重新选择报价单位并核对金额。
                      </p>
                    )}
                    {unsupportedQuotes.length > 0 && (
                      <div className="admin-warning" role="status">
                        <p>
                          以下旧报价不适用于当前分类，尚未删除。请切回原分类，或确认后逐项移除。
                        </p>
                        {unsupportedQuotes.map(([unit, amount]) => (
                          <div key={unit}>
                            <span>
                              {amount || "未填写"} {unitLabels[unit] || unit}
                            </span>
                            <button
                              type="button"
                              className="text-button"
                              onClick={() => {
                                setQuotes((old) => {
                                  const next = { ...old };
                                  delete next[unit];
                                  return next;
                                });
                                setDirty(true);
                              }}
                            >
                              移除此报价
                            </button>
                          </div>
                        ))}
                      </div>
                    )}
                    <p className="field-hint">
                      可同时填写多种租期。上方报价优先展示；未填主价时使用这里的首个报价，不自动换算。
                    </p>
                    <div className="admin-grid">
                      {["hour", "day", "week", "month"]
                        .filter(
                          (unit) =>
                            unit !== form.price_unit &&
                            allowedUnits.includes(unit),
                        )
                        .map((unit) => (
                          <div key={unit}>
                            <label className="agreement-check">
                              <input
                                type="checkbox"
                                checked={Object.hasOwn(quotes, unit)}
                                onChange={(e) => {
                                  setQuotes((old) => {
                                    const next = { ...old };
                                    if (e.target.checked) next[unit] = "";
                                    else delete next[unit];
                                    return next;
                                  });
                                  setDirty(true);
                                }}
                              />
                              {unitLabels[unit]}
                            </label>
                            {Object.hasOwn(quotes, unit) && (
                              <input
                                aria-label={`${unitLabels[unit]}报价`}
                                type="number"
                                min="0"
                                step="0.01"
                                required
                                value={quotes[unit]}
                                onChange={(e) => {
                                  setQuotes((old) => ({
                                    ...old,
                                    [unit]: e.target.value,
                                  }));
                                  setDirty(true);
                                }}
                              />
                            )}
                          </div>
                        ))}
                    </div>
                  </section>
                )}
                <label className="form-field">
                  {formFields.description.label}
                  <textarea
                    rows={7}
                    required
                    placeholder={formFields.description.placeholder}
                    value={form.description}
                    onChange={(e) => change("description", e.target.value)}
                  />
                </label>
                {formFields.custom_tags.enabled && (
                  <label className="form-field">
                    {formFields.custom_tags.label}
                    <input
                      maxLength={600}
                      required={formFields.custom_tags.required}
                      placeholder={formFields.custom_tags.placeholder}
                      value={tags}
                      onChange={(e) => {
                        setTags(e.target.value);
                        setDirty(true);
                      }}
                    />
                    <small>
                      用逗号分隔，最多 20 个标签，新标签每个最多 5
                      字。历史标签保留在原始属性中，不计入这里的额度。
                    </small>
                  </label>
                )}
                {formFields.private_credentials.enabled && (
                  <section className="subform-card private-credentials">
                    <h3>{formFields.private_credentials.label}</h3>
                    <p className="field-hint">
                      {formFields.private_credentials.placeholder}{" "}
                      {row?.has_private_credentials
                        ? "已保存私密资料；留空保留现有内容。"
                        : "尚未保存私密资料。"}
                    </p>
                    <div className="admin-grid">
                      <label className="form-field">
                        QQ 账号
                        <input
                          autoComplete="off"
                          maxLength={15}
                          inputMode="numeric"
                          pattern="[1-9][0-9]{4,14}"
                          title="请输入 5 至 15 位 QQ 账号"
                          required={
                            !!privatePassword ||
                            (formFields.private_credentials.required &&
                              !row?.has_private_credentials)
                          }
                          value={privateAccount}
                          onChange={(e) => {
                            setPrivateAccount(e.target.value);
                            setDirty(true);
                          }}
                        />
                      </label>
                      <label className="form-field">
                        QQ 密码
                        <input
                          type={privateVisible ? "text" : "password"}
                          autoComplete="new-password"
                          maxLength={128}
                          required={
                            !!privateAccount ||
                            (formFields.private_credentials.required &&
                              !row?.has_private_credentials)
                          }
                          value={privatePassword}
                          onChange={(e) => {
                            setPrivatePassword(e.target.value);
                            setDirty(true);
                          }}
                        />
                      </label>
                    </div>
                    <label className="agreement-check">
                      <input
                        type="checkbox"
                        checked={privateVisible}
                        onChange={(e) => setPrivateVisible(e.target.checked)}
                      />
                      显示本次输入的密码
                    </label>
                  </section>
                )}
                <div className="admin-grid fixed-product-fields">
                  {groups.map((group) => {
                    const existing = attributes[group.key];
                    const selected = Array.isArray(existing)
                      ? existing.map(String)
                      : existing
                        ? [String(existing)]
                        : [];
                    const current = selected[0] ?? "";
                    const active = group.options.filter(
                      (option) => option.enabled !== false,
                    );
                    const legacy =
                      current &&
                      !active.some((option) => option.value === current);
                    return (
                      <label className="form-field" key={group.key}>
                        {group.label}
                        {group.required ? " *" : ""}
                        <select
                          aria-label={group.label}
                          required={!!group.required}
                          value={current}
                          onChange={(event) =>
                            change(
                              "attributes",
                              updateAttribute(
                                form.attributes,
                                group.key,
                                event.target.value,
                              ),
                            )
                          }
                        >
                          <option value="">
                            {group.required ? "请选择" : "暂不填写"}
                          </option>
                          {legacy && (
                            <option value={current}>
                              {group.options.find(
                                (option) => option.value === current,
                              )?.label ?? current}
                              （原资料）
                            </option>
                          )}
                          {active.map((option) => (
                            <option value={option.value} key={option.value}>
                              {option.label}
                            </option>
                          ))}
                        </select>
                        {legacy && (
                          <small>
                            原资料可保留，新增或更换请使用当前共享字典。
                          </small>
                        )}
                      </label>
                    );
                  })}
                </div>
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
                if (dirtyRef.current) {
                  if (next.version !== row?.version) {
                    setConflict(true);
                    setError(
                      "商品资料已更新，未保存编辑仍保留，请重新加载最新版本后继续。",
                    );
                    throw new Error("请重新加载最新版本后继续。");
                  }
                  return;
                }
                accept(editorRecordFromProduct(row, next));
                onSaved();
              }}
              onBusyChange={setImageBusy}
              onPendingChange={setImagePending}
              disabled={
                busy ||
                conflict ||
                (!!product && dirty) ||
                ["publishing", "rejected_final"].includes(row?.status)
              }
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
              <span>
                确认标题、报价和图片无误。管理员新建商品可直接确认上架；会员投稿需审核通过。
              </span>
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
                <span className="pill">{statusLabel}</span>
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
                {row.status === "publishing" && (
                  <MediaSyncPanel
                    productId={row.id}
                    onStateChange={(next) => void receiveMediaState(next)}
                  />
                )}
                {row.review_note && <p>最近审核说明：{row.review_note}</p>}
                {["pending", "published", "publishing"].includes(
                  row.status,
                ) && (
                  <label className="form-field">
                    操作原因
                    <input
                      value={reason}
                      onChange={(e) => setReason(e.target.value)}
                      placeholder="退回或下架时必填"
                      maxLength={500}
                      disabled={writeLocked}
                    />
                  </label>
                )}
                <div className="admin-actions">
                  {row.status === "pending" && (
                    <>
                      <button
                        className="button-white"
                        disabled={writeLocked || dirty || imagePending}
                        onClick={() => void action("approve")}
                      >
                        审核通过并上架
                      </button>
                      <button
                        className="button-outline"
                        disabled={writeLocked}
                        onClick={() => void action("reject")}
                      >
                        退回修改
                      </button>
                      <button
                        className="button-outline"
                        disabled={writeLocked}
                        onClick={() => void action("reject_final")}
                      >
                        最终驳回并清理图片
                      </button>
                    </>
                  )}
                  {["draft", "rejected", "offline", "sold", "rented"].includes(
                    row.status,
                  ) && (
                    <>
                      {row.source === "member" &&
                        ["draft", "rejected"].includes(row.status) && (
                          <button
                            className="button-outline"
                            disabled={writeLocked || dirty || imagePending}
                            onClick={() => void action("submit")}
                          >
                            提交审核
                          </button>
                        )}
                      {(row.source !== "member" ||
                        ["offline", "sold", "rented"].includes(row.status)) && (
                        <button
                          className="button-white"
                          disabled={writeLocked || dirty || imagePending}
                          onClick={() => void action("publish")}
                        >
                          确认上架
                        </button>
                      )}
                    </>
                  )}
                  {["published", "publishing"].includes(row.status) && (
                    <button
                      className="button-outline"
                      disabled={writeLocked}
                      onClick={() => void action("offline")}
                    >
                      {row.status === "publishing" ? "取消上架" : "下架商品"}
                    </button>
                  )}
                  {row.status === "published" && (
                    <button
                      className="button-outline"
                      disabled={writeLocked}
                      onClick={() =>
                        void action(
                          row.trade_type === "sale" ? "sold" : "rented",
                        )
                      }
                    >
                      {row.trade_type === "sale" ? "标记已售出" : "标记出租中"}
                    </button>
                  )}
                </div>
                <Link className="button-outline" to="/settings">
                  前往首页装修设置轮播文案与图片 →
                </Link>
              </section>
            )}
          </div>
        </>
      )}
    </section>
  );
}
