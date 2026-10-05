// A pointer and a wheel, for the focus manager (shared with the webOS app,
// where LG's checklist makes the Magic Remote's both mandatory). Samsung's
// Smart Remote has neither, but a mouse on the TV, the emulator's and a
// desktop browser's do: a hover is a focus, a notch is a ↑/↓ step. The app has one selection effect, the
// focus ring, so the pointer moves the ring (a hover is a focus) and the
// wheel moves it a step per notch, as ↑/↓ would, which scrolls the page,
// the grid's paging and every row the way the D-pad does. Pure (the clock
// is passed in) for the tests; the manager wires it to the DOM.

import { writable } from 'svelte/store';
import type { RemoteKey } from './keys';

/** Whether the pointer is on screen: webOS's cursorStateChange, a real
 *  pointer move (true) and a D-pad press (false). The player shows its
 *  click targets (play / pause, seek) only while it is. */
export const pointerShown = writable(false);

/**
 * One OK in pointer mode can reach the app twice: as the click and as an
 * Enter keydown, in either order. Over something clickable the focus
 * manager leaves it to the click and holds the Enter back; these windows
 * catch the rest: an Enter this soon after a click that acted, and a click
 * while an Enter that acted is down or this soon after (the player's own
 * buttons use the same window).
 */
export const POINTER_ECHO_MS = 400;

/** How long an Enter that acted counts as still held when its keyup never
 *  comes (the app went to the background mid-press): a long press is 600
 *  ms, and a click while the press is down is its own release. */
export const ENTER_HELD_MAX_MS = 3000;

export class PointerEcho {
  private clickAt = -Infinity;
  private enterAt = -Infinity;
  // An Enter that acted is down: the click its release sends is on its way
  // (a hold, a long press, may outlast POINTER_ECHO_MS).
  private enterDown = false;

  /** A pointer click at `at` was let through. */
  clicked(at: number): void {
    this.clickAt = at;
  }

  /** A fresh Enter keydown at `at` was let through. */
  entered(at: number): void {
    this.enterAt = at;
    this.enterDown = true;
  }

  /** An Enter keyup at `at`. Ends the press that acted (an echo's keyup
   *  changes nothing): its click may come just after. */
  released(at: number): void {
    if (!this.enterDown) return;
    this.enterDown = false;
    this.enterAt = at;
  }

  /** An Enter keydown now repeats a click that already acted. */
  enterIsEcho(at: number): boolean {
    return at - this.clickAt < POINTER_ECHO_MS && at >= this.clickAt;
  }

  /** A pointer click now repeats an Enter that already acted: while that
   *  press is down, or just after it. */
  clickIsEcho(at: number): boolean {
    if (at < this.enterAt) return false;
    return at - this.enterAt < (this.enterDown ? ENTER_HELD_MAX_MS : POINTER_ECHO_MS);
  }
}

/**
 * A real pointer move, not the mousemove Chromium sends when the page
 * scrolls under a pointer that stayed put (a D-pad or wheel scroll would
 * otherwise hand the focus to whatever slid under the cursor).
 */
export class PointerTrack {
  private x = NaN;
  private y = NaN;

  moved(x: number, y: number): boolean {
    if (x === this.x && y === this.y) return false;
    this.x = x;
    this.y = y;
    return true;
  }

  /** Where the pointer last was; null before it ever moved. */
  at(): { x: number; y: number } | null {
    return Number.isNaN(this.x) ? null : { x: this.x, y: this.y };
  }
}

/** Shortest time between two wheel steps. A notch is one event on the
 *  Magic Remote, but a spun wheel (or a desktop's smooth wheel) sends
 *  bursts, which would race down a page faster than it can draw. */
export const WHEEL_STEP_GAP_MS = 90;

/** Wheel deltas in px: line mode counts a line as 40, page mode a page as
 *  the panel's height. */
function px(delta: number, mode: number): number {
  return mode === 1 ? delta * 40 : mode === 2 ? delta * 1080 : delta;
}

export class WheelSteps {
  private lastAt = -Infinity;

  /** The D-pad press a wheel event at `at` stands for, or null (no
   *  movement, or too soon after the last step). Down / right for a
   *  positive delta: the list moves the way the wheel turns. */
  step(deltaX: number, deltaY: number, deltaMode: number, at: number): RemoteKey | null {
    const dx = px(deltaX || 0, deltaMode);
    const dy = px(deltaY || 0, deltaMode);
    if (dx === 0 && dy === 0) return null;
    if (at - this.lastAt < WHEEL_STEP_GAP_MS && at >= this.lastAt) return null;
    this.lastAt = at;
    if (Math.abs(dx) > Math.abs(dy)) return dx > 0 ? 'right' : 'left';
    return dy > 0 ? 'down' : 'up';
  }
}

/** The bits of a scroll box the wheel check reads. */
export interface ScrollBox {
  overflowY: string;
  scrollTop: number;
  scrollHeight: number;
  clientHeight: number;
  /** It holds something the focus ring can go to. */
  hasFocusables: boolean;
}

/**
 * Whether a vertical wheel over this box should scroll it natively instead
 * of moving focus: a box that scrolls on its own (overflow auto / scroll),
 * has room that way, and holds nothing focusable (a long text, which the
 * focus ring can't walk through). A box of focusables scrolls by its focus.
 */
export function scrollsNatively(box: ScrollBox, deltaY: number): boolean {
  if (box.hasFocusables || deltaY === 0) return false;
  if (box.overflowY !== 'auto' && box.overflowY !== 'scroll') return false;
  if (box.scrollHeight <= box.clientHeight) return false;
  return deltaY > 0 ? box.scrollTop + box.clientHeight < box.scrollHeight - 1 : box.scrollTop > 0;
}
