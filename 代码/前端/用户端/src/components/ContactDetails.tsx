import { useState } from 'react';
import { Copy, QrCode, RefreshCw } from 'lucide-react';
import type { Contact } from '../types';
import '../consultation.css';

export function ContactDetails({
  contact,
  notify,
}: {
  contact: Contact;
  notify: (message: string) => void;
}) {
  const [failed, setFailed] = useState(false),
    [revision, setRevision] = useState(0);
  const channel =
    contact.channel.toLowerCase() === 'qq'
      ? 'QQ'
      : contact.channel.toLowerCase() === 'wechat'
        ? '微信'
        : contact.channel;
  return (
    <section className="contact-details" aria-label={contact.name}>
      <header>
        <h3>{contact.name}</h3>
        {contact.hours && <p>服务时间：{contact.hours}</p>}
      </header>
      <div className="contact-qr-frame">
        {contact.qrUrl && !failed ? (
          <img
            key={revision}
            src={contact.qrUrl}
            alt={`${contact.name} ${channel}联系二维码`}
            onError={() => setFailed(true)}
          />
        ) : (
          <div className="contact-qr-empty">
            <QrCode />
            <span>{failed ? '二维码加载失败' : '暂未上传二维码'}</span>
            {failed && (
              <button
                type="button"
                className="text-button"
                onClick={() => {
                  setFailed(false);
                  setRevision((v) => v + 1);
                }}
              >
                <RefreshCw />
                重新加载
              </button>
            )}
          </div>
        )}
      </div>
      <p className="contact-scan-hint">
        {channel === 'QQ'
          ? '打开 QQ 扫一扫，或保存二维码后在 QQ 中识别。'
          : '使用对应应用扫码，或复制下方联系方式。'}
      </p>
      <div className="contact-account">
        <span>{channel} 号码</span>
        <strong className="mono">{contact.value}</strong>
      </div>
      <button
        type="button"
        className="button-white contact-copy"
        onClick={() =>
          (navigator.clipboard ? navigator.clipboard.writeText(contact.value) : Promise.reject())
            .then(() => notify(`${channel}联系方式已复制`))
            .catch(() => notify(`请手动复制：${contact.value}`))
        }
      >
        <Copy />
        复制{channel}联系方式
      </button>
    </section>
  );
}
