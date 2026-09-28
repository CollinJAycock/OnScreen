// ReplayGain math for the browser audio path.
//
// The desktop client's native engine applies ReplayGain in Rust; browsers
// get it through a Web Audio GainNode (see webAudioGain.ts + AudioPlayer).
// This module is the pure part: pick the tag for the mode, fold in the
// preamp, and cap against the peak so the gain never pushes a sample past
// full scale. No DOM, no Web Audio — unit-tested in replaygain.test.ts.

import type { ItemFile } from './api';
import type { ReplayGainMode } from './native';

export type { ReplayGainMode };

/** ReplayGain tags for one file. Gains are in dB (REPLAYGAIN_*_GAIN),
 *  peaks are linear sample peaks (REPLAYGAIN_*_PEAK, 1.0 = full scale).
 *  Every field is optional: a file can carry track tags only, album tags
 *  only, or nothing at all. */
export interface ReplayGainInfo {
  trackGain?: number;
  trackPeak?: number;
  albumGain?: number;
  albumPeak?: number;
}

/** Preamp bounds — same ±15 dB clamp the native engine applies, so the
 *  one setting means the same thing on both paths. */
export const PREAMP_MIN_DB = -15;
export const PREAMP_MAX_DB = 15;

export function clampPreamp(db: number): number {
  if (!Number.isFinite(db)) return 0;
  return Math.max(PREAMP_MIN_DB, Math.min(PREAMP_MAX_DB, db));
}

function finiteOrUndefined(v: number | null | undefined): number | undefined {
  return typeof v === 'number' && Number.isFinite(v) ? v : undefined;
}

/** Lift the replaygain_* fields off an ItemFile (the shape /items/{id}
 *  returns). Always returns an object — an empty one means "looked up,
 *  file has no tags", which the player treats as unity. */
export function replayGainFromFile(
  file:
    | Pick<
        ItemFile,
        'replaygain_track_gain' | 'replaygain_track_peak' | 'replaygain_album_gain' | 'replaygain_album_peak'
      >
    | null
    | undefined,
): ReplayGainInfo {
  if (!file) return {};
  const out: ReplayGainInfo = {};
  const tg = finiteOrUndefined(file.replaygain_track_gain);
  const tp = finiteOrUndefined(file.replaygain_track_peak);
  const ag = finiteOrUndefined(file.replaygain_album_gain);
  const ap = finiteOrUndefined(file.replaygain_album_peak);
  if (tg !== undefined) out.trackGain = tg;
  if (tp !== undefined) out.trackPeak = tp;
  if (ag !== undefined) out.albumGain = ag;
  if (ap !== undefined) out.albumPeak = ap;
  return out;
}

/** The tag pair a mode resolves to, or null when the mode is off or the
 *  file carries no usable gain.
 *
 *  - track: track gain + track peak.
 *  - album: album gain + album peak, falling back to the track pair when
 *    the file has no album gain (singles, partially-tagged rips). When the
 *    album gain is present but the album peak isn't, the track peak is the
 *    next-best clip guard (it's this file's real peak; the album peak is
 *    only ever >= it). */
export function selectReplayGain(
  info: ReplayGainInfo | null | undefined,
  mode: ReplayGainMode,
): { gainDb: number; peak?: number } | null {
  if (!info || mode === 'off') return null;
  const tg = finiteOrUndefined(info.trackGain);
  const tp = finiteOrUndefined(info.trackPeak);
  const ag = finiteOrUndefined(info.albumGain);
  const ap = finiteOrUndefined(info.albumPeak);
  if (mode === 'album' && ag !== undefined) {
    return { gainDb: ag, peak: ap ?? tp };
  }
  if (tg !== undefined) return { gainDb: tg, peak: tp };
  return null;
}

/** Linear gain multiplier for a file under a mode + preamp.
 *
 *  gain = 10^((tagDb + preampDb) / 20), then capped so peak * gain <= 1.
 *
 *  - mode off -> 1 (unity; the element plays untouched).
 *  - no usable tag -> 1. Common practice (foobar2000, mpv): untagged files
 *    are left alone rather than getting the preamp on its own — otherwise
 *    a +6 dB preamp would make every untagged file 6 dB louder and clip.
 *  - peak unknown -> treated as 1.0 (full scale), same default as mpv, so
 *    a tag + preamp can attenuate freely but never boost past unity. The
 *    Web Audio graph is float, but the output device clamps at ±1.0 —
 *    without a known peak there's no way to prove a boost is clip-free.
 */
export function replayGainLinear(
  info: ReplayGainInfo | null | undefined,
  mode: ReplayGainMode,
  preampDb: number,
): number {
  const sel = selectReplayGain(info, mode);
  if (!sel) return 1;
  const db = sel.gainDb + clampPreamp(preampDb);
  let linear = Math.pow(10, db / 20);
  const peak = sel.peak !== undefined && sel.peak > 0 ? sel.peak : 1;
  if (linear * peak > 1) linear = 1 / peak;
  return Number.isFinite(linear) && linear >= 0 ? linear : 1;
}
