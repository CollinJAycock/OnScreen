// Pure helpers for the web client's watch-state UI: home rows (Next Up),
// the show / season Play button, library-grid badges + filter, and the
// optimistic "mark watched" updates. No Svelte, no network — everything
// here is unit-tested in watchState.test.ts.
import type { HubItem, MediaItem, UpNext, WatchFilter } from '$lib/api';

/** "S2 · E5". Missing halves are dropped ("E5", "S2"); both missing → "". */
export function episodeCode(season?: number | null, episode?: number | null): string {
  const parts: string[] = [];
  if (season != null) parts.push(`S${season}`);
  if (episode != null) parts.push(`E${episode}`);
  return parts.join(' · ');
}

/** Second line of a Next Up tile: "S2 · E5 — Episode title". */
export function nextUpSubtitle(item: Pick<HubItem, 'title' | 'season_number' | 'episode_number'>): string {
  const code = episodeCode(item.season_number, item.episode_number);
  return code ? `${code} — ${item.title}` : item.title;
}

/**
 * Label for the primary button on a show / season page, or null when
 * there's nothing to play (mode "none", or no episode in the response).
 *   resume  → "Resume S3 · E4"
 *   next    → "Play S3 · E5"
 *   start   → "Play S1 · E1"
 *   rewatch → "Watch again"
 */
export function upNextLabel(u: UpNext | null | undefined): string | null {
  if (!u || u.mode === 'none' || !u.episode) return null;
  const code = episodeCode(u.episode.season_number, u.episode.episode_number);
  switch (u.mode) {
    case 'resume':
      return code ? `Resume ${code}` : 'Resume';
    case 'next':
    case 'start':
      return code ? `Play ${code}` : 'Play';
    case 'rewatch':
      return 'Watch again';
    default:
      return null;
  }
}

/**
 * Which "Mark all …" buttons a show / season page offers, from its
 * up-next state: nothing watched yet → only "watched", everything
 * watched → only "unwatched", no episodes → neither. Unknown (the
 * up-next call failed) → both.
 */
export function markAllOptions(u: UpNext | null | undefined): { watched: boolean; unwatched: boolean } {
  switch (u?.mode) {
    case 'none':
      return { watched: false, unwatched: false };
    case 'start':
      return { watched: true, unwatched: false };
    case 'rewatch':
      return { watched: false, unwatched: true };
    default:
      return { watched: true, unwatched: true };
  }
}

export function progressPct(offsetMs?: number | null, durationMs?: number | null): number {
  if (!offsetMs || !durationMs || durationMs <= 0 || offsetMs <= 0) return 0;
  return Math.min(100, Math.max(0, (offsetMs / durationMs) * 100));
}

type WatchFields = Pick<
  MediaItem,
  'watch_state' | 'view_offset_ms' | 'duration_ms' | 'leaf_count' | 'unwatched_count'
>;

export interface CardWatchBadge {
  /** Fully watched: render the checkmark. */
  watched: boolean;
  /** Shows with episodes left: render the count badge. */
  unwatchedCount: number | null;
  /** In-progress movie / episode: 0–100 for the bar under the poster. */
  progressPct: number | null;
  /** Screen-reader text for whichever of the above is shown. */
  label: string;
}

/**
 * What watch indicator a poster card shows, or null for none. Only
 * derives from fields the server actually sent, so callers that list
 * items without watch state (older servers, music, photos) render
 * exactly as before.
 */
export function cardWatchBadge(item: WatchFields): CardWatchBadge | null {
  // Show-like: counts win over watch_state.
  if (item.unwatched_count != null) {
    const leaves = item.leaf_count;
    if (leaves === 0) return null;
    if (item.unwatched_count > 0) {
      const n = item.unwatched_count;
      return {
        watched: false,
        unwatchedCount: n,
        progressPct: null,
        label: `${n} unwatched episode${n === 1 ? '' : 's'}`,
      };
    }
    return { watched: true, unwatchedCount: null, progressPct: null, label: 'Watched' };
  }
  if (item.watch_state === 'watched') {
    return { watched: true, unwatchedCount: null, progressPct: null, label: 'Watched' };
  }
  if (item.watch_state === 'in_progress') {
    const pct = progressPct(item.view_offset_ms, item.duration_ms);
    if (pct > 0) {
      return {
        watched: false,
        unwatchedCount: null,
        progressPct: pct,
        label: `In progress, ${Math.round(pct)}% watched`,
      };
    }
  }
  return null;
}

