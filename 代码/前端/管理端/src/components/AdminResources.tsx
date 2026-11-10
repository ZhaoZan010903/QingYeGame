import { useEffect, useState, type FormEvent } from "react";
import { adminApi, type AdminRecord } from "../lib/admin-api";
import { api, uploadConfig } from "../lib/api";
import {
  resolveHomePresentation,
  readHomePresentation,
  type HomePresentation,
} from "../lib/home-content";
import { StateView } from "./shared";

export type Resource = "contacts" | "pages" | "categories" | "settings";
const names = {
  contacts: "客服",
  pages: "说明页",
  categories: "分类",
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
function HomeFields({
  value,
  onChange,
}: {
  value: HomePresentation;
  onChange: (next: HomePresentation) => void;
}) {
  return (
    <>
      <h3>首页甄选</h3>
      <div className="admin-grid">
        <Field
          label="甄选标题"
          value={value.editorial.title}
          onChange={(title) =>
            onChange({ ...value, editorial: { ...value.editorial, title } })
          }
        />
        <Field
          label="甄选说明"
          value={value.editorial.description}
          onChange={(description) =>
            onChange({
              ...value,
              editorial: { ...value.editorial, description },
            })
          }
        />
        <label className="form-field">
          轮播间隔（秒）
          <input
            type="number"
            min="4"
            max="30"
            step="0.5"
            value={value.editorial.intervalMs / 1000}
            onChange={(e) =>
              onChange({
                ...value,
                editorial: {
                  ...value.editorial,
                  intervalMs: Number(e.target.value) * 1000,
                },
              })
            }
          />
        </label>
      </div>
      <h3>商品分区</h3>
      {value.sections.map((section, index) => (
        <div className="admin-grid admin-subsection" key={section.source}>
          <Field
            label={section.source === "hot" ? "热门分区标题" : "最新分区标题"}
            value={section.title}
            onChange={(title) =>
              onChange({
                ...value,
                sections: value.sections.map((item, i) =>
                  i === index ? { ...item, title } : item,
                ),
              })
            }
          />
          <Field
            label="分区说明"
            value={section.description}
            onChange={(description) =>
              onChange({
                ...value,
                sections: value.sections.map((item, i) =>
                  i === index ? { ...item, description } : item,
                ),
              })
            }
          />
          <label className="form-field">
            展示数量
            <input
              type="number"
              min="1"
              max="24"
              value={section.limit}
              onChange={(e) =>
                onChange({
                  ...value,
                  sections: value.sections.map((item, i) =>
                    i === index
                      ? { ...item, limit: Number(e.target.value) }
                      : item,
                  ),
                })
              }
            />
          </label>
        </div>
      ))}
      <h3>快捷入口</h3>
      {value.shortcuts.map((shortcut, index) => (
        <div className="admin-grid admin-subsection" key={index}>
          <Field
            label="入口标题"
            value={shortcut.title}
            onChange={(title) =>
              onChange({
                ...value,
                shortcuts: value.shortcuts.map((item, i) =>
                  i === index ? { ...item, title } : item,
                ),
              })
            }
          />
          <Field
            label="入口说明"
            value={shortcut.description}
            onChange={(description) =>
              onChange({
                ...value,
                shortcuts: value.shortcuts.map((item, i) =>
                  i === index ? { ...item, description } : item,
                ),
              })
            }
          />
          <Field
            label="站内地址（账号集市或发布页）"
            value={shortcut.href}
            onChange={(href) =>
              onChange({
                ...value,
                shortcuts: value.shortcuts.map((item, i) =>
                  i === index ? { ...item, href } : item,
                ),
              })
            }
          />
        </div>
      ))}
      <h3>发布邀请</h3>
      <Field
        label="标题"
        value={value.publishing.title}
        onChange={(title) =>
          onChange({ ...value, publishing: { ...value.publishing, title } })
        }
      />
      <Field
        label="说明"
        value={value.publishing.description}
        onChange={(description) =>
          onChange({
            ...value,
            publishing: { ...value.publishing, description },
          })
        }
      />
      <Field
        label="按钮文字"
        value={value.publishing.action}
        onChange={(action) =>
          onChange({ ...value, publishing: { ...value.publishing, action } })
        }
      />
    </>
  );
}

export function AdminResources({ resource }: { resource: Resource }) {
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
  const idOf = (item: AdminRecord) =>
    String(
      resource === "pages"
        ? item.slug
        : resource === "settings"
          ? item.key
          : item.id,
    );
  async function load() {
    setLoading(true);
    setError("");
    try {
      const rows = await adminApi.list(resource);
      setItems(
        resource === "settings"
          ? rows.filter((row) => row.key === "home_presentation")
          : rows,
      );
    } catch (e) {
      setError(text(e));
    } finally {
      setLoading(false);
    }
  }
  useEffect(() => {
    setEditing(false);
    setNotice("");
    setDirty(false);
    void load();
  }, [resource]);
  function edit(item?: AdminRecord) {
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
      setNotice("已保存。公开页面刷新后读取最新内容。");
      const rows = await adminApi.list(resource);
      setItems(
        resource === "settings"
          ? rows.filter((row) => row.key === "home_presentation")
          : rows,
      );
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
          <h2>{names[resource]}</h2>
          <p>修改后由服务端保存，公开页面统一读取。</p>
        </div>
        <button className="button-white" onClick={() => edit()}>
          {resource === "settings" ? "配置首页" : `新增${names[resource]}`}
        </button>
      </div>
      {loading ? (
        <StateView loading />
      ) : (
        <div className="admin-resource-layout">
          <div
            className="admin-resource-list"
            aria-label={`${names[resource]}列表`}
          >
            {items.map((item) => (
              <button
                key={idOf(item)}
                className={selected === idOf(item) ? "active" : ""}
                onClick={() => edit(item)}
              >
                <strong>{item.name ?? item.title ?? item.key}</strong>
                <span>
                  {resource === "settings"
                    ? "配置项"
                    : item.enabled === false || item.published === false
                      ? "未启用"
                      : "已启用"}
                </span>
              </button>
            ))}
            {!items.length && <p className="muted">暂未配置。</p>}
          </div>
          {editing ? (
            <form
              className="panel member-form admin-resource-form"
              onSubmit={save}
            >
              <fieldset disabled={busy}>
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
                    <Field
                      label="页面标识（例如 terms、privacy、about）"
                      value={selected ?? form.slug ?? ""}
                      onChange={(v) => change("slug", v)}
                      required
                    />
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
                      label="分类名称"
                      value={form.name ?? ""}
                      onChange={(v) => change("name", v)}
                      required
                    />
                    {!selected && (
                      <>
                        <Field
                          label="分类编码（英文或数字）"
                          value={form.code ?? ""}
                          onChange={(v) => change("code", v)}
                          required
                        />
                        <label className="form-field">
                          交易类型
                          <select
                            value={form.trade_type}
                            onChange={(e) =>
                              change("trade_type", e.target.value)
                            }
                          >
                            <option value="sale">出售</option>
                            <option value="rent">租赁</option>
                          </select>
                        </label>
                      </>
                    )}
                    <Field
                      label="可选报价单位（account/hour/day/week/month，英文逗号分隔）"
                      value={(form.price_units ?? []).join(",")}
                      onChange={(v) =>
                        change(
                          "price_units",
                          v
                            .split(",")
                            .map((s) => s.trim())
                            .filter(Boolean),
                        )
                      }
                    />
                    <label className="form-field">
                      显示顺序
                      <input
                        type="number"
                        value={form.sort_order ?? 0}
                        onChange={(e) => change("sort_order", e.target.value)}
                      />
                    </label>
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
                  </>
                )}
                {resource === "settings" &&
                  ((selected ?? form.key) === "home_presentation" &&
                  !repairHome ? (
                    <HomeFields
                      value={home}
                      onChange={(v) => {
                        setHome(v);
                        setDirty(true);
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
                    {resource === "pages" ? "公开展示" : "启用"}
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
                <button className="button-white" type="submit">
                  {busy ? "正在保存…" : "保存配置"}
                </button>
              </fieldset>
            </form>
          ) : (
            <div className="panel admin-empty">选择一项查看和编辑。</div>
          )}
        </div>
      )}
      {!editing && error && (
        <StateView error={new Error(error)} onRetry={() => void load()} />
      )}
    </section>
  );
}
