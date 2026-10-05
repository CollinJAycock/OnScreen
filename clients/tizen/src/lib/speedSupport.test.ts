import { describe, expect, it } from 'vitest';
import { rememberSpeedUnsupported, speedKnownUnsupported } from './speedSupport';

describe('speedSupport', () => {
  it('remembers that this TV ignores playback speed', () => {
    const m = new Map<string, string>();
    const store = { getItem: (k: string) => m.get(k) ?? null, setItem: (k: string, v: string) => void m.set(k, v) };
    expect(speedKnownUnsupported(store)).toBe(false);
    rememberSpeedUnsupported(store);
    expect(speedKnownUnsupported(store)).toBe(true);
  });

  it('treats unreadable storage as unknown', () => {
    const broken = { getItem: () => { throw new Error('blocked'); }, setItem: () => { throw new Error('blocked'); } };
    expect(speedKnownUnsupported(broken)).toBe(false);
    expect(() => rememberSpeedUnsupported(broken)).not.toThrow();
  });
});
