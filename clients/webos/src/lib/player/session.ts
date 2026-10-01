// Server-stream timeline math for the player: where a transcode / remux
// session sits in the item, where in it playback starts, how long the item
// is, and whether a seek can stay inside the session or needs a new one.
// Pure so the rules are checkable without mounting the player.
//
// Two clocks are in play. CONTENT time is a position in the file: the
// resume point, the progress heartbeat, markers, chapters, trickplay cues,
// the bar. STREAM time is the media element's currentTime on a server HLS
// session, which starts at 0 at the session's own head. A session opened at
// a resume point begins `offsetMs` into the content, so
//   content = stream + offsetMs.
// Direct audio plays the file itself: its offset is 0 and the clocks agree.
//
// Mirrors the web client's attachHls / seekToContentTime (same hls.js
// engine) and the Android TV client's StreamSession / PlaybackHelper /
// ContentTimeForwardingPlayer.

import type { HlsConfig } from 'hls.js';
import type { ItemDetail, ItemFile, TranscodeSession } from '../api/types';

/** How long ←/→/◀◀/▶▶ presses keep adding to one scrub target before it
 *  commits: a burst of presses becomes one seek (and at most one session
 *  re-issue), not one per press. */
export const SCRUB_COMMIT_MS = 700;

/** A re-issue is never opened closer than this to the item's end. Asked to
 *  start at (or a hair before) the listed length, the server's -ss lands
 *  past the last frame and the session has nothing to write: an empty
 *  playlist and an error instead of the last few seconds and Up Next. */
export const REISSUE_END_GUARD_MS = 5_000;

/** Cross-device sync leaves a position within this of the local one alone
 *  (Android parity): it is this device's own report echoed back, or close
 *  enough that a seek would only cost a rebuffer. */
export const SYNC_MIN_DELTA_MS = 2_000;

/** Content time the session's stream opens at (stream 0 = this far into the
 *  item). The server's keyframe-aligned start_offset_sec, which a remux puts
 *  up to several seconds before the requested position. A real 0 is kept: a
 *  stream covering the whole file (a pre-encoded ladder), or a start before
 *  the first keyframe, opens at 0:00, and reading it as "not sent" put every
 *  content time on screen off by the resume point. Only a server that omits
 *  the field falls back to the requested position. */
export function sessionOffsetMs(
  resp: Pick<TranscodeSession, 'start_offset_sec'>,
  requestedMs: number
): number {
  const sec = resp.start_offset_sec;
  if (typeof sec === 'number' && Number.isFinite(sec) && sec >= 0) {
    return Math.round(sec * 1000);
  }
  return Math.max(0, Math.round(requestedMs));
}

/** How far into the stream seg 0's audio begins (an AAC re-encode after a
 *  mid-stream -ss warms up for a moment of silent video). 0 when the server
 *  measured no gap or doesn't send the field. */
export function seg0GapMs(resp: Pick<TranscodeSession, 'seg0_audio_gap_sec'>): number {
  const sec = resp.seg0_audio_gap_sec;
  if (typeof sec === 'number' && Number.isFinite(sec) && sec > 0) {
    return Math.round(sec * 1000);
  }
  return 0;
}

/** Where in the STREAM playback starts so it begins at `requestedMs`
 *  (content time) in a stream that opens at `offsetMs`: a remux opens on the
 *  keyframe before the request, so its head replays a few seconds; a
 *  full-timeline stream (offset 0) has to seek all the way to a resume. Never
 *  before the first audible frame (`seg0Gap`). The web client's
 *  desiredStartSec, Android's hlsStartMs. */
export function inStreamStartMs(requestedMs: number, offsetMs: number, seg0Gap: number): number {
  return Math.max(requestedMs - offsetMs, seg0Gap, 0);
}

/** hls.js `startPosition` for an in-stream start: seconds, or -1 (let
 *  hls.js pick the head) for a start within half a second of 0. Forcing a
 *  0 start made recoverMediaError snap the player back to an empty buffer
 *  on every media hiccup (the web client's finding). A measured audio gap is
 *  always honoured, however short: it is the first frame with sound. */
export function hlsStartPositionSec(inStreamMs: number, seg0Gap: number): number {
  if (inStreamMs > 500 || (seg0Gap > 0 && inStreamMs > 0)) return inStreamMs / 1000;
  return -1;
}

/** Media-element time (seconds) on a session → content time (ms). */
export function toContentMs(streamSec: number, offsetMs: number): number {
  const s = Number.isFinite(streamSec) ? streamSec : 0;
  return Math.round(s * 1000) + offsetMs;
}

