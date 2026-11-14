import { useEffect, useRef, useState, type FormEvent } from 'react';
import { Link, useLocation, useNavigate, useSearchParams } from 'react-router-dom';
import { api, isDemo } from '../lib/api';
import { useSession, useUi } from '../context';
import '../member.css';
import { CompleteRegistration, type VerifiedPhone } from '../components/CompleteRegistration';
import {
  RegistrationVerification,
  type SmsChallenge,
} from '../components/RegistrationVerification';

const errorText = (error: unknown) =>
  error instanceof Error ? error.message : '操作未完成，请稍后重试。';
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
  const { notify, contact } = useUi();
  const register = location.pathname === '/register';
  const reset = location.pathname === '/forgot-password';
  const title = register ? '开启你的旅程' : reset ? '找回账户密码' : '欢迎回来';
  const returnTo = safeReturnTo(params.get('returnTo'));
  const suffix = `?returnTo=${encodeURIComponent(returnTo)}`;
  const [method, setMethod] = useState<'sms' | 'password'>('password');
  const [phone, setPhone] = useState('');
  const [password, setPassword] = useState('');
  const [passwordConfirm, setPasswordConfirm] = useState('');
  const [pendingRegistration, setPendingRegistration] = useState<VerifiedPhone | null>(null);
  const [code, setCode] = useState('');
  const [challenge, setChallenge] = useState('');
  const [agreed, setAgreed] = useState(false);
  const [showPassword, setShowPassword] = useState(false);
  const [busy, setBusy] = useState(false);
  const [sending, setSending] = useState(false);
  const [remaining, setRemaining] = useState(0);
  const [error, setError] = useState('');
  const [capabilities, setCapabilities] = useState({
    registration_sms: false,
    sms_login: false,
    password_reset: false,
  });
  const [capabilitiesStatus, setCapabilitiesStatus] = useState<'loading' | 'ready' | 'error'>(
    'loading',
  );
  const [capabilitiesRetry, setCapabilitiesRetry] = useState(0);
  const [verification, setVerification] = useState<SmsChallenge | null>(null);
  const [agreementVersions, setAgreementVersions] = useState<{
    terms: number;
    privacy: number;
  } | null>(null);
  const [agreementError, setAgreementError] = useState('');
  const [agreementRetry, setAgreementRetry] = useState(0);
  const generation = useRef(0);
  const smsEnabled = register
    ? capabilities.registration_sms
    : reset
      ? capabilities.password_reset
      : capabilities.sms_login;
  const needsSms = register || reset || method === 'sms';
  const resetUnavailable = reset && capabilitiesStatus === 'ready' && !capabilities.password_reset;
  useEffect(() => {
    let active = true;
    const controller = new AbortController();
    setCapabilitiesStatus('loading');
    void api
      .authCapabilities(AbortSignal.any([controller.signal, AbortSignal.timeout(15000)]))
      .then((result) => {
        if (active) {
          setCapabilities(result);
          setCapabilitiesStatus('ready');
        }
      })
      .catch(() => {
        if (active) setCapabilitiesStatus('error');
      });
    return () => {
      active = false;
      controller.abort();
      generation.current++;
    };
  }, [capabilitiesRetry]);
  useEffect(() => {
    if (
      !register &&
      !reset &&
      params.get('method') === 'sms' &&
      capabilitiesStatus === 'ready' &&
      capabilities.sms_login
    )
      setMethod('sms');
  }, [register, reset, params, capabilitiesStatus, capabilities.sms_login]);
  useEffect(() => {
    generation.current++;
    setVerification(null);
    setSending(false);
    setError('');
    setPassword('');
    setPasswordConfirm('');
    setPendingRegistration(null);
    setBusy(false);
    setCode('');
    setChallenge('');
    setAgreed(false);
    setRemaining(0);
    setAgreementVersions(null);
  }, [location.pathname]);
  useEffect(() => {
    if (!register) return;
    let active = true;
    setAgreementError('');
    setAgreementVersions(null);
    setAgreed(false);
    void Promise.all([api.page('terms'), api.page('privacy')])
      .then(([terms, privacy]) => {
        if (!active) return;
        const versions = { terms: Number(terms.version), privacy: Number(privacy.version) };
        if (
          !terms.published ||
          !privacy.published ||
          !Object.values(versions).every((v) => Number.isInteger(v) && v > 0)
        )
          throw new Error('协议尚未发布');
        setAgreementVersions(versions);
      })
      .catch(() => {
        if (active) setAgreementError('服务协议或隐私说明暂不可用，暂时无法注册。请稍后重试。');
      });
    return () => {
      active = false;
    };
  }, [register, agreementRetry]);
  useEffect(() => {
    if (user && !reset) navigate(returnTo, { replace: true });
  }, [user, reset, returnTo, navigate]);
  useEffect(() => {
    if (remaining <= 0) return;
    const timer = window.setTimeout(() => setRemaining((v) => Math.max(v - 1, 0)), 1000);
    return () => window.clearTimeout(timer);
  }, [remaining]);
  async function sendSms() {
    if (capabilitiesStatus !== 'ready') {
      setError('请先重新读取登录方式，再获取验证码。');
      return;
    }
    if (register && !agreementVersions) {
      setError('服务协议与隐私说明尚未就绪，请先重新加载协议。');
      return;
    }
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
    const version = ++generation.current;
    setChallenge('');
    setCode('');
    try {
      const result = await api.smsChallenge(phone, register ? 'register' : 'login');
      if (version !== generation.current) return;
      if (!result.challenge_id) throw new Error('短信服务未返回有效凭据，请稍后重试。');
      setVerification(result);
    } catch (e) {
      if (version === generation.current) setError(errorText(e));
    } finally {
      if (version === generation.current) setSending(false);
    }
  }
  async function verifyAndSend(token: string) {
    if (!verification || sending) return;
    const version = generation.current;
    setSending(true);
    try {
      const result = await api.sms(
        phone,
        register ? 'register' : 'login',
        verification.challenge_id,
        token,
      );
      if (version !== generation.current) return;
      setChallenge(result.challenge_id);
      setRemaining(Math.max(60, Number(result.retry_after) || 60));
      notify('验证码已提交发送，请查看手机短信。');
    } catch (e) {
      if (version === generation.current) {
        setError(errorText(e));
        setRemaining(60);
      }
    } finally {
      if (version === generation.current) {
        setSending(false);
        setVerification(null);
      }
    }
  }
  async function submit(event: FormEvent) {
    event.preventDefault();
    setError('');
    if (needsSms && capabilitiesStatus !== 'ready') {
      setError('请先重新读取登录方式，再继续操作。');
      return;
    }
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
    if (needsSms && !/^\d{6}$/.test(code)) {
      setError('请输入短信中的验证码。');
      return;
    }
    if (
      (!needsSms || register || reset) &&
      (Array.from(password).length < 8 || new TextEncoder().encode(password).length > 72)
    ) {
      setError('密码至少需要 8 位，且不超过 72 字节。');
      return;
    }
    if (register && !agreed) {
      setError('请阅读并同意服务协议与隐私说明。');
      return;
    }
    if (register && password !== passwordConfirm) {
      setError('两次输入的密码不一致。');
      return;
    }
    const version = generation.current;
    setBusy(true);
    try {
      if (reset) {
        await api.resetPassword({ phone, code, challenge_id: challenge, new_password: password });
        await refresh();
        notify('密码已更新，请重新登录。');
        navigate('/login' + suffix, { replace: true });
      } else {
        if (register) {
          if (!agreementVersions)
            throw new Error('服务协议与隐私说明尚未配置正式版本，暂时无法注册。');
          await api.register({
            phone,
            code,
            challenge_id: challenge,
            password,
            password_confirm: passwordConfirm,
            agreement: {
              accepted: true,
              terms_version: agreementVersions.terms,
              privacy_version: agreementVersions.privacy,
            },
          });
        } else if (method === 'sms') {
          const proof = { phone, code, challenge_id: challenge };
          const result = await api.smsLogin(proof);
          if (version !== generation.current) return;
          if (result.status === 'registration_required') {
            setPendingRegistration(proof);
            return;
          }
        } else await api.login({ method, phone, password });
        if (version !== generation.current) return;
        await refresh();
        navigate(returnTo, { replace: true });
      }
    } catch (e) {
      if (version === generation.current) setError(errorText(e));
    } finally {
      if (version === generation.current) setBusy(false);
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
            ? resetUnavailable
              ? '选择可用的登录方式，或联系平台客服协助。'
              : '查看可用的账户找回方式。'
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
                disabled={
                  busy ||
                  sending ||
                  (value === 'sms' && (capabilitiesStatus !== 'ready' || !capabilities.sms_login))
                }
                className={method === value ? 'active' : ''}
                onClick={() => {
                  setMethod(value);
                  generation.current++;
                  setVerification(null);
                  setChallenge('');
                  setCode('');
                  setPassword('');
                  setError('');
                }}
              >
                {value === 'password' ? '密码登录' : '短信登录'}
              </button>
            ))}
          </div>
        )}
        {!register && !reset && method === 'sms' && (
          <p className="auth-sms-hint">已有账户验证后直接登录；新手机号验证后可继续注册。</p>
        )}
        {capabilitiesStatus === 'loading' && (
          <p className="auth-service-note" role="status">
            正在读取登录方式…
          </p>
        )}
        {capabilitiesStatus === 'error' && (
          <div className="auth-service-note" role="alert">
            <p>暂时无法读取登录方式，请重试。密码登录仍可使用。</p>
            <button
              type="button"
              className="text-button"
              disabled={busy || sending || verification !== null}
              onClick={() => {
                setError('');
                setCapabilitiesRetry((value) => value + 1);
              }}
            >
              重新读取登录方式
            </button>
          </div>
        )}
        {resetUnavailable && (
          <div className="auth-service-note">
            <p>自助找回密码暂未开放。</p>
            <p>
              {capabilities.sms_login
                ? '已有账户可通过手机验证码登录；如需处理密码问题，请联系平台客服。'
                : '如需找回账户，请联系平台客服协助。'}
            </p>
            <div className="auth-recovery-actions">
              {capabilities.sms_login && (
                <Link className="button-white" to={'/login' + suffix + '&method=sms'}>
                  使用短信登录
                </Link>
              )}
              <button type="button" className="button-outline" onClick={() => contact()}>
                联系平台客服
              </button>
            </div>
          </div>
        )}
        {!reset && capabilitiesStatus === 'ready' && needsSms && !smsEnabled && (
          <p className="auth-service-note" role="note">
            {register ? '注册短信暂未启用，请稍后再试。' : '此短信功能暂未启用，请使用密码登录。'}
          </p>
        )}
        {(!reset || (capabilitiesStatus === 'ready' && capabilities.password_reset)) && (
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
                    generation.current++;
                    setCode('');
                    setVerification(null);
                    setSending(false);
                    setError('');
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
                    onChange={(e) => setCode(e.target.value.replace(/\D/g, '').slice(0, 6))}
                    placeholder="输入验证码"
                    required
                    disabled={busy}
                  />
                  <button
                    type="button"
                    className="text-button"
                    onClick={sendSms}
                    disabled={
                      !smsEnabled ||
                      capabilitiesStatus !== 'ready' ||
                      sending ||
                      busy ||
                      remaining > 0 ||
                      verification !== null ||
                      (register && !agreementVersions)
                    }
                  >
                    {sending ? '正在发送…' : remaining > 0 ? `${remaining}s 后重发` : '获取验证码'}
                  </button>
                </div>
              </label>
            )}
            {verification && (
              <RegistrationVerification
                challenge={verification}
                sending={sending}
                onToken={verifyAndSend}
                onError={(message) => {
                  setError(message);
                  setVerification(null);
                }}
                onCancel={() => {
                  generation.current++;
                  setVerification(null);
                }}
              />
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
              <label className="form-field">
                确认密码
                <input
                  type={showPassword ? 'text' : 'password'}
                  autoComplete="new-password"
                  value={passwordConfirm}
                  onChange={(e) => setPasswordConfirm(e.target.value)}
                  placeholder="再次输入密码"
                  minLength={8}
                  maxLength={128}
                  required
                  disabled={busy}
                />
              </label>
            )}
            {register && (
              <label className="agreement-check">
                <input
                  type="checkbox"
                  checked={agreed}
                  onChange={(e) => setAgreed(e.target.checked)}
                  disabled={!agreementVersions || busy}
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
            {register && !agreementVersions && (
              <div className="auth-service-note" role="status">
                {agreementError || '正在加载服务协议与隐私说明…'}
                {agreementError && (
                  <button
                    type="button"
                    className="text-button"
                    onClick={() => setAgreementRetry((v) => v + 1)}
                  >
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
            <button
              className="button-white full-width"
              disabled={
                busy ||
                sending ||
                verification !== null ||
                (needsSms && !smsEnabled) ||
                (needsSms && capabilitiesStatus !== 'ready') ||
                (register && !agreementVersions)
              }
              type="submit"
            >
              {busy ? '正在处理…' : register ? '注册账户' : reset ? '确认重置密码' : '登录'}
              <span aria-hidden="true">↗</span>
            </button>
          </form>
        )}
        {pendingRegistration && (
          <CompleteRegistration
            proof={pendingRegistration}
            onClose={() => {
              setPendingRegistration(null);
              setError('');
            }}
            onComplete={async () => {
              await refresh();
              navigate(returnTo, { replace: true });
            }}
          />
        )}
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
