import { createContext, useContext, useEffect, useState, type ReactNode } from 'react';
import { api, isDemo } from './lib/api';
import type { User } from './types';
import { Modal, StateView } from './components/shared';
import { useLoad } from './lib-use';
const Session = createContext<{
  user: User | null;
  loading: boolean;
  error: unknown;
  refresh: () => Promise<void>;
  notify: (s: string) => void;
}>(null!);
const Ui = createContext<{
  notify: (s: string) => void;
  contact: (id?: string, purpose?: string) => void;
}>(null!);
export const useSession = () => useContext(Session);
export const useUi = () => useContext(Ui);
function ContactDialog({
  id,
  purpose,
  onClose,
  notify,
}: {
  id?: string;
  purpose?: string;
  onClose: () => void;
  notify: (s: string) => void;
}) {
  const result = useLoad(() => api.contacts(purpose), purpose || 'all');
  return (
    <Modal title="联系清叶客服" onClose={onClose}>
      <p className="dialog-intro">
        {id ? '咨询账号资料、报价及交付安排。' : '选择你需要咨询的服务，平台客服协助处理。'}
      </p>
      {id && (
        <div className="contact-product">
          <span>
            咨询商品 <b className="mono">{id}</b>
          </span>
          <button
            className="button-outline"
            onClick={() =>
              (navigator.clipboard ? navigator.clipboard.writeText(id) : Promise.reject())
                .then(() => notify('商品编号已复制'))
                .catch(() => notify(`请手动复制编号：${id}`))
            }
          >
            复制编号
          </button>
        </div>
      )}
      {result.loading || result.error ? (
        <StateView {...result} onRetry={result.retry} />
      ) : result.data?.length ? (
        result.data.map((c) => (
          <div className="contact-card" key={c.id}>
            <strong>{c.name}</strong>
            <p>
              {c.channel} · {c.value}
            </p>
            {c.hours && <small>{c.hours}</small>}
            {c.qrUrl && <img src={c.qrUrl} alt={`${c.name} 联系二维码`} />}
            <button
              className="button-outline"
              onClick={() =>
                (navigator.clipboard ? navigator.clipboard.writeText(c.value) : Promise.reject())
                  .then(() => notify('联系方式已复制'))
                  .catch(() => notify(`请手动复制：${c.value}`))
              }
            >
              复制联系方式
            </button>
          </div>
        ))
      ) : (
        <div className="notice-box">
          <h3>客服信息尚未配置</h3>
          <p>
            {isDemo
              ? '当前为演示环境，未提供真实客服账号或二维码。'
              : '平台尚未提供此用途的客服配置，请稍后再试。'}
          </p>
        </div>
      )}
    </Modal>
  );
}
export function Providers({ children }: { children: ReactNode }) {
  const [user, setUser] = useState<User | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<unknown>();
  const [message, notify] = useState('');
  const [support, setSupport] = useState<{ id?: string; purpose?: string } | null>(null);
  const refresh = async () => {
    setError(undefined);
    try {
      setUser(await api.me());
    } catch (e) {
      setError(e);
    } finally {
      setLoading(false);
    }
  };
  useEffect(() => {
    void refresh();
  }, []);
  useEffect(() => {
    if (!message) return;
    const timer = setTimeout(() => notify(''), 4500);
    return () => clearTimeout(timer);
  }, [message]);
  return (
    <Session.Provider value={{ user, loading, error, refresh, notify }}>
      <Ui.Provider value={{ notify, contact: (id, purpose) => setSupport({ id, purpose }) }}>
        {children}
        {support && <ContactDialog {...support} notify={notify} onClose={() => setSupport(null)} />}
        <div className={`toast ${message ? 'visible' : ''}`} role="status">
          {message}
        </div>
      </Ui.Provider>
    </Session.Provider>
  );
}
