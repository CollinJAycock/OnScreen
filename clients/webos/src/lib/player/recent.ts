// A short-lived memo of the lookups the player repeats for every track of an
// album. Each track remounts the player (/watch/{id}), and every mount asked
// again for the album's listing (the previous / next track, "Track N of M"),
// the album (its title and cover) and its artist: the same answers for the
// whole album, three requests per track. A failed lookup isn't kept, entries
// go after a few minutes, and the caller keys them by account (a restricted
// profile mustn't be handed a listing fetched for another).

import type { ChildItem, ItemDetail } from '../api';

export const RECENT_TTL_MS = 5 * 60_000;
export const RECENT_MAX = 16;

export class RecentRequests<T> {
  private readonly entries = new Map<string, { at: number; value: Promise<T> }>();

  constructor(
    private readonly ttlMs = RECENT_TTL_MS,
    private readonly max = RECENT_MAX,
    private readonly now: () => number = () => Date.now(),
  ) {}

  /** `load()`'s answer for `key`, shared with a call made in the last
   *  `ttlMs` (one in flight too). */
  get(key: string, load: () => Promise<T>): Promise<T> {
    const t = this.now();
    const hit = this.entries.get(key);
    if (hit && t - hit.at < this.ttlMs) return hit.value;
    const value = load();
    this.entries.delete(key);
    this.entries.set(key, { at: t, value });
    value.catch(() => {
      if (this.entries.get(key)?.value === value) this.entries.delete(key);
    });
    while (this.entries.size > this.max) {
      const oldest = this.entries.keys().next().value;
      if (oldest === undefined) break;
      this.entries.delete(oldest);
    }
    return value;
  }
}

/** An album's (a book's) listing and its parent items, shared by its
 *  tracks' players. */
export const recentChildren = new RecentRequests<ChildItem[]>();
export const recentItems = new RecentRequests<ItemDetail>();
