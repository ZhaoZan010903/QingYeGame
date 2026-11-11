import { useState } from 'react';
import { chatTransport } from '../lib/chat-api';

export function ChatConnectionNotice({ message }: { message: string }) {
  const [retryNote, setRetryNote] = useState('');
  const expired = message.includes('登录状态已失效');
  return (
    <div className="chat-connection-note">
      <div className="chat-connection-summary">
        <p role="status">{message || '正在连接客服…'}</p>
        {!expired && (
          <button type="button" className="button-outline" onClick={() => {
            setRetryNote(chatTransport.reconnect() ? '正在重新连接…' : '连接正在尝试中，请稍候。');
          }}>重新连接</button>
        )}
      </div>
      {retryNote && <p className="chat-connection-feedback" role="status">{retryNote}</p>}
      <details className="chat-connection-help">
        <summary>网页正常打开，为什么聊天连不上？</summary>
        <p>聊天使用独立的实时连接。网页能打开，不代表这条连接已经建立。</p>
        <ol>
          <li>如使用 Clash 等系统代理，可将 <code>{window.location.hostname}</code> 加入“系统代理绕过／排除地址”，保留其他网站走代理。仅在代理软件中添加 DIRECT 分流规则可能仍不生效。</li>
          <li>若使用 TUN 或公司网络，请检查该站点的 UDP 连接是否允许；也可换一个网络对照测试。</li>
          <li>确认通过 HTTPS 访问，并使用更新的浏览器。调整网络设置后点击“重新连接”。</li>
        </ol>
        <p>连接恢复前无法发送消息，当前输入不会自动重发。仍失败时，请反馈浏览器版本和上方提示。</p>
      </details>
    </div>
  );
}
