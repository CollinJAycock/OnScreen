// The player page's "is this still about the stream that is playing?" gates.
// Each was a bug once, and each lived only as a condition inline in the
// watch page, so deleting a clause still passed every test. Pure: the page
// passes its state in, these decide.

/** What a dead-stream probe (one heartbeat after a 403 / 404 on the session)
 *  ends in once it answers. */
export type ProbeOutcome =
  /** The server refused playback: end it with that message. */
  | 'refusal'
  /** The stream that died is still the one bound: the error overlay. */
  | 'fatal'
  /** Nothing to do: the player is gone, already stopped, or has moved on. */
  | 'ignore';

export interface ProbeState {
  /** The probe's heartbeat came back refused (stopped / parental / revoked). */
  refused: boolean;
  /** The player already ended (a playback.stop event landed meanwhile), is
   *  on its way out, or was torn down. */
  ended: boolean;
  /** attachGen when the probe started, and now. */
  genAtStart: number;
  genNow: number;
  /** A new stream is on its way (a seek re-issue, an audio switch). */
  opening: boolean;
  /** The app is in the background; the resume re-opens. */
  suspended: boolean;
  /** The error overlay is already up. */
  errorShown: boolean;
}

/**
 * The probe can wait behind a hung heartbeat on the report lane, so the
 * stream it was about may be long replaced when it answers (an audio switch,
 * Home and back). A refusal still ends playback: it is about the user and the
 * item, not the stream. The generic error is only for the stream that died:
 * shown over a newer stream it tore that stream down for nothing.
 */
export function probeOutcome(s: ProbeState): ProbeOutcome {
  if (s.ended) return 'ignore';
  if (s.refused) return 'refusal';
  if (s.genAtStart !== s.genNow || s.opening || s.suspended || s.errorShown) return 'ignore';
  return 'fatal';
}

/**
 * Whether the element's 'ended' is about the stream that is playing. While a
 * re-issue is out the old session plays on from its buffer, and its end (a
 * chapter jump back from the last seconds) marked the item watched and
 * advanced instead of landing the jump. A source being swapped in (not
 * ready) has no end of its own yet. A page already torn down (`destroyed`)
 * reports nothing more: its element's late events are about an item the
 * user has left. Whether the end is the END OF THE ITEM is endedAction's.
 */
export function shouldHandleEnded(s: {
  streamReady: boolean;
  opening: boolean;
  refused: boolean;
  leaving: boolean;
  errorShown: boolean;
  destroyed?: boolean;
}): boolean {
  return s.streamReady && !s.opening && !s.refused && !s.leaving && !s.errorShown && !s.destroyed;
}

/** A natural end's last playhead reading falls within this of the item's
 *  length: timeupdate comes every ~250 ms, and the listed length can run a
 *  little past the stream's last frame. */
export const END_TOLERANCE_MS = 2_000;
/** A long item gets 1% of its length instead, up to this: a container's
 *  listed length can overshoot its last frame by more than 2 s, and an end
 *  that close to the credits is the end anyway. */
export const END_TOLERANCE_MAX_MS = 10_000;

/** How far from the item's length (ms) an 'ended' still counts as its end. */
export function endToleranceMs(durationMs: number): number {
  const proportional = Number.isFinite(durationMs) && durationMs > 0 ? durationMs * 0.01 : 0;
  return Math.max(END_TOLERANCE_MS, Math.min(END_TOLERANCE_MAX_MS, proportional));
}

/** One automatic retry per early end; the budget comes back once the playhead
 *  is this far from where the retry re-opened, in either direction (a stall
 *  an hour later, or after a seek back, is a new one, not the same one
 *  again). */
export const FALSE_END_REFILL_MS = 30_000;

/** What the page does with the element's 'ended'. */
export type EndedAction =
  /** Not about the stream that is playing (see shouldHandleEnded). */
  | { kind: 'ignore' }
  /** The end of the item: report it watched, then the next item or out. */
  | { kind: 'complete' }
  /** The stream stopped short of the end: re-open it once at `atMs`. */
  | { kind: 'retry'; atMs: number }
  /** It stopped short again: the error overlay (OK retries from `atMs`). */
  | { kind: 'fail'; atMs: number };

