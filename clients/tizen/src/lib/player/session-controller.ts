// Who opens the player's stream, and when. Every open goes through here: the
// first start, a seek re-issue outside the produced window, an audio-track
// switch, the codec-demotion restart, the remux → transcode fallback, the
// re-open after the app comes back from the background and the retry after an
// error. The page injects the server call and the binding; this decides
// ordering, staleness and the suspend / resume capture. Pure so those rules
// are tested with fake timers.
//
// Rules:
//  - One start in flight. A request made meanwhile is queued, and a newer one
//    replaces it. The server's Start supersedes the user's running session of
//    the item, in the order the server handles the requests rather than the
//    order they were sent: two starts side by side could leave the client on
//    the one the server had just killed.
//  - A start that comes back when it is no longer wanted (a newer request is
//    queued, the app went to the background, the player left) is retired, not
//    bound.
//  - Start tears the old session down server-side as soon as it runs, so a
//    failed start can't fall back on "the old session keeps playing": it is
//    retried once at the same point with the same settings, then reported
//    (failed()), and the page shows its error. A refusal (an admin stop, a
//    parental limit) is reported at once.
//  - Backgrounded (suspend), requests wait: resume() opens the last one. The
//    capture is the start in flight or queued when there is one (its target,
//    not the stale playhead: Home pressed during "Starting playback…" used to
//    save 0:00), a pending scrub target, else the live position.

export interface OpenRequest {
  /** Content position to start at. */
  positionMs: number;
  /** Stream-copy the video (remux) instead of re-encoding it. */
  videoCopy: boolean;
  /** 0-based ordinal within file.audio_streams; null leaves the track to the
   *  server's default (the first start when no preferred audio language
   *  matched). Every re-issue names the live track explicitly. */
  audioOrdinal: number | null;
  /** Play once ready; false keeps a paused player paused. */
  autoplay: boolean;
  /** The PlayIntent version `autoplay` was read at (see PlayIntent). */
  intentSeq?: number;
}

export type OpenOutcome =
  /** Bound to the player. */
  | 'attached'
  /** A newer request replaced this one. */
  | 'superseded'
  /** The app is in the background: resume() opens it (or its successor). */
  | 'deferred'
  /** The player left, playback was refused, or an error is on screen. */
  | 'closed'
  /** The start failed for good; failed() has been called. */
  | 'failed';

export interface SessionControllerDeps<S> {
  /** Ask for a session (POST /items/{id}/transcode), or a direct source. */
  start(req: OpenRequest): Promise<S>;
  /** Bind it to the player (the previous one is the page's to retire). */
  attach(session: S, req: OpenRequest): void;
  /** End a session nothing will read. */
  retire(session: S): void;
  /** False while nothing may be opened: the player left, playback was
   *  refused, an error overlay is up. */
  canOpen(): boolean;
  /** A failure that ends playback without a retry. */
  isRefusal(err: unknown): boolean;
  /** A start failed for good (after its retry, or a refusal). */
  failed(err: unknown, req: OpenRequest): void;
  /** Wait before the one retry of a failed start. */
  retryDelayMs?: number;
}

/** The player at the moment the app is backgrounded. */
export interface SuspendSnapshot {
  /** A stream is bound (attached), so `positionMs` is the player's own. */
  live: boolean;
  /** Content position of the playhead. */
  positionMs: number;
  /** A scrub target still pending (it never got to land), or null. */
  scrubTargetMs: number | null;
  /** Whether the user means it to play (not the element's paused flag,
   *  which is true while a new source attaches). */
  wantPlaying: boolean;
  /** The PlayIntent version wantPlaying was read at. */
  intentSeq?: number;
  videoCopy: boolean;
  audioOrdinal: number;
}

export const OPEN_RETRY_DELAY_MS = 1_000;

/**
 * Whether the user means the player to play, across source swaps. The
 * element's paused flag can't say (hls.destroy() and a media detach pause it
 * without a 'pause' event), and a request's `autoplay` is only what the
 * intent was when the request was made: a start, a seek re-issue or an audio
 * switch can take seconds, and a pause pressed meanwhile used to be undone
 * when the new stream attached with the request's stale autoplay=true.
 *
 * Every change of intent (a key, the element's own play / pause on a ready
 * stream) bumps a version. A request stamps the version it read (stamp());
 * at attach, forAttach() takes the request's autoplay only when nothing
 * changed the intent since, else keeps the newer intent.
 */
export class PlayIntent {
  private want: boolean;
  private version = 0;

  constructor(initial = true) {
    this.want = initial;
  }

  get playing(): boolean {
    return this.want;
  }

  get seq(): number {
    return this.version;
  }

  /** The user (or the element, for a ready stream) says play or pause. */
  set(play: boolean): void {
    this.want = play;
    this.version++;
  }

  /** A request's play state: the intent now, and the version it was read at. */
  stamp(): { autoplay: boolean; intentSeq: number } {
    return { autoplay: this.want, intentSeq: this.version };
  }

  /** Whether a stream bound for `req` plays, recorded as the intent: the
   *  request's autoplay unless the intent changed after the request was
   *  made (a request without a stamp, e.g. a retry carrying its own
   *  autoplay, always counts as current). */
  forAttach(req: Pick<OpenRequest, 'autoplay' | 'intentSeq'>): boolean {
    if (req.intentSeq === undefined || req.intentSeq === this.version) this.want = req.autoplay;
    return this.want;
  }
}

