// The Watchlist screen's logic (routes/watchlist): the request and response
// parsing for GET /watchlist and /watchlist/counts, row and card labels, and
// a paged row that stays in step with status changes made on the screen. No
// Svelte, no network: the fetch is injected. Mirrors the web app's
// lib/watchlist.ts. Kept identical between the webOS, Tizen and Xbox apps.

import type {
  WatchlistCounts,
  WatchlistItem,
  WatchlistQuery,
  WatchStatusValue,
} from './api/endpoints';
import { appendUnique, type PageResult } from './paging';

/** Cards a row asks for at a time (the server's default page). */
export const WATCHLIST_PAGE_SIZE = 50;

/** How close to a row's last loaded card focus gets before the next page
 *  is asked for: about two screens' worth of posters. */
export const WATCHLIST_LOOKAHEAD = 10;

/** Row order: Plan to Watch first, then the tracker progression. */
export const WATCHLIST_STATUSES: readonly WatchStatusValue[] = [
  'plan_to_watch',
  'watching',
  'on_hold',
  'completed',
  'dropped',
];

export const STATUS_LABELS: Record<WatchStatusValue, string> = {
  plan_to_watch: 'Plan to Watch',
  watching: 'Watching',
  on_hold: 'On Hold',
  completed: 'Completed',
  dropped: 'Dropped',
};

/** What the screen says with nothing on any list. */
export const WATCHLIST_EMPTY = "Nothing on your watchlist yet — use Status on a title's page to add it.";

/** What it says on a server from before the watchlist (v2.5 and older). */
export const WATCHLIST_MISSING = "This server doesn't have a watchlist yet. Update OnScreen to use it.";

function isStatus(v: unknown): v is WatchStatusValue {
  return typeof v === 'string' && (WATCHLIST_STATUSES as readonly string[]).includes(v);
}

// ── Requests and responses ─────────────────────────────────────────────

/** The GET /watchlist query string (no leading `?`). */
export function watchlistQueryString(q: WatchlistQuery = {}): string {
  const p: string[] = [`status=${encodeURIComponent(q.status ?? 'plan_to_watch')}`];
  if (q.type) p.push(`type=${encodeURIComponent(q.type)}`);
  if (q.sort && q.sort !== 'recent') p.push(`sort=${q.sort}`);
  p.push(`limit=${q.limit ?? WATCHLIST_PAGE_SIZE}`);
  p.push(`offset=${q.offset ?? 0}`);
  return p.join('&');
}

/** A GET /watchlist envelope as a page: the rows that are items (an id and
 *  a type at least) and meta.total, null when it isn't a number. */
export function watchlistPage(env: { data?: unknown; meta?: { total?: unknown } } | null | undefined): PageResult<WatchlistItem> {
  const data = Array.isArray(env?.data) ? (env!.data as unknown[]) : [];
  const items = data.filter(
    (it): it is WatchlistItem =>
      !!it && typeof it === 'object' &&
      typeof (it as WatchlistItem).id === 'string' && (it as WatchlistItem).id !== '' &&
      typeof (it as WatchlistItem).type === 'string',
  );
  const total = env?.meta?.total;
  return { items, total: typeof total === 'number' && Number.isFinite(total) ? total : null };
}

function count(v: unknown): number {
  return typeof v === 'number' && Number.isFinite(v) && v > 0 ? Math.floor(v) : 0;
}

/** GET /watchlist/counts (already unwrapped from `data`), with anything
 *  missing or malformed read as zero and unknown statuses dropped. */
export function parseWatchlistCounts(raw: unknown): WatchlistCounts {
  const r = (raw && typeof raw === 'object' ? raw : {}) as { total?: unknown; statuses?: unknown };
  const statuses: WatchlistCounts['statuses'] = {};
  const src = r.statuses && typeof r.statuses === 'object' ? (r.statuses as Record<string, unknown>) : {};
  for (const key of Object.keys(src)) {
    if (!isStatus(key)) continue;
    const s = (src[key] && typeof src[key] === 'object' ? src[key] : {}) as { total?: unknown; types?: unknown };
    const types: Record<string, number> = {};
    const t = s.types && typeof s.types === 'object' ? (s.types as Record<string, unknown>) : {};
    for (const type of Object.keys(t)) {
      const n = count(t[type]);
      if (n > 0) types[type] = n;
    }
    statuses[key] = { total: count(s.total), types };
  }
  return { total: count(r.total), statuses };
}

/** The statuses to show a row for: those the counts have something under,
 *  in row order. Every status when the counts are unknown (their request
 *  failed), the rows' own first pages then deciding which are empty. */
export function statusesToLoad(counts: WatchlistCounts | null): WatchStatusValue[] {
  if (!counts) return [...WATCHLIST_STATUSES];
  return WATCHLIST_STATUSES.filter((s) => (counts.statuses[s]?.total ?? 0) > 0);
}

// ── Labels ─────────────────────────────────────────────────────────────

/** A row's heading: "Plan to Watch · 12", the bare label when the count
 *  isn't known. */
export function rowTitle(status: WatchStatusValue, total: number | null): string {
  return total == null ? STATUS_LABELS[status] : `${STATUS_LABELS[status]} · ${total}`;
}

/** "S1 E2"; a missing season drops the S half, a missing or 0 episode the
 *  E half. */
export function episodeLabel(season?: number | null, episode?: number | null): string {
  const parts: string[] = [];
  if (season != null) parts.push(`S${season}`);
  if (episode != null && episode > 0) parts.push(`E${episode}`);
  return parts.join(' ');
}

/** A card's title and the line under it: "Show · S1 E2" under an episode,
 *  the show under a season, "Artist · Album" under a track, the author or
 *  artist and year under an album or book, else the year. */
