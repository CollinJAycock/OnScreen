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

/** Probe the running browser's decode capabilities via MediaSource Extensions
 *  + a media query. HLS.js transmuxes MPEG-TS → fMP4 before feeding MSE, so we
 *  check mp4 codec strings. Safe in non-browser / SSR contexts (returns all
 *  false). */
export function detectClientCaps(): ClientCaps {
  const isTypeSupported = (s: string): boolean => {
    try {
      return typeof MediaSource !== 'undefined' && MediaSource.isTypeSupported(s);
    } catch {
      return false; // MSE unavailable
    }
  };
  return {
    hevc: isTypeSupported('video/mp4; codecs="hvc1.1.6.L150.B0"'),
    hevc10bit: isTypeSupported('video/mp4; codecs="hvc1.2.4.L150.B0"'),
    av1: isTypeSupported('video/mp4; codecs="av01.0.05M.08"'),
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
 *  Memoized — capabilities don't change within a session. Returns '' in
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
