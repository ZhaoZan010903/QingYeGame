import { useEffect, useRef, useState, type FormEvent } from "react";
import { useSession } from "../context";
import { adminApi, type AdminRecord } from "../lib/admin-api";
import { StateView } from "./shared";

const message = (error: unknown) =>
  error instanceof Error ? error.message : "操作失败，请重试。";
const date = (value: unknown) => {
  if (!value) return "尚未计算";
  const parsed = new Date(String(value));
  return Number.isNaN(parsed.getTime())
    ? "时间格式无效"
    : parsed.toLocaleString("zh-CN");
};
export function AdminUsers() {
  const requestSequence = useRef(0);
  const { user } = useSession();
  const [search, setSearch] = useState("");
  const [query, setQuery] = useState("");
  const [items, setItems] = useState<AdminRecord[]>([]);
  const [total, setTotal] = useState(0);
  const [cursor, setCursor] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [notice, setNotice] = useState("");
  async function load(more = false) {
    const sequence = ++requestSequence.current;
    setBusy(true);
    setError("");
    try {
      const params = new URLSearchParams({ q: query, limit: "20" });
      if (more && cursor) params.set("cursor", cursor);
      const page = await adminApi.users(params);
      if (sequence !== requestSequence.current) return;
      setItems((old) => (more ? [...old, ...page.items] : page.items));
      setTotal(page.total);
      setCursor(page.nextCursor);
    } catch (e) {
      if (sequence === requestSequence.current) setError(message(e));
    } finally {
      if (sequence === requestSequence.current) setBusy(false);
    }
  }
  useEffect(() => {
    void load();
    return () => {
      requestSequence.current++;
    };
  }, [query]);
  async function toggle(member: AdminRecord) {
    if (
      member.enabled &&
      !window.confirm(
        `停用「${member.nickname}」会立即撤销其登录会话。已发布商品需另行管理，确认停用？`,
      )
    )
      return;
    setBusy(true);
    setError("");
    setNotice("");
    try {
      await adminApi.setUserEnabled(member.id, !member.enabled);
      setItems((old) =>
        old.map((item) =>
          item.id === member.id ? { ...item, enabled: !member.enabled } : item,
        ),
      );
      setNotice(
        member.enabled
          ? "账户已停用，其登录会话已撤销。"
          : "账户已启用，可重新登录。",
      );
    } catch (e) {
      setError(message(e));
    } finally {
      setBusy(false);
    }
  }
  return (
    <section>
      <div className="admin-heading">
        <div>
          <h2>会员管理</h2>
          <p>共 {total} 个账户 · 手机号脱敏展示</p>
        </div>
      </div>
      <div className="admin-toolbar">
        <form
          onSubmit={(e) => {
            e.preventDefault();
            setQuery(search.trim());
          }}
        >
          <input
            aria-label="搜索会员"
            value={search}
            onChange={(e) => setSearch(e.target.value)}
            placeholder="昵称或完整手机号"
          />
          <button className="button-outline">查询</button>
        </form>
      </div>
      {error && (
        <p className="form-error" role="alert">
          {error}
          <button className="text-button" onClick={() => void load()}>
            重新读取
          </button>
        </p>
      )}
      {notice && (
        <p className="admin-success" role="status">
          {notice}
        </p>
      )}
      <div className="admin-table-wrap">
        <table className="admin-table">
          <thead>
            <tr>
              <th>账户</th>
              <th>身份</th>
              <th>状态</th>
              <th>操作</th>
            </tr>
          </thead>
          <tbody>
            {items.map((member) => (
              <tr key={member.id}>
                <td>
                  <strong>{member.nickname}</strong>
                  <span>{member.phone_masked}</span>
                </td>
                <td>{member.role === "admin" ? "管理员" : "会员"}</td>
                <td>{member.enabled ? "可用" : "已停用"}</td>
                <td>
                  {member.id === user?.id ? (
                    "当前登录账户"
                  ) : (
                    <button
                      className="button-outline"
                      disabled={busy}
                      onClick={() => void toggle(member)}
                    >
                      {member.enabled ? "停用账户" : "启用账户"}
                    </button>
                  )}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
      {busy && <p role="status">正在处理…</p>}
      {!busy && !items.length && (
        <p className="admin-empty">没有匹配的账户。</p>
      )}
      {cursor && (
        <button
          className="button-outline"
          disabled={busy}
          onClick={() => void load(true)}
        >
          加载更多
        </button>
      )}
    </section>
  );
}

const fields = [
  ["window_days", "统计窗口（天）", 1, 30],
  ["view_weight", "每次有效浏览权重", 0, 100],
  ["favorite_weight", "首次收藏权重", 0, 100],
  ["new_bonus", "新品加分", 0, 100],
  ["new_hours", "新品时限（小时）", 1, 720],
] as const;
export function AdminRecommendations() {
  const [result, setResult] = useState<AdminRecord | null>(null);
  const [config, setConfig] = useState<AdminRecord>({});
  const [dirty, setDirty] = useState(false);
  const [loading, setLoading] = useState(true);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [notice, setNotice] = useState("");
  async function load(refresh = false) {
    setBusy(true);
    setError("");
    try {
      const value = await adminApi.recommendations(refresh);
      if (!value.config || !Array.isArray(value.data))
        throw new Error("推荐服务返回的配置不完整，请检查后台服务。");
      setResult(value);
      setConfig(value.config);
      setDirty(false);
      if (refresh) setNotice("推荐榜单已重新计算。");
    } catch (e) {
      setError(message(e));
    } finally {
      setLoading(false);
      setBusy(false);
    }
  }
  useEffect(() => {
    void load();
  }, []);
  async function save(event: FormEvent) {
    event.preventDefault();
    setBusy(true);
    setError("");
    try {
      await adminApi.save("settings", "recommendation", { value: config });
      setDirty(false);
      setNotice(
        "参数已保存。点击「重新计算榜单」立即生效，也可等待下一次定时计算。",
      );
    } catch (e) {
      setError(message(e));
    } finally {
      setBusy(false);
    }
  }
  function change(key: string, value: unknown) {
    setConfig((old) => ({ ...old, [key]: value }));
    setDirty(true);
    setNotice("");
  }
  return (
    <section>
      <div className="admin-heading">
        <div>
          <h2>推荐规则与榜单</h2>
          <p>
            生效版本：{result?.rule_version ?? "—"} · 最近计算：
            {date(result?.computed_at)}
          </p>
        </div>
        <button
          className="button-outline"
          disabled={busy || dirty}
          onClick={() => void load(true)}
        >
          {busy ? "正在处理…" : "重新计算榜单"}
        </button>
      </div>
      {loading ? (
        <StateView loading />
      ) : (
        <>
          {error && (
            <p className="form-error" role="alert">
              {error}
              <button className="text-button" onClick={() => void load()}>
                重新读取
              </button>
            </p>
          )}
          {notice && (
            <p className="admin-success" role="status">
              {notice}
            </p>
          )}
          {result && (
            <>
              <form
                className="panel member-form admin-resource-form"
                onSubmit={save}
              >
                <fieldset disabled={busy}>
                  <div className="admin-grid">
                    <label className="form-field">
                      规则版本
                      <input
                        required
                        minLength={1}
                        maxLength={40}
                        value={config.version ?? ""}
                        onChange={(e) => change("version", e.target.value)}
                      />
                    </label>
                    {fields.map(([key, label, min, max]) => (
                      <label key={key} className="form-field">
                        {label}
                        <input
                          type="number"
                          min={min}
                          max={max}
                          step="1"
                          required
                          value={config[key] ?? ""}
                          onChange={(e) => change(key, Number(e.target.value))}
                        />
                      </label>
                    ))}
                  </div>
                  <p className="muted">
                    保存参数后重新计算，榜单再使用新规则。人工精选在商品管理中单独设置。
                  </p>
                  <button className="button-white">保存推荐参数</button>
                </fieldset>
              </form>
              <h3 className="admin-ranking-title">当前榜单</h3>
              <div className="admin-table-wrap">
                <table className="admin-table">
                  <thead>
                    <tr>
                      <th>商品</th>
                      <th>分数</th>
                      <th>计算时间</th>
                    </tr>
                  </thead>
                  <tbody>
                    {result.data.map((item: AdminRecord, index: number) => (
                      <tr key={item.product_id}>
                        <td>
                          <strong>
                            {index + 1}. {item.title || item.product_id}
                          </strong>
                          <span>{item.product_id}</span>
                        </td>
                        <td>{Number(item.score).toLocaleString("zh-CN")}</td>
                        <td>{date(item.computed_at ?? result.computed_at)}</td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
              {!result.data.length && (
                <p className="admin-empty">
                  榜单暂为空，可在商品公开后重新计算。
                </p>
              )}
            </>
          )}
        </>
      )}
    </section>
  );
}
