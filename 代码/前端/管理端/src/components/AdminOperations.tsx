import { useEffect, useRef, useState, type FormEvent } from "react";
import { useSession } from "../context";
import { adminApi, type AdminRecord } from "../lib/admin-api";
import { StateView } from "./shared";
import { Link } from "react-router-dom";
import { RefreshCw, TrendingUp } from "lucide-react";
import { useUnsavedChanges } from "../lib/admin-ux";
import { UploadRestrictionPanel } from "./UploadRestrictionPanel";

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
  const [restrictionMember, setRestrictionMember] =
    useState<AdminRecord | null>(null);
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
          <span className="eyebrow">账户维护</span>
          <h1>会员管理</h1>
          <p>共 {total} 个账户 · 手机号脱敏展示</p>
        </div>
      </div>
      <div className="location-note">
        <strong>这里可以控制账户能否登录</strong>
        <span>
          停用会撤销该账户的登录会话，但不会自动下架它发布的商品。商品展示请到商品管理处理。
        </span>
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
                  {member.role === "member" && (
                    <button
                      className="button-outline"
                      disabled={busy}
                      onClick={() => setRestrictionMember(member)}
                    >
                      上传权限
                    </button>
                  )}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
      {restrictionMember && (
        <UploadRestrictionPanel
          key={restrictionMember.id}
          member={restrictionMember}
          close={() => setRestrictionMember(null)}
        />
      )}
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
  ["consultation_weight", "每位有效咨询用户权重", 0, 100],
] as const;
export function AdminRecommendations() {
  const [result, setResult] = useState<AdminRecord | null>(null);
  const [config, setConfig] = useState<AdminRecord>({});
  const [dirty, setDirty] = useState(false);
  const [loading, setLoading] = useState(true);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [notice, setNotice] = useState("");
  useUnsavedChanges(dirty, busy);
  async function load(refresh = false) {
    if (dirty && !window.confirm("重新读取会替换未保存的参数，确认继续？"))
      return;
    setBusy(true);
    setError("");
    try {
      const value = await adminApi.recommendations(refresh);
      if (!value.config || !Array.isArray(value.data))
        throw new Error("推荐服务返回的配置不完整，请检查后台服务。");
      setResult(value);
      setConfig(value.config);
      setDirty(false);
      if (refresh)
        setNotice(
          "热门榜单已更新，刷新首页可查看。热门、为你推荐、最新上架各自独立排序。",
        );
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
        "参数已保存。点击「更新榜单」应用新规则，也可等待下一次自动计算。",
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
          <span className="eyebrow">网站运营</span>
          <h1>热门榜单</h1>
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
          <RefreshCw size={16} />
          {busy ? "正在处理…" : "更新榜单"}
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
              <div className="recommendation-summary">
                <div className="panel">
                  <h3>
                    <TrendingUp size={18} /> 热门商品自动排序
                  </h3>
                  <p>
                    根据最近 {config.window_days}{" "}
                    天的有效浏览、收藏和去重咨询人数计算。系统约每 15
                    分钟更新一次，通常无需手动调整。
                  </p>
                  <p className="formula">
                    浏览 × {config.view_weight} + 收藏 ×{" "}
                    {config.favorite_weight} + 咨询人数 ×{" "}
                    {config.consultation_weight ?? 8}
                  </p>
                  <p>
                    算例：100 次有效浏览、10 次有效收藏，热度分为{" "}
                    {100 * Number(config.view_weight || 0) +
                      10 * Number(config.favorite_weight || 0)}
                    （未计咨询）。同一用户对同一商品反复咨询只计一人；上架时间不加分。
                  </p>
                </div>
                <div className="panel">
                  <h3>要修改哪个位置？</h3>
                  <p>
                    调整标题、展示数量和显示顺序，请进入首页装修。首页轮播使用人工精选，与自动热门榜分开管理。
                  </p>
                  <div className="admin-actions">
                    <Link className="button-outline" to="/settings">
                      修改首页展示
                    </Link>
                    <Link className="button-outline" to="/settings">
                      设置轮播精选
                    </Link>
                  </div>
                </div>
              </div>
              <details className="admin-advanced ranking-advanced">
                <summary>
                  调整热门权重（高级设置）{dirty ? " · 有修改未保存" : ""}
                </summary>
                <p>
                  日常运营无需修改。调整后先保存参数，再更新榜单。修改只影响热门榜，不改变个人推荐偏好或最新上架顺序。
                </p>
                <form
                  className="member-form admin-resource-form"
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
                            onChange={(e) =>
                              change(key, Number(e.target.value))
                            }
                          />
                        </label>
                      ))}
                    </div>
                    <p className="muted">
                      保存参数后更新榜单，才能立即使用新规则。
                    </p>
                    <div className="save-bar">
                      <span>
                        {dirty ? "参数有修改，尚未保存" : "当前参数已读取"}
                      </span>
                      <button className="button-white">保存推荐参数</button>
                    </div>
                  </fieldset>
                </form>
              </details>
              <h3 className="admin-ranking-title">当前榜单</h3>
              <div className="admin-table-wrap">
                <table className="admin-table">
                  <thead>
                    <tr>
                      <th>商品</th>
                      <th>分数</th>
                      <th>咨询人数</th>
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
                        <td>{Number(item.consultations ?? 0)}</td>
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
