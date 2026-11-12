import { describe, expect, it, vi } from 'vitest';
import { ChatOutbox } from '../../../共享/chat-outbox';
import { validateChatImage, CHAT_IMAGE_BYTES } from '../../../共享/chat-upload';
import { chatApi, chatTransport } from './chat-api';

describe('R19 图文消息', () => {
  it('重试保留图片顺序与消息标识，修改附件生成新意图', () => {
    const box = new ChatOutbox();
    let n = 0;
    const id = () => String(++n);
    const ids = ['one', 'two'];
    const first = box.prepare('a', '配置', id, ids);
    ids[0] = 'mutated';
    expect(first.images).toEqual(['one', 'two']);
    expect(box.prepare('a', '配置', id, ['one', 'two'])).toBe(first);
    expect(box.prepare('a', '配置', id, ['two', 'one']).client).not.toBe(first.client);
    box.acknowledge(first);
    expect(box.prepare('a', '配置', id, ['two', 'one']).client).toBe('2');
  });
  it('限制图片大小及格式，不接受伪装扩展名或空文件', () => {
    expect(validateChatImage({ name: '截图.PNG', type: 'image/png', size: CHAT_IMAGE_BYTES })).toBe(
      '',
    );
    for (const file of [
      { name: 'x.png', type: 'image/png', size: CHAT_IMAGE_BYTES + 1 },
      { name: 'x.svg', type: 'image/svg+xml', size: 50 },
      { name: 'x.jpg', type: 'image/png', size: 50 },
      { name: 'x.png', type: 'image/png', size: 0 },
    ])
      expect(validateChatImage(file)).not.toBe('');
    expect(
      validateChatImage({ name: 'x.png', type: 'image/png', size: 2 * 1024 * 1024 + 1 }, true),
    ).not.toBe('');
  });
  it('图文通过当前实时通道发送，只传受控图片编号', async () => {
    const command = vi.spyOn(chatTransport, 'command').mockResolvedValue({ data: { id: '42' } });
    try {
      await chatApi.send('conversation', '说明', 'intent', ['one', 'two']);
      expect(command).toHaveBeenCalledWith('send', 'conversation', {
        body: '说明',
        client_id: 'intent',
        image_ids: ['one', 'two'],
      });
    } finally {
      command.mockRestore();
    }
  });
});