/** The length the server lists for what plays: the file's probed duration,
 *  else the item's runtime (TMDB's, in whole minutes, so only a fallback). A
 *  zero is no length either. 0 when neither is known. */
export function listedDurationMs(
  file: Pick<ItemFile, 'duration_ms'> | null | undefined,
  item: Pick<ItemDetail, 'duration_ms'> | null | undefined
): number {
  const f = file?.duration_ms ?? 0;
  if (f > 0) return f;
  const i = item?.duration_ms ?? 0;
  return i > 0 ? i : 0;
}

export interface DurationContext {
  /** The media element's duration in ms (0 when NaN / Infinity / unknown). */
  playerDurationMs: number;
  /** The session's content offset (0 for direct audio). */
  offsetMs: number;
  /** Direct play of the file (audio): the player's own duration IS the
   *  file's once it is known. */
  directAudio: boolean;
  /** A server session whose playlist has ended (ENDLIST seen): its duration
   *  is final, so offset + it is the content length. */
  ended: boolean;
}

/** The item's length in content time, which the bar, the heartbeat and Up
 *  Next all divide by. In order: a direct play's settled player duration;
 *  the listed length; an ended session's duration re-absolutised. A server
 *  session's player duration is otherwise only what ffmpeg has written so
 *  far (or Infinity), and trusting it put Up Next up, and marked items
 *  watched, minutes into a resume. 0 when nothing is known yet; callers
 *  treat that as "don't report". Android's contentDurationMs. */
export function contentDurationMs(
  file: Pick<ItemFile, 'duration_ms'> | null | undefined,
  item: Pick<ItemDetail, 'duration_ms'> | null | undefined,
  ctx: DurationContext
): number {
  const player = Number.isFinite(ctx.playerDurationMs) && ctx.playerDurationMs > 0
    ? Math.round(ctx.playerDurationMs)
    : 0;
  if (ctx.directAudio && player > 0) return player;
  const listed = listedDurationMs(file, item);
  if (listed > 0) return listed;
  if (!ctx.directAudio && ctx.ended && player > 0) return player + ctx.offsetMs;
  return 0;
}

/** What a session's playlist has produced so far: `producedEndMs` is how far
 *  into the STREAM its fragments reach (every segment ffmpeg has written is
 *  listed, so this grows in step with the encoder; the media element's
 *  seekable range only reflects hls.js's forward buffer). `ended` once the
 *  playlist carries ENDLIST. The web client's hlsPlaylistDurationSec. */
export interface StreamWindow {
  producedEndMs: number;
  ended: boolean;
}

export function playlistWindow(
  details: { fragments: ReadonlyArray<{ start: number; duration: number }>; live: boolean } | null | undefined
): StreamWindow | null {
  if (!details) return null;
  const frags = details.fragments;
  const last = frags.length > 0 ? frags[frags.length - 1] : null;
  const end = last ? last.start + last.duration : 0;
  return {
    producedEndMs: Number.isFinite(end) && end > 0 ? Math.round(end * 1000) : 0,
    ended: details.live === false,
  };
}

export type SeekPlan =
  /** Inside what the session has produced: set currentTime. */
  | { kind: 'local'; streamSec: number }
  /** Outside it (before the session's head, or past a still-growing edge):
   *  open a new session at this content position. */
  | { kind: 'reissue'; positionMs: number }
  /** Past the end of a FINISHED window: nothing further to transcode, so
   *  seek to its end (which ends the item, as a direct play would). Also a
   *  relative key seek a little past a growing edge (see
   *  RELATIVE_SEEK_EDGE_MARGIN_MS): it lands at the edge. */
  | { kind: 'clamp'; streamSec: number };

export interface SeekWindow extends StreamWindow {
  offsetMs: number;
  /** The item's content length; 0 = unknown (no upper clamp). */
  durationMs: number;
}

/** A ←/→/◀◀/▶▶ seek at most this far past a still-growing edge lands at the
 *  edge instead of re-issuing. Right after a (re)start the playlist lists
 *  only the first few segments while ffmpeg's initial ~60 s burst is seconds
 *  from written, so a ▶▶ pressed during "Starting playback…" re-issued a
 *  second session (and doubled the start) for a point that was about to be
 *  there anyway. Android's seekRelative never re-issues forward at all; this
 *  keeps the re-issue for a jump well past what ffmpeg will have soon. */
export const RELATIVE_SEEK_EDGE_MARGIN_MS = 60_000;

