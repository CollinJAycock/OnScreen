// Audiobook listening speed — pure helpers for the player's speed picker.
// Mirrors web/src/lib/audiobook.ts (presets, clamping, labels) and the
// Android TV client's AudiobookSpeed. The server stores one speed per user
// and book (GET / PUT /api/v1/items/{id}/playback-rate; a chapter resolves
// to its book). Music always plays at 1×. Kept identical between the Tizen
// and webOS apps.

/** Speeds the picker offers. The server takes any rate from MIN_RATE to MAX_RATE. */
export const RATE_PRESETS: readonly number[] = [0.75, 1, 1.25, 1.5, 1.75, 2, 2.5, 3];
export const MIN_RATE = 0.5;
export const MAX_RATE = 3;

/** Item types that get the speed control: a book and its chapter files. */
export function hasListeningSpeed(itemType: string | null | undefined): boolean {
  return itemType === 'audiobook' || itemType === 'audiobook_chapter';
}

/** A playable speed: 1 for anything that isn't a finite number, else
 *  clamped to [MIN_RATE, MAX_RATE] and kept to two decimals, as the server
 *  stores it. */
export function clampRate(rate: unknown): number {
  if (typeof rate !== 'number' || !Number.isFinite(rate)) return 1;
  return Math.round(Math.min(MAX_RATE, Math.max(MIN_RATE, rate)) * 100) / 100;
}

/** "1×", "1.25×", "0.75×". */
export function formatRate(rate: number): string {
  return `${Number(rate.toFixed(2))}×`;
}

/** The preset closest to a rate — where the picker's cursor starts when the
 *  book is at a speed set elsewhere between presets. */
export function presetIndex(rate: number): number {
  let best = 0;
  for (let i = 1; i < RATE_PRESETS.length; i++) {
    if (Math.abs(RATE_PRESETS[i] - rate) < Math.abs(RATE_PRESETS[best] - rate)) best = i;
  }
  return best;
}

export function sameRate(a: number, b: number): boolean {
  return Math.abs(a - b) < 0.005;
}

/**
 * Detects a media pipeline that accepts `playbackRate` but keeps playing at
 * normal speed (some TV platform players do this for some containers). The
 * player feeds it (wall clock, media position) samples while playing; after
 * two consecutive windows of ~3 s that both advanced at 1× although another
 * speed was asked for, it reports 'ignored' and the player falls back to
 * 1× with a note. Stalls and seeks can only make a window slower or jump, so
 * resetting on those and requiring "≈ exactly 1×" twice keeps false alarms
 * out.
 */
export class RateCheck {
  private startWall = -1;
  private startMedia = -1;
  private strikes = 0;
  private readonly windowMs: number;
  private readonly tolerance: number;

  constructor(windowMs = 3000, tolerance = 0.08) {
    this.windowMs = windowMs;
    this.tolerance = tolerance;
  }

  /** Start a fresh window (after play, seek, rate change or rebuffering). */
  reset(): void {
    this.startWall = -1;
    this.startMedia = -1;
  }

  /** Forget everything, including earlier strikes (new speed chosen). */
  clear(): void {
    this.reset();
    this.strikes = 0;
  }

  /** One sample while playing. 'pending' until a verdict. */
  sample(wallMs: number, mediaMs: number, requested: number): 'pending' | 'ok' | 'ignored' {
    if (Math.abs(requested - 1) < 0.2) {
      this.clear();
      return 'ok';
    }
    if (this.startWall < 0) {
      this.startWall = wallMs;
      this.startMedia = mediaMs;
      return 'pending';
    }
    const wall = wallMs - this.startWall;
    if (wall < this.windowMs) return 'pending';
    const media = mediaMs - this.startMedia;
    this.startWall = wallMs;
    this.startMedia = mediaMs;
    if (media <= 0) return 'pending'; // stalled or seeked back — no evidence
    const observed = media / wall;
    const atNormal = Math.abs(observed - 1) <= this.tolerance;
    const farFromAsked = Math.abs(observed - requested) >= 0.2;
    if (atNormal && farFromAsked) {
      this.strikes++;
      return this.strikes >= 2 ? 'ignored' : 'pending';
    }
    this.strikes = 0;
    return 'ok';
  }
}

/** Set a speed on an HTML media element with pitch kept where the engine
 *  allows. Returns false when the element refused it (threw, or reads back
 *  another value). defaultPlaybackRate is set too because a new `src`
 *  resets playbackRate to it. */
export function applyMediaRate(el: HTMLMediaElement, rate: number): boolean {
  const m = el as HTMLMediaElement & {
    preservesPitch?: boolean;
    webkitPreservesPitch?: boolean;
    mozPreservesPitch?: boolean;
  };
  try {
    m.preservesPitch = true;
    m.webkitPreservesPitch = true;
    m.mozPreservesPitch = true;
  } catch {
    /* older engines: pitch correction is their default anyway */
  }
  try {
    el.defaultPlaybackRate = rate;
    el.playbackRate = rate;
  } catch {
    return false;
  }
  return sameRate(el.playbackRate, rate);
}
