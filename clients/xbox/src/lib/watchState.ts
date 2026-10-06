// Pure helpers behind the TV watch-state UI: Next Up tiles, the show /
// season Play button (up-next), "Mark all" options, poster-card badges, the
// library Watch filter and the optimistic Continue Watching removal. No
// Svelte, no network. Mirrors web/src/lib/watchState.ts and the Android TV
// client's WatchStateUi so every client labels things the same way. Kept
// identical between the Tizen and webOS apps.

import type {
  ChildItem,
  HubData,
  HubItem,
  MediaItem,
  UpNext,
  WatchFilter,
} from './api/types';

/** "S2 · E5". A missing season drops the S half; a missing or 0 episode
 *  number (the server's "unknown") drops the E half. */
export function episodeCode(season?: number | null, episode?: number | null): string {
  const parts: string[] = [];
  if (season != null) parts.push(`S${season}`);
  if (episode != null && episode > 0) parts.push(`E${episode}`);
  return parts.join(' · ');
}

/** Second line of a Next Up tile: "S2 · E5 — Episode title". */
export function nextUpSubtitle(
  item: Pick<HubItem, 'title' | 'season_number' | 'episode_number'>,
): string {
  const code = episodeCode(item.season_number, item.episode_number);
  return code ? `${code} — ${item.title}` : item.title;
}

/** Title + subtitle for a home tile. Episode tiles that carry their show
 *  (Next Up) read show on top, "S2 · E5 — Episode" underneath; everything
 *  else is title + year. */
export function hubTileText(item: HubItem): { title: string; subtitle?: string } {
  if (item.type === 'episode' && item.show_title && item.show_title.trim() !== '') {
    return { title: item.show_title, subtitle: nextUpSubtitle(item) };
  }
  return { title: item.title, subtitle: item.year ? String(item.year) : undefined };
}

/**
 * Label for the primary button on a show / season page, or null when
 * there's nothing to play (mode "none", no episode, unknown mode).
 *   resume  → "Resume S3 · E4"
 *   next    → "Play S3 · E5"
 *   start   → "Play S1 · E1"
 *   rewatch → "Watch again"
 */
