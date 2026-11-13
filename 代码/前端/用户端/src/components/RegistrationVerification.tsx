import { useEffect, useRef } from 'react';
import { loadWidget } from '../../../共享/MediaSecurity';

export type SmsChallenge = { challenge_id: string; site_key: string; action: string };

export function RegistrationVerification({
  challenge,
  onToken,
  onError,
  onCancel,
  sending,
}: {
  challenge: SmsChallenge;
  onToken: (token: string) => void;
  onError: (message: string) => void;
  onCancel: () => void;
  sending: boolean;
}) {
  const container = useRef<HTMLDivElement>(null);
  const callbacks = useRef({ onToken, onError });
  callbacks.current = { onToken, onError };
  useEffect(() => {
    let active = true;
    let used = false;
    let widget: string | undefined;
    let remove: ((id: string) => void) | undefined;
    const failed = () => {
      if (active && !used) callbacks.current.onError('安全验证未完成，请重新获取验证码。');
    };
    void loadWidget()
      .then((api) => {
        if (!active || !container.current) return;
        remove = (id) => api.remove(id);
        widget = api.render(container.current, {
          sitekey: challenge.site_key,
          action: challenge.action,
          cData: challenge.challenge_id,
          theme: document.documentElement.classList.contains('dark') ? 'dark' : 'light',
          size: 'flexible',
          retry: 'never',
          'refresh-expired': 'never',
          callback: (token: string) => {
            if (active && !used) {
              used = true;
              callbacks.current.onToken(token);
            }
          },
          'error-callback': failed,
          'expired-callback': failed,
          'timeout-callback': failed,
        });
      })
      .catch((error: unknown) => {
        if (active)
          callbacks.current.onError(error instanceof Error ? error.message : '验证服务暂不可用。');
      });
    return () => {
      active = false;
      if (widget !== undefined) remove?.(widget);
    };
  }, [challenge.challenge_id, challenge.site_key, challenge.action]);
  return (
    <div className="registration-verification" aria-label="短信安全验证">
      <div className="registration-verification-heading">
        <span>{sending ? '正在提交短信请求…' : '完成安全验证后发送验证码'}</span>
        <button type="button" className="text-button" disabled={sending} onClick={onCancel}>
          取消
        </button>
      </div>
      <div ref={container} />
    </div>
  );
}
