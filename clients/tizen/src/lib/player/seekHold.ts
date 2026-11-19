// A held ←/→ (◀◀/▶▶) scrubs. A tap still moves the pending scrub target one
// step (10 s, or 30 s for ◀◀/▶▶) and the burst lands after the usual idle
// (PendingScrub); a hold moves it on for as long as the key is down, faster
// the longer it is held, and its release plays from there (the page flushes
// the scrub on the keyup). Before, every auto-repeat was a 10 s tap: one
// flat ~90 s a second on the Q80B, landing only 0.7 s after the release.
//
// The Samsung remote flags none of its auto-repeats (e.repeat is false on
// every one; first ~350 ms after the press, then every ~108 ms), but it sends
// keyups, so a keydown with no keyup since the last one of the same key is
// the hold going on (the rule KeyHold uses for OK, focus/hold.ts). A hold's
// step is its speed times the time since the last keydown, so the scrub
// runs at the same pace whatever the remote's repeat rate.
//
// Pure (the clock is passed in) for the tests.

import { COLD_HOLD_GAP_MS, HOLD_STALE_MS } from '../focus/hold';

/** Scrub speed while a seek key is held, by how long it has been held:
 *  content ms per ms held. 45 s in the first 1.5 s, 3 min by 3 s, 9 min by
 *  4.5 s, then 10 min a second (the middle of a 2-hour film ~10 s from its
 *  start). Let go and tap to fine-tune. */
export const HOLD_SPEEDS: ReadonlyArray<{ fromMs: number; rate: number }> = [
  { fromMs: 0, rate: 30 },
  { fromMs: 1500, rate: 90 },
  { fromMs: 3000, rate: 240 },
  { fromMs: 4500, rate: 600 },
];

/** The most time one repeat counts for: the first repeat's delay (~350 ms
 *  on Samsung, ~500 ms on webOS) and no more, so a page busy rendering
 *  can't turn one late repeat into a leap. */
export const HOLD_MAX_STEP_GAP_MS = 500;

/** The scrub speed `heldMs` into a hold. */
export function holdRate(heldMs: number): number {
  let rate = HOLD_SPEEDS[0].rate;
  for (const s of HOLD_SPEEDS) if (heldMs >= s.fromMs) rate = s.rate;
  return rate;
}

/** The content ms a hold covers from `fromMs` to `toMs` into it: the
 *  speeds integrated, so a repeat that spans a change of speed counts each
 *  part at its own. */
export function holdDistance(fromMs: number, toMs: number): number {
  let d = 0;
  for (let i = 0; i < HOLD_SPEEDS.length; i++) {
    const next = i + 1 < HOLD_SPEEDS.length ? HOLD_SPEEDS[i + 1].fromMs : Infinity;
    const lo = Math.max(fromMs, HOLD_SPEEDS[i].fromMs);
    const hi = Math.min(toMs, next);
    if (hi > lo) d += (hi - lo) * HOLD_SPEEDS[i].rate;
  }
  return d;
}

export class SeekHold {
  private key: string | null = null;
  // A keydown of `key` with no keyup since.
  private down_ = false;
  private sawKeyUp = false;
  // A repeat has come: the release is a hold's.
  private holding = false;
  private startAt = 0;
  private lastAt = -Infinity;

  /**
   * A seek key's keydown at `at` (the event's timeStamp): how far to move
   * the target, signed. `tapMs` is a tap's step (-10 000 for ←, and so on);
   * `repeat` is the event's own flag, for remotes that set it.
   */
  down(key: string, tapMs: number, repeat: boolean, at: number): number {
    const gap = at - this.lastAt;
    this.lastAt = at;
    const same = key === this.key;
    // Before the first keyup the remote may send none at all: only the gap
    // tells a repeat from a second press then (as KeyHold).
    const continues =
      same && (repeat || (this.down_ && gap < (this.sawKeyUp ? HOLD_STALE_MS : COLD_HOLD_GAP_MS)));
    this.key = key;
    this.down_ = true;
    if (!continues) {
      this.holding = false;
      this.startAt = at;
      return tapMs;
    }
    this.holding = true;
    const held = at - this.startAt;
    const step = holdDistance(held - Math.min(gap, HOLD_MAX_STEP_GAP_MS), held);
    return Math.sign(tapMs) * Math.round(step);
  }

  /** A keyup. True when it ends a hold of the seek key being held: the
   *  scrub lands now. A tap's keyup leaves the burst to its idle timer. */
  up(key: string): boolean {
    if (key !== this.key) return false;
    this.sawKeyUp = true;
    this.down_ = false;
    const wasHold = this.holding;
    this.holding = false;
    return wasHold;
  }

  /** Forget the key (the player is going away, a picker took the keys). */
  reset(): void {
    this.key = null;
    this.down_ = false;
    this.holding = false;
    this.lastAt = -Infinity;
  }
}