export function upNextLabel(u: UpNext | null | undefined): string | null {
  if (!u || !u.episode) return null;
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

/** Which "Mark all …" buttons a show / season offers, from its up-next
 *  state: nothing watched → only "watched"; everything watched → only
 *  "unwatched"; no episodes → neither; part-watched → both. */
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

/** 0..1 for a progress bar, or undefined when there's nothing to show. */
export function progressRatio(offsetMs?: number | null, durationMs?: number | null): number | undefined {
  if (!offsetMs || !durationMs || offsetMs <= 0 || durationMs <= 0) return undefined;
  return Math.min(1, offsetMs / durationMs);
}

type WatchFields = Pick<
  MediaItem,
  'watch_state' | 'view_offset_ms' | 'duration_ms' | 'leaf_count' | 'unwatched_count'
>;

export interface CardBadge {
  /** Fully watched: the check mark. */
  watched: boolean;
  /** Shows / seasons with episodes left: the count pill. */
  unwatchedCount: number | null;
  /** In-progress video: 0..1 for the bar under the poster. */
  progress: number | null;
}

/**
 * What a library card shows, or null for nothing. Only derives from fields
 * the server actually sent, so listings without watch state (older servers,
 * music, photos) render exactly as before. Show-like counts win over
 * watch_state.
 */
export function cardWatchBadge(item: WatchFields): CardBadge | null {
  if (item.unwatched_count != null) {
    if (item.leaf_count === 0) return null;
    if (item.unwatched_count > 0) {
      return { watched: false, unwatchedCount: item.unwatched_count, progress: null };
    }
    return { watched: true, unwatchedCount: null, progress: null };
  }
  if (item.watch_state === 'watched') return { watched: true, unwatchedCount: null, progress: null };
  if (item.watch_state === 'in_progress') {
    const p = progressRatio(item.view_offset_ms, item.duration_ms);
    if (p !== undefined) return { watched: false, unwatchedCount: null, progress: p };
  }
  return null;
}

/** Badge for an episode card on a show / season page (children listing:
 *  `watched` + `view_offset_ms`). */
export function childWatchBadge(
  child: Pick<ChildItem, 'watched' | 'view_offset_ms' | 'duration_ms'>,
): CardBadge | null {
  if (child.watched) return { watched: true, unwatchedCount: null, progress: null };
  const p = progressRatio(child.view_offset_ms, child.duration_ms);
  return p !== undefined ? { watched: false, unwatchedCount: null, progress: p } : null;
}

// ── Library watch filter ───────────────────────────────────────────────────

export const WATCH_FILTER_OPTIONS: ReadonlyArray<{ value: WatchFilter | ''; label: string }> = [
  { value: '', label: 'All items' },
  { value: 'unwatched', label: 'Unwatched' },
  { value: 'in_progress', label: 'In progress' },
  { value: 'watched', label: 'Watched' },
];

/** Stored / query value → filter; anything unrecognised means "All". */
export function parseWatchFilter(v: string | null | undefined): WatchFilter | '' {
  return v === 'unwatched' || v === 'in_progress' || v === 'watched' ? v : '';
}

/** `&watch=…` for the listing / random query string ('' for "All"). */
export function watchQuery(filter: WatchFilter | ''): string {
  return filter ? `&watch=${filter}` : '';
}

// Library types whose items carry a watch state (the grid gets the filter
// and Surprise me). Music, photos, books, audiobooks and podcasts don't.
const WATCHABLE_LIBRARY_TYPES = new Set(['movie', 'show', 'anime', 'cartoons', 'home_video', 'dvr']);

export function supportsWatchState(libraryType: string | null | undefined): boolean {
  return !!libraryType && WATCHABLE_LIBRARY_TYPES.has(libraryType);
}

/** True when a library listing came from a server that attaches watch state
 *  (v2.5+). Older servers ignore ?watch= and have no /random, so the filter
 *  and Surprise me stay hidden for them. */
export function hasWatchFields(items: readonly WatchFields[]): boolean {
  return items.some(
    (i) => i.watch_state !== undefined || i.unwatched_count !== undefined || i.leaf_count !== undefined,
  );
}

// Item types a manual mark applies to: playable videos, plus shows and
// seasons (which mark every episode underneath).
const MARKABLE_LEAF_TYPES = new Set(['movie', 'episode', 'music_video', 'home_video']);

export function isMarkableLeaf(type: string): boolean {
  return MARKABLE_LEAF_TYPES.has(type);
}

export function isWatchContainer(type: string): boolean {
  return type === 'show' || type === 'season';
}

// ── Continue Watching removal (optimistic, restorable) ─────────────────────

/** Remove by id; returns the new list plus where the item was (-1 if absent). */
export function removeById<T extends { id: string }>(
  list: readonly T[],
  id: string,
): { list: T[]; index: number; item: T | null } {
  const index = list.findIndex((x) => x.id === id);
  if (index < 0) return { list: [...list], index, item: null };
  return { list: [...list.slice(0, index), ...list.slice(index + 1)], index, item: list[index] };
}

/** Put an item back at its old position, unless something re-added it. */
export function restoreAt<T extends { id: string }>(list: readonly T[], item: T, index: number): T[] {
  if (list.some((x) => x.id === item.id)) return [...list];
  const at = Math.max(0, Math.min(index, list.length));
  return [...list.slice(0, at), item, ...list.slice(at)];
}

const CW_KEYS = [
  'continue_watching',
  'continue_watching_tv',
  'continue_watching_movies',
  'continue_watching_other',
] as const;

/** The hub with `id` taken out of every Continue Watching list (the legacy
 *  combined feed and the three split rows). Other rows are untouched. */
export function withoutContinueWatching(hub: HubData, id: string): HubData {
  const next: HubData = { ...hub };
  for (const k of CW_KEYS) {
    const list = hub[k];
    if (list) next[k] = removeById(list, id).list;
  }
  return next;
}

/** Undo withoutContinueWatching for one item after the server refused the
 *  removal: back where `before` had it in each Continue Watching list.
 *  Anything else changed since (another tile removed meanwhile) stays. */
export function restoreContinueWatching(hub: HubData, before: HubData, id: string): HubData {
  const next: HubData = { ...hub };
  for (const k of CW_KEYS) {
    const old = before[k];
    const index = old ? old.findIndex((x) => x.id === id) : -1;
    if (!old || index < 0) continue;
    next[k] = restoreAt(hub[k] ?? [], old[index], index);
  }
  return next;
}

// ── Feature detection on older servers ─────────────────────────────────────

/** HTTP status of a thrown API error, if it carries one. */
export function errorStatus(e: unknown): number | undefined {
  const s = typeof e === 'object' && e !== null ? (e as { status?: unknown }).status : undefined;
  return typeof s === 'number' ? s : undefined;
}

/** True when an endpoint isn't on this server (404 / 405 from a router that
 *  predates it, 501 from a build without the backing store). Callers hide
 *  the feature quietly rather than showing an error. */
export function endpointMissing(e: unknown): boolean {
  const s = errorStatus(e);
  return s === 404 || s === 405 || s === 501;
}

/** The sentence for a failed watched mark / Continue Watching removal. */
export function watchWriteError(e: unknown, fallback: string): string {
  return errorStatus(e) === 429 ? 'Too many changes at once. Try again in a minute.' : fallback;
}
