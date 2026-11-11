import { useState } from "react";
import { chatTransport } from "../lib/chat-api";

export function ChatConnectionNotice({ message }: { message: string }) {
  const [retryNote, setRetryNote] = useState("");
  const expired = message.includes("登录状态已失效");
  return (
    <div className="chat-connection-note">
      <div className="chat-connection-summary">
        <p role="status">{message || "正在连接客服…"}</p>
        {!expired && (
          <button
            type="button"
            className="button-outline"
            onClick={() => {
              setRetryNote(
                chatTransport.reconnect()
                  ? "正在重新连接…"
                  : "连接正在尝试中，请稍候。",
              );
            }}
          >
            重新连接
          </button>
        )}
      </div>
      {retryNote && (
        <p className="chat-connection-feedback" role="status">
          {retryNote}
        </p>
      )}
      <details className="chat-connection-help">
        <summary>网页正常打开，为什么聊天连不上？</summary>
        <p>系统会自动尝试兼容连接，使用 Clash 等代理时通常无需调整设置。</p>
        <ol>
          <li>确认通过 HTTPS 打开页面，并等待连接完成。</li>
          <li>
            如果仍无法连接，请检查代理节点能否正常访问本站，再点击“重新连接”。
          </li>
          <li>也可更换代理节点或网络后重试。</li>
        </ol>
        <p>
          连接恢复前无法发送消息，当前输入不会自动重发。仍失败时，请反馈浏览器版本和上方提示。
        </p>
      </details>
    </div>
  );
}