export interface SeekOptions {
  /** For a relative key seek: past a growing edge by no more than this,
   *  clamp to the edge rather than re-issue. 0 / absent = absolute seek. */
  forwardMarginMs?: number;
}

/**
 * How to reach content position `targetMs` on a server session. A seek
 * before the session's head can't be served (that part was never
 * transcoded), and one past a growing edge stalls behind ffmpeg's 1x
 * -readrate, so both re-issue the session at the target (Android's
 * reissueAt; the web's seekToContentTime). The target is clamped to the
 * item first; a re-issue also stays REISSUE_END_GUARD_MS clear of the end.
 * A relative key seek only a little past a growing edge clamps to it
 * (opts.forwardMarginMs).
 */
export function classifySeek(targetMs: number, w: SeekWindow, opts: SeekOptions = {}): SeekPlan {
  const target = clampToItem(targetMs, w.durationMs);
  const windowEnd = w.offsetMs + w.producedEndMs;
  if (target >= w.offsetMs && target <= windowEnd) {
    return { kind: 'local', streamSec: (target - w.offsetMs) / 1000 };
  }
  if (target > windowEnd && w.ended) {
    return { kind: 'clamp', streamSec: w.producedEndMs / 1000 };
  }
  const margin = opts.forwardMarginMs ?? 0;
  if (target > windowEnd && margin > 0 && w.producedEndMs > 0 && target - windowEnd <= margin) {
    return { kind: 'clamp', streamSec: w.producedEndMs / 1000 };
  }
  let at = target;
  if (w.durationMs > 0) at = Math.min(at, Math.max(0, w.durationMs - REISSUE_END_GUARD_MS));
  return { kind: 'reissue', positionMs: at };
}

/** Where the player is, for planContentSeek. */
export interface ContentSeekState {
  /** The bound stream's metadata is in: it can take a seek. */
  streamReady: boolean;
  /** A new session is on its way (the session controller's `opening`). */
  opening: boolean;
  /** A server HLS session; false = the file itself (direct audio), where
   *  content time is media time. */
  serverStream: boolean;
  /** The item's content length; 0 = unknown. */
  durationMs: number;
  /** The live session's content offset and produced window (only read for
   *  a server stream). */
  offsetMs: number;
  window: StreamWindow;
}

export type ContentSeekAction =
  /** Can't seek now (not ready, or a session is being swapped in): hold the
   *  target as a pending scrub; the next stream's loadedmetadata lands it.
   *  `relative` travels with it (a key seek may later clamp to an edge). */
  | { kind: 'park'; targetMs: number; relative: boolean }
  /** Set currentTime to `streamSec`; the playhead is then `contentMs`. */
  | { kind: 'seek'; streamSec: number; contentMs: number }
  /** Open a new session at this content position. */
  | { kind: 'reissue'; positionMs: number };

/**
 * The player's seek decision for a content-time target, the one the watch
 * page's seekToContent acts on (scrubs, chapters, Skip Intro / Credits all go
 * through it): park while the player can't seek, a plain media seek on direct
 * audio, else classifySeek on the live session (a key seek with the
 * RELATIVE_SEEK_EDGE_MARGIN_MS edge margin). Pure so the page's own rule is
 * what the tests run.
 */
export function planContentSeek(targetMs: number, relative: boolean, s: ContentSeekState): ContentSeekAction {
  if (!s.streamReady || s.opening) return { kind: 'park', targetMs, relative };
  if (!s.serverStream) {
    const t = clampToItem(targetMs, s.durationMs);
    return { kind: 'seek', streamSec: t / 1000, contentMs: t };
  }
  const plan = classifySeek(
    targetMs,
    { offsetMs: s.offsetMs, durationMs: s.durationMs, ...s.window },
    { forwardMarginMs: relative ? RELATIVE_SEEK_EDGE_MARGIN_MS : 0 },
  );
  if (plan.kind === 'reissue') return plan;
  return { kind: 'seek', streamSec: plan.streamSec, contentMs: toContentMs(plan.streamSec, s.offsetMs) };
}

/** HTMLMediaElement.HAVE_FUTURE_DATA: enough is buffered to play on. */
export const HAVE_FUTURE_DATA = 3;

/** The element can't play on from what it has (or is mid-seek): a spinner
 *  belongs on screen. Not merely "a load was retried": a retry while the
 *  forward buffer still plays put a spinner over moving video that no
 *  'playing' / 'seeked' / 'canplay' ever cleared (and froze Up Next). */
