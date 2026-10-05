// Holding OK is one press. webOS keeps sending Enter keydowns while OK is
// held, and each used to activate whatever had focus: a pill's click
// navigates, the next page autofocuses a control in a microtask, and the
// next repeat clicked that one too (a held OK on the Settings pill reached
// Sign out and then its confirm). The focus manager asks this, for every
// Enter keydown, whether it starts a press or repeats the one being held,
// and drops the repeats before anything sees them (bar the few elements
// that ask for them: see repeatActivates).
//
// Pure (the clock is passed in) for the tests.

/**
 * Before the session's first Enter keyup, keydowns closer together than
 * this are one hold. Until then the app can't know whether the remote
 * sends keyups at all, so the gap is all it has to tell a repeat from a
 * press, and it has to clear the longest gap inside a hold: the initial
 * repeat delay. webOS's is 500 ms (the TV test logged the first repeat
 * 503-507 ms after the press, then one every ~51 ms), so 700 leaves 200 ms
 * for the repeat timer running late. It used to be 350, under that delay:
 * an unflagged first hold of a session took its first repeat for a second
 * press and went hub → 4K Movies → 12 Angry Men.
 *
 * What it costs: on a remote that never sends keyups, two deliberate OKs
 * less than 0.7 s apart count as one. A remote that does (the C1's MR21)
 * leaves this window with its first release (see KeyHold.up), so for it
 * only the session's very first press is judged by it.
 */
export const COLD_HOLD_GAP_MS = 700;

/**
 * Once the remote has shown it sends keyups, a keydown with no keyup since
 * the last one is the hold going on, unless this long has passed: then the
 * keyup was lost (the app went to the background mid-hold, say) and this
 * is a fresh press. Longer than the first repeat's delay (about 500 ms),
 * which is the longest gap inside a hold.
 */
export const HOLD_STALE_MS = 1000;

/**
 * Longest gap between two auto-repeats once a hold is repeating (they come
 * ~51 ms apart on webOS), with room to spare. The focus manager extends its
 * after-long-press swallow window by this much on every keydown it swallows.
 */
export const REPEAT_GAP_MS = 350;

/**
 * When a key event happened, in ms on the performance clock: the event's
 * own timeStamp, which Chromium stamps when the platform creates the event
 * (Chromium 49 on; webOS 6, this app's floor, runs 79), not when a busy main
 * thread gets round to handling it. Date.now() at handling time let a page
 * rendering between two repeats stretch a ~51 ms gap to 578 ms on the TV.
 * `fallback` (performance.now(), the same clock) only for an event with no
 * stamp.
 */
export function eventTime(e: { timeStamp: number }, fallback: () => number = () => performance.now()): number {
  return e.timeStamp > 0 ? e.timeStamp : fallback();
}

export class KeyHold {
  private held = false;
  private lastAt = -Infinity;
  private sawKeyUp = false;

  /**
   * An Enter keydown at `at` (eventTime): true when it starts a press,
   * false when it repeats the one being held (`repeat` is the event's flag,
   * which most remotes set on the auto-repeats).
   */
  down(repeat: boolean, at: number): boolean {
    const gap = at - this.lastAt;
    this.lastAt = at;
    if (repeat) return false;
    if (this.held && gap < (this.sawKeyUp ? HOLD_STALE_MS : COLD_HOLD_GAP_MS)) return false;
    this.held = true;
    return true;
  }

  /** The Enter keyup: the next keydown is a fresh press. */
  up(): void {
    this.held = false;
    this.sawKeyUp = true;
  }
}

/** The focused element, as far as an Enter repeat is concerned. */
export interface RepeatTarget {
  /** It opted in to repeats (focusable's repeatOk: data-repeat-ok). */
  repeatOk: boolean;
  /** It's what the hold's press activated. */
  pressed: boolean;
  /** It has a long press (focusable's onLongPress). */
  longPress: boolean;
}

/**
 * Whether a repeat of the held OK (KeyHold.down said false) activates the
 * focused element again rather than being dropped: only for an element that
 * asked for it (the on-screen keyboard's delete key, so a held OK deletes
 * on and on as Android TV's keyboards do), only while it's still the one
 * the press activated (a press that moved focus, or was a page's to
 * handle, doesn't hand its hold to whatever has focus now), and never for
 * one with a long press (there the hold is the long press).
 */
export function repeatActivates(t: RepeatTarget | null): boolean {
  return !!t && t.repeatOk && t.pressed && !t.longPress;
}
