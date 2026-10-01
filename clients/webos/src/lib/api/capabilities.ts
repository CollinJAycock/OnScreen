// X-Client-Capabilities profile for LG webOS TVs.
//
// webOS plays video through hls.js + MediaSource Extensions (see the watch
// page), so — exactly like the web client — actual codec support is *probed*
// via MediaSource.isTypeSupported rather than assumed. (Tizen differs: it uses
// the native AVPlay hardware path and can claim HEVC/AC-3 unconditionally.)
// Audio over MSE is limited to the browser-decodable set (no AC-3/E-AC-3/DTS),
// and 7.1 AAC is undecodable over MSE, so channels cap at 5.1. See
// docs/capability-profiles.md for the grammar.
//
// The header itself is built by a pure function from injected probes
// (buildClientCapabilitiesHeader, unit-tested); clientCapabilitiesHeader()
// feeds it the live ones. The panel's size and HDR come from lib/platform
// (PalmSystem.deviceInfo, and Luna getConfigs where the bridge answers).

import { panelHdr, readPanelSize, startPanelProbe, type PanelSize } from '../platform';

/** The MSE type strings each claim is probed with. hls.js hands MSE fMP4,
 *  so every probe names the mp4 container. */
export const PROBE_TYPES = {
  hevc: 'video/mp4; codecs="hvc1.1.6.L150.B0"',
  hevc10: 'video/mp4; codecs="hvc1.2.4.L150.B0"',
  av1: 'video/mp4; codecs="av01.0.05M.08"',
  // webOS's spec lists VP9 in mkv only; whether this firmware takes it in
  // fMP4 (what hls.js would append) is exactly what this asks.
  vp9: 'video/mp4; codecs="vp09.00.10.08"',
  // VP9 Profile 2 (10-bit).
  vp9Profile2: 'video/mp4; codecs="vp09.02.10.10"',
} as const;

function isTypeSupported(s: string): boolean {
  try {
    return typeof MediaSource !== 'undefined' && MediaSource.isTypeSupported(s);
  } catch {
    return false; // MSE unavailable
  }
}

// ── Runtime codec demotion (mirrors the web client) ─────────────────────────
//
// isTypeSupported is a CLAIM, not a promise: a webview can enumerate a
// platform decoder and still reject the actual SourceBuffer append. When the
// watch page proves a claim wrong (a fatal bufferAppendError on a stream the
// claim produced), the codec is demoted here so both the capability header
// and the transcode-start supports_hevc flag tell the server the truth — the
// escalated retry then really comes back H.264 instead of the codec that just
// failed. Persisted in localStorage: unlike a desktop browser (where a
// missing HEVC extension can be installed later), a TV panel's hardware
// decode support never changes, so a proven demotion is permanent.

const DEMOTED_KEY = 'onscreen:demoted-codecs';

function readDemotions(): Array<'hevc' | 'av1'> {
  try {
    const v = JSON.parse(localStorage.getItem(DEMOTED_KEY) ?? '[]');
    return Array.isArray(v) ? v.filter((c): c is 'hevc' | 'av1' => c === 'hevc' || c === 'av1') : [];
  } catch {
    return [];
  }
}

const demoted = new Set<'hevc' | 'av1'>(readDemotions());

/** Record a codec the panel PROVED it cannot decode. Idempotent. */
export function demoteCodec(codec: 'hevc' | 'av1'): void {
  if (demoted.has(codec)) return;
  demoted.add(codec);
  try {
    localStorage.setItem(DEMOTED_KEY, JSON.stringify([...demoted]));
  } catch {
    // Storage blocked — the in-memory demotion still covers this launch.
  }
}

/** Map a media file's codec string onto the demotion registry. */
export function isCodecDemoted(codec: string | undefined): boolean {
  const c = (codec ?? '').toLowerCase();
  if (c === 'hevc' || c === 'h265') return demoted.has('hevc');
  if (c === 'av1') return demoted.has('av1');
  return false;
}

/** Whether this TV can decode HEVC over MSE (hls.js transmuxes to fMP4,
 *  so we probe the mp4 codec string). Drives the transcode-start
 *  supports_hevc flag — telling the server we can take HEVC lets it
 *  stream-copy or HEVC-encode instead of falling back to H.264 on a
 *  panel that can't actually decode it. A runtime demotion overrides
 *  the probe. */
