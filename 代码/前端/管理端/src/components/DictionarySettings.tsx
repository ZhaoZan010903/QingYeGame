import { useEffect, useState, type FormEvent } from "react";
import { ArrowDown, ArrowUp, Plus, Search } from "lucide-react";
import { adminApi } from "../lib/admin-api";
import {
  addDictionaryOption,
  validateDictionary,
  fixedProductFields,
  type DictionaryGroup,
  type DictionaryKey,
} from "../lib/dictionaries";
import { useUnsavedChanges } from "../lib/admin-ux";
import { AdminResources } from "./AdminResources";

export function DictionaryOptions({
  group,
  query,
  onChange,
}: {
  group: DictionaryGroup;
  query: string;
  onChange: (group: DictionaryGroup) => void;
}) {
  const matches = group.options
    .map((option, index) => ({ option, index }))
    .filter(({ option }) =>
      `${option.label} ${option.value}`
        .toLocaleLowerCase()
        .includes(query.trim().toLocaleLowerCase()),
    );
  function move(index: number, delta: number) {
    const options = [...group.options],
      target = index + delta;
    if (target < 0 || target >= options.length) return;
    [options[index], options[target]] = [options[target], options[index]];
    onChange({
      ...group,
      options: options.map((option, sort_order) => ({ ...option, sort_order })),
    });
  }
  return (
    <div className="dictionary-options">
      {matches.map(({ option, index }) => (
        <div
          className={`dictionary-row ${option.enabled ? "" : "disabled"}`}
          key={option.value}
        >
          <label className="form-field">
            <span className="sr-only">选项名称 {index + 1}</span>
            <input
              value={option.label}
              maxLength={80}
              required
              onChange={(event) =>
                onChange({
                  ...group,
                  options: group.options.map((value, i) =>
                    i === index
                      ? { ...value, label: event.target.value }
                      : value,
                  ),
                })
              }
            />
            <small>标识：{option.value}</small>
          </label>
          <label className="agreement-check">
            <input
              type="checkbox"
              checked={option.enabled}
              onChange={(event) =>
                onChange({
                  ...group,
                  options: group.options.map((value, i) =>
                    i === index
                      ? { ...value, enabled: event.target.checked }
                      : value,
                  ),
                })
              }
            />
            {option.enabled ? "可选" : "已停用"}
          </label>
          <div className="dictionary-order">
            <button
              type="button"
              className="icon-button"
              title="上移"
              aria-label={`上移${option.label}`}
              disabled={index === 0 || !!query.trim()}
              onClick={() => move(index, -1)}
            >
              <ArrowUp size={16} />
            </button>
            <button
              type="button"
              className="icon-button"
              title="下移"
              aria-label={`下移${option.label}`}
              disabled={index === group.options.length - 1 || !!query.trim()}
              onClick={() => move(index, 1)}
            >
              <ArrowDown size={16} />
            </button>
          </div>
        </div>
      ))}
      {!matches.length && <p className="admin-empty">没有找到对应选项。</p>}
    </div>
  );
}