export interface EndedState {
  streamReady: boolean;
  opening: boolean;
  refused: boolean;
  leaving: boolean;
  errorShown: boolean;
  destroyed: boolean;
  /** The bound source played (its 'playing' fired), or the user moved its
   *  playhead (a seek: one to the very end ends the item). Attaching alone
   *  is not playing. PlayheadWatch.played. */
  played: boolean;
  /** Content ms where the playhead really was last: playback's own
   *  timeupdates, a seek, the start. Never the element's clock at the end:
   *  Chromium reports currentTime = duration once a stream has ended, early
   *  or not. PlayheadWatch.lastMs. */
  lastMs: number;
  /** The item's content length; 0 = unknown. */
  durationMs: number;
  /** A server session whose playlist is finished (ENDLIST): the content
   *  position its last segment ends at. null otherwise (direct play, a
   *  playlist still growing). */
  streamEndMs: number | null;
  /** Where this page's last early-end retry re-opened, null if none yet. */
  retriedAtMs: number | null;
}

/**
 * Whether an 'ended' is the end of the item. Only when the source actually
 * played and its playhead was within endToleranceMs of the item's length (or
 * the length is unknown and it played), or within END_TOLERANCE_MS of the end
 * of a finished server session: the server terminates a session's playlist
 * once its transcode is done (one that stopped short of the source is
 * extended first), and the listed length (the container's) can overshoot the
 * last frame, where a re-opened session has nothing left to write and would
 * end a film on an error. Anything else is a stall the media
 * stack reported as an end: on the C1 a track started, never got going, and
 * ~30 s later the element fired 'ended' (most likely Chromium's demuxer
 * taking a read that failed mid-file for the end of the stream; the page
 * logs the element's state when it happens). Taken as the end, that
 * reported the track watched at its full length and skipped to the next
 * one. Instead it re-opens once from where it really was, then shows the
 * error, never completion.
 */
export function endedAction(s: EndedState): EndedAction {
  if (!shouldHandleEnded(s)) return { kind: 'ignore' };
  if (s.played) {
    if (s.durationMs <= 0 || s.durationMs - s.lastMs <= endToleranceMs(s.durationMs)) return { kind: 'complete' };
    if (s.streamEndMs !== null && s.streamEndMs - s.lastMs <= END_TOLERANCE_MS) return { kind: 'complete' };
  }
  const atMs = Math.max(0, Math.round(Number.isFinite(s.lastMs) ? s.lastMs : 0));
  if (s.retriedAtMs === null || Math.abs(atMs - s.retriedAtMs) >= FALSE_END_REFILL_MS) return { kind: 'retry', atMs };
  return { kind: 'fail', atMs };
}

/** How far one timeupdate may move the playhead (at 1×) without a seek
 *  before it counts as a jump of the media clock rather than playback.
 *  timeupdate comes every ~250 ms while playing. */
export const CLOCK_JUMP_MS = 5_000;

/**
 * Whether a timeupdate moved the playhead further than playback can: the
 * media clock jumping on its own (to the end, as an early end does on
 * pipelines that don't flag it as ended yet) rather than playing there. A
 * seek since the last update (the user's, or hls.js's own start / gap skip,
 * all of which fire 'seeking') moves it legitimately; so does going back.
 */
export function isClockJump(s: { fromMs: number; toMs: number; rate: number; seeked: boolean }): boolean {
  if (s.seeked) return false;
  const rate = Number.isFinite(s.rate) && s.rate > 1 ? s.rate : 1;
  return s.toMs - s.fromMs > CLOCK_JUMP_MS * rate;
}

/**
 * Where the playhead really was, for endedAction's `lastMs` and `played`.
 * The element's own clock can't say once a stream has ended: Chromium
 * reports currentTime = duration then, however early the end came, and the
 * timeupdate that precedes 'ended' carries that clock. So a reading counts
 * only while the element hasn't ended and the clock didn't jump further than
 * playback can (isClockJump: the same false end on a pipeline that doesn't
 * flag it as ended yet). The page feeds it every start, seek and timeupdate
 * of the bound source.
 */
