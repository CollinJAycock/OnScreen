// Batched progress reporter. Flushes `playing` events every 10s and
// `paused`/`stopped` events immediately. Safe to call stop() multiple times.

import { endpoints, ApiError } from '$lib/api';

const INTERVAL_MS = 10_000;

export class ProgressReporter {
  private timer: ReturnType<typeof setInterval> | null = null;
  private itemID: string;
  private lastSent = -1;

  constructor(itemID: string) {
    this.itemID = itemID;
  }

  // onBlocked fires when a 'playing' heartbeat is rejected with a parental
  // watch-limit 403 (a daily cap reached or the allowed-hours window closing
  // mid-session). The caller pauses playback and shows the block message.
  start(
    getState: () => { positionMs: number; durationMs: number },
    onBlocked?: (reason: string) => void
  ) {
    this.stop();
    this.timer = setInterval(() => {
      const s = getState();
      if (s.durationMs <= 0) return;
      if (Math.abs(s.positionMs - this.lastSent) < 1000) return;
      this.lastSent = s.positionMs;
      void endpoints.items.progress(this.itemID, s.positionMs, s.durationMs, 'playing').catch((e) => {
        if (onBlocked && e instanceof ApiError && e.code === 'PARENTAL_LIMIT') {
          onBlocked(e.message);
        }
      });
    }, INTERVAL_MS);
  }

  stop() {
    if (this.timer) {
      clearInterval(this.timer);
      this.timer = null;
    }
  }

  paused(positionMs: number, durationMs: number) {
    if (durationMs <= 0) return;
    void endpoints.items.progress(this.itemID, positionMs, durationMs, 'paused').catch(() => {});
  }

  stopped(positionMs: number, durationMs: number) {
    this.stop();
    if (durationMs <= 0) return;
    void endpoints.items.progress(this.itemID, positionMs, durationMs, 'stopped').catch(() => {});
  }
}
