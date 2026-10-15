// Image subtitles (PGS, VobSub, DVB, XSUB) can't be served as WebVTT, so the
// page can't draw them the way it draws text tracks (lib/player/subtitles).
// The owner's rule for them: use the OCR'd text if it is ready, else burn
// the track into the picture.
//
//  - An image track with a text version (subtitle_streams[].text_version_id,
//    the files[].external_subtitles entry OCR'd from it) is a text row keyed
//    'ext:<id>' (buildSubtitleOptions): picking it loads that WebVTT like any
//    external track, with no restart.
//  - One without is a burn row keyed 'burn:<stream index>'. Picking it
//    re-opens the session at the current position with
//    burn_subtitle_stream_index (the ABSOLUTE ffprobe index) and video_copy
//    false: burning is always a full re-encode. Off, or another track, opens
//    it again without the burn, back to the verdict's own mode.
//
// The burn is part of the chosen key, so every open made while it is chosen
// (a seek re-issue, an audio switch, a resume, a retry) carries it: the page
// reads it from the key when the start runs (streamVideoFields).
//
// Pure so the decisions are unit-tested.

/** Key prefix of a burn row. */
const BURN_PREFIX = 'burn:';

/** The picker key of the burn row for an image track (absolute index). */
export function burnKey(streamIndex: number): string {
  return `${BURN_PREFIX}${streamIndex}`;
}

/** The stream a key burns in, or null for a text row, Off (null) or
 *  anything else. */
export function burnIndexOfKey(key: string | null | undefined): number | null {
  if (!key || key.indexOf(BURN_PREFIX) !== 0) return null;
  const raw = key.slice(BURN_PREFIX.length);
  if (!/^\d+$/.test(raw)) return null;
  return parseInt(raw, 10);
}

/** What a pick in the subtitle picker does. */
export interface SubtitlePick {
  /** The row the picker marks from now on (null = Off). */
  key: string | null;
  /** The burn changes: the stream is re-opened (with the new burn, or
   *  without one). A text pick that leaves the burn as it was doesn't. */
  reissue: boolean;
}

/**
 * Picking `pickedKey` (null = Off) while `currentKey` is chosen. A text row
 * (embedded, downloaded, an image track's OCR text) just loads its WebVTT;
 * only a change of burn re-opens the stream: onto a burn row, off one (Off,
 * a text row), or from one burn row to another.
 */
export function planSubtitlePick(pickedKey: string | null, currentKey: string | null): SubtitlePick {
  return { key: pickedKey, reissue: burnIndexOfKey(pickedKey) !== burnIndexOfKey(currentKey) };
}

/** The video side of a start: the verdict's mode, unless a burn is chosen. */
export interface StreamVideoFields {
  videoCopy: boolean;
  /** burn_subtitle_stream_index: the absolute index of the image track. */
  burnSubtitleStreamIndex?: number;
}

/**
 * What a start sends for the request's `videoCopy` (the verdict's mode, or
 * a fallback's) with `activeKey` chosen. A burn row burns its stream into a
 * full re-encode; anything else leaves the request as it is, so un-burning
 * goes back to the remux a burn interrupted.
 */
export function streamVideoFields(videoCopy: boolean, activeKey: string | null): StreamVideoFields {
  const burn = burnIndexOfKey(activeKey);
  return burn === null ? { videoCopy } : { videoCopy: false, burnSubtitleStreamIndex: burn };
}

/** The server's 400 codes for a burn it won't do: no such subtitle stream,
 *  a text one, or a stacked movie (its parts play as one, see isStackPart). */
export const BURN_IN_ERROR_CODES: ReadonlySet<string> = new Set([
  'INVALID_SUBTITLE_STREAM',
  'SUBTITLE_NOT_IMAGE',
  'BURN_IN_UNSUPPORTED',
]);

/** 503 when no transcode worker can burn subtitles in (an older remote
 *  worker): refused the same way, and as safely — the server checks before
 *  it replaces the running session. */
export const BURN_IN_UNAVAILABLE = 'BURN_IN_UNAVAILABLE';

/**
 * The message to show for a start the server refused to burn in, or null
 * when `e` is anything else. A retry can't change the answer, and the
 * running stream is untouched (the server checks the request before it
 * replaces anything), so the page says why and stays on it.
 */
export function burnInRefusal(e: unknown): string | null {
  const err = e as { status?: unknown; code?: unknown; message?: unknown } | null;
  if (!err || typeof err.code !== 'string') return null;
  const refused = (err.status === 400 && BURN_IN_ERROR_CODES.has(err.code)) ||
    (err.status === 503 && err.code === BURN_IN_UNAVAILABLE);
  if (!refused) return null;
  const msg = typeof err.message === 'string' ? err.message.trim() : '';
  return msg || 'These subtitles can’t be burned in.';
}

/**
 * The row to mark after a burn was refused: the burn the live session has
 * (`liveBurnIndex`, null = none), else the row chosen before the pick, if
 * that wasn't a burn the session never got.
 */
export function keyAfterBurnRefused(liveBurnIndex: number | null, keyBeforePick: string | null): string | null {
  if (liveBurnIndex !== null) return burnKey(liveBurnIndex);
  return burnIndexOfKey(keyBeforePick) === null ? keyBeforePick : null;
}
