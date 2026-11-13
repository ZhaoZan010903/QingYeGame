import { useEffect, useRef, useState, type FormEvent } from "react";
import { ProtectedImage } from "../../../共享/MediaSecurity";
import { uploadChatImage, validateChatImage } from "../../../共享/chat-upload";
import {
  profileDate,
  type AvatarLibraryItem,
} from "../../../共享/profile-types";
import { request } from "../lib/api";
import { profileAdminApi, type AdminNicknameRequest } from "../lib/profile-api";
import { useSession } from "../context";
import "../profiles.css";

const errorText = (error: unknown) =>
  error instanceof Error ? error.message : "操作失败，请重试。";
const statusNames = {
  pending: "待审核",
  approved: "已通过",
  rejected: "未通过",
};

export function NicknameReviewCard({
  item,
  disabled,
  onReview,
}: {
  item: AdminNicknameRequest;
  disabled: boolean;
  onReview: (
    item: AdminNicknameRequest,
    decision: "approved" | "rejected",
    note: string,
  ) => void;
}) {
  const [note, setNote] = useState("");
  return (
    <article className="panel nickname-review-card">
      <div className="nickname-review-heading">
        <div>
          <small>
            {item.current_nickname || "会员"}
            {item.phone_masked ? ` · ${item.phone_masked}` : ""}
          </small>
          <h2>申请昵称：{item.nickname}</h2>
          <p>提交于 {profileDate(item.created_at)}</p>
        </div>
        <span>{statusNames[item.status]}</span>
      </div>
      {item.status === "pending" ? (
        <>
          <label className="form-field">
            审核说明
            <textarea
              value={note}
              onChange={(event) =>
                setNote(Array.from(event.target.value).slice(0, 300).join(""))
              }
              maxLength={600}
              placeholder="未通过时请说明原因，方便会员修改"
              disabled={disabled}
            />
            <small>最多 300 个字符。</small>
          </label>
          <div className="nickname-review-actions">
            <button
              className="button-outline"
              disabled={disabled || !note.trim()}
              onClick={() => onReview(item, "rejected", note.trim())}
            >
              不通过
            </button>
            <button
              className="button-white"
              disabled={disabled}
              onClick={() => onReview(item, "approved", note.trim())}
            >
              通过审核
            </button>
          </div>
        </>
      ) : (
        <p className="muted">
          {item.review_note || "无审核说明"}
          {item.reviewed_at && ` · ${profileDate(item.reviewed_at)}`}
        </p>
      )}
    </article>
  );
}

export function AdminNicknameReviews() {
  const { user } = useSession();
  const [status, setStatus] = useState("pending");
  const [page, setPage] = useState(1);
  const [items, setItems] = useState<AdminNicknameRequest[]>([]);
  const [total, setTotal] = useState(0);
  const [loading, setLoading] = useState(true);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [notice, setNotice] = useState("");
  const [revision, setRevision] = useState(0);
  const generation = useRef(0);
  const operation = useRef(false);
  useEffect(() => {
    const current = ++generation.current;
    setLoading(true);
    setItems([]);
    setError("");
    profileAdminApi
      .nicknameRequests(status, page)
      .then((result) => {
        if (generation.current !== current) return;
        setItems(result.data);
        setTotal(result.pagination.total);
      })
      .catch((e) => {
        if (generation.current === current) setError(errorText(e));
      })
      .finally(() => {
        if (generation.current === current) setLoading(false);
      });
    return () => {
      generation.current++;
    };
  }, [status, page, revision, user?.id]);
  async function review(
    item: AdminNicknameRequest,
    decision: "approved" | "rejected",
    note: string,
  ) {
    if (operation.current) return;
    operation.current = true;
    setBusy(true);
    setError("");
    setNotice("");
    const current = generation.current;
    try {
      await profileAdminApi.reviewNickname(item.id, decision, note);
      if (generation.current !== current) return;
      setNotice(
        decision === "approved"
          ? "昵称已通过审核，30 天后可再次申请修改。"
          : "已退回昵称申请。",
      );
      if (items.length === 1 && page > 1) setPage((value) => value - 1);
      else setRevision((value) => value + 1);
    } catch (e) {
      if (generation.current === current) setError(errorText(e));
    } finally {
      operation.current = false;
      setBusy(false);
    }
  }
  return (
    <section>
      <div className="admin-heading">
        <div>
          <span className="eyebrow">会员资料</span>
          <h1>昵称审核</h1>
          <p>审核通过后生效，会员需等待 30 天才能再次申请修改。</p>
        </div>
      </div>
      <div className="admin-toolbar">
        <label>
          审核状态{" "}
          <select
            value={status}
            disabled={busy}
            onChange={(e) => {
              setStatus(e.target.value);
              setPage(1);
              setNotice("");
            }}
          >
            {Object.entries(statusNames).map(([value, label]) => (
              <option key={value} value={value}>
                {label}
              </option>
            ))}
          </select>
        </label>
        <button
          className="button-outline"
          disabled={busy || loading}
          onClick={() => setRevision((v) => v + 1)}
        >
          刷新
        </button>
      </div>
      {error && (
        <p role="alert" className="form-error">
          {error}
        </p>
      )}
      {notice && (
        <p role="status" className="admin-profile-notice">
          {notice}
        </p>
      )}
      {loading ? (
        <p role="status">正在读取昵称申请…</p>
      ) : !error && !items.length ? (
        <div className="state-view">
          <p>
            暂无{statusNames[status as keyof typeof statusNames]}的昵称申请。
          </p>
        </div>
      ) : null}
      <div className="nickname-review-list">
        {items.map((item) => (
          <NicknameReviewCard
            key={item.id}
            item={item}
            disabled={busy || loading}
            onReview={review}
          />
        ))}
      </div>
      <div className="admin-profile-pagination">
        <span>
          共 {total} 条 · 第 {page} 页
        </span>
        <button
          className="button-outline"
          disabled={busy || loading || page === 1}
          onClick={() => setPage((v) => v - 1)}
        >
          上一页
        </button>
        <button
          className="button-outline"
          disabled={busy || loading || page * 20 >= total}
          onClick={() => setPage((v) => v + 1)}
        >
          下一页
        </button>
      </div>
    </section>
  );
}

