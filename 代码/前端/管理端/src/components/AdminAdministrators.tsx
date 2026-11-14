import { useEffect, useRef, useState, type FormEvent } from "react";
import { flushSync } from "react-dom";
import { useNavigate } from "react-router-dom";
import { Plus, UserRound, X } from "lucide-react";
import { ProtectedImage } from "../../../共享/MediaSecurity";
import type { AvatarLibraryItem } from "../../../共享/profile-types";
import { useSession } from "../context";
import { ApiError, createRequestId } from "../lib/api";
import { useUnsavedChanges } from "../lib/admin-ux";
import { profileAdminApi } from "../lib/profile-api";
import {
  administratorApi,
  administratorFailure,
  administratorLoginError,
  administratorNameError,
  administratorPasswordError,
  type Administrator,
  type CreateAdministrator,
  type ResetAdministratorPassword,
  type UpdateAdministrator,
} from "../lib/administrator-api";
import "../accounts.css";

type Editor =
  { kind: "create" } | { kind: "edit" | "password"; item: Administrator };
type Attempt =
  | { kind: "create"; input: CreateAdministrator }
  | { kind: "edit"; id: string; input: UpdateAdministrator }
  | { kind: "password"; id: string; input: ResetAdministratorPassword };
const message = (error: unknown) =>
  error instanceof Error ? error.message : "操作失败，请重试。";

export function AdministratorCard({
  item,
  current,
  disabled,
  onEdit,
  onPassword,
}: {
  item: Administrator;
  current: boolean;
  disabled: boolean;
  onEdit: () => void;
  onPassword: () => void;
}) {
  return (
    <article className="account-card">
      <div className="account-identity">
        <span className="account-avatar">
          {item.avatar_url ? (
            <ProtectedImage src={item.avatar_url} alt="" loading="lazy" />
          ) : (
            <UserRound size={22} />
          )}
        </span>
        <div>
          <strong>{item.nickname}</strong>
          <p>登录账号：{item.login}</p>
          {current && <small>当前登录账号</small>}
        </div>
      </div>
      <span className={`account-state${item.enabled ? "" : " disabled"}`}>
        {item.enabled ? "可用" : "已停用"}
      </span>
      <div className="account-actions">
        <button className="button-outline" disabled={disabled} onClick={onEdit}>
          编辑资料
        </button>
        <button
          className="button-outline"
          disabled={disabled}
          onClick={onPassword}
        >
          重置密码
        </button>
      </div>
    </article>
  );
}

export function AdministratorAvatarPicker({
  items,
  value,
  onChange,
}: {
  items: AvatarLibraryItem[];
  value: string;
  onChange: (id: string) => void;
}) {
  return (
    <div className="account-avatar-picker" aria-label="管理员头像库">
      <button
        type="button"
        aria-pressed={value === ""}
        onClick={() => onChange("")}
      >
        <span className="account-avatar">
          <UserRound size={24} />
        </span>
        <span>默认头像</span>
      </button>
      {items
        .filter((item) => item.enabled)
        .map((item) => (
          <button
            key={item.id}
            type="button"
            aria-pressed={value === item.media_id}
            onClick={() => onChange(item.media_id)}
            title={item.name}
          >
            <span className="account-avatar">
              <ProtectedImage src={item.url} alt="" loading="lazy" />
            </span>
            <span>{item.name}</span>
          </button>
        ))}
    </div>
  );
}

