// Browser playback decision: given a file's technical metadata and the
// client's decode capabilities, decide whether the browser can play it
// directly, only needs a container remux, or needs a full transcode.
//
// Extracted from the watch page so it can be unit-tested. Capabilities are
// passed in (not read from globals) so the decision functions are pure;
// detectClientCaps() probes the real browser once at call time.

import type { ItemFile } from './api';

export interface ClientCaps {
  /** 8-bit HEVC (Main profile) decode. */
  hevc: boolean;
  /** 10-bit HEVC (Main 10 profile) decode — distinct from 8-bit Main. */
  hevc10bit: boolean;
  /** AV1 decode. */
  av1: boolean;
  /** Display is HDR-capable (avoid washed-out direct-play of HDR on SDR). */
  hdr: boolean;
}

// Audio codecs browsers decode natively in MP4/WebM. AC-3, E-AC-3, DTS,
// TrueHD, ALAC, MP2, raw PCM are not reliably supported anywhere — transcode.
const browserAudioCodecs = new Set(['aac', 'mp3', 'opus', 'flac', 'vorbis']);
// The channel-count ceiling this client declares to the server
// (maxaudiochannels=6 in the capability header): browsers reject >5.1 AAC
// outright, and the layout is fixed in the bitstream, so a 7.1 source needs a
// server downmix. The local decision must apply the SAME ceiling — it used to
// skip it, so the direct-play fallback handed the <video> element 7.1 audio
// the header had just told the server we can't decode (silent audio or an
// instant decode error, depending on browser).
const maxBrowserAudioChannels = 6;

/** Channel count of the default (first) audio stream; 0 = unknown. */
function defaultAudioChannels(file: ItemFile): number {
  return file.audio_streams?.[0]?.channels ?? 0;
}
// Containers browsers handle reliably for direct play (faststart MP4/MOV/WebM).
const browserContainers = new Set(['mp4', 'webm', 'mov']);

// Video codecs browsers decode natively. HEVC is added when the client
// reports H.265 support (treated like H.264 — direct play from MP4).
function browserVideoCodecs(caps: ClientCaps): Set<string> {
  const s = new Set(['h264', 'vp8', 'vp9', 'av1']);
  if (caps.hevc) {
    s.add('hevc');
    s.add('h265');
  }
  return s;
}

// Video codecs that can be stream-copied (remuxed) into MPEG-TS for HLS.js.
// AV1 and VP8/VP9 are excluded: MPEG-TS can't carry AV1, and VP8/VP9 are
// WebM-only. HEVC is added when the client supports it.
function remuxableVideoCodecs(caps: ClientCaps): Set<string> {
  const s = new Set(['h264']);
  if (caps.hevc) {
    s.add('hevc');
    s.add('h265');
  }
  return s;
}

/** True when the browser can decode this file's video at its bit depth.
 *  Browsers can't decode 10-bit H.264 (Hi10P) at all, and HEVC bit depth must
 *  not exceed the decoder's: Main 10 support (hevc10bit) covers ≤10-bit, but
 *  12-bit (Main 12 — e.g. some anime encodes like Fruits Basket) is NOT
 *  decodable since we only probe up to Main 10. 8-bit always passes; AV1/VP9
 *  high-bit-depth decode tracks their 8-bit support so it isn't separately
 *  gated. Uses video_bit_depth (the video stream's depth), NOT bit_depth (which
 *  is audio). Undefined → assume 8-bit (safe — no false transcodes; a
 *  genuinely-undecodable source falls back at decode). */
export function videoBitDepthOK(file: ItemFile | undefined, caps: ClientCaps): boolean {
  if (!file) return false;
  const depth = file.video_bit_depth ?? 8;
  if (depth < 10) return true;
  const codec = (file.video_codec ?? '').toLowerCase();
  if (codec === 'h264' || codec === 'avc') return false; // Hi10P/Hi12P — no browser decodes it
  if (codec === 'hevc' || codec === 'h265') return depth <= 10 && caps.hevc10bit; // Main 10 covers ≤10-bit only
  return true; // av1 / vp9 high-bit-depth ≈ their 8-bit support
}

/** True when the browser can play this file directly — compatible container +
 *  codecs + bit depth + faststart, and not HDR-on-SDR. */
