import { ArrowUpRight, MessageCircle, QrCode } from 'lucide-react';
import { Link } from 'react-router-dom';
import { Modal } from './shared';
import '../consultation.css';

export function ConsultationOptions({
  productId,
  title,
  code,
  onClose,
  onQQ,
}: {
  productId: string;
  title: string;
  code: string;
  onClose: () => void;
  onQQ: () => void;
}) {
  return (
    <Modal title="选择咨询方式" onClose={onClose} className="consultation-dialog">
      <div className="consultation-context">
        <span className="mono">{code}</span>
        <p>{title}</p>
      </div>
      <div className="consultation-options">
        <Link
          className="consultation-option"
          to={`/messages?product=${encodeURIComponent(productId)}`}
          onClick={onClose}
        >
          <span className="consultation-icon">
            <MessageCircle />
          </span>
          <span>
            <strong>在线咨询客服</strong>
            <small>在站内沟通，商品资料与聊天记录一起查看</small>
          </span>
          <ArrowUpRight aria-hidden="true" />
        </Link>
        <button type="button" className="consultation-option" onClick={onQQ}>
          <span className="consultation-icon">
            <QrCode />
          </span>
          <span>
            <strong>联系 QQ 客服</strong>
            <small>打开客服二维码，使用 QQ 扫码联系</small>
          </span>
          <ArrowUpRight aria-hidden="true" />
        </button>
      </div>
      <p className="consultation-footnote">在线咨询需登录；扫码联系 QQ 客服无需登录。</p>
    </Modal>
  );
}
