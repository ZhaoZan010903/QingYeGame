import { useEffect, useRef, useState } from "react";
import { request } from "../lib/api";
type AccessRow = {
  subject: string;
  kind: string;
  surface: string;
  reason: string;
  until: number;
  version: string;
  requests: number;
  bytes_sent: number;
};
type Summary = {
  items: AccessRow[];
  metrics: Record<string, string>;
  coverage: string;
  retention: string;
};
const reasonLabels: Record<string, string> = {
  MEDIA_VERIFICATION_REQUIRED: "等待安全验证",
  MEDIA_RATE_LIMITED: "请求或流量偏多",
  MEDIA_CONCURRENCY: "同时读取较多",
  MEDIA_RESTRICTED: "管理员临时限制",
};
export function AdminMediaSecurity() {
  const [data, setData] = useState<Summary | null>(null),
    [error, setError] = useState(""),
    [busy, setBusy] = useState(false);
  const [selected, setSelected] = useState<AccessRow | null>(null),
    [reason, setReason] = useState(""),
    [duration, setDuration] = useState(600);
  const [filter, setFilter] = useState("abnormal"),
    [page, setPage] = useState(1);
  const modal = useRef<HTMLDivElement>(null);
  useEffect(() => {
    if (!selected) return;
    const previous = document.activeElement as HTMLElement | null;
    const overflow = document.body.style.overflow;
    document.body.style.overflow = "hidden";
    modal.current?.focus();
    const key = (event: KeyboardEvent) => {
      if (event.key === "Escape" && !busy) setSelected(null);
      if (event.key !== "Tab") return;
      const items = modal.current?.querySelectorAll<HTMLElement>(
        "button:not(:disabled),select,textarea",
      );
      if (!items?.length) return;
      const first = items[0],
        last = items[items.length - 1];
      if (
        event.shiftKey &&
        (document.activeElement === first ||
          document.activeElement === modal.current)
      ) {
        event.preventDefault();
        last.focus();
      } else if (!event.shiftKey && document.activeElement === last) {
        event.preventDefault();
        first.focus();
      }
    };
    document.addEventListener("keydown", key);
    return () => {
      document.removeEventListener("keydown", key);
      document.body.style.overflow = overflow;
      previous?.focus();
    };
  }, [selected, busy]);
  async function load() {
    setError("");
    try {
      const result = await request("/admin/security/media-access");
      setData(result.data as Summary);
    } catch (failure) {
      setError(
        failure instanceof Error ? failure.message : "读取失败，请重试。",
      );
    }
  }
  useEffect(() => {
    void load();
  }, []);
  async function save() {
    if (!selected) return;
    setBusy(true);
    setError("");
    try {
      await request(`/admin/security/media-access/${selected.subject}`, "PUT", {
        expected_version: selected.version,
        duration_seconds: duration,
        reason: reason.trim(),
      });
      setSelected(null);
      setReason("");
      await load();
    } catch (failure) {
      await load();
      setError(
        failure instanceof Error
          ? failure.message
          : "操作未确认，请刷新后检查。",
      );
    } finally {
      setBusy(false);
    }
  }
  const filtered =
    data?.items.filter((item) => filter === "all" || item.reason) ?? [];
  const pages = Math.max(1, Math.ceil(filtered.length / 20));
  const currentPage = Math.min(page, pages);
  const rows = filtered.slice((currentPage - 1) * 20, currentPage * 20);
  return (
    <section className="admin-media-security">
      <div className="page-heading">
        <div>
          <h1>图片访问保护</h1>
          <p>
            查看异常看图请求，处理误限制。仅影响图片访问，不停用账号或删除图片。
          </p>
        </div>
        <button className="button-outline" onClick={() => void load()}>
          刷新记录
        </button>
      </div>
      <div className="admin-panel" style={{ padding: 24 }}>
        <details>
          <summary>第一次使用？查看操作说明</summary>
          <p>
            先查看原因和到期时间。确认误限制后，选择「解除限制」，填写原因并保存；持续异常可选择「限制10分钟」。解除后仍保留正常限流，必要时用户需完成验证。
          </p>
          <p>
            列表为有时效的运行记录；人工处置另存审计日志。发送字节是服务端写入响应的统计，不是云厂商计费账单。
          </p>
        </details>
        {data && (
          <p>
            已放行{" "}
            <strong className="mono">{data.metrics.allowed ?? "0"}</strong> 次 ·
            验证通过{" "}
            <strong className="mono">{data.metrics.verified ?? "0"}</strong> 次
            · 响应数据{" "}
            <strong className="mono">
              {(Number(data.metrics.bytes_sent ?? 0) / 1048576).toFixed(1)} MB
            </strong>
          </p>
        )}
        {error && (
          <p className="form-error" role="alert">
            {error}
          </p>
        )}
        <label>
          显示范围{" "}
          <select
            value={filter}
            onChange={(event) => {
              setFilter(event.target.value);
              setPage(1);
            }}
          >
            <option value="abnormal">异常访问</option>
            <option value="all">全部最近访问</option>
          </select>
        </label>
        <div style={{ overflowX: "auto", marginTop: 18 }}>
          <table className="admin-table">
            <thead>
              <tr>
                <th>访问主体</th>
                <th>入口</th>
                <th>当前情况</th>
                <th>请求数</th>
                <th>到期时间</th>
                <th>操作</th>
              </tr>
            </thead>
            <tbody>
              {rows.map((item) => (
                <tr key={item.subject}>
                  <td>
                    {item.kind === "admin"
                      ? "管理员"
                      : item.kind === "member"
                        ? "会员"
                        : "访客"}
                    <small className="mono" style={{ display: "block" }}>
                      {item.subject.slice(0, 12)}
                    </small>
                  </td>
                  <td>{item.surface === "admin" ? "管理端" : "用户端"}</td>
                  <td>{reasonLabels[item.reason] ?? "正常"}</td>
                  <td className="mono">{item.requests}</td>
                  <td>
                    {item.until > Math.floor(Date.now() / 1000)
                      ? new Date(item.until * 1000).toLocaleTimeString("zh-CN")
                      : "—"}
                  </td>
                  <td>
                    <button
                      className="button-outline"
                      onClick={() => {
                        setSelected(item);
                        setDuration(
                          item.reason === "MEDIA_RESTRICTED" ? 0 : 600,
                        );
                        setReason("");
                      }}
                    >
                      处理
                    </button>
                  </td>
                </tr>
              ))}
              {!rows.length && (
                <tr>
                  <td colSpan={6}>
                    {data ? "当前没有符合条件的记录。" : "正在读取…"}
                  </td>
                </tr>
              )}
            </tbody>
          </table>
        </div>
        <div
          style={{
            display: "flex",
            alignItems: "center",
            gap: 12,
            marginTop: 16,
          }}
        >
          <button
            className="button-outline"
            disabled={currentPage <= 1}
            onClick={() => setPage(currentPage - 1)}
          >
            上一页
          </button>
          <span>
            第 {currentPage} / {pages} 页 · {filtered.length} 条
          </span>
          <button
            className="button-outline"
            disabled={currentPage >= pages}
            onClick={() => setPage(currentPage + 1)}
          >
            下一页
          </button>
        </div>
        {data && (
          <p className="helper">
            {data.coverage}。{data.retention}。
          </p>
        )}
      </div>
      {selected && (
        <div className="read-safety-overlay">
          <div
            ref={modal}
            tabIndex={-1}
            className="read-safety-dialog"
            role="dialog"
            aria-modal="true"
            aria-labelledby="media-control-title"
          >
            <h2 id="media-control-title">处理图片访问</h2>
            <p>
              访问编号：
              <span className="mono">{selected.subject.slice(0, 12)}</span>
            </p>
            <label>
              处理方式{" "}
              <select
                value={duration}
                onChange={(event) => setDuration(Number(event.target.value))}
              >
                <option value={600}>限制图片访问10分钟</option>
                <option value={0}>解除临时限制</option>
              </select>
            </label>
            <label style={{ display: "block", marginTop: 16 }}>
              原因
              <textarea
                value={reason}
                onChange={(event) => setReason(event.target.value)}
                minLength={2}
                maxLength={160}
                placeholder="例如：已确认是用户正常浏览"
                style={{ width: "100%", marginTop: 8 }}
              />
            </label>
            <p>不会影响登录、聊天或商品资料，也不会删除图片。</p>
            {error && (
              <p role="alert" className="read-safety-error">
                {error}
              </p>
            )}
            <div className="read-safety-actions">
              <button disabled={busy} onClick={() => setSelected(null)}>
                取消
              </button>
              <button
                disabled={busy || reason.trim().length < 2}
                onClick={() => void save()}
              >
                {busy ? "保存中…" : "确认保存"}
              </button>
            </div>
          </div>
        </div>
      )}
    </section>
  );
}