export function canDirectPlay(file: ItemFile | undefined, caps: ClientCaps): boolean {
  if (!file) return false;
  // HDR content on an SDR display needs tonemapping — can't direct play.
  if (file.hdr_type && !caps.hdr) return false;
  // 10-bit video the browser can't decode (Hi10P H.264, HEVC Main10 on a
  // Main-only decoder) must transcode, not direct play.
  if (!videoBitDepthOK(file, caps)) return false;
  const container = (file.container ?? '').toLowerCase();
  const videoCodec = (file.video_codec ?? '').toLowerCase();
  const audioCodec = (file.audio_codec ?? '').toLowerCase();
  if (!browserContainers.has(container)) return false;
  if (videoCodec && !browserVideoCodecs(caps).has(videoCodec)) return false;
  if (audioCodec && !browserAudioCodecs.has(audioCodec)) return false;
  // Channel layouts above the ceiling we declare to the server can't be
  // decoded locally either — the downmix has to happen server-side.
  if (defaultAudioChannels(file) > maxBrowserAudioChannels) return false;
  // Non-faststart MP4/MOV files have moov at the end — the browser must fetch
  // the tail before playback can begin. Route these through the remux path.
  if (!file.faststart) return false;
  return true;
}

/** True when the video can be stream-copied (remuxed) into MPEG-TS HLS
 *  instead of re-encoded. */
export function canRemuxVideo(file: ItemFile | undefined, caps: ClientCaps): boolean {
  if (!file) return false;
  // HDR content on an SDR display needs tonemapping — can't remux.
  if (file.hdr_type && !caps.hdr) return false;
  // Remux preserves the source bit depth — a 10-bit stream the browser can't
  // decode stays undecodable after a copy, so force a transcode.
  if (!videoBitDepthOK(file, caps)) return false;
  const videoCodec = (file.video_codec ?? '').toLowerCase();
  return remuxableVideoCodecs(caps).has(videoCodec);
}

// ── Runtime codec demotion ───────────────────────────────────────────────────
//
// MediaSource.isTypeSupported is a CLAIM, not a promise. Windows Chrome
// enumerates a platform HEVC decoder and answers true for every HEVC probe,
// then rejects the actual SourceBuffer append when the Microsoft HEVC Video
// Extensions aren't installed. When playback proves a claim wrong (the watch
// page's decode-failure escalation), the codec is demoted HERE — the single
// registry both consumers read — so the local play-decision heuristics
// (detectClientCaps) and the server-facing X-Client-Capabilities header
// (clientCapabilitiesHeader) agree the codec is gone. Demoting only page-local
// state was the original sin: the memoized header kept claiming h265, the
// server's preferHEVC trusted it, and the "fallback" transcode came back as
// the very codec that had just failed.
//
// Persisted in sessionStorage so a page reload or the user's next title
// doesn't restart the probe→fail→demote cycle from scratch. Session-scoped on
// purpose: installing the HEVC extensions mid-browser-session is rare, and a
// wrong demotion costs one conservative transcode, not a broken stream.

const DEMOTED_KEY = 'onscreen:demoted-codecs';
const demotedCodecs = new Set<DemotableCodec>(readPersistedDemotions());

export type DemotableCodec = 'hevc' | 'av1';

function readPersistedDemotions(): DemotableCodec[] {
  try {
    if (typeof sessionStorage === 'undefined') return []; // SSR
    const v = JSON.parse(sessionStorage.getItem(DEMOTED_KEY) ?? '[]');
    return Array.isArray(v) ? v.filter((c): c is DemotableCodec => c === 'hevc' || c === 'av1') : [];
  } catch {
    return []; // corrupted entry / storage blocked — start clean
  }
}

/** demoteCodec records that the browser PROVED it cannot decode a codec it
 *  claimed, drops it from future detectClientCaps() results, and invalidates
 *  the memoized X-Client-Capabilities header so the very next API call tells
 *  the server the truth. Idempotent. */
