// Progress reporting for the player: the 10 s 'playing' heartbeat and the
// 'paused' / 'stopped' reports, as PUT /items/{id}/progress. Mirrors the
// Android TV client's ProgressTracker + ReportLane + HeartbeatRefusal.
//
//  - Every report goes out on one lane, in the order it was asked for: a
//    teardown's 'paused' and 'stopped' leave a few ms apart, and sent side by
//    side, a 'paused' (or a late heartbeat) landing second put the item back
//    in the server's Now Playing after it had stopped.
//  - 'stopped' is terminal and latched: the end of an item, the advance to
//    the next one and the page's cleanup all reach it, and the server got two
//    or three 'stopped' reports per item. After it nothing else is sent.
//  - A terminal report waits at most GRACE_MS for whatever is ahead of it on
//    the lane: a hung heartbeat held the final position behind it for as long
//    as its request took to time out.
//  - Any 403 on a 'playing' heartbeat ends playback (classifyHeartbeatError).
//
// Pure (no $lib imports): the page injects the sender, so the ordering and the
// latch are tested with a fake one and fake timers.

/** A refused 'playing' heartbeat: the server no longer lets this profile
 *  watch the item. */
export type HeartbeatRefusal =
  /** PARENTAL_LIMIT: a daily cap reached or the allowed-hours window closed.
   *  `reason` is the server's reason code (daily_limit_reached, …). */
  | { kind: 'parental'; reason: string }
  /** PLAYBACK_STOPPED: an admin stopped this stream from Now Playing (the
   *  backstop for a missed playback.stop event). `message` is the server's
   *  sentence, '' when it sent none. */
  | { kind: 'stopped'; message: string }
  /** Any other 403: library access revoked or the content-rating ceiling
   *  lowered while this was playing. */
  | { kind: 'revoked' };

/** What the player shows for a revoked item (Android's content_revoked). */
export const CONTENT_REVOKED_TEXT = 'This title is no longer available on this profile.';

/** The refusal a failed report represents, or null when it is none: another
 *  state (a refused pause / stop report is not a refusal of playback),
 *  another status, or a transport failure. Duck-typed on the API error's
 *  status / code / message so this module needn't import the client. */
export function classifyHeartbeatError(state: ReportState, e: unknown): HeartbeatRefusal | null {
  if (state !== 'playing' || !e || typeof e !== 'object') return null;
  const err = e as { status?: unknown; code?: unknown; message?: unknown };
  if (err.status !== 403) return null;
  const message = typeof err.message === 'string' ? err.message : '';
  if (err.code === 'PARENTAL_LIMIT') return { kind: 'parental', reason: message };
  if (err.code === 'PLAYBACK_STOPPED') return { kind: 'stopped', message };
  return { kind: 'revoked' };
}

export type ReportState = 'playing' | 'paused' | 'stopped';

/** How the item is being played, for the server's direct-vs-transcode split. */
export type PlayDecision = 'directPlay' | 'directStream' | 'transcode';

export interface ProgressReport {
  itemId: string;
  positionMs: number;
  durationMs: number;
  state: ReportState;
  clientName?: string;
  decision?: PlayDecision;
  fileId?: string;
}

export interface ReporterOptions {
  itemId: string;
  /** Sends one report; rejects with the API error on failure. */
  send: (r: ProgressReport) => Promise<unknown>;
  clientName?: string;
  fileId?: string;
  /** Called once when the server refuses a 'playing' heartbeat. The
   *  heartbeat has already stopped; the page ends playback with the reason. */
  onRefused?: (r: HeartbeatRefusal) => void;
  /** Heartbeat interval (10 s, as every client). */
  intervalMs?: number;
  /** How long a 'paused' / 'stopped' report waits for the reports ahead of
   *  it before going out anyway. */
  graceMs?: number;
  /** stopped()'s promise resolves by this long after the call even if the
   *  report hasn't settled: the page awaits it before leaving, and a dead
   *  server must not hold the remote's Back. */
  stopWaitMs?: number;
}

/** A position snapshot for the heartbeat; null when nothing is playing (a
 *  pause, a rebuffer, a start not yet live), which skips the beat. */
export type HeartbeatState = { positionMs: number; durationMs: number } | null;

export const HEARTBEAT_MS = 10_000;
export const GRACE_MS = 2_000;
export const STOP_WAIT_MS = 2_500;

/** `p`, or undefined once `ms` have passed: whichever comes first. Never
 *  rejects. */
export function settleWithin<T>(p: Promise<T>, ms: number): Promise<T | undefined> {
  return new Promise((resolve) => {
    const t = setTimeout(() => resolve(undefined), ms);
    p.then(
      (v) => { clearTimeout(t); resolve(v); },
      () => { clearTimeout(t); resolve(undefined); },
    );
  });
}

export class ProgressReporter {
  private readonly opts: ReporterOptions;
  private timer: ReturnType<typeof setInterval> | null = null;
  /** Settles once every report asked for so far has settled. */
  private lane: Promise<void> = Promise.resolve();
  /** The last heartbeat while it is in flight (a beat due meanwhile is skipped). */
  private beatInFlight = false;
  private lastBeatMs = -1;
  /** The last 'paused' report's position: a teardown fires several pauses. */
  private lastPausedMs: number | null = null;
  /** No terminal report before the player has actually played: backing out
   *  of "Starting playback…" must not write a stop event for a title that
   *  never started (it would land in History / Continue Watching). */
  private armed = false;
  private stoppedPromise: Promise<void> | null = null;
  private refused = false;
  private decision: PlayDecision | undefined;

  constructor(opts: ReporterOptions) {
    this.opts = opts;
  }

