// Sleep timer store. The watch page and the global audio player each
// subscribe for the countdown badge and register a `fire` callback that
// runs when the timer expires. Three timer flavours:
//
//   - duration modes (15m / 30m / 45m / 60m) tick down every second
//     and fire `onFire` when the remaining time hits zero
//   - episode mode has no countdown; the consumer reads the active
//     mode and short-circuits "auto-next-episode" navigation so the
//     stream stops cleanly when the current item ends
//   - chapter mode (audio player) has no countdown either; the player
//     pauses itself at the end of the chapter it's in
//
// The module-level sleepTimer / startSleepTimer / cancelSleepTimer are
// the watch page's timer. The audio player keeps its own from
// createSleepTimer(), so a video's timer and an audiobook's never cancel
// each other.
//
// Cancel on tab close / route change is the consumer's responsibility
// — call cancel in onDestroy when the player tears down. The store
// deliberately keeps no global tab-close hook because the timer should
// follow the player, not the page.

import { writable, derived, type Readable } from 'svelte/store';

export type SleepTimerMode = 'off' | '15m' | '30m' | '45m' | '60m' | 'episode' | 'chapter';

export interface SleepTimerState {
  mode: SleepTimerMode;
  remainingMs: number; // 0 when mode is 'off', 'episode' or 'chapter'
}

export interface SleepTimer extends Readable<SleepTimerState> {
  /**
   * Arm the timer. `onFire` runs when a duration timer expires; episode
   * and chapter modes never fire it (the consumer reads the mode and
   * stops at the boundary itself).
   */
  start(mode: SleepTimerMode, onFire: () => void): void;
  cancel(): void;
}

const initial: SleepTimerState = { mode: 'off', remainingMs: 0 };

/**
 * Convert a duration-mode key to its millisecond value. Episode and
 * chapter modes return 0 because they don't drive a countdown.
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

/** A sleep timer of its own, independent of every other. */
export function createSleepTimer(): SleepTimer {
  const internal = writable<SleepTimerState>(initial);
  let intervalId: ReturnType<typeof setInterval> | null = null;
  let fireCallback: (() => void) | null = null;

  function cancel(): void {
    if (intervalId !== null) {
      clearInterval(intervalId);
      intervalId = null;
    }
    fireCallback = null;
    internal.set(initial);
  }

  function start(mode: SleepTimerMode, onFire: () => void): void {
    cancel();
    if (mode === 'off') return;

    fireCallback = onFire;

    const total = durationMs(mode);
    if (total === 0) {
      internal.set({ mode, remainingMs: 0 });
      return;
    }

    const startedAt = Date.now();
    internal.set({ mode, remainingMs: total });

    intervalId = setInterval(() => {
      const elapsed = Date.now() - startedAt;
      const remaining = Math.max(0, total - elapsed);
      if (remaining === 0) {
        const cb = fireCallback;
        cancel();
        cb?.();
        return;
      }
      internal.set({ mode, remainingMs: remaining });
    }, 1000);
  }

  return { subscribe: internal.subscribe, start, cancel };
}

const shared = createSleepTimer();

export const sleepTimer: Readable<SleepTimerState> = { subscribe: shared.subscribe };

/** Arm the watch page's timer (see SleepTimer.start). */
export function startSleepTimer(mode: SleepTimerMode, onFire: () => void): void {
  shared.start(mode, onFire);
}

export function cancelSleepTimer(): void {
  shared.cancel();
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
