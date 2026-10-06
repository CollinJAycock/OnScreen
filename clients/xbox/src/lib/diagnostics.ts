// Settings > Diagnostics: what this console's web view can play, read once on
// the console so the Xbox app's codec claims and its player can be settled
// from facts (docs: the Xbox plan's phase 1 visit). Every answer is the
// browser's own: MediaSource.isTypeSupported (what hls.js can append),
// canPlayType for native HLS, MediaCapabilities for HDR and 4K, and the
// display media queries. The capability header the app sends is listed with
// them, so the server's view of this console is on the same screen.

export interface DiagRow {
  label: string;
  value: string;
  /** true / false colours the row; null is plain information. */
  ok: boolean | null;
}

export interface DiagSection {
  title: string;
  rows: DiagRow[];
}

/** MSE type strings: video in fMP4 as hls.js appends it, audio in mp4. */
export const MSE_PROBES: Array<[string, string]> = [
  ['H.264 High 4.0 (1080p)', 'video/mp4; codecs="avc1.640028"'],
  ['H.264 High 5.1 (4K)', 'video/mp4; codecs="avc1.640033"'],
  ['HEVC Main (hvc1)', 'video/mp4; codecs="hvc1.1.6.L150.B0"'],
  ['HEVC Main 10 (hvc1)', 'video/mp4; codecs="hvc1.2.4.L153.B0"'],
  ['HEVC Main 10 (hev1)', 'video/mp4; codecs="hev1.2.4.L153.B0"'],
  ['AV1 8-bit', 'video/mp4; codecs="av01.0.08M.08"'],
  ['AV1 10-bit', 'video/mp4; codecs="av01.0.13M.10"'],
  ['VP9 profile 0', 'video/mp4; codecs="vp09.00.10.08"'],
  ['VP9 profile 2 (10-bit)', 'video/mp4; codecs="vp09.02.10.10"'],
  ['Dolby Vision (dvh1)', 'video/mp4; codecs="dvh1.05.06"'],
  ['AAC', 'audio/mp4; codecs="mp4a.40.2"'],
  ['AC-3 (Dolby Digital)', 'audio/mp4; codecs="ac-3"'],
  ['E-AC-3 (Dolby Digital Plus)', 'audio/mp4; codecs="ec-3"'],
  ['DTS', 'audio/mp4; codecs="dtsc"'],
  ['DTS-HD', 'audio/mp4; codecs="dtsh"'],
  ['FLAC', 'audio/mp4; codecs="flac"'],
  ['Opus', 'audio/mp4; codecs="opus"'],
  ['MP3', 'audio/mpeg'],
];

/** MediaCapabilities configurations: can it decode these smoothly? */
export const DECODE_PROBES: Array<[string, MediaDecodingConfiguration]> = [
  ['4K60 H.264', { type: 'media-source', video: { contentType: 'video/mp4; codecs="avc1.640033"', width: 3840, height: 2160, bitrate: 40_000_000, framerate: 60 } }],
  ['4K60 HEVC Main 10', { type: 'media-source', video: { contentType: 'video/mp4; codecs="hvc1.2.4.L153.B0"', width: 3840, height: 2160, bitrate: 40_000_000, framerate: 60 } }],
  ['4K HEVC HDR10 (PQ, BT.2020)', { type: 'media-source', video: { contentType: 'video/mp4; codecs="hvc1.2.4.L153.B0"', width: 3840, height: 2160, bitrate: 40_000_000, framerate: 24, transferFunction: 'pq', colorGamut: 'rec2020', hdrMetadataType: 'smpteSt2086' } as VideoConfiguration }],
  ['4K AV1 HDR10', { type: 'media-source', video: { contentType: 'video/mp4; codecs="av01.0.13M.10"', width: 3840, height: 2160, bitrate: 30_000_000, framerate: 24, transferFunction: 'pq', colorGamut: 'rec2020', hdrMetadataType: 'smpteSt2086' } as VideoConfiguration }],
];

export interface DiagEnv {
  isTypeSupported(type: string): boolean | null;
  canPlayHls(): string;
  decodingInfo(c: MediaDecodingConfiguration): Promise<{ supported: boolean; smooth: boolean; powerEfficient: boolean } | null>;
  matches(query: string): boolean | null;
  info: Record<string, string>;
  capabilityHeader: string;
}

function yesNo(v: boolean | null): string {
  return v === null ? 'unknown' : v ? 'yes' : 'no';
}

/** Every section, from an injected environment (the page passes the live
 *  one; tests a fake). Never throws: a probe that fails reads "unknown". */
export async function collectDiagnostics(env: DiagEnv): Promise<DiagSection[]> {
  const mse = MSE_PROBES.map(([label, type]) => {
    const v = env.isTypeSupported(type);
    return { label, value: yesNo(v), ok: v };
  });

  const decode: DiagRow[] = [];
  for (const [label, config] of DECODE_PROBES) {
    let r: Awaited<ReturnType<DiagEnv['decodingInfo']>> = null;
    try {
      r = await env.decodingInfo(config);
    } catch {
      r = null;
    }
    decode.push(
      r
        ? { label, value: r.supported ? `yes${r.smooth ? ', smooth' : ''}${r.powerEfficient ? ', hardware' : ''}` : 'no', ok: r.supported }
        : { label, value: 'unknown', ok: null },
    );
  }

  const display: DiagRow[] = [
    ['HDR display (dynamic-range: high)', '(dynamic-range: high)'],
    ['HDR video plane (video-dynamic-range: high)', '(video-dynamic-range: high)'],
    ['Wide colour (color-gamut: p3)', '(color-gamut: p3)'],
    ['BT.2020 colour (color-gamut: rec2020)', '(color-gamut: rec2020)'],
  ].map(([label, q]) => {
    const v = env.matches(q);
    return { label, value: yesNo(v), ok: v };
  });
  const hls = env.canPlayHls();
  display.push({ label: 'Native HLS in <video>', value: hls || 'no', ok: hls === 'probably' || hls === 'maybe' });

  return [
    { title: 'This console', rows: Object.entries(env.info).map(([label, value]) => ({ label, value, ok: null })) },
    { title: 'Display', rows: display },
    { title: 'MSE (what hls.js can play)', rows: mse },
    { title: 'Decoding (MediaCapabilities)', rows: decode },
    { title: 'Sent to the server', rows: [{ label: 'X-Client-Capabilities', value: env.capabilityHeader, ok: null }] },
  ];
}

/** The live environment, for the Diagnostics page. */
export function liveDiagEnv(info: Record<string, string>, capabilityHeader: string): DiagEnv {
  return {
    isTypeSupported(type) {
      try {
        return typeof MediaSource !== 'undefined' ? MediaSource.isTypeSupported(type) : null;
      } catch {
        return null;
      }
    },
    canPlayHls() {
      try {
        return document.createElement('video').canPlayType('application/vnd.apple.mpegurl');
      } catch {
        return '';
      }
    },
    async decodingInfo(config) {
      if (!navigator.mediaCapabilities?.decodingInfo) return null;
      const r = await navigator.mediaCapabilities.decodingInfo(config);
      return { supported: r.supported, smooth: r.smooth, powerEfficient: r.powerEfficient };
    },
    matches(query) {
      try {
        return window.matchMedia(query).matches;
      } catch {
        return null;
      }
    },
    info,
    capabilityHeader,
  };
}
