import { describe, expect, it } from 'vitest';
import { EntranceGesture } from './entrance-gesture';
describe('homepage downward intent', () => {
  it('accumulates a short downward gesture and enters only once', () => {
    const g = new EntranceGesture();
    expect(g.move(0, 10, 10, true, false)).toBe('pass');
    expect(g.move(0, 14, 30, true, false)).toBe('enter');
    expect(g.move(0, 50, 40, true, true)).toBe('hold');
    expect(g.move(0, 50, 50, true, false)).toBe('pass');
  });
  it('does not hijack horizontal gestures, content browsing or isolated tiny movements', () => {
    const g = new EntranceGesture();
    expect(g.move(60, 10, 10, true, false)).toBe('pass');
    expect(g.move(0, 100, 30, false, false)).toBe('pass');
    expect(g.move(0, 12, 40, true, false)).toBe('pass');
    expect(g.move(0, 12, 400, true, false)).toBe('pass');
  });
  it('upward intent stops automatic scrolling, and returning to the top can rearm', () => {
    const g = new EntranceGesture();
    expect(g.move(0, 100, 1, true, false)).toBe('enter');
    expect(g.move(0, -5, 20, true, true)).toBe('cancel');
    expect(g.move(0, 100, 40, true, false)).toBe('pass');
    g.reset();
    expect(g.move(0, 25, 60, true, false)).toBe('enter');
  });
});
