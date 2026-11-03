// X-Client-Capabilities profile for the Xbox app.
//
// The app plays video through hls.js + MediaSource Extensions in the shell's
// WebView2 (see the watch page). VP9 is probed via MediaSource.isTypeSupported;
// HEVC and AV1 are not claimed at all, because the probe can't be trusted for
// them here. A Series X (OS 26100, WebView2 150, November 2026) answers yes for
// HEVC, AV1, AC-3 and E-AC-3, yet its web view decodes none of them: every
// HEVC stream (Main / Main 10, Main / High tier, 1080p / 4K) fails decoder
// set-up with kUnsupportedConfig, and AV1 and AC-3 / E-AC-3 append but never
// produce a frame. H.264 and VP9 (Profile 0 and 2) play. Claimed, HEVC and AV1
// only cost a failed start and a demotion before the H.264 retry. Audio stays
// at the browser-decodable set, and 7.1 AAC is undecodable over MSE, so
// channels cap at 5.1. See docs/capability-profiles.md for the grammar.
//
// The header itself is built by a pure function from injected probes
// (buildClientCapabilitiesHeader, unit-tested); clientCapabilitiesHeader()
// feeds it the live ones. The display's size and HDR come from lib/platform
// (the shell's HDMI answer).

import { panelHdr, readPanelSize, startPanelProbe, type PanelSize } from '../platform';

/** The MSE type strings each claim is probed with. hls.js hands MSE fMP4,
 *  so every probe names the mp4 container. */
export const PROBE_TYPES = {
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

/** The transcode-start supports_hevc flag: always false, so the server
 *  never stream-copies or HEVC-encodes for the Xbox web view, which can't
 *  decode HEVC whatever MSE claims (see the top of this file). */
export function supportsHEVC(): boolean {
  return false;
}

/** The transcode-start supports_av1 flag, sent on every start as an
 *  explicit bool (Android parity): always false, as for HEVC. */
export function supportsAV1(): boolean {
  return false;
}

/** Everything the header depends on, injected so the builder is pure. */
export interface CapabilityProbes {
  /** MediaSource.isTypeSupported (false when MSE is missing). */
  isTypeSupported: (type: string) => boolean;
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
 *   - h264, and no h265 / av1 (see the top of this file), so maxbitdepth
 *     stays 8: the server transcodes 10-bit H.264 whatever it says.
 *   - vp9 only when MSE takes VP9 in fMP4 (it used to be claimed outright,
 *     though webOS lists VP9 in mkv only), with vp9MaxBitDepth from the
 *     Profile 2 probe (Android sends the same key; without it the server
 *     takes 10-bit VP9 for playable wherever VP9 is).
 *   - maxWidth / maxHeight from the panel, so an FHD set isn't sent 4K.
 *   - hdr from the panel's own answer when Luna gave one, else the media
 *     query (the long-standing behaviour).
 */
export function buildClientCapabilitiesHeader(p: CapabilityProbes): string {
  const vp9 = p.isTypeSupported(PROBE_TYPES.vp9);
  const vp9Profile2 = vp9 && p.isTypeSupported(PROBE_TYPES.vp9Profile2);
  const hdr = p.panelHdr ?? p.mediaHdr;

  const video = ['h264'];
  if (vp9) video.push('vp9');

  const keys = [
    `videoDecoder=${video.join(':')}`,
    'audioDecoder=aac:mp3:opus:flac',
    'protocols=mp4:webm:mov',
    `maxWidth=${p.panel.width}`,
    `maxHeight=${p.panel.height}`,
    'maxAudioChannels=6',
    'maxbitdepth=8',
  ];
  if (vp9) keys.push(`vp9MaxBitDepth=${vp9Profile2 ? 10 : 8}`);
  keys.push(`hdr=${hdr ? 1 : 0}`);
  // Plays an optimized version (an H.264/AAC MP4 made ahead of time) when
  // the decision hands one out instead of a transcode (v2.6; older servers
  // ignore the key). See the watch page's startStream.
  keys.push('optimized=1');
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
    panel: readPanelSize(),
    panelHdr: panelHdr(),
    mediaHdr: mediaQueryHdr(),
  });
}
