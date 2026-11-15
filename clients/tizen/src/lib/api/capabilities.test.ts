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
  vi.useRealTimers();
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

  it('declares that it plays optimized versions', async () => {
    const { mod } = await load();
    expect(keys(mod.clientCapabilitiesHeader()).optimized).toBe('1');
    mod.demoteCodec('hevc');
    expect(keys(mod.clientCapabilitiesHeader()).optimized).toBe('1');
  });

  it('drops HEVC and 10-bit once a panel proves it cannot decode HEVC, until the demotion expires', async () => {
    vi.useFakeTimers();
    vi.setSystemTime(new Date('2026-11-15T00:00:00Z'));
    const { mod, store } = await load();
    mod.demoteCodec('hevc');
    const h = keys(mod.clientCapabilitiesHeader());
    expect(h.videoDecoder).toBe('h264');
    expect(h.maxbitdepth).toBe('8');
    expect(mod.supportsHEVC()).toBe(false);
    expect(mod.isCodecDemoted('h265')).toBe(true);
    expect(JSON.parse(store.map.get('onscreen:demoted-codecs') as string)).toEqual({
      hevc: Date.now() + mod.DEMOTION_TTL_MS,
    });
    // The next launch reads it back.
    const again = await load(Object.fromEntries(store.map));
    expect(again.mod.supportsHEVC()).toBe(false);
    // Expired: HEVC is claimed again, in the running app as on the next launch.
    vi.setSystemTime(Date.now() + mod.DEMOTION_TTL_MS + 1);
    expect(again.mod.supportsHEVC()).toBe(true);
    expect(keys(again.mod.clientCapabilitiesHeader()).videoDecoder).toBe('h264:h265');
    expect((await load(Object.fromEntries(store.map))).mod.supportsHEVC()).toBe(true);
  });

  it('drops the old list format, written by the rule that demoted whatever failed', async () => {
    const { mod } = await load({ 'onscreen:demoted-codecs': '["hevc"]' });
    expect(mod.supportsHEVC()).toBe(true);
  });

  it('forgets demotions on clearCodecDemotions (sign-out, Forget server)', async () => {
    const { mod, store } = await load();
    mod.demoteCodec('hevc');
    mod.clearCodecDemotions();
    expect(mod.supportsHEVC()).toBe(true);
    expect(store.map.has('onscreen:demoted-codecs')).toBe(false);
  });
});
