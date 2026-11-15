import { afterEach, describe, expect, it, vi } from 'vitest';
import {
  PROBE_TYPES,
  buildClientCapabilitiesHeader,
  clientCapabilitiesHeader,
  supportsAV1,
  supportsHEVC,
  type CapabilityProbes,
} from './capabilities';
import { FHD_PANEL, UHD_PANEL } from '../platform';

/** Header keys as a map, so one key can be checked at a time. */
function keys(header: string): Record<string, string> {
  return Object.fromEntries(header.split(',').map((kv) => kv.split('=') as [string, string]));
}

/** Probes for a TV whose MSE takes every type in `supported`. */
function probes(supported: string[], over: Partial<CapabilityProbes> = {}): CapabilityProbes {
  return {
    isTypeSupported: (t) => supported.includes(t),
    panel: UHD_PANEL,
    panelHdr: null,
    mediaHdr: false,
    ...over,
  };
}

const ALL = Object.values(PROBE_TYPES);

describe('buildClientCapabilitiesHeader', () => {
  it('claims no vp9 when MSE refuses VP9 in fMP4', () => {
    const h = keys(buildClientCapabilitiesHeader(probes(ALL.filter((t) => t !== PROBE_TYPES.vp9))));
    expect(h.videoDecoder).toBe('h264');
    expect(h.vp9MaxBitDepth).toBeUndefined();
  });

  it('claims vp9 with its own bit depth when MSE takes it', () => {
    expect(keys(buildClientCapabilitiesHeader(probes(ALL))).vp9MaxBitDepth).toBe('10');
    const eightBit = keys(buildClientCapabilitiesHeader(probes([PROBE_TYPES.vp9])));
    expect(eightBit.videoDecoder).toBe('h264:vp9');
    expect(eightBit.vp9MaxBitDepth).toBe('8');
  });

  it('takes maxWidth / maxHeight from the panel', () => {
    const h = keys(buildClientCapabilitiesHeader(probes(ALL, { panel: FHD_PANEL })));
    expect(h.maxWidth).toBe('1920');
    expect(h.maxHeight).toBe('1080');
  });

  it("claims HDR on the panel's own say-so, over the media query", () => {
    expect(keys(buildClientCapabilitiesHeader(probes(ALL, { panelHdr: true }))).hdr).toBe('1');
    expect(keys(buildClientCapabilitiesHeader(probes(ALL, { panelHdr: false, mediaHdr: true }))).hdr).toBe('0');
  });

  it('falls back to the media query while the panel is unknown', () => {
    expect(keys(buildClientCapabilitiesHeader(probes(ALL, { mediaHdr: true }))).hdr).toBe('1');
    expect(keys(buildClientCapabilitiesHeader(probes(ALL))).hdr).toBe('0');
  });

  // A Series X's MSE says yes to both, but its web view decodes neither.
  it('never claims HEVC or AV1, whatever MSE says', () => {
    const yes = probes([], { isTypeSupported: () => true });
    const h = keys(buildClientCapabilitiesHeader(yes));
    expect(h.videoDecoder).toBe('h264:vp9');
    expect(h.maxbitdepth).toBe('8');
    expect(supportsHEVC()).toBe(false);
    expect(supportsAV1()).toBe(false);
  });

  it('keeps the fixed audio and container keys', () => {
    const h = keys(buildClientCapabilitiesHeader(probes(ALL)));
    expect(h.audioDecoder).toBe('aac:mp3:opus:flac');
    expect(h.protocols).toBe('mp4:webm:mov');
    expect(h.maxAudioChannels).toBe('6');
    expect(h.maxbitdepth).toBe('8');
    // Plays an optimized version the decision hands out (v2.6).
    expect(h.optimized).toBe('1');
  });

  it('builds the whole header in a stable order', () => {
    expect(buildClientCapabilitiesHeader(probes([PROBE_TYPES.vp9], { panel: FHD_PANEL }))).toBe(
      'videoDecoder=h264:vp9,audioDecoder=aac:mp3:opus:flac,protocols=mp4:webm:mov,' +
        'maxWidth=1920,maxHeight=1080,maxAudioChannels=6,maxbitdepth=8,vp9MaxBitDepth=8,hdr=0,optimized=1',
    );
  });
});

