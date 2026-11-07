// Pure helpers for the /watchlist page: tab + type-filter labels, card
// subtitles, and the local list update after a status change. No Svelte,
// no network — unit-tested in watchlist.test.ts.
//
// Labels are catalog keys or come from the translator (tx defaults to the
// current one; Svelte callers pass $i18n so the text follows a language
// change).
import { get } from 'svelte/store';
import type { WatchlistCounts, WatchlistItem, WatchStatusValue } from '$lib/api';
import { episodeCode } from '$lib/watchState';
import { i18n, type MessageKey, type Translator } from '$lib/i18n/index.svelte';

/** Tab order: Plan to Watch first, then the tracker progression. */
export const WATCHLIST_STATUSES: WatchStatusValue[] = [
  'plan_to_watch',
  'watching',
  'on_hold',
  'completed',
  'dropped',
];

/** Each status's tab name, as a catalog key. */
export const STATUS_LABELS: Record<WatchStatusValue, MessageKey> = {
  plan_to_watch: 'watchlist.statusPlanToWatch',
  watching: 'watchlist.statusWatching',
  on_hold: 'watchlist.statusOnHold',
  completed: 'watchlist.statusCompleted',
  dropped: 'watchlist.statusDropped',
};

/** A status's tab name ("Plan to Watch"). */
export function statusLabel(status: WatchStatusValue, tx: Translator = get(i18n)): string {
  const key = STATUS_LABELS[status];
  return key ? tx(key) : status;
}

const TYPE_LABELS: Record<string, MessageKey> = {
  movie: 'watchlist.typeMovies',
  show: 'watchlist.typeShows',
  season: 'watchlist.typeSeasons',
  episode: 'watchlist.typeEpisodes',
  track: 'watchlist.typeTracks',
  album: 'watchlist.typeAlbums',
  artist: 'watchlist.typeArtists',
  photo: 'watchlist.typePhotos',
  music_video: 'watchlist.typeMusicVideos',
  audiobook: 'watchlist.typeAudiobooks',
  audiobook_chapter: 'watchlist.typeAudiobookChapters',
  book_author: 'watchlist.typeAuthors',
  book_series: 'watchlist.typeBookSeries',
  podcast: 'watchlist.typePodcasts',
  podcast_episode: 'watchlist.typePodcastEpisodes',
  home_video: 'watchlist.typeHomeVideos',
  book: 'watchlist.typeBooks',
};

/** Plural label for a media type in the type filter ("Movies"). A type
 *  this page doesn't know shows as sent, underscores as spaces. */
export function typeLabel(type: string, tx: Translator = get(i18n)): string {
  const key = TYPE_LABELS[type];
  return key ? tx(key) : type.replace(/_/g, ' ');
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
export function typeOptions(
  counts: WatchlistCounts | null,
  status: WatchStatusValue,
  tx: Translator = get(i18n),
): TypeOption[] {
  const types = counts?.statuses?.[status]?.types ?? {};
  return Object.entries(types)
    .filter(([, n]) => n > 0)
    .map(([type, count]) => ({ type, label: typeLabel(type, tx), count }))
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
