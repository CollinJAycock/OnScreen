// Polling helpers for the admin "Refresh metadata" and "Fix Match" actions
// on the detail page. Both endpoints (POST /items/{id}/enrich and
// POST /items/{id}/match) return 204 at once and do the provider work in
// the background, so the page polls GET /items/{id} until the row changes
// and then re-renders in place — no full page reload, no fixed delay.

/** Default cadence: 12 polls × 2 s = a 24 s window. */
export const POLL_INTERVAL_MS = 2000;
export const POLL_ATTEMPTS = 12;

export interface PollOptions {
  intervalMs?: number;
  attempts?: number;
  /** Injected for tests; defaults to setTimeout. */
  sleep?: (ms: number) => Promise<void>;
  /** Stop early (e.g. the user navigated to another item). */
  cancelled?: () => boolean;
}

const defaultSleep = (ms: number) => new Promise<void>((r) => setTimeout(r, ms));

/**
 * Poll `fetchItem` until `changed(fresh)` is true. Returns the fresh item,
 * or null when the window ends (or is cancelled) without a change. A fetch
 * error propagates to the caller, which shows it as an error toast.
 */
export async function pollForItemChange<T>(
  fetchItem: () => Promise<T>,
  changed: (fresh: T) => boolean,
  opts: PollOptions = {},
): Promise<T | null> {
  const attempts = opts.attempts ?? POLL_ATTEMPTS;
  const intervalMs = opts.intervalMs ?? POLL_INTERVAL_MS;
  const sleep = opts.sleep ?? defaultSleep;
  for (let i = 0; i < attempts; i++) {
    await sleep(intervalMs);
    if (opts.cancelled?.()) return null;
    const fresh = await fetchItem();
    if (opts.cancelled?.()) return null;
    if (changed(fresh)) return fresh;
  }
  return null;
}

/** An item's `updated_at`: Unix ms on GET /items/{id}, ISO text elsewhere. */
export type Stamp = number | string | undefined | null;

function ts(v: Stamp): number {
  if (v === undefined || v === null || v === '') return NaN;
  return typeof v === 'number' ? v : Date.parse(v);
}

/**
 * True when `fresh.updated_at` is later than `startUpdated`. Compares times
 * rather than strings, so a change in fractional-second precision
 * ("…:05Z" vs "…:05.123Z") can't fake or hide an update. Unparseable
 * timestamps fall back to a plain inequality check.
 */
export function updatedSince(startUpdated: Stamp) {
  return (fresh: { updated_at?: Stamp }): boolean => {
    const a = ts(startUpdated);
    const b = ts(fresh.updated_at);
    if (Number.isNaN(a) || Number.isNaN(b)) {
      const u = fresh.updated_at;
      return u !== undefined && u !== null && u !== '' && u !== startUpdated;
    }
    return b > a;
  };
}

/**
 * Fix Match is done when the row carries the chosen TMDB id and has been
 * rewritten since the match was applied. Re-picking the item's current
 * match still waits for the rewrite, so the page shows the refreshed data.
 */
export function matchApplied(tmdbId: number, startUpdated: Stamp) {
  const moved = updatedSince(startUpdated);
  return (fresh: { tmdb_id?: number | null; updated_at?: Stamp }): boolean =>
    fresh.tmdb_id === tmdbId && moved(fresh);
}

/** Toast copy for the end of a refresh / match poll. */
export const REFRESH_UPDATED_MSG = 'Metadata refreshed';
export const REFRESH_NO_CHANGE_MSG =
  'No changes found. The providers returned nothing new for this item.';
export const MATCH_PENDING_MSG =
  'Match saved. The new metadata is still loading; reopen the page in a moment to see it.';
export function matchAppliedMsg(title: string | undefined): string {
  return title ? `Matched to “${title}”` : 'Match applied';
}
