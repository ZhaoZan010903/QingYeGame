import { useEffect, useState, type FormEvent } from "react";
import { adminApi } from "../lib/admin-api";
import { useUnsavedChanges } from "../lib/admin-ux";
import { AdminResources } from "./AdminResources";
import {
  defaultPublishForm,
  resolvePublishForm,
} from "../../../共享/publish-form";

type Field = {
  label: string;
  placeholder: string;
  required: boolean;
  enabled: boolean;
};
type Fields = Record<string, Field>;
export const defaultPublishFields: Fields = defaultPublishForm.fields;

function GeneralFormSettings({
  onDirtyChange,
  onBusyChange,
}: {
  onDirtyChange: (dirty: boolean) => void;
  onBusyChange: (busy: boolean) => void;
}) {
  const [fields, setFields] = useState<Fields>(defaultPublishFields);
  const [busy, setBusy] = useState(true);
  const [dirty, setDirty] = useState(false);
  const [error, setError] = useState("");
  const [notice, setNotice] = useState("");
  useUnsavedChanges(dirty, busy);
  useEffect(() => onDirtyChange(dirty), [dirty, onDirtyChange]);
  useEffect(() => onBusyChange(busy), [busy, onBusyChange]);
  async function load() {
    setBusy(true);
    setError("");
    try {
      const rows = await adminApi.list("settings");
      const value = resolvePublishForm(
        rows.find((row) => row.key === "publish_form")?.value,
      ).fields;
      setFields(
        Object.fromEntries(
          Object.entries(defaultPublishFields).map(([key, field]) => [
            key,
            { ...field, ...value[key as keyof typeof value] },
          ]),
        ),
      );
    } catch (e) {
      setError(e instanceof Error ? e.message : "读取配置失败，请重试");
    } finally {
      setBusy(false);
    }
  }
  useEffect(() => {
    void load();
  }, []);
  function update(key: string, patch: Partial<Field>) {
    setFields((old) => ({ ...old, [key]: { ...old[key], ...patch } }));
    setDirty(true);
    setNotice("");
  }
  async function save(event: FormEvent) {
    event.preventDefault();
    setBusy(true);
    setError("");
    setNotice("");
    try {
      await adminApi.save("settings", "publish_form", { value: { fields } });
      setDirty(false);
      setNotice("发布表单已更新，用户下次打开表单时生效。");
    } catch (e) {
      setError(e instanceof Error ? e.message : "保存失败");
    } finally {
      setBusy(false);
    }
  }
  return (
    <section className="publish-form-settings">
      <div className="admin-heading">
        <div>
          <span className="eyebrow">商品录入规则</span>
          <h1>发布表单设置</h1>
          <p>在一处设置输入提示、必填项、租售模板和下拉选项，两端共用。</p>
        </div>
      </div>
      <form className="panel member-form" onSubmit={save}>
        <h2>通用输入框</h2>
        <p className="field-hint">
          游戏统一为 QQ 炫舞，商品编号自动生成。标题与介绍为固定必填项。
        </p>
        <fieldset disabled={busy} className="admin-grid">
          {Object.entries(fields).map(([key, field]) => (
            <div className="subform-card" key={key}>
              <strong>{defaultPublishFields[key].label}</strong>
              <label className="form-field">
                显示名称
                <input
                  required
                  maxLength={40}
                  value={field.label}
                  onChange={(e) => update(key, { label: e.target.value })}
                />
              </label>
              <label className="form-field">
                填写提示
                <textarea
                  rows={2}
                  maxLength={200}
                  value={field.placeholder}
                  onChange={(e) => update(key, { placeholder: e.target.value })}
                />
              </label>
              <div className="checkbox-options">
                <label className="agreement-check">
                  <input
                    type="checkbox"
                    disabled={key === "title" || key === "description"}
                    checked={field.enabled}
                    onChange={(e) =>
                      update(key, {
                        enabled: e.target.checked,
                        ...(!e.target.checked ? { required: false } : {}),
                      })
                    }
                  />
                  显示输入框
                </label>
                <label className="agreement-check">
                  <input
                    type="checkbox"
                    disabled={
                      !field.enabled || key === "title" || key === "description"
                    }
                    checked={field.required}
                    onChange={(e) =>
                      update(key, { required: e.target.checked })
                    }
                  />
                  必填
                </label>
              </div>
            </div>
          ))}
        </fieldset>
        {error && (
          <p className="form-error" role="alert">
            {error}
            <button
              type="button"
              className="text-button"
              onClick={() => void load()}
            >
              重新读取
            </button>
          </p>
        )}
        {notice && (
          <p role="status" className="admin-success">
            {notice}
          </p>
        )}
        <div className="save-bar">
          <span>{dirty ? "有修改未保存" : "通用规则"}</span>
          <button className="button-white" disabled={busy || !dirty}>
            保存通用表单
          </button>
        </div>
      </form>
    </section>
  );
}

export function PublishFormSettings() {
  const [tab, setTab] = useState("general");
  const [dirty, setDirty] = useState(false);
  const [busy, setBusy] = useState(true);
  function changeTab(next: string) {
    if (next === tab || busy) return;
    if (dirty && !window.confirm("当前有未保存的修改。确认放弃并切换？"))
      return;
    setDirty(false);
    setTab(next);
  }
  return (
    <>
      <nav className="status-tabs" aria-label="发布表单设置区域">
        <button
          disabled={busy}
          className={tab === "general" ? "active" : ""}
          onClick={() => changeTab("general")}
        >
          通用输入框
        </button>
        <button
          disabled={busy}
          className={tab === "templates" ? "active" : ""}
          onClick={() => changeTab("templates")}
        >
          租售模板与下拉选项
        </button>
      </nav>
      {tab === "general" ? (
        <GeneralFormSettings onDirtyChange={setDirty} onBusyChange={setBusy} />
      ) : (
        <AdminResources
          resource="categories"
          onDirtyChange={setDirty}
          onBusyChange={setBusy}
        />
      )}
    </>
  );
}