export function isStarved(el: { readyState: number; seeking: boolean }): boolean {
  return el.seeking || el.readyState < HAVE_FUTURE_DATA;
}

/** A cross-device sync position as a local seek (stream seconds), or null to
 *  leave the player alone: within SYNC_MIN_DELTA_MS of where it is, or
 *  outside the loaded session (sync never re-issues a session behind the
 *  user's back; Android parity). */
export function syncSeekStreamSec(reportedMs: number, currentMs: number, w: SeekWindow): number | null {
  if (!Number.isFinite(reportedMs) || reportedMs < 0) return null;
  if (Math.abs(reportedMs - currentMs) < SYNC_MIN_DELTA_MS) return null;
  const plan = classifySeek(reportedMs, w);
  return plan.kind === 'local' ? plan.streamSec : null;
}

/** A content position kept inside the item: [0, durationMs], or just ≥ 0
 *  while the length is unknown (0). */
export function clampToItem(ms: number, durationMs: number): number {
  const cap = durationMs > 0 ? durationMs : Number.MAX_SAFE_INTEGER;
  return Math.max(0, Math.min(cap, Math.round(ms)));
}

/** One ←/→/◀◀/▶▶ press added to the pending scrub target (or, with none
 *  pending, to the current position), clamped to the item. */
export function nudgeScrub(
  pendingMs: number | null,
  currentMs: number,
  deltaMs: number,
  durationMs: number
): number {
  return clampToItem((pendingMs ?? currentMs) + deltaMs, durationMs);
}

/** Whether `n` names one of `count` audio tracks. The server's
 *  audio_stream_index is the 0-based ORDINAL among the file's audio streams
 *  (`-map 0:a:N`), never AudioStream.index (the absolute ffprobe index, which
 *  counts the video and subtitle streams too and gets a 400 "out of range",
 *  or the wrong track). */
export function validAudioOrdinal(n: number, count: number): boolean {
  return Number.isInteger(n) && n >= 0 && n < count;
}

/** A first load that would leave less than this (seconds) of the listed
 *  stream past its start counts as starting at the playlist's edge (see
 *  firstLoadPositionSec): it shows a frame or two, then waits as long as a
 *  start past the edge does. */
export const FIRST_LOAD_MIN_RUNWAY_SEC = 1;

/** How much a segment may run over its playlist's TARGETDURATION, which is
 *  the longest segment rounded to the nearest second (RFC 8216 4.3.3.1). */
const TARGET_DURATION_ROUNDING_SEC = 0.5;

/** The part of a parsed playlist firstLoadPositionSec reads (hls.js's
 *  LevelDetails). */
export interface FirstPlaylist {
  /** No ENDLIST yet: ffmpeg is still writing it. */
  live: boolean;
  targetduration: number;
  fragments: ReadonlyArray<{ start: number; duration: number }>;
}

/**
 * Where hls.js starts loading a session (stream seconds, or -1 for its own
 * pick), decided once the session's first playlist is in. `wantSec` is the
 * start the page asked for (hlsStartPositionSec), `minStartSec` the first
 * audible frame (the seg0 audio gap; 0 when none was measured).
 *
 * A start at (or within FIRST_LOAD_MIN_RUNWAY_SEC of) the end of what a
 * growing playlist lists has nothing to play: hls.js loads the last listed
 * segment, parks at the start with no picture, and waits a whole live
 * reload (one target duration, ~10 s for a long-GOP 4K remux whose first
 * playlist lists one segment) before the next segment is even named. That
 * was ~27 s to the first frame resuming 1917. Such a start instead begins at
 * the start of the segment it falls in (the last listed one), replaying at
 * most one segment (one GOP on a remux) while ffmpeg writes the next.
 *
 * The replay plays on rather than seeking to `wantSec` once that is
 * buffered: getting there needs the very segment whose wait this skips, and
 * a jump forward mid-picture would be worse than a few seconds seen twice.
 * So the replay is capped at one target duration (plus its rounding); a
 * start further past the edge than that keeps `wantSec` and waits as before.
 * Nothing changes for a finished (VOD) playlist, one with nothing listed, or
 * a start hls.js picks itself (-1). The content position stays
 * stream + offset either way: the caller moves its playhead to the result.
 */
