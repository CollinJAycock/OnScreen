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

/** Whether this TV can decode HEVC over MSE (hls.js transmuxes to fMP4,
 *  so we probe the mp4 codec string). Drives the transcode-start
 *  supports_hevc flag — telling the server we can take HEVC lets it
 *  stream-copy or HEVC-encode instead of falling back to H.264 on a
 *  panel that can't actually decode it. */
export function supportsHEVC(): boolean {
  return isTypeSupported('video/mp4; codecs="hvc1.1.6.L150.B0"');
}

export function clientCapabilitiesHeader(): string {
  const hevc = isTypeSupported('video/mp4; codecs="hvc1.1.6.L150.B0"');
  const hevc10bit = isTypeSupported('video/mp4; codecs="hvc1.2.4.L150.B0"');
  const av1 = isTypeSupported('video/mp4; codecs="av01.0.05M.08"');
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
