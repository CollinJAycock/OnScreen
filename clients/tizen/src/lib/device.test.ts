import { describe, expect, it } from 'vitest';
import {
  CLIENT_NAME_MAX,
  computeClientName,
  formatClientName,
  installTag,
  modelNameFrom,
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

describe('modelNameFrom', () => {
  it("reads ProductInfo's real model first, then its model", () => {
    expect(modelNameFrom({ getRealModel: () => 'QN75Q80BAFXZA', getModel: () => '22_PONTUSM' })).toBe('QN75Q80BAFXZA');
    expect(modelNameFrom({ getRealModel: () => '', getModel: () => ' UN55TU7000 ' })).toBe('UN55TU7000');
  });
  it('goes on to the next when one throws (a refused privilege)', () => {
    expect(modelNameFrom({ getRealModel: () => { throw new Error('denied'); }, getModel: () => 'QE65S95C' })).toBe('QE65S95C');
  });
  it("is '' for anything unreadable", () => {
    expect(modelNameFrom(undefined)).toBe('');
    expect(modelNameFrom(null)).toBe('');
    expect(modelNameFrom({ getRealModel: () => 42 })).toBe('');
    expect(modelNameFrom({})).toBe('');
  });
});

describe('formatClientName', () => {
  it('names the platform, the model and the install tag', () => {
    expect(formatClientName('QN75Q80BAFXZA', 'a1b2')).toBe('Samsung TV — QN75Q80BAFXZA #a1b2');
  });
  it('drops the model part when there is none', () => {
    expect(formatClientName('', '0f0f')).toBe('Samsung TV #0f0f');
  });
  it('caps at 60 characters and keeps the tag whole', () => {
    const name = formatClientName('X'.repeat(100), 'beef');
    expect(name.length).toBe(CLIENT_NAME_MAX);
    expect(name.endsWith(' #beef')).toBe(true);
    expect(name.startsWith('Samsung TV — XXX')).toBe(true);
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
  it('still names the TV when storage throws', () => {
    const broken = {
      getItem: () => { throw new Error('blocked'); },
      setItem: () => { throw new Error('blocked'); },
    };
    expect(installTag(broken, () => 0)).toBe('0000');
  });
});

describe('computeClientName', () => {
  it('puts it together from the device info and the stored tag', () => {
    const store = memoryStore({ 'onscreen.client_tag': 'c0de' });
    expect(computeClientName({ productInfo: { getRealModel: () => 'QN65QN90B' }, store })).toBe(
      'Samsung TV — QN65QN90B #c0de',
    );
  });
});
