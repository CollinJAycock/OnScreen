import { describe, expect, it } from 'vitest';
import {
  PROBE_TYPES,
  buildClientCapabilitiesHeader,
  clientCapabilitiesHeader,
  dolbyVisionProfiles,
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
    isDemoted: () => false,
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
    expect(h.videoDecoder).toBe('h264:h265:av1');
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

  it('drops a demoted codec and its bit depth', () => {
    const h = keys(
      buildClientCapabilitiesHeader(probes(ALL, { isDemoted: (c) => c === 'hevc' })),
    );
    expect(h.videoDecoder).toBe('h264:vp9:av1');
    expect(h.maxbitdepth).toBe('8');
    const noAv1 = keys(buildClientCapabilitiesHeader(probes(ALL, { isDemoted: (c) => c === 'av1' })));
    expect(noAv1.videoDecoder).toBe('h264:vp9:h265');
  });

  it('keeps the fixed audio and container keys', () => {
    const h = keys(buildClientCapabilitiesHeader(probes(ALL)));
    expect(h.audioDecoder).toBe('aac:mp3:opus:flac');
    expect(h.protocols).toBe('mp4:webm:mov');
    expect(h.maxAudioChannels).toBe('6');
    expect(h.maxbitdepth).toBe('10');
    // Plays an optimized version the decision hands out (v2.6).
    expect(h.optimized).toBe('1');
  });

  it('builds the whole header in a stable order', () => {
    expect(buildClientCapabilitiesHeader(probes([PROBE_TYPES.hevc], { panel: FHD_PANEL }))).toBe(
      'videoDecoder=h264:h265,audioDecoder=aac:mp3:opus:flac,protocols=mp4:webm:mov,' +
        'maxWidth=1920,maxHeight=1080,maxAudioChannels=6,maxbitdepth=8,hdr=0,optimized=1',
    );
  });
});

describe('Dolby Vision (dovi)', () => {
  const dvTV = { panelDv: true, panelHdr: true };

  it('declares 5, 8.1 and 8.4 on a Dolby Vision TV whose MSE takes dvh1', () => {
    const p = probes(ALL, dvTV);
    expect(dolbyVisionProfiles(p)).toEqual(['5', '8.1', '8.4']);
    expect(keys(buildClientCapabilitiesHeader(p)).dovi).toBe('5:8.1:8.4');
  });

  it('declares only what MSE takes', () => {
    const no5 = probes(ALL.filter((t) => t !== PROBE_TYPES.dv5), dvTV);
    expect(keys(buildClientCapabilitiesHeader(no5)).dovi).toBe('8.1:8.4');
    const only5 = probes(ALL.filter((t) => t !== PROBE_TYPES.dv8), dvTV);
    expect(keys(buildClientCapabilitiesHeader(only5)).dovi).toBe('5');
  });

  it('never declares profile 7 (MSE takes one layer)', () => {
    expect(dolbyVisionProfiles(probes(ALL, dvTV))).not.toContain('7');
  });

  // MSE answering yes isn't enough: the panel has to show it, or a working
  // HDR10 stream becomes a failed Dolby Vision one. Unknown counts as no.
  it('declares nothing unless the TV says it shows Dolby Vision', () => {
    for (const panelDv of [false, null, undefined]) {
      expect(keys(buildClientCapabilitiesHeader(probes(ALL, { panelHdr: true, panelDv }))).dovi).toBeUndefined();
    }
  });

  it('declares nothing without HEVC Main 10, or once HEVC is demoted', () => {
    const noMain10 = probes(ALL.filter((t) => t !== PROBE_TYPES.hevc10), dvTV);
    expect(dolbyVisionProfiles(noMain10)).toEqual([]);
    const demoted = probes(ALL, { ...dvTV, isDemoted: (c) => c === 'hevc' });
    expect(keys(buildClientCapabilitiesHeader(demoted)).dovi).toBeUndefined();
  });

  it('probes the codec strings hls.js appends', () => {
    expect(PROBE_TYPES.dv8).toBe('video/mp4; codecs="dvh1.08.06"');
    expect(PROBE_TYPES.dv5).toBe('video/mp4; codecs="dvh1.05.06"');
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
