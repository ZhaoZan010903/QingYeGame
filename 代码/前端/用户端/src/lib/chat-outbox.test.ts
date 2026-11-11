import { describe, expect, it, vi } from 'vitest';
import { ChatOutbox } from '../../../共享/chat-outbox';

describe('uncertain chat delivery across conversations', () => {
  it('reuses A’s original deduplication key after sending in B', () => {
    const box = new ChatOutbox();
    const id = vi.fn().mockReturnValueOnce('a-request').mockReturnValueOnce('b-request');
    const a = box.prepare('A', '想了解价格', id);
    // A persisted on the server, but its acknowledgement was lost. B is sent meanwhile.
    const b = box.prepare('B', '这个账号还在吗', id);
    box.acknowledge(b);
    expect(box.prepare('A', '想了解价格', id).client).toBe(a.client);
    expect(id).toHaveBeenCalledTimes(2);
  });

  it('starts a new intent after confirmed delivery, but preserves a replacement during late ack', () => {
    const box = new ChatOutbox();
    let n = 0;
    const id = () => String(++n);
    const first = box.prepare('A', '原问题', id);
    const replacement = box.prepare('A', '修改的问题', id);
    box.acknowledge(first);
    expect(box.prepare('A', '修改的问题', id)).toBe(replacement);
    box.acknowledge(replacement);
    expect(box.prepare('A', '修改的问题', id).client).not.toBe(replacement.client);
  });

  it('clears identity-owned retries and ignores an old account acknowledgement', () => {
    const box = new ChatOutbox();
    let n = 0;
    const id = () => String(++n);
    const old = box.prepare('A', '问题', id);
    box.clear();
    const current = box.prepare('A', '问题', id);
    box.acknowledge(old);
    expect(current.client).not.toBe(old.client);
    expect(box.prepare('A', '问题', id)).toBe(current);
  });
});
