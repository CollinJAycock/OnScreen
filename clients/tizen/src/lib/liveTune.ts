// What Live TV does about a FATAL hls.js error on a channel's stream. hls.js
// has already retried a failed load by the time it calls one fatal (its own
// load policies), so this is the ladder above that:
//
//  hiccup   A fragment or playlist-refresh failure that isn't a 4xx (a
//           timeout, a 5xx, the network gone): a tuner stream drops those on
//           signal hiccups and tuner re-keys, so startLoad() resumes it, a
//           few times at most. It used to be startLoad() for every network
//           fatal, without end.
//  media    One recoverMediaError().
//  failed   The playlist itself failing (manifestLoadError / ...TimeOut /
//           ...ParsingError), any 4xx (a channel the tuner can't find, a
//           segment that rolled out of the live window, a refused token), an
//           exhausted hiccup budget, a second media fatal, anything else:
//           re-tune the channel once (a fresh hls.js on a fresh playlist, which
//           rejoins the live edge), then show the error. startLoad() can't help
//           these: hls.js 1.6 requests a playlist only from loadSource(), so
//           before one ever loaded it does nothing at all, and a 404 on the
//           stream sat black for good with no error and no further request.
//
// The re-tune keeps the budget it's spending (its own hiccup and media tries
// start over: it's a new hls.js), so a channel that fails every time ends on
// the error rather than re-tuning forever. A tune by the user starts a fresh
// budget, and so does the picture coming up: a channel that has played for a
// while has earned another re-tune when it drops.
//
// Pure so the ladder is tested without hls.js or a tuner; the page carries the
// state and does what the plan says.

import { t, tn } from './i18n/index.svelte';
import type { RemoteKey } from './focus/keys';

export type LiveFatalKind = 'network' | 'media' | 'other';

export interface LiveFatal {
  kind: LiveFatalKind;
  /** hls.js data.details, e.g. 'manifestLoadError', 'fragLoadError'. */
  details: string;
  /** HTTP status of the failed load, when hls.js has one. */
  httpStatus?: number;
}

/** What a tune may still spend on recovering. */
export interface TuneBudget {
  /** startLoad() restarts spent on hiccups. */
  loadRestarts: number;
  mediaRecovered: boolean;
  /** The automatic re-tune has been used. */
  retuned: boolean;
}

export type LivePlan =
  /** startLoad() on the same hls.js. */
  | 'restartLoad'
  /** recoverMediaError() on the same hls.js. */
  | 'recoverMedia'
  /** Tear down and tune the same channel again. */
  | 'retune'
  /** Stop and show the error. */
  | 'fail';

/** startLoad() restarts per hls.js instance. Each fatal hiccup already comes
 *  after hls.js's own retries, so a few is plenty. */
export const MAX_LOAD_RESTARTS = 3;

/** How long a tune (or a stall once it's playing) may go without a picture
 *  before the error shows. Longer than the server's own wait for the tuner's
 *  first playlist (10 s, then a 504) plus hls.js's retry of that 504; a tune
 *  that's merely slow is up well inside it. */
export const TUNE_WATCHDOG_MS = 20_000;

/** HTMLMediaElement.HAVE_FUTURE_DATA: enough buffered to play on. */
const HAVE_FUTURE_DATA = 3;

/**
 * Whether the watchdog, going off, ends the tune on the error. Not while
 * the user has paused a stream that has its data: the watchdog stands down
 * on 'playing', which a paused video never fires, so pausing a slow tune
 * (OK on the black screen while the tuner warms up) or a stall used to end
 * a channel that had come up fine on "Couldn't tune this channel." 20 s in.
 * The watchdog isn't re-armed then: play() on that data fires 'playing',
 * and play() into a stream that died meanwhile fires 'waiting', which arms
 * it afresh. Paused with no picture to show, the tune is as dead as it
 * would be playing, and fails.
 */
export function watchdogFails(video: { paused: boolean; readyState: number }): boolean {
  return !(video.paused && video.readyState >= HAVE_FUTURE_DATA);
}