function AvatarLibraryCard({
  item,
  disabled,
  onSave,
}: {
  item: AvatarLibraryItem;
  disabled: boolean;
  onSave: (value: AvatarLibraryItem) => Promise<void>;
}) {
  const [draft, setDraft] = useState(item);
  useEffect(() => setDraft(item), [item.id, item.version]);
  return (
    <form
      className="panel admin-avatar-card"
      onSubmit={(event) => {
        event.preventDefault();
        void onSave({ ...draft, name: draft.name.trim() });
      }}
    >
      <ProtectedImage src={item.url} alt={item.name} loading="lazy" />
      <label className="form-field">
        头像名称
        <input
          value={draft.name}
          maxLength={30}
          required
          disabled={disabled}
          onChange={(e) => setDraft({ ...draft, name: e.target.value })}
        />
      </label>
      <label className="form-field">
        排序
        <input
          type="number"
          min={0}
          max={1000000}
          step={1}
          value={draft.sort_order}
          required
          disabled={disabled}
          onChange={(e) =>
            setDraft({ ...draft, sort_order: Number(e.target.value) })
          }
        />
      </label>
      <label className="admin-avatar-enabled">
        <input
          type="checkbox"
          checked={draft.enabled}
          disabled={disabled}
          onChange={(e) => setDraft({ ...draft, enabled: e.target.checked })}
        />
        允许会员选用
      </label>
      <button
        type="submit"
        className="button-outline"
        disabled={disabled || !draft.name.trim()}
      >
        保存头像设置
      </button>
    </form>
  );
}