export function firstLoadPositionSec(
  wantSec: number,
  playlist: FirstPlaylist | null | undefined,
  minStartSec = 0,
): number {
  if (!(wantSec > 0) || !playlist || !playlist.live) return wantSec;
  const frags = playlist.fragments;
  if (frags.length === 0) return wantSec;
  const last = frags[frags.length - 1];
  const edge = last.start + last.duration;
  if (!Number.isFinite(edge) || edge - wantSec >= FIRST_LOAD_MIN_RUNWAY_SEC) return wantSec;
  // The segment the start falls in: the last one starting at or before it
  // (the last listed one when the start lies past the edge).
  let segStart = -1;
  for (let i = frags.length - 1; i >= 0; i--) {
    if (frags[i].start <= wantSec) {
      segStart = frags[i].start;
      break;
    }
  }
  if (segStart < 0) return wantSec;
  // Never before the first audible frame.
  const from = Math.max(segStart, minStartSec > 0 ? minStartSec : 0);
  if (from >= wantSec) return wantSec;
  const target = Number.isFinite(playlist.targetduration) && playlist.targetduration > 0 ? playlist.targetduration : 0;
  if (wantSec - from > target + TARGET_DURATION_ROUNDING_SEC) return wantSec;
  return from;
}

/**
 * hls.js settings for a server session, ported from the web client's
 * attachHls (same engine). The session is an HLS EVENT playlist that grows
 * as ffmpeg writes it, so hls.js treats it as live:
 *  - startPosition puts the first frame at the requested point (see
 *    hlsStartPositionSec), and liveSyncDurationCount / liveMaxLatency keep
 *    hls.js from jumping toward (and stalling at) the live edge before
 *    ENDLIST.
 *  - autoStartLoad is off: the page starts loading once the first playlist
 *    is parsed, at firstLoadPositionSec, and turns it back on once that
 *    event dispatch is over (armFirstLoad, ./first-load.ts: sooner, hls.js's
 *    own autostart replaced the start) so a media-error recovery re-attach
 *    restarts loading as hls.js always does. startPosition stays the
 *    requested start for those restarts.
 *  - A 30 s forward buffer (hls.js's default, what HEAD ran) and a 30 s back
 *    buffer. 60 s forward with a 90 s back buffer overran webOS 6's MSE
 *    quota on high-bitrate remuxes (60 s at 15-30 Mbps is 110-225 MB):
 *    appends failed with bufferFullError, hls.js flushed and the picture
 *    stalled mid-play, and Chromium's own eviction emptied the back buffer
 *    (a 30 s rewind refetched everything). maxMaxBufferLength pins the
 *    forward buffer at the same 30 s (letting it grow raced hls.js to a
 *    growing session's edge, where it stalled). The buffer is bounded by
 *    time alone: maxBufferSize only ever lengthens it, for a level with a
 *    BANDWIDTH (the session's media playlist has none) and under that same
 *    cap, so it isn't set.
 *  - 30 s to first byte / 60 s per load: ffmpeg can take 10-20 s to open an
 *    HEVC/AV1 source before segment 0 exists (Android uses 30/60 too).
 *  - nudgeMaxRetry stays at hls.js's default of 3 (the web client sets 0).
 *    A 0 doesn't seek past anything: in hls.js 1.6 the first stall inside a
 *    buffered range becomes a fatal bufferStalledError, which our recovery
 *    ladder would answer with recoverMediaError and then a permanent
 *    remux->transcode fallback. Holes between ranges are skipped by
 *    _trySkipBufferHole whatever this is set to, and startPosition already
 *    handles the PTS-offset start.
 */
export function hlsSessionConfig(startPositionSec: number): Partial<HlsConfig> {
  return {
    lowLatencyMode: false,
    autoStartLoad: false,
    startPosition: startPositionSec,
    liveSyncDurationCount: 999,
    liveMaxLatencyDurationCount: 1002,
    maxBufferLength: 30,
    maxMaxBufferLength: 30,
    backBufferLength: 30,
    startFragPrefetch: true,
    fragLoadPolicy: {
      default: {
        maxTimeToFirstByteMs: 30_000,
        maxLoadTimeMs: 60_000,
        timeoutRetry: { maxNumRetry: 4, retryDelayMs: 1000, maxRetryDelayMs: 8000 },
        errorRetry: { maxNumRetry: 6, retryDelayMs: 1000, maxRetryDelayMs: 8000 },
      },
    },
    manifestLoadPolicy: {
      default: {
        maxTimeToFirstByteMs: 30_000,
        maxLoadTimeMs: 60_000,
        timeoutRetry: { maxNumRetry: 2, retryDelayMs: 1000, maxRetryDelayMs: 8000 },
        errorRetry: { maxNumRetry: 4, retryDelayMs: 1000, maxRetryDelayMs: 8000 },
      },
    },
  };
}
