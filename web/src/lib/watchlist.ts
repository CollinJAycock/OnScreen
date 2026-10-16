// Pure helpers for the /watchlist page: tab + type-filter labels, card
// subtitles, and the local list update after a status change. No Svelte,
// no network — unit-tested in watchlist.test.ts.
import type { WatchlistCounts, WatchlistItem, WatchStatusValue } from '$lib/api';
import { episodeCode } from '$lib/watchState';

/** Tab order: Plan to Watch first, then the tracker progression. */
export const WATCHLIST_STATUSES: WatchStatusValue[] = [
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

const TYPE_LABELS: Record<string, string> = {
  movie: 'Movies',
  show: 'Shows',
  season: 'Seasons',
  episode: 'Episodes',
  track: 'Tracks',
  album: 'Albums',
  artist: 'Artists',
  photo: 'Photos',
  music_video: 'Music videos',
  audiobook: 'Audiobooks',
  audiobook_chapter: 'Audiobook chapters',
  book_author: 'Authors',
  book_series: 'Book series',
  podcast: 'Podcasts',
  podcast_episode: 'Podcast episodes',
  home_video: 'Home videos',
  book: 'Books',
};

/** Plural label for a media type in the type filter ("Movies"). */
export function typeLabel(type: string): string {
  return TYPE_LABELS[type] ?? type.replace(/_/g, ' ');
}

/** Read a `status` query value; anything unknown falls back to the first tab. */
export function parseStatus(raw: string | null | undefined): WatchStatusValue {
  return WATCHLIST_STATUSES.includes(raw as WatchStatusValue) ? (raw as WatchStatusValue) : 'plan_to_watch';
}

export interface TypeOption {
  type: string;
  label: string;
  count: number;
}

/**
 * The type filter's options for one tab: only types the caller has under
 * that status, most common first (ties by label). Empty when counts are
 * unknown — the page then shows no filter.
 */
export function typeOptions(counts: WatchlistCounts | null, status: WatchStatusValue): TypeOption[] {
  const types = counts?.statuses?.[status]?.types ?? {};
  return Object.entries(types)
    .filter(([, n]) => n > 0)
    .map(([type, count]) => ({ type, label: typeLabel(type), count }))
    .sort((a, b) => b.count - a.count || a.label.localeCompare(b.label));
}

/** Badge number for a status tab, or null when counts are unknown. */
export function tabCount(counts: WatchlistCounts | null, status: WatchStatusValue): number | null {
  const sc = counts?.statuses?.[status];
  return sc ? sc.total : null;
}

/**
 * The line under a card's title: "Show · S2 · E5" for an episode, the show
 * for a season, "Artist · Album" for a track, else the year.
 */
export function cardSubtitle(item: WatchlistItem): string {
  switch (item.type) {
    case 'episode': {
      const code = episodeCode(item.parent_index, item.index);
      return [item.grandparent_title, code].filter(Boolean).join(' · ');
    }
    case 'season':
      return item.parent_title ?? '';
    case 'track':
      return [item.grandparent_title, item.parent_title].filter(Boolean).join(' · ');
    case 'album':
    case 'audiobook':
    case 'book':
      return [item.parent_title, item.year].filter((v) => v != null && v !== '').join(' · ');
    default:
      return item.year != null ? String(item.year) : '';
  }
}

/**
 * Apply a status change made from a card to the loaded page and counts:
 * the item leaves the current tab (moved or removed) and the tab badges
 * shift with it. `next` null = removed from the list; `next` equal to the
 * current status changes nothing. `removed` says whether the card left the
 * loaded page (the caller drops its total by one). Pure: returns new values.
 */
export function applyStatusChange(
  items: WatchlistItem[],
  counts: WatchlistCounts | null,
  item: WatchlistItem,
  next: WatchStatusValue | null,
): { items: WatchlistItem[]; counts: WatchlistCounts | null; removed: boolean } {
  if (next === item.status) return { items, counts, removed: false };
  const remaining = items.filter((it) => it.id !== item.id);
  const removed = remaining.length !== items.length;
  if (!counts) return { items: remaining, counts, removed };

  const statuses = { ...counts.statuses };
  const bump = (s: WatchStatusValue, delta: number) => {
    const cur = statuses[s] ?? { total: 0, types: {} };
    const types = { ...cur.types };
    const n = Math.max(0, (types[item.type] ?? 0) + delta);
    if (n === 0) delete types[item.type];
    else types[item.type] = n;
    statuses[s] = { total: Math.max(0, cur.total + delta), types };
  };
  bump(item.status, -1);
  if (next) bump(next, 1);
  return {
    items: remaining,
    counts: { total: Math.max(0, counts.total - (next ? 0 : 1)), statuses },
    removed,
  };
}