describe('buildClientCapabilitiesHeader with the native player', () => {
  // The console's own pipeline decodes HEVC 10-bit, HDR10 and Dolby audio
  // (checked on a Series X), so the server can send HDR films as they are.
  it('claims HEVC 10-bit, Dolby audio, MKV and the HDR the shell found', () => {
    const h = keys(buildClientCapabilitiesHeader(probes([], { native: true, panelHdr: true })));
    expect(h.videoDecoder).toBe('h264:h265');
    expect(h.audioDecoder).toBe('aac:mp3:ac3:eac3');
    expect(h.protocols).toBe('mp4:mkv:mov');
    expect(h.maxbitdepth).toBe('10');
    expect(h.hdr).toBe('1');
    expect(h.dovi).toBe('8.1');
    expect(h.vp9MaxBitDepth).toBeUndefined();
  });

  it('claims no HDR on an SDR TV', () => {
    const sdr = keys(buildClientCapabilitiesHeader(probes([], { native: true, panelHdr: false })));
    expect(sdr.hdr).toBe('0');
    expect(sdr.dovi).toBeUndefined();
  });

  it('drops HEVC, its depth and HDR once HEVC is demoted', () => {
    const h = keys(buildClientCapabilitiesHeader(probes([], { native: true, panelHdr: true, isDemoted: (c) => c === 'hevc' })));
    expect(h.videoDecoder).toBe('h264');
    expect(h.maxbitdepth).toBe('8');
    expect(h.hdr).toBe('0');
  });
});

describe('clientCapabilitiesHeader (runtime)', () => {
  it('stays sane outside a TV: H.264 only, the 4K default, no HDR', () => {
    // No MediaSource, PalmSystem, PalmServiceBridge or window under node.
    expect(clientCapabilitiesHeader()).toBe(
      'videoDecoder=h264,audioDecoder=aac:mp3:opus:flac,protocols=mp4:webm:mov,' +
        'maxWidth=3840,maxHeight=2160,maxAudioChannels=6,maxbitdepth=8,hdr=0,optimized=1',
    );
  });
});

// The registry lives in module state read from localStorage at load, so each
// test loads a fresh copy over its own storage.
describe('codec demotions (registry)', () => {
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
  afterEach(() => {
    vi.unstubAllGlobals();
    vi.useRealTimers();
  });

  it('keeps a demotion across launches until it expires', async () => {
    vi.useFakeTimers();
    vi.setSystemTime(new Date('2026-11-15T00:00:00Z'));
    const { mod, store } = await load();
    mod.demoteCodec('hevc');
    expect(mod.isCodecDemoted('hevc')).toBe(true);
    expect(mod.isCodecDemoted('av1')).toBe(false);
    const again = await load(Object.fromEntries(store.map));
    expect(again.mod.isCodecDemoted('h265')).toBe(true);
    vi.setSystemTime(Date.now() + mod.DEMOTION_TTL_MS + 1);
    // Expired: in the running app as on the next launch.
    expect(again.mod.isCodecDemoted('hevc')).toBe(false);
    expect((await load(Object.fromEntries(store.map))).mod.isCodecDemoted('hevc')).toBe(false);
  });

  it('drops the old list format, written by the rule that demoted whatever failed', async () => {
    const { mod } = await load({ 'onscreen:demoted-codecs': '["hevc","av1"]' });
    expect(mod.isCodecDemoted('hevc')).toBe(false);
    expect(mod.isCodecDemoted('av1')).toBe(false);
  });

  it('forgets them all on clearCodecDemotions (sign-out, Forget server)', async () => {
    const { mod, store } = await load();
    mod.demoteCodec('hevc');
    mod.demoteCodec('av1');
    mod.clearCodecDemotions();
    expect(mod.isCodecDemoted('hevc')).toBe(false);
    expect(mod.isCodecDemoted('av1')).toBe(false);
    expect(store.map.has('onscreen:demoted-codecs')).toBe(false);
  });
});
