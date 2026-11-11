import { useEffect, useRef, useState, type FormEvent } from "react";
import { adminApi, type AdminRecord } from "../lib/admin-api";
import { api, uploadConfig } from "../lib/api";
import {
  resolveHomePresentation,
  readHomePresentation,
} from "../lib/home-content";
import { StateView } from "./shared";
import { HomeDesigner } from "./HomeDesigner";
import { CategoryFields } from "./CategoryFields";
import { GuideButton } from "./AdminGuide";
import {
  locations,
  makeCode,
  publicSite,
  unitLabels,
  useUnsavedChanges,
} from "../lib/admin-ux";

export type Resource = "contacts" | "pages" | "categories" | "settings";
const names = {
  contacts: "客服",
  pages: "说明页",
  categories: "发布模板",
  settings: "首页与站点配置",
};
const text = (error: unknown) =>
  error instanceof Error ? error.message : "操作失败，请重试。";
function Field({
  label,
  value,
  onChange,
  multiline = false,
  required = false,
}: {
  label: string;
  value: string;
  onChange: (value: string) => void;
  multiline?: boolean;
  required?: boolean;
}) {
  return (
    <label className="form-field">
      {label}
      {multiline ? (
        <textarea
          rows={8}
          value={value}
          onChange={(e) => onChange(e.target.value)}
          required={required}
        />
      ) : (
        <input
          value={value}
          onChange={(e) => onChange(e.target.value)}
          required={required}
        />
      )}
    </label>
  );
}
export function AdminResources({
  resource,
  onDirtyChange,
  onBusyChange,
}: {
  resource: Resource;
  onDirtyChange?: (dirty: boolean) => void;
  onBusyChange?: (busy: boolean) => void;
}) {
  const [items, setItems] = useState<AdminRecord[]>([]);
  const [selected, setSelected] = useState<string | null>(null);
  const [form, setForm] = useState<AdminRecord>({});
  const [json, setJson] = useState("{}");
  const [home, setHome] = useState(() => resolveHomePresentation());
  const [repairHome, setRepairHome] = useState(false);
  const [loading, setLoading] = useState(true);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [notice, setNotice] = useState("");
  const [editing, setEditing] = useState(false);
  const [dirty, setDirty] = useState(false);
  const sequence = useRef(0);
  useUnsavedChanges(dirty, busy);
  useEffect(() => onDirtyChange?.(dirty), [dirty, onDirtyChange]);
  useEffect(
    () => onBusyChange?.(busy || loading),
    [busy, loading, onBusyChange],
  );
  const idOf = (item: AdminRecord) =>
    String(
      resource === "pages"
        ? item.slug
        : resource === "settings"
          ? item.key
          : item.id,
    );
  async function load() {
    const current = ++sequence.current;
    setLoading(true);
    setError("");
    try {
      const rows = await adminApi.list(resource);
      if (current !== sequence.current) return;
      setItems(
        resource === "settings"
          ? rows.filter((row) => row.key === "home_presentation")
          : rows,
      );
      if (resource === "settings")
        edit(rows.find((row) => row.key === "home_presentation"));
    } catch (e) {
      if (current === sequence.current) setError(text(e));
    } finally {
      if (current === sequence.current) setLoading(false);
    }
  }
  useEffect(() => {
    setEditing(false);
    setNotice("");
    setDirty(false);
    void load();
    return () => {
      sequence.current++;
    };
  }, [resource]);
  function edit(item?: AdminRecord) {
    if (busy) return;
    if (dirty && !window.confirm("还有未保存的修改，确认切换？")) return;
    setSelected(item ? idOf(item) : null);
    setError("");
    setNotice("");
    setDirty(false);
    setEditing(true);
    setRepairHome(false);
    const next: AdminRecord = item
      ? { ...item }
      : {
          enabled: true,
          published: false,
          trade_type: "sale",
          price_units: ["account"],
          filter_config: [],
          purpose: ["general", "sale", "rent", "listing", "support"],
          channel: "wechat",
          key: "home_presentation",
          code: makeCode("category"),
        };
    setForm(next);
    setJson(
      JSON.stringify(
        resource === "categories"
          ? (next.filter_config ?? [])
          : (next.value ?? {}),
        null,
        2,
      ),
    );
    if (resource === "settings") {
      try {
        setHome(readHomePresentation(next.value));
      } catch (e) {
        setRepairHome(true);
        setError(`${text(e)} 已保留原始内容，请修正下方配置后保存。`);
      }
    }
  }
  function change(key: string, value: unknown) {
    setForm((old) => ({ ...old, [key]: value }));
    setDirty(true);
    setNotice("");
  }
  async function save(event: FormEvent) {
    event.preventDefault();
    if (
      resource === "categories" &&
      selected &&
      form.enabled === false &&
      items.find((item) => idOf(item) === selected)?.enabled !== false &&
      !window.confirm(
        "停用这个分类后，其中的公开商品也会隐藏。确认停用并保存？",
      )
    )
      return;
    setError("");
    setNotice("");
    setBusy(true);
    try {
      let input: AdminRecord;
      let key = selected ?? undefined;
      if (resource === "contacts")
        input = {
          name: form.name ?? "",
          purpose: form.purpose ?? [],
          channel: form.channel,
          contact_value: form.contact_value ?? "",
          qr_media_id: form.qr_media_id || null,
          hours: form.hours ?? "",
          enabled: !!form.enabled,
        };
      else if (resource === "pages") {
        key = selected ?? form.slug;
        if (!key || !/^[a-z0-9-]+$/.test(key))
          throw new Error("页面标识仅支持小写字母、数字和短横线。");
        input = {
          title: form.title ?? "",
          body: form.body ?? "",
          published: !!form.published,
        };
      } else if (resource === "categories") {
        input = {
          name: form.name ?? "",
          price_units: form.price_units ?? [],
          filter_config: JSON.parse(json),
          sort_order: Number(form.sort_order ?? 0),
          enabled: !!form.enabled,
        };
        if (!selected) {
          input.code = form.code;
          input.trade_type = form.trade_type;
        }
      } else {
        key = selected ?? form.key;
        if (!key) throw new Error("请选择配置项。");
        input = {
          value:
            key === "home_presentation"
              ? readHomePresentation(repairHome ? JSON.parse(json) : home)
              : JSON.parse(json),
        };
        if (
          key === "home_presentation" &&
          input.value.shortcuts.some(
            (s: { href: string }) =>
              !/^\/(accounts(?:\?[^?]*)?|publish)$/.test(s.href),
          )
        )
          throw new Error("快捷入口只支持 /accounts、其筛选地址或 /publish。");
      }
      const result = await adminApi.save(resource, key, input);
      setSelected(key ?? String(result.id));
      setDirty(false);
      setNotice(
        resource === "settings"
          ? "首页已保存，刷新用户首页即可查看。"
          : resource === "pages"
            ? form.published
              ? "已保存并公开，用户可以访问此页面。"
              : "已保存为未公开页面，用户暂时看不到。"
            : form.enabled
              ? "已保存并启用，用户页面刷新后生效。"
              : "已保存为停用状态，用户页面不再展示。",
      );
      try {
        const rows = await adminApi.list(resource);
        setItems(
          resource === "settings"
            ? rows.filter((row) => row.key === "home_presentation")
            : rows,
        );
      } catch {
        setNotice("内容已保存，但列表刷新失败。重新进入此页面可读取最新结果。");
      }
    } catch (e) {
      setError(
        e instanceof SyntaxError
          ? "配置格式有误，请检查 JSON 的括号和引号。"
          : text(e),
      );
    } finally {
      setBusy(false);
    }
  }
  async function uploadQr(file?: File) {
    if (!file) return;
    setBusy(true);
    setError("");
    setNotice("正在上传二维码…");
    try {
      const image = await api.upload(file);
      setForm((old) => ({ ...old, qr_media_id: image.id, qr_url: image.url }));
      setDirty(true);
      setNotice("二维码已上传，保存配置后生效。");
    } catch (e) {
      setError(text(e));
      setNotice("");
    } finally {
      setBusy(false);
    }
  }
  return (
    <section className="admin-resource">
      <div className="admin-heading">
        <div>
          <span className="eyebrow">网站运营</span>
          <h1>{locations[resource].title}</h1>
          <p>{locations[resource].hint}</p>
        </div>
        <div className="heading-actions">
          {(resource === "settings" || resource === "contacts") && (
            <GuideButton kind={resource === "settings" ? "home" : "contact"} />
          )}
          {resource !== "settings" && (
            <button
              className="button-white"
              disabled={busy || loading}
              onClick={() => edit()}
            >
              新增{names[resource]}
            </button>
          )}
        </div>
      </div>
      {loading ? (
        <StateView loading />
      ) : (
        <div
          className={`admin-resource-layout ${resource === "settings" ? "full-width-resource" : ""}`}
        >
          {resource !== "settings" && (
            <div
              className="admin-resource-list"
              aria-label={`${names[resource]}列表`}
            >
              {items.map((item) => (
                <button
                  key={idOf(item)}
                  className={selected === idOf(item) ? "active" : ""}
                  disabled={busy}
                  onClick={() => edit(item)}
                >
                  <strong>{item.name ?? item.title ?? item.key}</strong>
                  <span>
                    {item.enabled === false || item.published === false
                      ? "未启用"
                      : "已启用"}
                  </span>
                </button>
              ))}
              {!items.length && <p className="muted">暂未配置。</p>}
            </div>
          )}
          {editing ? (
            <form
              className="panel member-form admin-resource-form"
              onSubmit={save}
            >
              <fieldset disabled={busy}>
                {resource !== "settings" && (
                  <div className="location-note">
                    <strong>修改位置</strong>
                    <span>{locations[resource].where}</span>
                  </div>
                )}
                {resource === "contacts" && (
                  <>
                    <Field
                      label="客服名称"
                      value={form.name ?? ""}
                      onChange={(v) => change("name", v)}
                      required
                    />
                    <label className="form-field">
                      联系方式类型
                      <select
                        value={form.channel ?? "wechat"}
                        onChange={(e) => change("channel", e.target.value)}
                      >
                        <option value="wechat">微信</option>
                        <option value="qq">QQ</option>
                        <option value="phone">电话</option>
                        <option value="url">网站</option>
                      </select>
                    </label>
                    <Field
                      label="联系方式"
                      value={form.contact_value ?? ""}
                      onChange={(v) => change("contact_value", v)}
                    />
                    <Field
                      label="服务时间（选填）"
                      value={form.hours ?? ""}
                      onChange={(v) => change("hours", v)}
                    />
                    <fieldset className="admin-purposes">
                      <legend>显示在以下咨询入口</legend>
                      {[
                        ["general", "一般咨询"],
                        ["sale", "账号购买"],
                        ["rent", "账号租赁"],
                        ["listing", "发布协助"],
                        ["support", "平台支持"],
                      ].map(([key, label]) => (
                        <label className="agreement-check" key={key}>
                          <input
                            type="checkbox"
                            checked={(form.purpose ?? []).includes(key)}
                            onChange={(e) =>
                              change(
                                "purpose",
                                e.target.checked
                                  ? [...(form.purpose ?? []), key]
                                  : (form.purpose ?? []).filter(
                                      (p: string) => p !== key,
                                    ),
                              )
                            }
                          />
                          {label}
                        </label>
                      ))}
                    </fieldset>
                    <label className="form-field">
                      联系二维码（选填）
                      <input
                        type="file"
                        accept="image/jpeg,image/png,image/webp"
                        onChange={(e) => {
                          void uploadQr(e.target.files?.[0]);
                          e.target.value = "";
                        }}
                      />
                      <small>
                        JPG / PNG / WebP，单张不超过{" "}
                        {Math.round(uploadConfig.maxBytes / 1024 / 1024)} MB。
                      </small>
                    </label>
                    {form.qr_url && (
                      <img
                        className="admin-qr-preview"
                        src={form.qr_url}
                        alt="客服二维码预览"
                      />
                    )}
                    {form.qr_media_id && (
                      <button
                        type="button"
                        className="text-button"
                        onClick={() => {
                          change("qr_media_id", null);
                          change("qr_url", "");
                        }}
                      >
                        移除二维码
                      </button>
                    )}
                  </>
                )}
                {resource === "pages" && (
                  <>
                    {!selected ? (
                      <label className="form-field">
                        选择页面用途
                        <select
                          required
                          value={form.slug ?? ""}
                          onChange={(e) => change("slug", e.target.value)}
                        >
                          <option value="">请选择</option>
                          {[
                            ["about", "关于清叶"],
                            ["process", "咨询流程"],
                            ["publish", "上架说明"],
                            ["terms", "服务协议"],
                            ["privacy", "隐私政策"],
                            ["verification", "资料核验说明"],
                            ["recommendation", "推荐规则说明"],
                            ["listing", "发布须知"],
                          ].map(([key, label]) => (
                            <option
                              key={key}
                              value={key}
                              disabled={items.some((item) => item.slug === key)}
                            >
                              {label}
                            </option>
                          ))}
                        </select>
                        <small>已存在的页面请从左侧选择，避免重复创建。</small>
                      </label>
                    ) : (
                      <p className="field-hint">
                        当前页面：{form.title} · 页面地址保持不变
                      </p>
                    )}
                    <Field
                      label="页面标题"
                      value={form.title ?? ""}
                      onChange={(v) => change("title", v)}
                      required
                    />
                    <Field
                      label="正文（纯文本）"
                      value={form.body ?? ""}
                      onChange={(v) => change("body", v)}
                      multiline
                    />
                  </>
                )}
                {resource === "categories" && (
                  <>
                    <Field
                      label="发布模板名称"
                      value={form.name ?? ""}
                      onChange={(v) => change("name", v)}
                      required
                    />
                    {!selected && (
                      <>
                        <label className="form-field">
                          交易类型
                          <select
                            value={form.trade_type}
                            onChange={(e) => {
                              change("trade_type", e.target.value);
                              change(
                                "price_units",
                                e.target.value === "sale"
                                  ? ["account"]
                                  : ["day"],
                              );
                            }}
                          >
                            <option value="sale">出售</option>
                            <option value="rent">租赁</option>
                          </select>
                        </label>
                      </>
                    )}
                    <div className="subform-card">
                      <h3>允许使用的报价单位</h3>
                      <div className="checkbox-options">
                        {Object.entries(unitLabels)
                          .filter(([key]) =>
                            key === "unknown"
                              ? (form.price_units ?? []).includes(key)
                              : form.trade_type === "sale"
                                ? key === "account"
                                : form.trade_type === "rent"
                                  ? key !== "account"
                                  : true,
                          )
                          .map(([key, label]) => (
                            <label key={key} className="agreement-check">
                              <input
                                type="checkbox"
                                checked={(form.price_units ?? []).includes(key)}
                                onChange={(e) =>
                                  change(
                                    "price_units",
                                    e.target.checked
                                      ? [...(form.price_units ?? []), key]
                                      : (form.price_units ?? []).filter(
                                          (u: string) => u !== key,
                                        ),
                                  )
                                }
                              />
                              {label}
                            </label>
                          ))}
                      </div>
                      <p className="field-hint">
                        不勾选表示不额外限制；出售与租赁仍受各自类型约束。
                      </p>
                    </div>
                    <label className="form-field">
                      显示顺序
                      <input
                        type="number"
                        step="1"
                        value={form.sort_order ?? 0}
                        onChange={(e) => change("sort_order", e.target.value)}
                      />
                      <small>数字越小，分类排得越靠前。</small>
                    </label>
                    <CategoryFields
                      value={json}
                      onChange={(v) => {
                        setJson(v);
                        setDirty(true);
                        setNotice("");
                      }}
                    />
                    <details className="admin-advanced">
                      <summary>高级配置（供技术维护）</summary>
                      <p>
                        一般操作请使用上方表单。保留已有编码，避免影响商品关联。
                      </p>
                      <Field
                        label="筛选分组（JSON）"
                        value={json}
                        onChange={(v) => {
                          setJson(v);
                          setDirty(true);
                        }}
                        multiline
                      />
                      <p className="muted">
                        保留已有分组和选项的编码。停用已有选项时使用 enabled:
                        false，避免破坏已发布商品。
                      </p>
                    </details>
                  </>
                )}
                {resource === "settings" &&
                  ((selected ?? form.key) === "home_presentation" &&
                  !repairHome ? (
                    <HomeDesigner
                      value={home}
                      onChange={(v) => {
                        setHome(v);
                        setDirty(true);
                        setNotice("");
                      }}
                    />
                  ) : (
                    <Field
                      label={`${selected} 配置（JSON）`}
                      value={json}
                      onChange={(v) => {
                        setJson(v);
                        setDirty(true);
                      }}
                      multiline
                    />
                  ))}
                {resource !== "settings" && (
                  <label className="agreement-check">
                    <input
                      type="checkbox"
                      checked={
                        !!form[resource === "pages" ? "published" : "enabled"]
                      }
                      onChange={(e) =>
                        change(
                          resource === "pages" ? "published" : "enabled",
                          e.target.checked,
                        )
                      }
                    />
                    {resource === "pages"
                      ? "公开展示：用户可以访问此页面"
                      : resource === "contacts"
                        ? "启用这位客服：在选中的咨询入口显示"
                        : "启用这个分类：允许商品在前台展示"}
                  </label>
                )}
                {error && (
                  <p className="form-error" role="alert">
                    {error}
                  </p>
                )}
                {notice && (
                  <p className="admin-success" role="status">
                    {notice}
                  </p>
                )}
                <div className="save-bar">
                  <span>
                    {busy
                      ? "正在处理，请稍候…"
                      : dirty
                        ? "有修改未保存"
                        : "修改后，请点击保存"}
                  </span>
                  <button className="button-white" type="submit">
                    {busy
                      ? "正在保存…"
                      : resource === "settings"
                        ? "保存首页内容"
                        : "保存修改"}
                  </button>
                  {notice && publicSite && (
                    <a
                      className="button-outline"
                      href={
                        resource === "pages" && selected
                          ? `${publicSite}/help/${selected}`
                          : publicSite
                      }
                      target="_blank"
                      rel="noreferrer"
                    >
                      查看用户站 ↗
                    </a>
                  )}
                </div>
              </fieldset>
            </form>
          ) : (
            <div className="panel admin-empty">
              <h3>
                {items.length
                  ? "从左侧选择要修改的内容"
                  : `还没有${names[resource]}`}
              </h3>
              <p>
                {items.length
                  ? "点击名称即可打开。保存前可以继续调整。"
                  : `点击右上角“新增${names[resource]}”开始配置。`}
              </p>
              <div className="location-note">
                <strong>会显示在哪里？</strong>
                <span>{locations[resource].where}</span>
              </div>
            </div>
          )}
        </div>
      )}
      {!editing && error && (
        <StateView error={new Error(error)} onRetry={() => void load()} />
      )}
    </section>
  );
}
