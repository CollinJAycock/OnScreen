// The controller through the Gamepad API, as key events.
//
// On a console, Xbox hands the web view the controller as key events with
// Windows' gamepad key codes (lib/focus/keys GAMEPAD); a PC's browser (how
// the app is tested before it reaches a console) only offers the Gamepad
// API. This polls that API and dispatches the same buttons as keydown /
// keyup events named after them ('GamepadA', 'GamepadDPadUp', ...), so the
// focus manager sees one kind of input either way, auto-repeat and holds
// included (a held A is the long press, as a held OK is on a TV remote).
//
// Both at once would act twice, so the first real gamepad key event switches
// polling off for good. A press waits DEDUPE_MS before it is sent, long
// enough for the console's own key event to arrive first and cancel it; the
// delay only costs a PC a frame or two.

import { GAMEPAD, isGamepadKeyCode } from './focus/keys';
import { postToShell } from './shell';

/** A press is held back this long so a native key event can claim it. */
export const DEDUPE_MS = 60;
/** Auto-repeat, as a keyboard's: the first repeat after a beat, then fast. */
export const REPEAT_DELAY_MS = 400;
export const REPEAT_EVERY_MS = 100;
/** How far the left stick must lean to count as a D-pad press. */
export const STICK_THRESHOLD = 0.6;

/** The Standard Gamepad layout's button indices, by GAMEPAD name. */
const STANDARD_BUTTONS: Record<string, number> = {
  GamepadA: 0,
  GamepadB: 1,
  GamepadX: 2,
  GamepadY: 3,
  GamepadLeftShoulder: 4,
  GamepadRightShoulder: 5,
  GamepadLeftTrigger: 6,
  GamepadRightTrigger: 7,
  GamepadView: 8,
  GamepadMenu: 9,
  GamepadDPadUp: 12,
  GamepadDPadDown: 13,
  GamepadDPadLeft: 14,
  GamepadDPadRight: 15,
};

/** What one poll saw: which named buttons are down. */
export type PadState = ReadonlySet<string>;

/** The named buttons down in a Standard Gamepad (left stick as the D-pad). */
export function padState(buttons: ReadonlyArray<{ pressed: boolean }>, axes: ReadonlyArray<number>): PadState {
  const down = new Set<string>();
  for (const [name, i] of Object.entries(STANDARD_BUTTONS)) if (buttons[i]?.pressed) down.add(name);
  const x = axes[0] ?? 0;
  const y = axes[1] ?? 0;
  if (y <= -STICK_THRESHOLD) down.add('GamepadLeftThumbstickUp');
  if (y >= STICK_THRESHOLD) down.add('GamepadLeftThumbstickDown');
  if (x <= -STICK_THRESHOLD) down.add('GamepadLeftThumbstickLeft');
  if (x >= STICK_THRESHOLD) down.add('GamepadLeftThumbstickRight');
  return down;
}

export interface PadEvent {
  type: 'keydown' | 'keyup';
  key: string;
  repeat: boolean;
}

interface Held {
  since: number;
  /** When the next keydown (the first, then each repeat) is due. */
  next: number;
  sent: boolean;
}

/**
 * Turns successive pad states into key events. Pure: the caller supplies
 * the time and dispatches what update() returns.
 */
export class PadKeys {
  private held = new Map<string, Held>();
  private stopped = false;

  /** A real gamepad key event arrived: the platform delivers the controller
   *  itself. Drops held-back presses and ignores every later poll. */
  stop(): void {
    this.stopped = true;
    this.held.clear();
  }

  get isStopped(): boolean {
    return this.stopped;
  }

  update(state: PadState, now: number): PadEvent[] {
    if (this.stopped) return [];
    const out: PadEvent[] = [];
    for (const [key, h] of this.held) {
      if (state.has(key)) continue;
      if (h.sent) out.push({ type: 'keyup', key, repeat: false });
      this.held.delete(key);
    }
    for (const key of state) {
      if (!(key in GAMEPAD) || !GAMEPAD[key].key) continue;
      let h = this.held.get(key);
      if (!h) {
        h = { since: now, next: now + DEDUPE_MS, sent: false };
        this.held.set(key, h);
      }
      if (now < h.next) continue;
      out.push({ type: 'keydown', key, repeat: h.sent });
      h.next = h.sent ? now + REPEAT_EVERY_MS : h.since + DEDUPE_MS + REPEAT_DELAY_MS;
      h.sent = true;
    }
    return out;
  }
}

// ── Runtime ─────────────────────────────────────────────────────────────────

let started = false;

/** Start polling once a controller is seen; idempotent. */
export function startGamepadKeys(): void {
  if (started || typeof window === 'undefined') return;
  started = true;
  const keys = new PadKeys();
  const ours = new WeakSet<Event>();
  let frame = 0;

  // The platform's own gamepad key event: stop polling for good, and tell
  // the shell, which then stops forwarding Back requests as "back"
  // (lib/shell). Listened for even without a Gamepad API.
  window.addEventListener(
    'keydown',
    (e) => {
      if (!ours.has(e) && isGamepadKeyCode(e.keyCode) && !keys.isStopped) {
        keys.stop();
        postToShell('nativeKeys');
        if (frame) cancelAnimationFrame(frame);
        frame = 0;
      }
    },
    true,
  );
  if (typeof navigator.getGamepads !== 'function') return;

  const poll = () => {
    frame = 0;
    if (keys.isStopped) return;
    let pad: Gamepad | null = null;
    for (const g of navigator.getGamepads()) if (g && g.connected) { pad = g; break; }
    if (!pad) return; // asleep until the next gamepadconnected
    if (!document.hidden) {
      for (const ev of keys.update(padState(pad.buttons, pad.axes), performance.now())) {
        const e = new KeyboardEvent(ev.type, { key: ev.key, repeat: ev.repeat, bubbles: true, cancelable: true });
        ours.add(e);
        (document.activeElement ?? document.body).dispatchEvent(e);
      }
    }
    frame = requestAnimationFrame(poll);
  };
  const wake = () => {
    if (!frame && !keys.isStopped) frame = requestAnimationFrame(poll);
  };
  window.addEventListener('gamepadconnected', wake);
  wake();
}
