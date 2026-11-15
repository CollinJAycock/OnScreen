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
// on a stream that was delivering HEVC, lib/player/demotion — e.g. an
// SDR-only or pre-2016 model this blanket profile overclaims for), the codec
// is demoted below and every later request tells the server the truth.
// Persisted in localStorage, for DEMOTION_TTL_MS: a panel's hardware
// decoders don't change, but its firmware can, and a demotion may still be a
// wrong guess, so it is tried again after a while. Signing out or forgetting
// the server drops them (clearCodecDemotions).

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

/** The transcode-start supports_hevc flag — the static claim corrected by
 *  any runtime demotion. */
export function supportsHEVC(): boolean {
  return !isDemoted('hevc');
}

export function clientCapabilitiesHeader(): string {
  const video = ['h264'];
  if (!isDemoted('hevc')) video.push('h265');
  return [
    `videoDecoder=${video.join(':')}`,
    'audioDecoder=aac:ac3:eac3',
    'protocols=mp4:ts',
    'maxWidth=3840',
    'maxHeight=2160',
    'maxAudioChannels=6',
    // 10-bit rides the HEVC hardware path; a demoted panel gets 8-bit-safe
    // output so Main 10 sources can't sneak back in through the depth field.
    `maxbitdepth=${isDemoted('hevc') ? 8 : 10}`,
    'hdr=1',
    // Plays an optimized version (an H.264/AAC MP4 made ahead of time) when
    // the decision hands one out instead of a transcode (v2.6; older
    // servers ignore the key). See the watch page's startStream.
    'optimized=1',
  ].join(',');
}

/** The transcode-start supports_av1 flag. AV1 decode varies by Samsung
 *  model and AVPlay has no probe for it, so it is never claimed. */
export function supportsAV1(): boolean {
  return false;
}
