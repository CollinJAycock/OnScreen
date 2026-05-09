// Sleep timer store. The watch page (and eventually the global audio
// player) subscribes for the countdown badge and registers a `fire`
// callback that runs when the timer expires. Two timer flavours:
//
//   - duration modes (15m / 30m / 45m / 60m) tick down every second
//     and fire `onFire` when the remaining time hits zero
//   - episode mode has no countdown; the consumer reads the active
//     mode and short-circuits "auto-next-episode" navigation so the
//     stream stops cleanly when the current item ends
//
// Cancel on tab close / route change is the consumer's responsibility
// — call cancelSleepTimer() in onDestroy when the player tears down.
// The store deliberately keeps no global tab-close hook because the
// timer should follow the player, not the page.

import { writable, derived, type Readable } from 'svelte/store';

export type SleepTimerMode = 'off' | '15m' | '30m' | '45m' | '60m' | 'episode';

interface SleepTimerState {
  mode: SleepTimerMode;
  remainingMs: number; // 0 when mode === 'off' or mode === 'episode'
}

const initial: SleepTimerState = { mode: 'off', remainingMs: 0 };

const internal = writable<SleepTimerState>(initial);

export const sleepTimer: Readable<SleepTimerState> = { subscribe: internal.subscribe };

let intervalId: ReturnType<typeof setInterval> | null = null;
let fireCallback: (() => void) | null = null;

/**
 * Convert a duration-mode key to its millisecond value. Episode mode
 * returns 0 because it doesn't drive a countdown.
 */
function durationMs(mode: SleepTimerMode): number {
  switch (mode) {
    case '15m': return 15 * 60 * 1000;
    case '30m': return 30 * 60 * 1000;
    case '45m': return 45 * 60 * 1000;
    case '60m': return 60 * 60 * 1000;
    default: return 0;
  }
}

/**
 * Arm the timer. `onFire` runs when a duration timer expires; episode
 * mode never fires it (consumer reads `mode === 'episode'` directly to
 * decide whether to auto-advance).
 */
export function startSleepTimer(mode: SleepTimerMode, onFire: () => void): void {
  cancelSleepTimer();
  if (mode === 'off') return;

  fireCallback = onFire;

  if (mode === 'episode') {
    internal.set({ mode, remainingMs: 0 });
    return;
  }

  const total = durationMs(mode);
  const startedAt = Date.now();
  internal.set({ mode, remainingMs: total });

  intervalId = setInterval(() => {
    const elapsed = Date.now() - startedAt;
    const remaining = Math.max(0, total - elapsed);
    if (remaining === 0) {
      const cb = fireCallback;
      cancelSleepTimer();
      cb?.();
      return;
    }
    internal.set({ mode, remainingMs: remaining });
  }, 1000);
}

export function cancelSleepTimer(): void {
  if (intervalId !== null) {
    clearInterval(intervalId);
    intervalId = null;
  }
  fireCallback = null;
  internal.set(initial);
}

/** Format a remaining-ms value as `Mm:Ss` (e.g. "12:03"). */
export function formatRemaining(ms: number): string {
  const totalSec = Math.ceil(ms / 1000);
  const m = Math.floor(totalSec / 60);
  const s = totalSec % 60;
  return `${m}:${s.toString().padStart(2, '0')}`;
}

/** Convenience derived store for the active flag — `true` for any mode != 'off'. */
export const sleepTimerActive: Readable<boolean> = derived(
  sleepTimer,
  ($s) => $s.mode !== 'off'
);
