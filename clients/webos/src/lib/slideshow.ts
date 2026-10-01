// The photo viewer's slideshow, as Android's PhotoViewFragment has it: OK or
// Play/Pause toggles it, Play starts it, Pause or Stop stops it, and it moves
// on every 4 s. Pure (the step and the timers are injected), so the timing
// rules are unit-tested.

import type { RemoteKey } from './focus/keys';

/** How long each photo stays up (Android's SLIDESHOW_INTERVAL_MS). */
export const SLIDESHOW_INTERVAL_MS = 4000;
/** How long the one-time "Press OK to start a slideshow" hint stays up. */
export const SLIDESHOW_HINT_MS = 4000;

export type PhotoCommand = 'toggle' | 'start' | 'stop' | 'next' | 'prev';

/** What a remote key does in the photo viewer; null for keys the viewer
 *  leaves to the app (Back). OK toggles too: not every remote has a
 *  Play/Pause key, and the viewer has nothing else for OK to press. */
export function photoCommand(k: RemoteKey | null): PhotoCommand | null {
  switch (k) {
    case 'enter':
    case 'playpause':
      return 'toggle';
    case 'play':
      return 'start';
    case 'pause':
    case 'stop':
      return 'stop';
    case 'right':
    case 'forward':
      return 'next';
    case 'left':
    case 'rewind':
      return 'prev';
    default:
      return null;
  }
}

export interface SlideshowTimers {
  set(fn: () => void, ms: number): unknown;
  clear(handle: unknown): void;
}

const realTimers: SlideshowTimers = {
  set: (fn, ms) => setTimeout(fn, ms),
  clear: (h) => clearTimeout(h as ReturnType<typeof setTimeout>),
};

export class Slideshow {
  private on = false;
  private handle: unknown = null;

  /**
   * @param step      shows the next photo (wrapping round).
   * @param onChange  the slideshow started (true) or stopped (false): the
   *                  page's "▶ Slideshow" indicator.
   */
  constructor(
    private readonly step: () => void,
    private readonly onChange: (running: boolean) => void = () => {},
    private readonly timers: SlideshowTimers = realTimers,
    private readonly intervalMs = SLIDESHOW_INTERVAL_MS,
  ) {}

  get running(): boolean {
    return this.on;
  }

  /** Starts it, or restarts the wait when it is already running (Android's
   *  startSlideshow stops first). Needs two photos or more: a slideshow of
   *  one is nothing. False when it didn't start. */
  start(photoCount: number): boolean {
    if (photoCount < 2) return false;
    this.arm();
    if (!this.on) {
      this.on = true;
      this.onChange(true);
    }
    return true;
  }

  stop(): void {
    this.disarm();
    if (!this.on) return;
    this.on = false;
    this.onChange(false);
  }

  toggle(photoCount: number): void {
    if (this.on) this.stop();
    else this.start(photoCount);
  }

  /** The user moved with ←/→: the next automatic step waits a full
   *  interval from now, instead of jumping on a moment after the photo
   *  they picked appeared. */
  stepped(): void {
    if (this.on) this.arm();
  }

  private arm() {
    this.disarm();
    this.handle = this.timers.set(() => {
      this.handle = null;
      if (!this.on) return;
      this.step();
      this.arm();
    }, this.intervalMs);
  }

  private disarm() {
    if (this.handle !== null) this.timers.clear(this.handle);
    this.handle = null;
  }
}
