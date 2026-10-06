// The player's pending scrub target. ←/→/◀◀/▶▶ presses add up into one
// content-time target that the bar and the trickplay preview show, and it
// commits SCRUB_COMMIT_MS after the last press (or at once on OK). A held
// key or a fast burst is therefore one seek, and at most one session
// re-issue, instead of one per press; Android confirms a scrub before
// seeking for the same reason. Back drops the target.
//
// A target that can't land yet (the stream is still starting, or a new
// session is being swapped in) waits: flush() commits it once the player
// says it's ready. Kept out of the Svelte component so the timing is
// checkable with fake timers; the page mirrors the target into $state
// through `changed`.

import { SCRUB_COMMIT_MS, clampToItem, nudgeScrub } from './session';

export interface PendingScrubOptions {
  /** Seek to the committed target (content time). `relative` is true when
   *  the target was built by key presses (nudge) rather than parked from an
   *  absolute jump (a chapter, Skip Intro): a relative one may clamp to a
   *  growing edge instead of re-issuing (see classifySeek). */
  commit: (targetMs: number, relative: boolean) => void;
  /** Whether a seek can land now. While false, a due target stays pending. */
  ready: () => boolean;
  /** The pending target changed (null = none pending). */
  changed: (targetMs: number | null) => void;
  /** Idle time after the last press before the target commits. */
  delayMs?: number;
}

export class PendingScrub {
  private timer: ReturnType<typeof setTimeout> | null = null;
  private target: number | null = null;
  private relative = false;
  private readonly opts: PendingScrubOptions;

  constructor(opts: PendingScrubOptions) {
    this.opts = opts;
  }

  /** The pending target, or null. */
  get value(): number | null {
    return this.target;
  }

  /** Whether presses are still arriving (the idle timer is running). */
  get counting(): boolean {
    return this.timer !== null;
  }

  /** One press: move the target by `deltaMs` from where it is (or from the
   *  playhead when none is pending) and restart the idle timer. */
  nudge(deltaMs: number, currentMs: number, durationMs: number): void {
    // Keys moving a parked absolute target keep it absolute.
    if (this.target === null) this.relative = true;
    this.set(nudgeScrub(this.target, currentMs, deltaMs, durationMs));
    this.clearTimer();
    this.timer = setTimeout(() => {
      this.timer = null;
      this.flush();
    }, this.opts.delayMs ?? SCRUB_COMMIT_MS);
  }

  /** Hold a target that arrived while the player couldn't seek (a chapter
   *  jump during a session swap, say). No timer: flush() lands it. Absolute
   *  unless `relative` says it came from the keys. */
  park(targetMs: number, durationMs: number, relative = false): void {
    this.clearTimer();
    this.relative = relative;
    this.set(clampToItem(targetMs, durationMs));
  }

  /** Commit the target now if the player can take it; otherwise it stays
   *  pending for the next flush(). */
  flush(): void {
    this.clearTimer();
    const t = this.target;
    if (t === null || !this.opts.ready()) return;
    const relative = this.relative;
    this.set(null);
    this.opts.commit(t, relative);
  }

  /** Drop the target without seeking. */
  cancel(): void {
    this.clearTimer();
    this.set(null);
  }

  private set(t: number | null): void {
    if (t === this.target) return;
    this.target = t;
    this.opts.changed(t);
  }

  private clearTimer(): void {
    if (this.timer) {
      clearTimeout(this.timer);
      this.timer = null;
    }
  }
}