export function watchlistCardText(item: WatchlistItem): { title: string; subtitle?: string } {
  const join = (parts: Array<string | number | null | undefined>) =>
    parts.filter((p) => p != null && p !== '').join(' · ') || undefined;
  switch (item.type) {
    case 'episode':
      return { title: item.title, subtitle: join([item.grandparent_title, episodeLabel(item.parent_index, item.index)]) };
    case 'season':
      return { title: item.title, subtitle: join([item.parent_title]) };
    case 'track':
      return { title: item.title, subtitle: join([item.grandparent_title, item.parent_title]) };
    case 'album':
    case 'audiobook':
    case 'book':
      return { title: item.title, subtitle: join([item.parent_title, item.year]) };
    default:
      return { title: item.title, subtitle: item.year ? String(item.year) : undefined };
  }
}

/** The Hold OK menu's moves for a card: every other status, in row order. */
export function moveTargets(current: WatchStatusValue): WatchStatusValue[] {
  return WATCHLIST_STATUSES.filter((s) => s !== current);
}

// ── A row ──────────────────────────────────────────────────────────────

export type FetchWatchlistPage = (offset: number, limit: number) => Promise<PageResult<WatchlistItem>>;

/**
 * One status's row: its loaded cards and the server's total, a page at a
 * time. The offset is the number of cards loaded rather than a running
 * count of what was fetched, so a card moved off this row (or onto it) on
 * the screen leaves the next page lining up with the server's list, which
 * moved the same way. A page that lands after reset() is dropped.
 */
export class WatchlistRow {
  /** Loaded cards, without duplicates. A new array after every change. */
  items: WatchlistItem[] = [];
  /** meta.total from the last page, adjusted by moves; null until known. */
  total: number | null = null;
  /** Nothing more to fetch. */
  done = false;
  private inflight = false;
  private generation = 0;

  constructor(
    readonly status: WatchStatusValue,
    readonly limit = WATCHLIST_PAGE_SIZE,
  ) {}

  get loading(): boolean {
    return this.inflight;
  }

  /** Fetch the next page. True when it changed the row; false when it did
   *  nothing (loading, done) or was dropped by reset(). A failure rejects
   *  and leaves the row as it was, so the next call asks again. */
  async loadMore(fetchPage: FetchWatchlistPage): Promise<boolean> {
    if (this.inflight || this.done) return false;
    const gen = this.generation;
    this.inflight = true;
    let page: PageResult<WatchlistItem>;
    try {
      page = await fetchPage(this.items.length, this.limit);
    } catch (e) {
      if (gen === this.generation) this.inflight = false;
      throw e;
    }
    if (gen !== this.generation) return false;
    this.inflight = false;
    const merged = appendUnique(this.items, page.items, (i) => i.id);
    const grew = merged.length > this.items.length;
    this.items = merged;
    if (page.total != null) this.total = page.total;
    // A page of nothing new ends it too: its offset would only fetch the
    // same rows again.
    this.done =
      !grew ||
      page.items.length === 0 ||
      (this.total != null ? this.items.length >= this.total : page.items.length < this.limit);
    return true;
  }

  /** Drop everything loaded; a page in flight is dropped when it lands. */
  reset(): void {
    this.generation++;
    this.items = [];
    this.total = null;
    this.done = false;
    this.inflight = false;
  }

  /** A card left this row (moved to another status, or removed). Returns
   *  where it was, -1 when it wasn't loaded here. */
  remove(id: string): number {
    const at = this.items.findIndex((i) => i.id === id);
    if (at < 0) return -1;
    this.items = [...this.items.slice(0, at), ...this.items.slice(at + 1)];
    if (this.total != null) this.total = Math.max(0, this.total - 1);
    return at;
  }

  /** A card joined this row: first, as the server's newest-first order has
   *  it. A row whose total isn't known yet (its first page failed) keeps it
   *  unknown, and keeps asking for pages. */
  addFront(item: WatchlistItem): void {
    const had = this.items.some((i) => i.id === item.id);
    this.items = [item, ...this.items.filter((i) => i.id !== item.id)];
    if (this.total == null) return;
    if (!had) this.total++;
    if (this.items.length >= this.total) this.done = true;
  }

  /** A row for a status with nothing under it (a card about to move onto
   *  it): known empty, nothing to fetch. */
  static empty(status: WatchStatusValue, limit = WATCHLIST_PAGE_SIZE): WatchlistRow {
    const row = new WatchlistRow(status, limit);
    row.total = 0;
    row.done = true;
    return row;
  }
}

/** What the screen draws: the rows with cards, in row order, each with its
 *  heading. Rows that came back (or became) empty are left out. */
export function visibleRows(
  rows: readonly WatchlistRow[],
): Array<{ status: WatchStatusValue; title: string; items: WatchlistItem[] }> {
  return WATCHLIST_STATUSES.map((s) => rows.find((r) => r.status === s))
    .filter((r): r is WatchlistRow => !!r && r.items.length > 0)
    .map((r) => ({ status: r.status, title: rowTitle(r.status, r.total ?? r.items.length), items: r.items }));
}

/** The status a card's focus key ("<status>:<id>") names, null for any
 *  other key. */
export function statusOfFocusKey(key: string | null | undefined): WatchStatusValue | null {
  if (!key) return null;
  const s = key.slice(0, key.indexOf(':'));
  return isStatus(s) ? s : null;
}

/** The card as it reads after a move to `next`. */
export function withStatus(item: WatchlistItem, next: WatchStatusValue, now: Date = new Date()): WatchlistItem {
  return { ...item, status: next, status_updated_at: now.toISOString() };
}
