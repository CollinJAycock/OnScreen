import { describe, expect, it } from 'vitest';
import {
  CLIENT_NAME_MAX,
  computeClientName,
  formatClientName,
  installTag,
  randomTag,
} from './device';

function memoryStore(initial: Record<string, string> = {}) {
  const m = new Map(Object.entries(initial));
  return {
    getItem: (k: string) => m.get(k) ?? null,
    setItem: (k: string, v: string) => void m.set(k, v),
    map: m,
  };
}

describe('formatClientName', () => {
  it('names the console model and the install tag', () => {
    expect(formatClientName('Xbox Series X', 'a1b2')).toBe('Xbox Series X #a1b2');
  });
  it('is plain Xbox without a model', () => {
    expect(formatClientName('', '0f0f')).toBe('Xbox #0f0f');
    expect(formatClientName('  ', '0f0f')).toBe('Xbox #0f0f');
  });
  it('caps at 60 characters and keeps the tag whole', () => {
    const name = formatClientName('X'.repeat(100), 'beef');
    expect(name.length).toBe(CLIENT_NAME_MAX);
    expect(name.endsWith(' #beef')).toBe(true);
    expect(name.startsWith('XXX')).toBe(true);
  });
});

describe('install tag', () => {
  it('is four hex digits', () => {
    expect(randomTag(() => 0)).toBe('0000');
    expect(randomTag(() => 0.999999)).toBe('ffff');
    expect(randomTag(() => 0.5)).toBe('8000');
  });
  it('is made once and then read back', () => {
    const store = memoryStore();
    const first = installTag(store, () => 0.25);
    expect(first).toBe('4000');
    expect(installTag(store, () => 0.75)).toBe('4000');
  });
  it('replaces a corrupt stored tag', () => {
    const store = memoryStore({ 'onscreen.client_tag': 'zz' });
    expect(installTag(store, () => 0.5)).toBe('8000');
    expect(store.map.get('onscreen.client_tag')).toBe('8000');
  });
  it('still names the console when storage throws', () => {
    const broken = {
      getItem: () => { throw new Error('blocked'); },
      setItem: () => { throw new Error('blocked'); },
    };
    expect(installTag(broken, () => 0)).toBe('0000');
  });
});

describe('computeClientName', () => {
  it("puts it together from the shell's model and the stored tag", () => {
    const store = memoryStore({ 'onscreen.client_tag': 'c0de' });
    expect(computeClientName({ model: 'Xbox One X', store })).toBe('Xbox One X #c0de');
  });
});