function SharedDictionaries({
  onDirtyChange,
  onBusyChange,
}: {
  onDirtyChange: (dirty: boolean) => void;
  onBusyChange: (busy: boolean) => void;
}) {
  const [groups, setGroups] = useState<DictionaryGroup[]>([]),
    [selected, setSelected] = useState<DictionaryKey>("server");
  const [busy, setBusy] = useState(true),
    [dirty, setDirty] = useState(false),
    [error, setError] = useState(""),
    [notice, setNotice] = useState("");
  const [query, setQuery] = useState(""),
    [newLabel, setNewLabel] = useState("");
  useUnsavedChanges(dirty, busy);
  useEffect(() => onDirtyChange(dirty), [dirty, onDirtyChange]);
  useEffect(() => onBusyChange(busy), [busy, onBusyChange]);
  async function load() {
    setBusy(true);
    setError("");
    try {
      setGroups(await adminApi.dictionaries());
      setDirty(false);
    } catch (e) {
      setError(e instanceof Error ? e.message : "读取字典失败");
    } finally {
      setBusy(false);
    }
  }
  useEffect(() => {
    void load();
  }, []);
  const group = groups.find((item) => item.key === selected);
  function update(next: DictionaryGroup) {
    setGroups((old) =>
      old.map((value) => (value.key === next.key ? next : value)),
    );
    setDirty(true);
    setNotice("");
  }
  async function save(event: FormEvent) {
    event.preventDefault();
    if (!group) return;
    setBusy(true);
    setError("");
    try {
      validateDictionary(group);
      const saved = await adminApi.saveDictionary(group);
      setGroups((old) =>
        old.map((value) => (value.key === saved.key ? saved : value)),
      );
      setDirty(false);
      setNotice("已保存，出售与租赁共用这组最新选项。");
    } catch (e) {
      setError(e instanceof Error ? e.message : "保存失败");
    } finally {
      setBusy(false);
    }
  }
  function switchGroup(key: DictionaryKey) {
    if (busy || key === selected) return;
    if (dirty && !window.confirm("这组字典有未保存的修改。确认放弃并切换？"))
      return;
    setSelected(key);
    setQuery("");
    setNewLabel("");
    if (dirty) void load();
  }
  return (
    <section className="dictionary-settings">
      <div className="admin-heading">
        <div>
          <span className="eyebrow">统一商品选项</span>
          <h1>区服与账号字典</h1>
          <p>
            出售、租赁共用完整选项。录入字段固定，只需维护选项名称与可用状态。
          </p>
        </div>
      </div>
      <nav className="zone-tabs" aria-label="字典分类">
        {fixedProductFields.map((field) => (
          <button
            type="button"
            disabled={busy}
            key={field.key}
            className={field.key === selected ? "active" : ""}
            onClick={() => switchGroup(field.key)}
          >
            {field.label}
          </button>
        ))}
      </nav>
      <form onSubmit={save} className="panel member-form">
        <fieldset disabled={busy}>
          <div className="section-heading">
            <h2>
              {group?.label ||
                fixedProductFields.find((field) => field.key === selected)
                  ?.label}
            </h2>
            <span>
              {group?.options.filter((option) => option.enabled).length ?? 0}{" "}
              个可选 / {group?.options.length ?? 0} 个选项
            </span>
          </div>
          <div className="dictionary-toolbar">
            <label className="dictionary-search">
              <Search size={17} />
              <input
                aria-label="查找字典选项"
                placeholder="查找名称或标识"
                value={query}
                onChange={(e) => setQuery(e.target.value)}
              />
            </label>
            <div className="dictionary-add">
              <input
                aria-label="新增选项名称"
                placeholder="填写新选项名称"
                maxLength={80}
                value={newLabel}
                onChange={(e) => setNewLabel(e.target.value)}
                onKeyDown={(e) => {
                  if (e.key === "Enter") e.preventDefault();
                }}
              />
              <button
                type="button"
                className="button-outline"
                disabled={!group || !newLabel.trim()}
                onClick={() => {
                  if (!group) return;
                  try {
                    update(addDictionaryOption(group, newLabel));
                    setNewLabel("");
                    setQuery("");
                    setError("");
                  } catch (e) {
                    setError(e instanceof Error ? e.message : "添加失败");
                  }
                }}
              >
                <Plus size={16} />
                添加
              </button>
            </div>
          </div>
          <p className="field-hint">
            停用选项会从新发布和筛选中隐藏，已有商品资料保留。名称可以修改，标识固定；清空搜索后可调整顺序。
          </p>
          {group && (
            <DictionaryOptions group={group} query={query} onChange={update} />
          )}
        </fieldset>
        {error && (
          <p className="form-error" role="alert">
            {error}
            <button
              type="button"
              className="text-button"
              onClick={() => {
                if (!dirty || window.confirm("重新读取会放弃当前修改，继续？"))
                  void load();
              }}
            >
              重新读取
            </button>
          </p>
        )}
        {notice && (
          <p className="admin-success" role="status">
            {notice}
          </p>
        )}
        <div className="save-bar">
          <span>
            {busy ? "正在处理…" : dirty ? "有修改未保存" : "两种交易类型共用"}
          </span>
          <button className="button-white" disabled={busy || !dirty}>
            保存当前字典
          </button>
        </div>
      </form>
    </section>
  );
}

export function DictionarySettings() {
  const [tab, setTab] = useState("dictionary"),
    [dirty, setDirty] = useState(false),
    [busy, setBusy] = useState(true);
  function change(next: string) {
    if (busy || next === tab) return;
    if (dirty && !window.confirm("有未保存的修改，确认放弃并切换？")) return;
    setDirty(false);
    setTab(next);
  }
  return (
    <>
      <nav className="status-tabs" aria-label="商品设置">
        <button
          disabled={busy}
          className={tab === "dictionary" ? "active" : ""}
          onClick={() => change("dictionary")}
        >
          共享字典
        </button>
        <button
          disabled={busy}
          className={tab === "categories" ? "active" : ""}
          onClick={() => change("categories")}
        >
          交易分类与报价单位
        </button>
      </nav>
      {tab === "dictionary" ? (
        <SharedDictionaries onDirtyChange={setDirty} onBusyChange={setBusy} />
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