function AdministratorEditor({
  editor,
  currentId,
  close,
  saved,
  expired,
}: {
  editor: Editor;
  currentId: string;
  close: (reload?: boolean) => void;
  saved: (item: Administrator | null, reauthenticate: boolean) => Promise<void>;
  expired: (ownPassword: boolean) => void;
}) {
  const item = editor.kind === "create" ? null : editor.item;
  const self = item?.id === currentId;
  const dialog = useRef<HTMLDialogElement>(null);
  const firstInput = useRef<HTMLInputElement>(null);
  const mounted = useRef(false);
  const lock = useRef(false);
  // Passwords remain only in this dialog's memory and are discarded on success/close.
  const attempt = useRef<Attempt | null>(null);
  const [login, setLogin] = useState("");
  const [nickname, setNickname] = useState(item?.nickname ?? "");
  const [avatar, setAvatar] = useState(item?.avatar_id ?? "");
  const [enabled, setEnabled] = useState(item?.enabled ?? true);
  const [password, setPassword] = useState("");
  const [confirmation, setConfirmation] = useState("");
  const [avatars, setAvatars] = useState<AvatarLibraryItem[]>([]);
  const [avatarError, setAvatarError] = useState("");
  const [avatarLoading, setAvatarLoading] = useState(
    editor.kind !== "password",
  );
  const [avatarRevision, setAvatarRevision] = useState(0);
  const [busy, setBusy] = useState(false);
  const [completed, setCompleted] = useState(false);
  const [error, setError] = useState("");
  const [failure, setFailure] = useState<ReturnType<
    typeof administratorFailure
  > | null>(null);
  const dirty =
    editor.kind === "edit"
      ? nickname !== item?.nickname ||
        avatar !== item?.avatar_id ||
        enabled !== item?.enabled
      : editor.kind === "password"
        ? !!(password || confirmation)
        : !!(login || nickname || avatar || password || confirmation);
  useUnsavedChanges(
    !completed && (dirty || failure === "unknown"),
    !completed && busy,
  );
  const frozen = busy || failure === "unknown" || failure === "refresh";

  useEffect(() => {
    mounted.current = true;
    const previous = document.activeElement;
    dialog.current?.showModal();
    firstInput.current?.focus();
    return () => {
      mounted.current = false;
      attempt.current = null;
      dialog.current?.close();
      if (previous instanceof HTMLElement && previous.isConnected)
        previous.focus();
    };
  }, []);
  useEffect(() => {
    if (editor.kind === "password") return;
    const controller = new AbortController();
    setAvatarLoading(true);
    setAvatarError("");
    profileAdminApi
      .avatars(controller.signal)
      .then((values) => {
        if (!controller.signal.aborted) setAvatars(values);
      })
      .catch((e) => {
        if (!controller.signal.aborted) setAvatarError(message(e));
      })
      .finally(() => {
        if (!controller.signal.aborted) setAvatarLoading(false);
      });
    return () => controller.abort();
  }, [editor.kind, avatarRevision]);

  function dismiss() {
    if (lock.current) return;
    if (
      failure === "unknown" &&
      !window.confirm(
        "上次操作可能已成功。关闭后请重新读取账号列表核对；不要重复创建或使用不同密码重试。确认关闭？",
      )
    )
      return;
    if (
      failure !== "unknown" &&
      dirty &&
      !window.confirm("关闭后，本次未保存的内容不会保留。确认关闭？")
    )
      return;
    close(!!failure);
  }
  async function submit(event: FormEvent) {
    event.preventDefault();
    if (lock.current || failure === "refresh") return;
    if (!attempt.current) {
      const validation =
        (editor.kind === "create"
          ? administratorLoginError(login.trim())
          : "") ||
        (editor.kind !== "password" ? administratorNameError(nickname) : "") ||
        (editor.kind !== "edit"
          ? administratorPasswordError(password, confirmation)
          : "");
      if (validation) {
        setError(validation);
        return;
      }
      if (editor.kind === "edit" && !dirty) {
        close();
        return;
      }
      if (editor.kind === "create") {
        attempt.current = {
          kind: "create",
          input: {
            login: login.trim(),
            nickname: nickname.trim(),
            avatar_id: avatar,
            password,
            request_id: createRequestId(),
          },
        };
      } else if (editor.kind === "password") {
        attempt.current = {
          kind: "password",
          id: editor.item.id,
          input: {
            expected_version: editor.item.version,
            password,
            request_id: createRequestId(),
          },
        };
      } else {
        const input: UpdateAdministrator = {
          expected_version: editor.item.version,
        };
        if (nickname !== editor.item.nickname) input.nickname = nickname.trim();
        if (avatar !== editor.item.avatar_id) input.avatar_id = avatar;
        if (enabled !== editor.item.enabled) input.enabled = enabled;
        attempt.current = { kind: "edit", id: editor.item.id, input };
      }
    }
    lock.current = true;
    setBusy(true);
    setError("");
    try {
      const submitted = attempt.current;
      let result: Administrator | null = null;
      let reauthenticate = false;
      if (submitted.kind === "create")
        result = await administratorApi.create(submitted.input);
      else if (submitted.kind === "edit")
        result = await administratorApi.update(submitted.id, submitted.input);
      else {
        const response = await administratorApi.resetPassword(
          submitted.id,
          submitted.input,
        );
        reauthenticate = response.reauthenticate || self;
      }
      if (!mounted.current) return;
      attempt.current = null;
      // Release the leave guard before a successful self-password reset navigates to login.
      flushSync(() => {
        setCompleted(true);
        setBusy(false);
        setPassword("");
        setConfirmation("");
      });
      await saved(result, reauthenticate);
    } catch (e) {
      if (!mounted.current) return;
      if (e instanceof ApiError && e.status === 401) {
        attempt.current = null;
        flushSync(() => {
          setCompleted(true);
          setBusy(false);
          setPassword("");
          setConfirmation("");
        });
        expired(editor.kind === "password" && self);
        return;
      }
      const kind = administratorFailure(e);
      setFailure(kind);
      if (kind !== "unknown") attempt.current = null;
      setError(message(e));
    } finally {
      lock.current = false;
      if (mounted.current) setBusy(false);
    }
  }
  const title =
    editor.kind === "create"
      ? "新增管理员"
      : editor.kind === "edit"
        ? "编辑管理员"
        : "重置管理员密码";
  return (
    <dialog
      ref={dialog}
      className="account-dialog"
      aria-labelledby="administrator-editor-title"
      onCancel={(event) => {
        event.preventDefault();
        dismiss();
      }}
    >
      <form onSubmit={submit}>
        <div className="account-dialog-header">
          <div>
            <h2 id="administrator-editor-title">{title}</h2>
            {item && <p className="muted">登录账号：{item.login}</p>}
          </div>
          <button
            type="button"
            disabled={busy}
            onClick={dismiss}
            aria-label="关闭管理员表单"
          >
            <X size={21} />
          </button>
        </div>
        <fieldset disabled={frozen}>
          {editor.kind === "create" && (
            <label className="form-field">
              登录账号
              <input
                ref={firstInput}
                value={login}
                onChange={(e) => setLogin(e.target.value)}
                autoComplete="off"
                autoCapitalize="none"
                spellCheck={false}
                maxLength={20}
                required
              />
              <small>
                6–20
                位字母、数字、点、下划线或短横线，以字母或数字开头。创建后不可修改。
              </small>
            </label>
          )}
          {editor.kind !== "password" && (
            <>
              <label className="form-field">
                管理员名称
                <input
                  ref={editor.kind === "edit" ? firstInput : undefined}
                  value={nickname}
                  onChange={(e) => setNickname(e.target.value)}
                  required
                />
                <small>1–30 个字符，保存后立即生效。</small>
              </label>
              <div>
                <strong>头像</strong>
                <p className="muted">从启用的头像库选择，也可使用默认头像。</p>
                {avatarLoading ? (
                  <p role="status">正在读取头像库…</p>
                ) : avatarError ? (
                  <p className="form-error" role="alert">
                    {avatarError}
                    <button
                      className="text-button"
                      type="button"
                      onClick={() => setAvatarRevision((value) => value + 1)}
                    >
                      重新读取
                    </button>
                  </p>
                ) : (
                  <>
                    {avatar &&
                      !avatars.some(
                        (value) => value.enabled && value.media_id === avatar,
                      ) && (
                        <p className="muted">
                          当前头像已停用；本次不更换时保留原头像。
                        </p>
                      )}
                    <AdministratorAvatarPicker
                      items={avatars}
                      value={avatar}
                      onChange={setAvatar}
                    />
                  </>
                )}
              </div>
              {editor.kind === "edit" && (
                <label className="account-enabled">
                  <input
                    type="checkbox"
                    checked={enabled}
                    disabled={self}
                    onChange={(e) => setEnabled(e.target.checked)}
                  />
                  允许登录{self && <small>当前账号不能停用自己</small>}
                </label>
              )}
              {editor.kind === "edit" && item?.enabled && !enabled && (
                <p className="account-warning">
                  保存后将停用该账号并撤销其全部登录会话。
                </p>
              )}
            </>
          )}
          {editor.kind !== "edit" && (
            <>
              <label className="form-field">
                {editor.kind === "create" ? "初始密码" : "新密码"}
                <input
                  ref={editor.kind === "password" ? firstInput : undefined}
                  type="password"
                  autoComplete="new-password"
                  value={password}
                  onChange={(e) => setPassword(e.target.value)}
                  required
                />
                <small>12–72 字节；密码不会自动去除空格。</small>
              </label>
              <label className="form-field">
                确认密码
                <input
                  type="password"
                  autoComplete="new-password"
                  value={confirmation}
                  onChange={(e) => setConfirmation(e.target.value)}
                  required
                />
              </label>
              {editor.kind === "password" && (
                <p className="account-warning">
                  {self
                    ? "修改自己的密码后将退出管理台，请使用新密码重新登录。"
                    : "保存后会撤销该管理员的全部登录会话，需使用新密码重新登录。"}
                </p>
              )}
            </>
          )}
        </fieldset>
        {error && (
          <p className="form-error" role="alert">
            {error}
          </p>
        )}
        {failure === "unknown" && (
          <p className="account-warning">
            操作结果尚未确认。请使用下方按钮重试本次提交；提交内容已锁定。
          </p>
        )}
        {failure === "refresh" && (
          <p className="account-warning">
            资料或账号状态已变化。关闭表单后重新读取，再核对并操作。
          </p>
        )}
        <div className="account-dialog-actions">
          <button
            className="button-outline"
            type="button"
            disabled={busy}
            onClick={dismiss}
          >
            {failure ? "关闭并重新读取" : "取消"}
          </button>
          <button
            className="button-white"
            disabled={busy || failure === "refresh"}
            type="submit"
          >
            {busy
              ? "正在保存…"
              : failure === "unknown"
                ? "重试本次提交"
                : editor.kind === "create"
                  ? "创建管理员"
                  : "保存"}
          </button>
        </div>
      </form>
    </dialog>
  );
}