export function AdminAvatarLibrary() {
  const { user } = useSession();
  const [items, setItems] = useState<AvatarLibraryItem[]>([]);
  const [loading, setLoading] = useState(true);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [notice, setNotice] = useState("");
  const [revision, setRevision] = useState(0);
  const [name, setName] = useState("");
  const [sort, setSort] = useState(0);
  const [file, setFile] = useState<File | null>(null);
  const [progress, setProgress] = useState(0);
  const generation = useRef(0);
  const controller = useRef<AbortController | null>(null);
  const uploaded = useRef<{ file: File; mediaID: string } | null>(null);
  const input = useRef<HTMLInputElement>(null);
  useEffect(() => {
    const current = ++generation.current;
    setLoading(true);
    setError("");
    profileAdminApi
      .avatars()
      .then((result) => {
        if (generation.current === current) setItems(result);
      })
      .catch((e) => {
        if (generation.current === current) setError(errorText(e));
      })
      .finally(() => {
        if (generation.current === current) setLoading(false);
      });
    return () => {
      generation.current++;
    };
  }, [revision, user?.id]);
  useEffect(
    () => () => {
      controller.current?.abort();
    },
    [user?.id],
  );
  async function save(item: AvatarLibraryItem) {
    if (controller.current) return;
    const current = generation.current;
    controller.current = new AbortController();
    setBusy(true);
    setError("");
    setNotice("");
    try {
      await profileAdminApi.updateAvatar(item);
      if (generation.current !== current) return;
      setNotice("头像设置已保存。");
      setRevision((value) => value + 1);
    } catch (e) {
      if (generation.current === current) setError(errorText(e));
    } finally {
      controller.current = null;
      setBusy(false);
    }
  }
  async function add(event: FormEvent) {
    event.preventDefault();
    if (!file || !name.trim() || controller.current) return;
    const invalid = validateChatImage(file, true);
    if (invalid) {
      setError(invalid);
      return;
    }
    const current = generation.current;
    const abort = new AbortController();
    controller.current = abort;
    setBusy(true);
    setError("");
    setNotice("");
    setProgress(0);
    try {
      let mediaID =
        uploaded.current?.file === file ? uploaded.current.mediaID : "";
      if (!mediaID) {
        mediaID = await uploadChatImage(
          request,
          file,
          { purpose: "avatar" },
          abort.signal,
          (value) => {
            if (generation.current === current) setProgress(value);
          },
        );
        if (generation.current !== current) return;
        uploaded.current = { file, mediaID };
      }
      await profileAdminApi.createAvatar({
        name: name.trim(),
        media_id: mediaID,
        sort_order: sort,
        enabled: true,
      });
      if (generation.current !== current) return;
      setFile(null);
      setName("");
      uploaded.current = null;
      if (input.current) input.current.value = "";
      setNotice("头像已加入头像库，会员现在可以选用。");
      setRevision((value) => value + 1);
    } catch (e) {
      if (generation.current === current) setError(errorText(e));
    } finally {
      controller.current = null;
      setBusy(false);
    }
  }
  return (
    <section>
      <div className="admin-heading">
        <div>
          <span className="eyebrow">会员资料</span>
          <h1>头像库</h1>
          <p>
            会员只能选取已启用的头像。排序数字越小越靠前，停用后不再提供新选择。
          </p>
        </div>
      </div>
      <form className="panel admin-avatar-upload" onSubmit={add}>
        <h2>新增头像</h2>
        <label className="form-field">
          头像名称
          <input
            value={name}
            maxLength={30}
            required
            disabled={busy}
            onChange={(e) => setName(e.target.value)}
          />
        </label>
        <label className="form-field">
          排序
          <input
            type="number"
            min={0}
            max={1000000}
            step={1}
            value={sort}
            required
            disabled={busy}
            onChange={(e) => setSort(Number(e.target.value))}
          />
        </label>
        <label className="form-field">
          头像图片
          <input
            ref={input}
            type="file"
            accept="image/jpeg,image/png,image/webp"
            required
            disabled={busy}
            onChange={(e) => {
              setFile(e.target.files?.[0] || null);
              uploaded.current = null;
            }}
          />
          <small>JPG、PNG、WebP，单张不超过 2 MB。</small>
        </label>
        <button
          className="button-white"
          type="submit"
          disabled={busy || loading || !file || !name.trim()}
        >
          {busy
            ? progress > 0 && progress < 100
              ? `正在上传 ${progress}%`
              : "正在保存…"
            : "添加到头像库"}
        </button>
      </form>
      <div className="admin-toolbar">
        <span>共 {items.length} 个头像</span>
        <button
          className="button-outline"
          disabled={busy || loading}
          onClick={() => setRevision((value) => value + 1)}
        >
          重新加载
        </button>
      </div>
      {error && (
        <p role="alert" className="form-error">
          {error}
        </p>
      )}
      {notice && (
        <p role="status" className="admin-profile-notice">
          {notice}
        </p>
      )}
      {loading && <p role="status">正在加载头像库…</p>}
      {!loading && !error && !items.length && (
        <div className="state-view">
          <p>头像库为空，请添加首张头像。</p>
        </div>
      )}
      <div className="admin-avatar-grid">
        {items.map((item) => (
          <AvatarLibraryCard
            key={item.id}
            item={item}
            disabled={busy || loading}
            onSave={save}
          />
        ))}
      </div>
    </section>
  );
}
