import { useEffect, useState, type FormEvent } from 'react';
import { Link, useLocation, useNavigate, useSearchParams } from 'react-router-dom';
import { api, isDemo } from '../lib/api';
import { useSession, useUi } from '../context';
import '../member.css';

const errorText = (error: unknown) =>
  error instanceof Error ? error.message : '操作未完成，请稍后重试。';
const smsEnabled = isDemo || import.meta.env.VITE_SMS_ENABLED === 'true';
export function safeReturnTo(value: string | null) {
  if (
    !value ||
    !value.startsWith('/') ||
    value.startsWith('//') ||
    value.includes('\\') ||
    /[\u0000-\u001f]/.test(value)
  )
    return '/me';
  return /^\/(login|register|forgot-password)(?:[/?#]|$)/.test(value) ? '/me' : value;
}

export function AuthPage() {
  const location = useLocation();
  const navigate = useNavigate();
  const [params] = useSearchParams();
  const { user, refresh } = useSession();
  const { notify } = useUi();
  const register = location.pathname === '/register';
  const reset = location.pathname === '/forgot-password';
  const title = register ? '开启你的旅程' : reset ? '找回账户密码' : '欢迎回来';
  const returnTo = safeReturnTo(params.get('returnTo'));
  const suffix = `?returnTo=${encodeURIComponent(returnTo)}`;
  const [method, setMethod] = useState<'sms' | 'password'>('password');
  const [phone, setPhone] = useState('');
  const [password, setPassword] = useState('');
  const [code, setCode] = useState('');
  const [challenge, setChallenge] = useState('');
  const [agreed, setAgreed] = useState(false);
  const [showPassword, setShowPassword] = useState(false);
  const [busy, setBusy] = useState(false);
  const [sending, setSending] = useState(false);
  const [remaining, setRemaining] = useState(0);
  const [error, setError] = useState('');
  const needsSms = register || reset || method === 'sms';
  useEffect(() => {
    setError('');
    setPassword('');
    setCode('');
    setChallenge('');
    setAgreed(false);
    setRemaining(0);
  }, [location.pathname]);
  useEffect(() => {
    if (user && !reset) navigate(returnTo, { replace: true });
  }, [user, reset, returnTo, navigate]);
  useEffect(() => {
    if (remaining <= 0) return;
    const timer = window.setTimeout(() => setRemaining((v) => Math.max(v - 1, 0)), 1000);
    return () => window.clearTimeout(timer);
  }, [remaining]);
  async function sendSms() {
    if (!smsEnabled) {
      setError('短信服务暂未配置，请使用已开通账户的密码登录。');
      return;
    }
    if (!/^1[3-9]\d{9}$/.test(phone)) {
      setError('请输入有效的 11 位手机号。');
      return;
    }
    setSending(true);
    setError('');
    try {
      const result = await api.sms(
        phone,
        register ? 'register' : reset ? 'reset_password' : 'login',
      );
      if (!result.challenge_id) throw new Error('短信服务未返回有效凭据，请稍后重试。');
      setChallenge(result.challenge_id);
      setRemaining(Math.max(0, Number(result.retry_after) || 0));
      notify('验证码已发送，请查看手机短信。');
    } catch (e) {
      setError(errorText(e));
    } finally {
      setSending(false);
    }
  }
  async function submit(event: FormEvent) {
    event.preventDefault();
    setError('');
    if (needsSms && !smsEnabled) {
      setError('短信服务暂未配置，请使用已开通账户的密码登录。');
      return;
    }
    if (!/^1[3-9]\d{9}$/.test(phone)) {
      setError('请输入有效的 11 位手机号。');
      return;
    }
    if (needsSms && !challenge) {
      setError(
        isDemo
          ? '当前为前端演示，请使用下方「进入演示账户」。演示不发送短信。'
          : '请先获取短信验证码。',
      );
      return;
    }
    if (needsSms && !/^\d{4,8}$/.test(code)) {
      setError('请输入短信中的验证码。');
      return;
    }
    if ((!needsSms || register || reset) && password.length < 8) {
      setError('密码至少需要 8 位。');
      return;
    }
    if (register && !agreed) {
      setError('请阅读并同意服务协议与隐私说明。');
      return;
    }
    setBusy(true);
    try {
      if (reset) {
        await api.resetPassword({ phone, code, challenge_id: challenge, new_password: password });
        await refresh();
        notify('密码已更新，请重新登录。');
        navigate('/login' + suffix, { replace: true });
      } else {
        if (register) {
          const [terms, privacy] = await Promise.all([api.page('terms'), api.page('privacy')]);
          if (!terms.published || !privacy.published || !terms.version || !privacy.version)
            throw new Error('服务协议与隐私说明尚未配置正式版本，暂时无法注册。');
          await api.register({
            phone,
            code,
            challenge_id: challenge,
            password,
            agreement: {
              accepted: true,
              terms_version: terms.version,
              privacy_version: privacy.version,
            },
          });
        } else
          await api.login(
            method === 'sms'
              ? { method, phone, code, challenge_id: challenge }
              : { method, phone, password },
          );
        await refresh();
        navigate(returnTo, { replace: true });
      }
    } catch (e) {
      setError(errorText(e));
    } finally {
      setBusy(false);
    }
  }
  async function demo() {
    setBusy(true);
    setError('');
    try {
      await api.demoLogin();
      await refresh();
      navigate(returnTo, { replace: true });
    } catch (e) {
      setError(errorText(e));
    } finally {
      setBusy(false);
    }
  }
  return (
    <section className="auth-page page-width mx-auto" aria-label="账户登录与注册">
      <section className="auth-story" aria-label="清叶会员">
        <h1>
          每一份热爱，
          <br />
          <span>都有归处。</span>
        </h1>
        <p>
          收藏心仪账号，整理你的珍藏，
          <br />
          在清叶开启下一段故事。
        </p>
        <div className="auth-art asset-art art-0" aria-hidden="true" />
      </section>
      <section className="auth-panel panel">
        <header className="auth-heading">
          <p className="auth-welcome">{title}</p>
          <h2 className="auth-brand">
            <svg viewBox="0 0 33 32" fill="none" aria-hidden="true">
              <path d="M23 6H11L5 16l6 10h12l5-10-5-10Z" />
              <path d="m11 19 5-9 5 9h-10Zm7 3 5 5" />
            </svg>
            <span>清叶</span>
          </h2>
        </header>
        <p className="muted">
          {reset
            ? '验证手机号后设置新密码。'
            : register
              ? '注册后即可收藏和提交账号资料。'
              : '登录后，继续你的收藏与发布。'}
        </p>
        {!register && !reset && (
          <div className="member-tabs" role="tablist" aria-label="登录方式">
            {(['password', 'sms'] as const).map((value) => (
              <button
                key={value}
                type="button"
                role="tab"
                aria-selected={method === value}
                className={method === value ? 'active' : ''}
                onClick={() => {
                  setMethod(value);
                  setError('');
                }}
              >
                {value === 'password' ? '密码登录' : '短信登录'}
              </button>
            ))}
          </div>
        )}
        {!smsEnabled && (
          <p className="auth-service-note" role="note">
            当前测试环境使用已开通账户的密码登录。短信登录、注册与找回密码暂未启用。
          </p>
        )}
        <form onSubmit={submit} className="member-form">
          <label className="form-field">
            手机号
            <div className="phone-input">
              <span>+86</span>
              <input
                type="tel"
                inputMode="tel"
                autoComplete="tel"
                value={phone}
                onChange={(e) => {
                  setPhone(e.target.value.replace(/\D/g, '').slice(0, 11));
                  setChallenge('');
                }}
                placeholder="请输入手机号"
                required
                maxLength={11}
                disabled={busy}
              />
            </div>
          </label>
          {needsSms && (
            <label className="form-field">
              短信验证码
              <div className="input-action">
                <input
                  inputMode="numeric"
                  autoComplete="one-time-code"
                  value={code}
                  onChange={(e) => setCode(e.target.value.replace(/\D/g, '').slice(0, 8))}
                  placeholder="输入验证码"
                  required
                  disabled={busy}
                />
                <button
                  type="button"
                  className="text-button"
                  onClick={sendSms}
                  disabled={!smsEnabled || sending || busy || remaining > 0}
                >
                  {sending ? '正在发送…' : remaining > 0 ? `${remaining}s 后重发` : '获取验证码'}
                </button>
              </div>
            </label>
          )}
          {(!needsSms || register || reset) && (
            <label className="form-field">
              {reset ? '新密码' : '密码'}
              <div className="input-action">
                <input
                  type={showPassword ? 'text' : 'password'}
                  autoComplete={register || reset ? 'new-password' : 'current-password'}
                  value={password}
                  onChange={(e) => setPassword(e.target.value)}
                  placeholder="至少 8 位密码"
                  required
                  minLength={8}
                  maxLength={128}
                  disabled={busy}
                />
                <button
                  className="text-button"
                  type="button"
                  aria-label={showPassword ? '隐藏密码' : '显示密码'}
                  onClick={() => setShowPassword((v) => !v)}
                >
                  {showPassword ? '隐藏' : '显示'}
                </button>
              </div>
            </label>
          )}
          {register && (
            <label className="agreement-check">
              <input
                type="checkbox"
                checked={agreed}
                onChange={(e) => setAgreed(e.target.checked)}
              />{' '}
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
          )}
          {error && (
            <p className="form-error" role="alert">
              {error}
            </p>
          )}
          <button
            className="button-white full-width"
            disabled={busy || (needsSms && !smsEnabled)}
            type="submit"
          >
            {busy ? '正在处理…' : register ? '注册账户' : reset ? '确认重置密码' : '登录'}
            <span aria-hidden="true">↗</span>
          </button>
        </form>
        <div className="auth-links">
          {!register && !reset ? (
            <>
              <Link to={'/register' + suffix}>创建账户</Link>
              <Link to={'/forgot-password' + suffix}>忘记密码？</Link>
            </>
          ) : (
            <Link to={'/login' + suffix}>已有账户？返回登录</Link>
          )}
        </div>
        {isDemo && (
          <div className="demo-account">
            <span>先体验，再开启你的收藏</span>
            <p>
              当前未连接业务后台。演示账户用于体验收藏、草稿和发布流程，不发送短信或提交真实审核。
            </p>
            <button
              type="button"
              className="button-outline full-width"
              onClick={demo}
              disabled={busy}
            >
              进入演示账户
            </button>
          </div>
        )}
        <Link className="auth-home text-button" to="/">
          ← 返回首页，继续浏览
        </Link>
      </section>
    </section>
  );
}
