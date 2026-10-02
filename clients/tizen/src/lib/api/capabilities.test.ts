import { beforeEach, describe, expect, it, vi } from 'vitest';

// The module keeps its demotions in module state read from localStorage at
// load, so each test loads a fresh copy over its own storage.
function memoryStorage(initial: Record<string, string> = {}) {
  const m = new Map(Object.entries(initial));
  return {
    getItem: (k: string) => (m.has(k) ? (m.get(k) as string) : null),
    setItem: (k: string, v: string) => void m.set(k, String(v)),
    removeItem: (k: string) => void m.delete(k),
    map: m,
  };
}

async function load(initial: Record<string, string> = {}) {
  const store = memoryStorage(initial);
  vi.stubGlobal('localStorage', store);
  vi.resetModules();
  return { mod: await import('./capabilities'), store };
}

const keys = (h: string) => Object.fromEntries(h.split(',').map((kv) => kv.split('=')));

beforeEach(() => {
  vi.unstubAllGlobals();
});

describe('the Tizen capability profile', () => {
  it('claims H.264 + HEVC, 4K, 10-bit and HDR on the AVPlay path, never AV1', async () => {
    const { mod } = await load();
    const h = keys(mod.clientCapabilitiesHeader());
    expect(h.videoDecoder).toBe('h264:h265');
    expect(h.maxWidth).toBe('3840');
    expect(h.maxHeight).toBe('2160');
    expect(h.maxbitdepth).toBe('10');
    expect(h.hdr).toBe('1');
    expect(mod.supportsHEVC()).toBe(true);
    expect(mod.supportsAV1()).toBe(false);
  });

  it('drops HEVC and 10-bit once a panel proves it cannot decode HEVC, for good', async () => {
    const { mod, store } = await load();
    mod.demoteCodec('hevc');
    const h = keys(mod.clientCapabilitiesHeader());
    expect(h.videoDecoder).toBe('h264');
    expect(h.maxbitdepth).toBe('8');
    expect(mod.supportsHEVC()).toBe(false);
    expect(mod.isCodecDemoted('h265')).toBe(true);
    expect(JSON.parse(store.map.get('onscreen:demoted-codecs') as string)).toEqual(['hevc']);
    // The next launch reads it back.
    const again = await load(Object.fromEntries(store.map));
    expect(again.mod.supportsHEVC()).toBe(false);
  });
});