/**
 * The zap a key makes in the Live TV player: +1 the next channel in the
 * list (the server's lineup order: sort order, then number), -1 the
 * previous, 0 none. ▲ and CH ▲ both go up the lineup, ▼ and CH ▼ down, so
 * the hint's "▲▼ or CH ▲▼" is true of both. Up used to step back up the
 * list as the grid lays it out while CH ▲ went forward, so the two "up"s
 * went opposite ways. OnScreen's Android TV player doesn't zap (its D-pad
 * shows the player's controls, the CH keys aren't handled), so there was
 * no direction of its own to copy; this is Android TV's own live TV app's
 * (AOSP's TV app takes DPAD_UP as CHANNEL_UP), and a TV's.
 */
export function zapStep(k: RemoteKey): 1 | -1 | 0 {
  if (k === 'up' || k === 'channelUp') return 1;
  if (k === 'down' || k === 'channelDown') return -1;
  return 0;
}

export function freshTuneBudget(): TuneBudget {
  return { loadRestarts: 0, mediaRecovered: false, retuned: false };
}

/** The budget the automatic re-tune starts with: the re-tune is spent; the
 *  hiccup and media tries belong to the new hls.js. */
export function retuneBudget(): TuneBudget {
  return { loadRestarts: 0, mediaRecovered: false, retuned: true };
}

/** The tune itself failed, as opposed to a running stream hiccuping: the
 *  playlist never came (or came broken), or the server said no (4xx). */
export function isFailedTune(f: LiveFatal): boolean {
  if (/^manifest/i.test(f.details)) return true;
  return f.httpStatus !== undefined && f.httpStatus >= 400 && f.httpStatus <= 499;
}

/** The next step for a fatal error, given the tune's budget. Mutates nothing;
 *  the caller records what it did (see spendLivePlan). */
export function planLiveFatal(f: LiveFatal, b: TuneBudget): LivePlan {
  if (!isFailedTune(f)) {
    if (f.kind === 'network' && b.loadRestarts < MAX_LOAD_RESTARTS) return 'restartLoad';
    if (f.kind === 'media' && !b.mediaRecovered) return 'recoverMedia';
  }
  return b.retuned ? 'fail' : 'retune';
}

/** Spend the budget a plan uses. A re-tune is recorded by starting it with
 *  retuneBudget(). */
export function spendLivePlan(plan: LivePlan, b: TuneBudget): void {
  if (plan === 'restartLoad') b.loadRestarts++;
  else if (plan === 'recoverMedia') b.mediaRecovered = true;
}

/** The error the player shows, in the app's language (lib/i18n) as it was
 *  when the tune failed. */
export interface TuneFailure {
  title: string;
  /** Why, as far as the app can tell; '' when it can't. */
  detail: string;
}

/** Why a tune ended on the error: a fatal hls.js error, the watchdog, or a
 *  message of the app's own (no hls.js on the TV, a throw while starting). */
export type TuneFailureCause = LiveFatal | 'timeout' | { message: string };

/** The player's error for a tune that failed. `played`: the picture had come
 *  up since the user tuned, so it's the channel dropping, not the tune. */
export function describeTuneFailure(cause: TuneFailureCause, played: boolean): TuneFailure {
  const title = played ? t('live.stopped') : t('live.tuneFailed');
  return { title, detail: failureDetail(cause, played) };
}

function failureDetail(cause: TuneFailureCause, played: boolean): string {
  if (cause === 'timeout') {
    const secs = Math.round(TUNE_WATCHDOG_MS / 1000);
    return played ? tn('live.noPictureFor', secs) : tn('live.noPictureAfter', secs);
  }
  if ('message' in cause) return cause.message;
  const s = cause.httpStatus;
  // The server's answers on the stream (api/v1/livetv_stream.go): 404 for a
  // channel that's gone or disabled, or the tuner driver not finding it; 503
  // for every tuner busy or live TV off; 504 when the tuner produced no
  // playlist in time.
  if (s === 404) return t('live.http404');
  if (s === 503) return t('live.http503');
  if (s === 504) return t('live.http504');
  // The status as digits ('404', not a formatted number): it's a code.
  if (s === 401 || s === 403) return t('live.refused', { status: String(s) });
  if (s !== undefined && s >= 400) return t('live.httpError', { status: String(s) });
  if (cause.kind === 'media') return t('live.decodeFailed');
  // hls.js's own name for the error ('fragLoadError') stays as it is.
  return cause.details ? t('live.playbackError', { details: cause.details }) : '';
}
