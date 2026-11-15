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
// With the shell's native player (lib/player/nativeVideo, ?native=1) the
// console's own media pipeline plays the video instead, and it decodes what
// the web view can't: H.264 and HEVC to 10-bit, HDR10 (switching the TV to an
// HDR10 mode), AC-3 / E-AC-3, in MP4, MKV or the server's HLS. The header
// then says so, and supports_hevc is true, so HDR films reach the console as
// a direct play or a remux instead of a tone-mapped H.264 transcode. Not
// claimed there: VP9 and AV1 (untested in the console's pipeline), DTS and
// TrueHD. Dolby Vision profile 8.1 is (dovi=8.1, with HDR; the server
// converts profile 7 to it): the console decodes it as dvhe.08 and shows it
// in a Dolby Vision mode (checked on an LG C1). Profile 5 isn't claimed (no
// base layer to fall back on) and is refused as before.
//
// The header itself is built by a pure function from injected probes
// (buildClientCapabilitiesHeader, unit-tested); clientCapabilitiesHeader()
// feeds it the live ones. The display's size and HDR come from lib/platform
// (the shell's HDMI answer).

import { panelHdr, readPanelSize, startPanelProbe, type PanelSize } from '../platform';
import { shellParams } from '../shell';

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
// watch page proves a claim wrong (a fatal bufferAppendError, or the native
// player refusing a server stream, on a stream that was delivering the
// codec: lib/player/demotion), the codec is demoted here so both the
// capability header and the transcode-start supports_hevc flag tell the
// server the truth — the escalated retry then really comes back H.264
// instead of the codec that just failed. Persisted in localStorage, for
// DEMOTION_TTL_MS: the console's pipeline can change with an OS update (and
// the same app runs in a desktop WebView where a codec extension can be
// installed), and a demotion may still be a wrong guess, so it is tried
// again after a while. Signing out or forgetting the server drops them
// (clearCodecDemotions).

const DEMOTED_KEY = 'onscreen:demoted-codecs';
/** How long a demotion stands. */
export const DEMOTION_TTL_MS = 30 * 24 * 60 * 60 * 1000;

type DemotedCodec = 'hevc' | 'av1';
/** Codec → when its demotion expires (this TV's clock, ms). */
type Demotions = Partial<Record<DemotedCodec, number>>;

function readDemotions(): Demotions {
  try {
    const v: unknown = JSON.parse(localStorage.getItem(DEMOTED_KEY) ?? '{}');
    // The old bare list (no expiry) is dropped: it was written by the rule
    // that demoted whatever codec failed, so it may well be wrong.
    if (!v || typeof v !== 'object' || Array.isArray(v)) return {};
    const out: Demotions = {};
    for (const c of ['hevc', 'av1'] as const) {
      const at = Number((v as Record<string, unknown>)[c]);
      if (Number.isFinite(at) && at > Date.now()) out[c] = at;
    }
    return out;
  } catch {
    return {};
  }
}

let demoted: Demotions = readDemotions();

function isDemoted(codec: DemotedCodec): boolean {
  const at = demoted[codec];
  return at !== undefined && at > Date.now();
}

/** Record a codec the panel PROVED it cannot decode, for DEMOTION_TTL_MS.
 *  Idempotent while it stands. */
export function demoteCodec(codec: DemotedCodec): void {
  if (isDemoted(codec)) return;
  demoted = { ...demoted, [codec]: Date.now() + DEMOTION_TTL_MS };
  try {
    localStorage.setItem(DEMOTED_KEY, JSON.stringify(demoted));
  } catch {
    // Storage blocked — the in-memory demotion still covers this launch.
  }
}

/** Forget every demotion (sign-out, Forget server): the next account or
 *  server starts from the claims again. */
export function clearCodecDemotions(): void {
  demoted = {};
  try {
    localStorage.removeItem(DEMOTED_KEY);
  } catch {
    /* storage blocked */
  }
}

/** Map a media file's codec string onto the demotion registry. */
export function isCodecDemoted(codec: string | undefined): boolean {
  const c = (codec ?? '').toLowerCase();
  if (c === 'hevc' || c === 'h265') return isDemoted('hevc');
  if (c === 'av1') return isDemoted('av1');
  return false;
}

/** The transcode-start supports_hevc flag: true with the native player,
 *  which decodes HEVC; false for the web view, which can't whatever MSE
 *  claims (see the top of this file). A runtime demotion still wins. */
export function supportsHEVC(): boolean {
  return shellParams().native && !isDemoted('hevc');
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
  /** The shell plays video in the console's own player (?native=1). */
  native?: boolean;
  /** A codec PROVED undecodable here (runtime demotion); native only. */
  isDemoted?: (codec: 'hevc' | 'av1') => boolean;
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
  if (p.native) return nativeHeader(p);
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

/** The header for the console's own player (see the top of this file). */
function nativeHeader(p: CapabilityProbes): string {
  const hevc = !(p.isDemoted?.('hevc') ?? false);
  const video = hevc ? 'h264:h265' : 'h264';
  const hdr = hevc && (p.panelHdr ?? p.mediaHdr);
  return [
    `videoDecoder=${video}`,
    'audioDecoder=aac:mp3:ac3:eac3',
    'protocols=mp4:mkv:mov',
    `maxWidth=${p.panel.width}`,
    `maxHeight=${p.panel.height}`,
    'maxAudioChannels=6',
    `maxbitdepth=${hevc ? 10 : 8}`,
    `hdr=${hdr ? 1 : 0}`,
    // Dolby Vision profile 8.1 (HDR10 base; the server converts 7 to it):
    // kept on a direct play or remux, in a Dolby Vision display mode.
    ...(hdr ? ['dovi=8.1'] : []),
    'optimized=1',
  ].join(',');
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
    native: shellParams().native,
    isDemoted,
    panel: readPanelSize(),
    panelHdr: panelHdr(),
    mediaHdr: mediaQueryHdr(),
  });
}
