// X-Client-Capabilities profile for Samsung Tizen TVs.
//
// Tizen TVs decode H.264 and HEVC (including Main 10 / 4K HDR) and AAC/AC-3/
// E-AC-3 natively via the AVPlay hardware path; AV1 support varies by model and
// is not claimed. The behaviorally-significant fields here (HEVC + the 5.1
// channel cap) match what the watch page already declares to the transcode
// endpoint (supports_hevc, no channel override → 5.1), so sending this is
// additive: it gives the server the full declarative profile for transcode
// target selection and the server-authoritative play decision. See
// docs/capability-profiles.md (grammar) + the web client for the reference.
//
// The claims are STATIC (AVPlay has no isTypeSupported-style probe), which
// makes runtime demotion the only correction channel: when a playback attempt
// proves a claim wrong on a given panel (an AVPlay NOT_SUPPORTED-class error
// on an HEVC stream — e.g. an SDR-only or pre-2016 model this blanket profile
// overclaims for), the codec is demoted below and every later request tells
// the server the truth. Persisted in localStorage: a panel's hardware decode
// support never changes, so a proven demotion is permanent.

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

/** The transcode-start supports_hevc flag — the static claim corrected by
 *  any runtime demotion. */
export function supportsHEVC(): boolean {
  return !demoted.has('hevc');
}

export function clientCapabilitiesHeader(): string {
  const video = ['h264'];
  if (!demoted.has('hevc')) video.push('h265');
  return [
    `videoDecoder=${video.join(':')}`,
    'audioDecoder=aac:ac3:eac3',
    'protocols=mp4:ts',
    'maxWidth=3840',
    'maxHeight=2160',
    'maxAudioChannels=6',
    // 10-bit rides the HEVC hardware path; a demoted panel gets 8-bit-safe
    // output so Main 10 sources can't sneak back in through the depth field.
    `maxbitdepth=${demoted.has('hevc') ? 8 : 10}`,
    'hdr=1',
  ].join(',');
}