export function demoteCodec(codec: DemotableCodec): void {
  if (demotedCodecs.has(codec)) return;
  demotedCodecs.add(codec);
  capsHeaderCache = null; // rebuild without the demoted codec on next use
  try {
    if (typeof sessionStorage !== 'undefined') {
      sessionStorage.setItem(DEMOTED_KEY, JSON.stringify([...demotedCodecs]));
    }
  } catch {
    // Storage blocked (private mode, quota) — the in-memory demotion still
    // covers this page's lifetime, which is what correctness needs.
  }
}

/** isCodecDemoted reports whether a media codec string (as stored on the file
 *  row — 'hevc', 'h265', 'av1', …) has been runtime-demoted. */
export function isCodecDemoted(codec: string | undefined): boolean {
  const c = (codec ?? '').toLowerCase();
  if (c === 'hevc' || c === 'h265') return demotedCodecs.has('hevc');
  if (c === 'av1') return demotedCodecs.has('av1');
  return false;
}

/** resetDemotedCodecs clears the demotion registry (tests only). */
export function resetDemotedCodecs(): void {
  demotedCodecs.clear();
  capsHeaderCache = null;
  try {
    if (typeof sessionStorage !== 'undefined') sessionStorage.removeItem(DEMOTED_KEY);
  } catch { /* ignore */ }
}

/** Probe the running browser's decode capabilities via MediaSource Extensions
 *  + a media query. HLS.js transmuxes MPEG-TS → fMP4 before feeding MSE, so we
 *  check mp4 codec strings. Safe in non-browser / SSR contexts (returns all
 *  false). Runtime demotions override the probe: a codec the browser has
 *  PROVEN it can't decode stays false no matter what isTypeSupported says. */
export function detectClientCaps(): ClientCaps {
  const isTypeSupported = (s: string): boolean => {
    try {
      return typeof MediaSource !== 'undefined' && MediaSource.isTypeSupported(s);
    } catch {
      return false; // MSE unavailable
    }
  };
  const hevcOK = !demotedCodecs.has('hevc');
  return {
    hevc: hevcOK && isTypeSupported('video/mp4; codecs="hvc1.1.6.L150.B0"'),
    hevc10bit: hevcOK && isTypeSupported('video/mp4; codecs="hvc1.2.4.L150.B0"'),
    av1: !demotedCodecs.has('av1') && isTypeSupported('video/mp4; codecs="av01.0.05M.08"'),
    hdr: typeof window !== 'undefined' && window.matchMedia('(dynamic-range: high)').matches,
  };
}

let capsHeaderCache: string | null = null;

/** Build the `X-Client-Capabilities` header value from the running browser's
 *  decode capabilities, in the grammar the server's ParseCapabilities expects
 *  (`videoDecoder=h264:h265,audioDecoder=aac:...,maxAudioChannels=6,...`). This
 *  is the declarative profile the server uses to pick transcode targets +
 *  (eventually) the play decision — see docs/capability-profiles.md.
 *
 *  Memoized — the probe result is stable within a session, EXCEPT when a
 *  decode failure demotes a codec (demoteCodec clears the cache so the next
 *  call rebuilds without the demoted codec, and with maxbitdepth dropped to 8
 *  when HEVC falls, since hevc10bit falls with it). Returns '' in
 *  non-browser/SSR contexts (no MSE), where the caller should omit the header
 *  and let the server fall back to its safe defaults. */
export function clientCapabilitiesHeader(): string {
  if (capsHeaderCache !== null) return capsHeaderCache;
  if (typeof MediaSource === 'undefined') return ''; // SSR / no MSE — don't cache
  const caps = detectClientCaps();
  const video = ['h264', 'vp9'];
  if (caps.hevc) video.push('h265');
  if (caps.av1) video.push('av1');
  capsHeaderCache = [
    `videoDecoder=${video.join(':')}`,
    `audioDecoder=${[...browserAudioCodecs].join(':')}`,
    `protocols=${[...browserContainers].join(':')}`,
    'maxWidth=3840',
    'maxHeight=2160',
    // Browsers decode up to 5.1 AAC via MSE; 7.1 (8ch) AAC is undecodable and
    // stalls playback entirely (the 7.1 sweep finding) — declare 6, never 8.
    'maxAudioChannels=6',
    `maxbitdepth=${caps.hevc10bit ? 10 : 8}`,
    `hdr=${caps.hdr ? 1 : 0}`,
  ].join(',');
  return capsHeaderCache;
}
