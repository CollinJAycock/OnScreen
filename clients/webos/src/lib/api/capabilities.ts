// X-Client-Capabilities profile for LG webOS TVs.
//
// webOS plays video through hls.js + MediaSource Extensions (see the watch
// page), so — exactly like the web client — actual codec support is *probed*
// via MediaSource.isTypeSupported rather than assumed. (Tizen differs: it uses
// the native AVPlay hardware path and can claim HEVC/AC-3 unconditionally.)
// Audio over MSE is limited to the browser-decodable set (no AC-3/E-AC-3/DTS),
// and 7.1 AAC is undecodable over MSE, so channels cap at 5.1. See
// docs/capability-profiles.md for the grammar.

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
  return !demoted.has('hevc') && isTypeSupported('video/mp4; codecs="hvc1.1.6.L150.B0"');
}

export function clientCapabilitiesHeader(): string {
  const hevc = supportsHEVC();
  const hevc10bit = !demoted.has('hevc') && isTypeSupported('video/mp4; codecs="hvc1.2.4.L150.B0"');
  const av1 = !demoted.has('av1') && isTypeSupported('video/mp4; codecs="av01.0.05M.08"');
  const hdr = typeof window !== 'undefined' && window.matchMedia('(dynamic-range: high)').matches;

  const video = ['h264', 'vp9'];
  if (hevc) video.push('h265');
  if (av1) video.push('av1');

  return [
    `videoDecoder=${video.join(':')}`,
    'audioDecoder=aac:mp3:opus:flac',
    'protocols=mp4:webm:mov',
    'maxWidth=3840',
    'maxHeight=2160',
    'maxAudioChannels=6',
    `maxbitdepth=${hevc10bit ? 10 : 8}`,
    `hdr=${hdr ? 1 : 0}`,
  ].join(',');
}
