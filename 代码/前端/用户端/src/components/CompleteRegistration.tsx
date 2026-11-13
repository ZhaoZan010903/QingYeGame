import { useEffect, useRef, useState, type FormEvent } from 'react';
import { Link } from 'react-router-dom';
import { api } from '../lib/api';
import { Modal } from './shared';

export type VerifiedPhone = { phone: string; code: string; challenge_id: string };

export function CompleteRegistration({
  proof,
  onClose,
  onComplete,
}: {
  proof: VerifiedPhone;
  onClose: () => void;
  onComplete: () => Promise<void>;
}) {
  const [password, setPassword] = useState('');
  const [confirm, setConfirm] = useState('');
  const [visible, setVisible] = useState(false);
  const [agreed, setAgreed] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  const [versions, setVersions] = useState<{ terms: number; privacy: number } | null>(null);
  const [agreementError, setAgreementError] = useState('');
  const [retry, setRetry] = useState(0);
  const alive = useRef(true);
  useEffect(() => {
    alive.current = true;
    return () => {
      alive.current = false;
    };
  }, []);
  useEffect(() => {
    let active = true;
    setVersions(null);
    setAgreed(false);
    setAgreementError('');
    void Promise.all([api.page('terms'), api.page('privacy')])
      .then(([terms, privacy]) => {
        const next = { terms: Number(terms.version), privacy: Number(privacy.version) };
        if (
          !terms.published ||
          !privacy.published ||
          !Object.values(next).every((v) => Number.isInteger(v) && v > 0)
        )
          throw new Error('协议未就绪');
        if (active) setVersions(next);
      })
      .catch(() => {
        if (active) setAgreementError('服务协议或隐私说明暂不可用，请稍后重试。');
      });
    return () => {
      active = false;
    };
  }, [retry]);
  async function submit(event: FormEvent) {
    event.preventDefault();
    if (busy) return;
    setError('');
    if (Array.from(password).length < 8 || new TextEncoder().encode(password).length > 72)
      return setError('密码至少需要 8 位，且不超过 72 字节。');
    if (password !== confirm) return setError('两次输入的密码不一致。');
    if (!agreed || !versions) return setError('请阅读并同意服务协议与隐私说明。');
    setBusy(true);
    try {
      await api.register({
        ...proof,
        purpose: 'login',
        password,
        password_confirm: confirm,
        agreement: {
          accepted: true,
          terms_version: versions.terms,
          privacy_version: versions.privacy,
        },
      });
      if (alive.current) await onComplete();
    } catch (e) {
      if (alive.current) {
        setError(e instanceof Error ? e.message : '注册未完成，请稍后重试。');
        if ((e as { code?: string }).code === 'AGREEMENT_CHANGED') setRetry((v) => v + 1);
      }
    } finally {
      if (alive.current) setBusy(false);
    }
  }
  return (
    <Modal
      title="完成注册"
      className="registration-complete"
      onClose={() => {
        if (!busy) onClose();
      }}
    >
      <p className="registration-phone">
        <span aria-hidden="true">✓</span> 手机号 {proof.phone.slice(0, 3)}****{proof.phone.slice(7)}{' '}
        已验证
      </p>
      <p className="muted">设置登录密码并同意协议，即可创建清叶账户。</p>
      <form className="member-form" onSubmit={submit}>
        <label className="form-field">
          设置密码
          <div className="input-action">
            <input
              autoFocus
              type={visible ? 'text' : 'password'}
              autoComplete="new-password"
              placeholder="至少 8 位密码"
              value={password}
              onChange={(e) => setPassword(e.target.value)}
              minLength={8}
              maxLength={128}
              required
              disabled={busy}
            />
            <button
              type="button"
              className="text-button"
              onClick={() => setVisible((v) => !v)}
              aria-label={visible ? '隐藏密码' : '显示密码'}
            >
              {visible ? '隐藏' : '显示'}
            </button>
          </div>
        </label>
        <label className="form-field">
          确认密码
          <input
            type={visible ? 'text' : 'password'}
            autoComplete="new-password"
            placeholder="再次输入密码"
            value={confirm}
            onChange={(e) => setConfirm(e.target.value)}
            minLength={8}
            maxLength={128}
            required
            disabled={busy}
          />
        </label>
        <label className="agreement-check">
          <input
            type="checkbox"
            checked={agreed}
            onChange={(e) => setAgreed(e.target.checked)}
            disabled={!versions || busy}
          />
          <span>
            我已阅读并同意{' '}
            <Link to="/help/terms" target="_blank">
              服务协议
            </Link>{' '}
            与{' '}
            <Link to="/help/privacy" target="_blank">
              隐私说明
            </Link>
          </span>
        </label>
        {!versions && (
          <div className="auth-service-note" role="status">
            {agreementError || '正在加载服务协议与隐私说明…'}
            {agreementError && (
              <button type="button" className="text-button" onClick={() => setRetry((v) => v + 1)}>
                重新加载协议
              </button>
            )}
          </div>
        )}
        {error && (
          <p className="form-error" role="alert">
            {error}
          </p>
        )}
        <button className="button-white full-width" type="submit" disabled={busy || !versions}>
          {busy ? '正在创建账户…' : '同意协议并注册登录'}
        </button>
        <button type="button" className="text-button" disabled={busy} onClick={onClose}>
          暂不注册，返回登录
        </button>
      </form>
    </Modal>
  );
}