export class PlayheadWatch {
  /** Content ms where the playhead really was last. */
  lastMs = 0;
  /** The bound source played (its 'playing' fired) or the user seeked it. */
  played = false;
  /** The previous reading, which the next is measured from. */
  private fromMs = 0;
  /** A seek began since the previous reading. */
  private seeked = false;

  /** A new source was bound, asked to start at `ms`: it hasn't played. */
  attach(ms: number): void {
    this.at(ms);
    this.played = false;
  }

  /** The playhead is known to be at `ms` (an early start, a sync seek). */
  at(ms: number): void {
    this.lastMs = ms;
    this.fromMs = ms;
  }

  /** The user put the playhead at `ms`: a seek to the very end (a clamp past
   *  a finished window, ▶▶ in a track's last seconds) ends the item, even on
   *  a source that hadn't started playing yet. */
  userSeek(ms: number): void {
    this.at(ms);
    this.played = true;
  }

  /** The element began a seek (the user's, or hls.js's start / gap skip):
   *  the next reading may land anywhere. */
  seeking(): void {
    this.seeked = true;
  }

  /** The element's 'playing' for the bound source. */
  playing(): void {
    this.played = true;
  }

  /** A timeupdate read the playhead at `ms` (content time). */
  tick(ms: number, el: { ended: boolean; playbackRate: number }): void {
    const jump = isClockJump({ fromMs: this.fromMs, toMs: ms, rate: el.playbackRate, seeked: this.seeked });
    if (!el.ended && !jump) this.lastMs = ms;
    this.fromMs = ms;
    this.seeked = false;
  }
}

/**
 * Whether the page still navigates once a wait it started is over: the final
 * report before Back or the next item (stopAndLeave, goToNext; up to the
 * reporter's cap), or the next-item lookup at the end (onEnded). Those waits
 * last seconds against a slow server, and a "play on this TV" transfer or a
 * playerEpoch remount landing meanwhile has destroyed this instance and
 * moved the app on: the dead instance's goBack popped the hub the transfer
 * put under the new player, and its replaceTo navigated away from the new
 * player, ending its session. The end-of-item lookup also yields to a
 * refusal or a Back that came during it (`refused`, `leaving`); after the
 * final report `leaving` is the caller's own, so it isn't passed.
 */
export function navigatesAfterWait(s: { destroyed: boolean; refused?: boolean; leaving?: boolean }): boolean {
  return !s.destroyed && !s.refused && !s.leaving;
}

/** What a failed direct-play audio source leads to. */
export type DirectAudioFailure =
  /** MEDIA_ERR_NETWORK: the connection, not the file. The error overlay. */
  | 'network'
  /** The TV can't play the file: the same file through a server session. */
  | 'transcode'
  /** It already had its transcode: the error overlay. */
  | 'fatal';

/**
 * The direct audio source failed, and the server says it didn't stop it.
 * `mediaErrorCode` is the element's MediaError code. A file the TV can't
 * decode (a .dsf / .ape rip, an odd container) gets one server transcode,
 * the server's audio-only session, as Android falls back from a failed
 * direct play (fallbackFromDirectPlay); it used to end on "This TV can't
 * play this audio file". Once only: `fellBack` is set by the first.
 */
export function directAudioFailure(mediaErrorCode: number | null | undefined, fellBack: boolean): DirectAudioFailure {
  if (mediaErrorCode === 2) return 'network';
  return fellBack ? 'fatal' : 'transcode';
}

/**
 * Whether a buffered fragment clears the rebuffer spinner. A retried load
 * that recovered while the forward buffer was still playing fires no
 * 'playing' / 'seeked' / 'canplay', so the spinner stayed up over playing
 * video (and froze Up Next's countdown). Only for the live instance's ready
 * stream with no start on its way (a re-issue keeps its spinner until the
 * new stream can play), and only once the element isn't starved.
 */
export function clearsSpinnerOnFragBuffered(s: {
  buffering: boolean;
  liveInstance: boolean;
  streamReady: boolean;
  opening: boolean;
  starved: boolean;
}): boolean {
  return s.buffering && s.liveInstance && s.streamReady && !s.opening && !s.starved;
}