export function supportsHEVC(): boolean {
  return !demoted.has('hevc') && isTypeSupported(PROBE_TYPES.hevc);
}

/** Whether this TV can decode AV1 over MSE (webOS 5.0+ UHD panels). Drives
 *  the transcode-start supports_av1 flag, sent on every start as an explicit
 *  bool (Android parity), so a runtime demotion reaches the server as false.
 *  The capability header's av1 claim reads it too. */
export function supportsAV1(): boolean {
  return !demoted.has('av1') && isTypeSupported(PROBE_TYPES.av1);
}

/** Everything the header depends on, injected so the builder is pure. */
export interface CapabilityProbes {
  /** MediaSource.isTypeSupported (false when MSE is missing). */
  isTypeSupported: (type: string) => boolean;
  /** A codec this panel has PROVED it can't decode (runtime demotion). */
  isDemoted: (codec: 'hevc' | 'av1') => boolean;
  /** The video plane's size (lib/platform readPanelSize). */
  panel: PanelSize;
  /** Luna's tv.model.supportHDR: true / false, null when unknown. */
  panelHdr: boolean | null;
  /** matchMedia('(dynamic-range: high)'), the answer before Luna (always
   *  false before Chrome 98, i.e. on webOS 6/22/23). */
  mediaHdr: boolean;
}

/**
 * The X-Client-Capabilities value for these probes.
 *   - vp9 only when MSE takes VP9 in fMP4 (it used to be claimed outright,
 *     though webOS lists VP9 in mkv only), with vp9MaxBitDepth from the
 *     Profile 2 probe (Android sends the same key; without it the server
 *     takes 10-bit VP9 for playable wherever VP9 is).
 *   - maxWidth / maxHeight from the panel, so an FHD set isn't sent 4K.
 *   - hdr from the panel's own answer when Luna gave one, else the media
 *     query (the long-standing behaviour).
 */
export function buildClientCapabilitiesHeader(p: CapabilityProbes): string {
  const hevc = !p.isDemoted('hevc') && p.isTypeSupported(PROBE_TYPES.hevc);
  const hevc10bit = !p.isDemoted('hevc') && p.isTypeSupported(PROBE_TYPES.hevc10);
  const av1 = !p.isDemoted('av1') && p.isTypeSupported(PROBE_TYPES.av1);
  const vp9 = p.isTypeSupported(PROBE_TYPES.vp9);
  const vp9Profile2 = vp9 && p.isTypeSupported(PROBE_TYPES.vp9Profile2);
  const hdr = p.panelHdr ?? p.mediaHdr;

  const video = ['h264'];
  if (vp9) video.push('vp9');
  if (hevc) video.push('h265');
  if (av1) video.push('av1');

  const keys = [
    `videoDecoder=${video.join(':')}`,
    'audioDecoder=aac:mp3:opus:flac',
    'protocols=mp4:webm:mov',
    `maxWidth=${p.panel.width}`,
    `maxHeight=${p.panel.height}`,
    'maxAudioChannels=6',
    `maxbitdepth=${hevc10bit ? 10 : 8}`,
  ];
  if (vp9) keys.push(`vp9MaxBitDepth=${vp9Profile2 ? 10 : 8}`);
  keys.push(`hdr=${hdr ? 1 : 0}`);
  return keys.join(',');
}

function mediaQueryHdr(): boolean {
  try {
    return typeof window !== 'undefined' && typeof window.matchMedia === 'function' &&
      window.matchMedia('(dynamic-range: high)').matches;
  } catch {
    return false;
  }
}

/** The header for this TV, sent on every API request (client.ts). The
 *  first call also starts the one-off Luna panel probe. */
export function clientCapabilitiesHeader(): string {
  startPanelProbe();
  return buildClientCapabilitiesHeader({
    isTypeSupported,
    isDemoted: (codec) => demoted.has(codec),
    panel: readPanelSize(),
    panelHdr: panelHdr(),
    mediaHdr: mediaQueryHdr(),
  });
}