/**
 * Which of "Mark watched" / "Mark unwatched" make sense for a card.
 * Partly-watched items (a show with some episodes seen, a movie
 * stopped half way) get both.
 */
export function watchMenuActions(item: WatchFields): Array<'watched' | 'unwatched'> {
  if (item.unwatched_count != null) {
    const leaves = item.leaf_count;
    const unwatched = item.unwatched_count;
    const out: Array<'watched' | 'unwatched'> = [];
    if (unwatched > 0 || leaves == null) out.push('watched');
    if (leaves == null || unwatched < leaves) out.push('unwatched');
    return out;
  }
  switch (item.watch_state) {
    case 'watched':
      return ['unwatched'];
    case 'in_progress':
      return ['watched', 'unwatched'];
    default:
      return ['watched'];
  }
}

/**
 * The item as it will look after a successful mark — used for the
 * optimistic grid update (the caller keeps the original for rollback).
 */
export function applyWatchedMark<T extends WatchFields>(item: T, watched: boolean): T {
  const next: T = { ...item, watch_state: watched ? 'watched' : 'unwatched', view_offset_ms: 0 };
  if (item.unwatched_count != null || item.leaf_count != null) {
    next.unwatched_count = watched ? 0 : (item.leaf_count ?? item.unwatched_count);
  }
  return next;
}

// ── Library watch filter ───────────────────────────────────────────────────

export const WATCH_FILTER_OPTIONS: ReadonlyArray<{ value: WatchFilter | ''; label: string }> = [
  // "All items" rather than bare "All" — it sits next to "All Genres".
  { value: '', label: 'All items' },
  { value: 'unwatched', label: 'Unwatched' },
  { value: 'in_progress', label: 'In progress' },
  { value: 'watched', label: 'Watched' },
];

/** ?watch= value → filter; anything unrecognised means "All". */
export function parseWatchFilter(v: string | null | undefined): WatchFilter | '' {
  return v === 'unwatched' || v === 'in_progress' || v === 'watched' ? v : '';
}

/** Same URL with ?watch= set (or removed for "All"); other params kept. */
export function urlWithWatchFilter(url: URL, filter: WatchFilter | ''): URL {
  const next = new URL(url.href);
  if (filter) next.searchParams.set('watch', filter);
  else next.searchParams.delete('watch');
  return next;
}

// Library types whose items carry a watch state (the grid gets the
// filter, Surprise me and the mark-watched menu). Music, photos, books,
// audiobooks and podcasts don't.
const WATCHABLE_LIBRARY_TYPES = new Set(['movie', 'show', 'anime', 'cartoons', 'home_video', 'dvr']);

export function supportsWatchState(libraryType: string | null | undefined): boolean {
  return !!libraryType && WATCHABLE_LIBRARY_TYPES.has(libraryType);
}

// Item types a manual mark applies to (containers mark every episode).
const MARKABLE_ITEM_TYPES = new Set(['movie', 'show', 'season', 'episode', 'home_video', 'video', 'music_video']);

export function canMarkWatched(itemType: string): boolean {
  return MARKABLE_ITEM_TYPES.has(itemType);
}

/** True for an API error carrying HTTP 404 (e.g. Surprise me with no match). */
export function isNotFound(e: unknown): boolean {
  return typeof e === 'object' && e !== null && (e as { status?: unknown }).status === 404;
}

/**
 * Movie / episode detail: is this item watched? GET /items/{id} doesn't
 * carry a watch state today, so this reads one if a server sends it and
 * otherwise says "no" — the toggle then offers "Mark watched".
 */
export function detailWatched(item: object | null | undefined): boolean {
  if (!item) return false;
  const d = item as { watch_state?: unknown; watched?: unknown };
  return d.watch_state === 'watched' || d.watched === true;
}

// ── Continue Watching dismiss (optimistic, restorable) ─────────────────────

/** Remove by id; returns the new list plus where the item was (-1 if absent). */
export function removeById<T extends { id: string }>(list: T[], id: string): { list: T[]; index: number; item: T | null } {
  const index = list.findIndex((x) => x.id === id);
  if (index < 0) return { list, index, item: null };
  return { list: [...list.slice(0, index), ...list.slice(index + 1)], index, item: list[index] };
}

/** Put an item back at its old position, unless something re-added it. */
export function restoreAt<T extends { id: string }>(list: T[], item: T, index: number): T[] {
  if (list.some((x) => x.id === item.id)) return list;
  const at = Math.max(0, Math.min(index, list.length));
  return [...list.slice(0, at), item, ...list.slice(at)];
}