export function AdminAdministrators() {
  const { user, refresh, invalidate } = useSession();
  const navigate = useNavigate();
  const [search, setSearch] = useState("");
  const [query, setQuery] = useState("");
  const [items, setItems] = useState<Administrator[]>([]);
  const [total, setTotal] = useState(0);
  const [cursor, setCursor] = useState<string | null>(null);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState("");
  const [notice, setNotice] = useState("");
  const [editor, setEditor] = useState<Editor | null>(null);
  const sequence = useRef(0);
  const controller = useRef<AbortController | null>(null);
  function expire(ownPassword = false) {
    setEditor(null);
    invalidate(ownPassword ? "password-uncertain" : "session-expired");
    navigate("/login", { replace: true });
  }
  async function load(more = false, ownPassword = false) {
    controller.current?.abort();
    const requestController = new AbortController();
    controller.current = requestController;
    const generation = ++sequence.current;
    setLoading(true);
    setError("");
    if (!more) {
      setItems([]);
      setCursor(null);
      setTotal(0);
    }
    try {
      const page = await administratorApi.list(
        query,
        more ? cursor : null,
        requestController.signal,
      );
      if (requestController.signal.aborted || sequence.current !== generation)
        return;
      setItems((old) =>
        more
          ? [
              ...old,
              ...page.items.filter(
                (item) => !old.some((existing) => existing.id === item.id),
              ),
            ]
          : page.items,
      );
      setTotal(page.total);
      setCursor(page.nextCursor);
    } catch (e) {
      if (
        !requestController.signal.aborted &&
        sequence.current === generation
      ) {
        if (e instanceof ApiError && e.status === 401) expire(ownPassword);
        else setError(message(e));
      }
    } finally {
      if (sequence.current === generation && !requestController.signal.aborted)
        setLoading(false);
    }
  }
  useEffect(() => {
    void load();
    return () => {
      sequence.current++;
      controller.current?.abort();
    };
  }, [query, user?.id]);
  async function saved(item: Administrator | null, reauthenticate: boolean) {
    const previous = editor;
    setEditor(null);
    if (reauthenticate) {
      invalidate("password-changed");
      navigate("/login", { replace: true });
      return;
    }
    setNotice(
      previous?.kind === "create"
        ? "管理员已创建，可使用新账号登录。"
        : previous?.kind === "password"
          ? "密码已重置，该账号的登录会话已撤销。"
          : "管理员资料已保存。",
    );
    if (item?.id === user?.id) await refresh();
    await load();
  }
  return (
    <section className="admin-accounts">
      <div className="admin-heading">
        <div>
          <span className="eyebrow">账户维护</span>
          <h1>管理员管理</h1>
          <p>共 {total} 位管理员 · 所有管理员拥有相同管理权限</p>
        </div>
        <button
          className="button-white"
          onClick={() => {
            setNotice("");
            setEditor({ kind: "create" });
          }}
        >
          <Plus size={17} />
          新增管理员
        </button>
      </div>
      <div className="location-note">
        <strong>管理员独立于会员管理</strong>
        <span>
          名称和头像保存后立即生效。停用账号或重置密码会撤销该账号的登录会话。
        </span>
      </div>
      <div className="admin-toolbar">
        <form
          onSubmit={(event) => {
            event.preventDefault();
            const next = search.trim();
            if (next === query) void load();
            else setQuery(next);
          }}
        >
          <input
            aria-label="搜索管理员"
            value={search}
            onChange={(e) => setSearch(e.target.value)}
            placeholder="登录账号或管理员名称"
          />
          <button className="button-outline" type="submit" disabled={loading}>
            查询
          </button>
        </form>
      </div>
      {notice && (
        <p className="admin-success" role="status">
          {notice}
        </p>
      )}
      {error && (
        <p className="form-error" role="alert">
          {error}
          <button className="text-button" onClick={() => void load()}>
            重新读取
          </button>
        </p>
      )}
      <div className="account-list">
        {items.map((item) => (
          <AdministratorCard
            key={item.id}
            item={item}
            current={item.id === user?.id}
            disabled={loading}
            onEdit={() => setEditor({ kind: "edit", item })}
            onPassword={() => setEditor({ kind: "password", item })}
          />
        ))}
      </div>
      {loading && <p role="status">正在读取管理员…</p>}
      {!loading && !error && !items.length && (
        <p className="admin-empty">没有匹配的管理员。</p>
      )}
      {cursor && (
        <button
          className="button-outline"
          disabled={loading}
          onClick={() => void load(true)}
        >
          加载更多
        </button>
      )}
      {editor && (
        <AdministratorEditor
          key={
            editor.kind === "create"
              ? "create"
              : `${editor.kind}:${editor.item.id}`
          }
          editor={editor}
          currentId={user?.id ?? ""}
          close={(reload) => {
            setEditor(null);
            if (reload)
              void load(
                false,
                editor.kind === "password" && editor.item.id === user?.id,
              );
          }}
          saved={saved}
          expired={expire}
        />
      )}
    </section>
  );
}