  /** The playback decision the next reports carry (it changes when a remux
   *  falls back to a transcode). */
  setDecision(d: PlayDecision | undefined): void {
    this.decision = d;
  }

  /** The player has played: terminal reports are sent from now on. */
  arm(): void {
    this.armed = true;
  }

  /** Whether the player has played (see arm). */
  get isArmed(): boolean {
    return this.armed;
  }

  /** Whether stopped() has been called (nothing more will be sent). */
  get isStopped(): boolean {
    return this.stoppedPromise !== null;
  }

  /** Run the heartbeat. `getState` is read on every tick; a beat goes out
   *  when it returns a position that moved by a second or more since the
   *  last beat. */
  start(getState: () => HeartbeatState): void {
    this.hold();
    if (this.stoppedPromise || this.refused) return;
    this.timer = setInterval(() => this.tick(getState), this.opts.intervalMs ?? HEARTBEAT_MS);
  }

  /** Stop the heartbeat without a report. */
  hold(): void {
    if (this.timer) {
      clearInterval(this.timer);
      this.timer = null;
    }
  }

  /** Report a pause (once per position). */
  paused(positionMs: number, durationMs: number): void {
    if (this.stoppedPromise || !this.armed || durationMs <= 0) return;
    const pos = clampPos(positionMs, durationMs);
    if (this.lastPausedMs === pos) return;
    this.lastPausedMs = pos;
    void this.enqueue('paused', pos, durationMs, true);
  }

  /** The final report for this item, sent once however often it is called;
   *  every call gets the same promise. Resolves when the report has settled,
   *  or STOP_WAIT_MS after the first call, whichever is first; never rejects.
   *  The heartbeat stops for good. */
  stopped(positionMs: number, durationMs: number): Promise<void> {
    if (this.stoppedPromise) return this.stoppedPromise;
    this.hold();
    if (!this.armed || durationMs <= 0) {
      this.stoppedPromise = Promise.resolve();
      return this.stoppedPromise;
    }
    const sent = this.enqueue('stopped', clampPos(positionMs, durationMs), durationMs, true);
    this.stoppedPromise = settleWithin(sent, this.opts.stopWaitMs ?? STOP_WAIT_MS).then(() => undefined);
    return this.stoppedPromise;
  }

  /** One immediate 'playing' beat, for a stream that died under the player
   *  (an HLS 403 / 404 is what a server-side stop looks like to hls.js). The
   *  player has stopped beating by then, so the heartbeat's 403 backstop
   *  would never run. Resolves with the refusal (and stops the heartbeat),
   *  or null: accepted, or failed for another reason. onRefused is NOT
   *  called; the caller acts on the result. Nothing is sent before the
   *  player has played: a 'playing' beat for a title that never started
   *  would put it on the server's Now Playing with no 'stopped' to close it
   *  (stopped() sends nothing unarmed either). */
  async probe(positionMs: number, durationMs: number): Promise<HeartbeatRefusal | null> {
    if (this.stoppedPromise || !this.armed || durationMs <= 0) return null;
    let refusal: HeartbeatRefusal | null = null;
    await this.enqueue('playing', clampPos(positionMs, durationMs), durationMs, false, (r) => {
      refusal = r;
    });
    if (refusal) {
      this.refused = true;
      this.hold();
    }
    return refusal;
  }

  private tick(getState: () => HeartbeatState): void {
    if (this.stoppedPromise || this.refused || this.beatInFlight) return;
    const s = getState();
    if (!s || s.durationMs <= 0) return;
    const pos = clampPos(s.positionMs, s.durationMs);
    if (Math.abs(pos - this.lastBeatMs) < 1000) return;
    this.lastBeatMs = pos;
    // Playing again: the next pause at this same spot is a new pause.
    this.lastPausedMs = null;
    this.beatInFlight = true;
    void this.enqueue('playing', pos, s.durationMs, false, (r) => this.refuse(r)).finally(() => {
      this.beatInFlight = false;
    });
  }

  private refuse(r: HeartbeatRefusal): void {
    if (this.refused) return;
    this.refused = true;
    this.hold();
    this.opts.onRefused?.(r);
  }

  /** Queue one report behind the ones before it. A terminal one waits for
   *  them at most GRACE_MS. Resolves (never rejects) once it has settled. */
  private enqueue(
    state: ReportState,
    positionMs: number,
    durationMs: number,
    terminal: boolean,
    onRefusal?: (r: HeartbeatRefusal) => void,
  ): Promise<void> {
    const before = this.lane;
    const report: ProgressReport = {
      itemId: this.opts.itemId,
      positionMs,
      durationMs,
      state,
      clientName: this.opts.clientName || undefined,
      decision: this.decision,
      fileId: this.opts.fileId || undefined,
    };
    const wait = terminal ? settleWithin(before, this.opts.graceMs ?? GRACE_MS) : before;
    const mine = wait.then(() =>
      this.opts.send(report).then(
        () => undefined,
        (e) => {
          const refusal = classifyHeartbeatError(state, e);
          if (refusal) onRefusal?.(refusal);
        },
      ),
    );
    // The next report waits for this one AND everything before it: a
    // terminal report may have given up on a hung beat, but what follows it
    // still queues behind that beat (a terminal one again only for its own
    // grace period).
    this.lane = Promise.all([mine, before]).then(() => undefined);
    return mine;
  }
}

/** A position inside the item: a VBR file can play a little past the length
 *  it lists, and the server takes the report as-is. */
function clampPos(positionMs: number, durationMs: number): number {
  const p = Math.max(0, Math.round(positionMs));
  return durationMs > 0 ? Math.min(p, Math.round(durationMs)) : p;
}