interface Waiter {
  req: OpenRequest;
  resolve: (o: OpenOutcome) => void;
  retried: boolean;
}

export class SessionController<S> {
  private readonly deps: SessionControllerDeps<S>;
  private running: Waiter | null = null;
  private queued: Waiter | null = null;
  private resumeReq: OpenRequest | null = null;
  /** Bumped by suspend() and close(): a start that comes back to another
   *  epoch is stale. */
  private epoch = 0;
  private isSuspended = false;
  private isClosed = false;

  constructor(deps: SessionControllerDeps<S>) {
    this.deps = deps;
  }

  /** A start is in flight or queued: seeks park, the bar holds. */
  get opening(): boolean {
    return this.running !== null || this.queued !== null;
  }

  get suspended(): boolean {
    return this.isSuspended;
  }

  /** The latest open asked for and not yet bound (queued, else in flight). */
  get intent(): OpenRequest | null {
    return this.queued?.req ?? this.running?.req ?? null;
  }

  /** Open a stream for `req`, after the one in flight. */
  request(req: OpenRequest): Promise<OpenOutcome> {
    if (this.isClosed) return Promise.resolve('closed');
    if (this.isSuspended) {
      this.resumeReq = req;
      return Promise.resolve('deferred');
    }
    if (!this.deps.canOpen()) return Promise.resolve('closed');
    return new Promise<OpenOutcome>((resolve) => {
      const w: Waiter = { req, resolve, retried: false };
      if (this.running) {
        this.queued?.resolve('superseded');
        this.queued = w;
        return;
      }
      void this.drive(w);
    });
  }

  /** The app went to the background. Whatever is in flight is retired when
   *  it comes back; resume() opens the capture. */
  suspend(snap: SuspendSnapshot): void {
    if (this.isClosed || this.isSuspended) return;
    this.isSuspended = true;
    this.epoch++;
    const intent = this.intent;
    if (this.queued) {
      this.queued.resolve('deferred');
      this.queued = null;
    }
    let capture: OpenRequest | null = null;
    if (intent) {
      capture = { ...intent };
    } else if (snap.live) {
      capture = {
        positionMs: snap.positionMs,
        videoCopy: snap.videoCopy,
        audioOrdinal: snap.audioOrdinal,
        autoplay: snap.wantPlaying,
        intentSeq: snap.intentSeq,
      };
    }
    // Neither: nothing has been asked for yet (the item is still loading).
    // The start's own request, made while suspended, is deferred and becomes
    // the capture: it knows the start point, the player doesn't yet.
    if (capture && snap.scrubTargetMs !== null) capture.positionMs = snap.scrubTargetMs;
    this.resumeReq = capture;
  }

  /** Back in the foreground: open what the suspend captured (null when
   *  there is nothing to open). */
  resume(): Promise<OpenOutcome | null> {
    if (!this.isSuspended) return Promise.resolve(null);
    this.isSuspended = false;
    const r = this.resumeReq;
    this.resumeReq = null;
    return r ? this.request(r) : Promise.resolve(null);
  }

  /** The player is done (left, or playback refused): nothing more opens. */
  close(): void {
    if (this.isClosed) return;
    this.isClosed = true;
    this.epoch++;
    this.resumeReq = null;
    if (this.queued) {
      this.queued.resolve('closed');
      this.queued = null;
    }
  }

  private async drive(first: Waiter): Promise<void> {
    let w: Waiter | null = first;
    while (w) {
      this.running = w;
      const outcome = await this.runOnce(w);
      if (outcome === 'retry') {
        w.retried = true;
        continue;
      }
      w.resolve(outcome);
      w = this.queued;
      this.queued = null;
    }
    this.running = null;
  }

  private async runOnce(w: Waiter): Promise<OpenOutcome | 'retry'> {
    const epoch = this.epoch;
    if (w.retried) {
      await delay(this.deps.retryDelayMs ?? OPEN_RETRY_DELAY_MS);
      if (this.stale(epoch) || this.queued) return this.dropped(epoch);
      if (!this.deps.canOpen()) return 'closed';
    }
    let session: S;
    try {
      session = await this.deps.start(w.req);
    } catch (e) {
      // A newer request is the retry; a player that left needs none.
      if (this.stale(epoch) || this.queued) return this.dropped(epoch);
      if (!this.deps.canOpen()) return 'closed';
      if (this.deps.isRefusal(e) || w.retried) {
        this.deps.failed(e, w.req);
        return 'failed';
      }
      return 'retry';
    }
    if (this.stale(epoch) || this.queued || !this.deps.canOpen()) {
      this.deps.retire(session);
      return this.stale(epoch) || this.queued ? this.dropped(epoch) : 'closed';
    }
    this.deps.attach(session, w.req);
    return 'attached';
  }

  private stale(epoch: number): boolean {
    return epoch !== this.epoch || this.isClosed || this.isSuspended;
  }

  /** Why a start that is no longer wanted was dropped. */
  private dropped(epoch: number): OpenOutcome {
    if (this.isClosed) return 'closed';
    if (this.isSuspended) return 'deferred';
    return this.queued || epoch !== this.epoch ? 'superseded' : 'closed';
  }
}

function delay(ms: number): Promise<void> {
  return new Promise((resolve) => setTimeout(resolve, ms));
}
