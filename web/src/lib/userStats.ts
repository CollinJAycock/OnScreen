// A user's own stats (My stats, Year in review) and the digest email
// settings: API calls, response types and the small pure helpers the pages
// chart with. Kept out of api.ts so the pages' logic is testable on its own.
//
// Wording comes from the i18n catalog. The display helpers take the
// translator as an optional last argument: Svelte callers pass $i18n so the
// text re-renders when the language changes; it defaults to the current one.
// Dates and numbers use the interface language.
import { get } from 'svelte/store';
import { api, assetUrl } from './api';
import { formatNumber, locale, i18n, type MessageKey, type Translator } from '$lib/i18n/index.svelte';

export type StatsRange = '30d' | '90d' | '365d' | 'all' | `year:${number}`;

export interface StatsTotals {
  plays: number;
  watch_ms: number;
  listen_ms: number;
  hours_watched: number;
  hours_listened: number;
  items_finished: number;
  books_read: number;
  active_days: number;
}

export interface StatsTitle {
  id: string;
  title: string;
  year?: number;
  poster_path?: string;
  parent_title?: string;
  plays: number;
  ms: number;
}

export interface StatsCount { plays: number; ms: number }
export interface TypeCount extends StatsCount { type: string }
export interface GenreCount extends StatsCount { genre: string }
export interface DeviceCount extends StatsCount { device: string }
export interface MonthCount extends StatsCount { month: string }
export interface HeatCell { weekday: number; hour: number; plays: number }
export interface Streak { days: number; start?: string; end?: string }

export interface UserStats {
  range: string;
  since: string;
  until: string;
  timezone: string;
  totals: StatsTotals;
  by_type: TypeCount[];
  top_genres: GenreCount[];
  top_titles: { movies: StatsTitle[]; shows: StatsTitle[]; artists: StatsTitle[]; albums: StatsTitle[] };
  by_month: MonthCount[];
  heatmap: HeatCell[];
  longest_streak: Streak;
  devices: DeviceCount[];
}

export interface YearInReview {
  year: number;
  timezone: string;
  has_data: boolean;
  totals: StatsTotals;
  longest_streak: Streak;
  top_movies: StatsTitle[];
  top_shows: StatsTitle[];
  top_artists: StatsTitle[];
  top_albums: StatsTitle[];
  top_genres: GenreCount[];
  top_device: DeviceCount | null;
  first_play: {
    id: string; type: string; title: string; year?: number;
    parent_title?: string; poster_path?: string; occurred_at: string;
  } | null;
  busiest_day: { date: string; plays: number; ms: number } | null;
  new_artists: { count: number; top: StatsTitle[] };
  by_month: MonthCount[];
  previous_year: { year: number; totals: StatsTotals } | null;
}

export type DigestFrequency = 'off' | 'weekly' | 'monthly';

export interface MyDigest {
  frequency: DigestFrequency;
  last_sent_at?: string;
  has_email: boolean;
  available: boolean;
}

export interface DigestSettings {
  enabled: boolean;
  weekday: number;
  hour: number;
  include_continue_watching: boolean;
  include_stats: boolean;
  // read-only
  smtp_configured?: boolean;
  base_url?: string;
  server_timezone?: string;
}

function tz(): string {
  try {
    return Intl.DateTimeFormat().resolvedOptions().timeZone || 'UTC';
  } catch {
    return 'UTC';
  }
}

export const userStatsApi = {
  get: (range: StatsRange) =>
    api.get<UserStats>(`/users/me/stats?${new URLSearchParams({ range, tz: tz() })}`),
  yearInReview: (year: number) =>
    api.get<YearInReview>(`/users/me/year-in-review?${new URLSearchParams({ year: String(year), tz: tz() })}`),
};

export const digestApi = {
  getMine: () => api.get<MyDigest>('/users/me/digest'),
  setMine: (frequency: DigestFrequency) => api.put<MyDigest>('/users/me/digest', { frequency }),
  getSettings: () => api.get<DigestSettings>('/settings/digest'),
  updateSettings: (s: DigestSettings) => api.put<DigestSettings>('/settings/digest', s),
  sendTest: () => api.post<{ message: string }>('/settings/digest/test', {}),
};

// ── Helpers ─────────────────────────────────────────────────────────────────

/** "12.5 h", "45 min", "0 min" from milliseconds. */
export function fmtDuration(ms: number, tx: Translator = get(i18n)): string {
  if (!ms || ms < 0) return tx('stats.minutes', { n: 0 });
  const minutes = Math.round(ms / 60000);
  if (minutes < 60) return tx('stats.minutes', { n: minutes });
  const hours = ms / 3_600_000;
  return tx('stats.hours', { n: hours >= 100 ? Math.round(hours) : Math.round(hours * 10) / 10 });
}

/** Hours as a short number for a big stat tile ("12.5", "1,204"). Pass
 *  $i18n from a template so it follows a language change. */
export function fmtHours(h: number, _tx: Translator = get(i18n)): string {
  return formatNumber(h >= 100 ? Math.round(h) : Math.round(h * 10) / 10);
}

/** "Oct" / "Oct 2026" for a YYYY-MM month key, in the interface language.
 *  Pass $i18n from a template so it follows a language change. */
export function monthLabel(month: string, withYear = false, _tx: Translator = get(i18n)): string {
  const [y, m] = month.split('-').map(Number);
  if (!y || !m) return month;
  const d = new Date(Date.UTC(y, m - 1, 1));
  return d.toLocaleString(locale(), { month: 'short', ...(withYear ? { year: 'numeric' } : {}), timeZone: 'UTC' });
}

/** A local calendar date ("2026-03-04") for display, in the interface
 *  language. Pass $i18n from a template so it follows a language change. */
export function dayLabel(date: string, _tx: Translator = get(i18n)): string {
  const [y, m, d] = date.split('-').map(Number);
  if (!y || !m || !d) return date;
  return new Date(Date.UTC(y, m - 1, d)).toLocaleDateString(locale(), {
    weekday: 'short', month: 'short', day: 'numeric', timeZone: 'UTC',
  });
}

/** Short weekday names Mon..Sun (the heatmap's rows) in the interface
 *  language. Pass $i18n from a template so they follow a language change. */
export function weekdayLabels(_tx: Translator = get(i18n)): string[] {
  const f = new Intl.DateTimeFormat(locale(), { weekday: 'short', timeZone: 'UTC' });
  // 2024-01-01 was a Monday.
  return Array.from({ length: 7 }, (_, i) => f.format(new Date(Date.UTC(2024, 0, 1 + i))));
}

/** Expands the sparse heatmap into a 7×24 grid (rows Mon..Sun) plus its max. */
export function heatGrid(cells: HeatCell[]): { grid: number[][]; max: number } {
  const grid = Array.from({ length: 7 }, () => new Array<number>(24).fill(0));
  let max = 0;
  for (const c of cells ?? []) {
    if (c.weekday < 1 || c.weekday > 7 || c.hour < 0 || c.hour > 23) continue;
    grid[c.weekday - 1][c.hour] += c.plays;
    max = Math.max(max, grid[c.weekday - 1][c.hour]);
  }
  return { grid, max };
}

/** Change from previous to current as a signed whole percentage, or null
 *  when there's nothing to compare against. */
export function pctChange(current: number, previous: number): number | null {
  if (!previous) return null;
  return Math.round(((current - previous) / previous) * 100);
}

const TYPE_KEYS: Record<string, MessageKey> = {
  movie: 'stats.typeMovies', episode: 'stats.typeEpisodes', track: 'stats.typeMusic', audiobook: 'stats.typeAudiobooks',
  audiobook_chapter: 'stats.typeAudiobooks', podcast_episode: 'stats.typePodcasts', music_video: 'stats.typeMusicVideos',
  home_video: 'stats.typeHomeVideos', book: 'stats.typeBooks', photo: 'stats.typePhotos',
};

/** Friendly media-type names for the by-type breakdown; an unknown type
 *  shows as sent. */
export function typeLabel(type: string, tx: Translator = get(i18n)): string {
  const key = TYPE_KEYS[type];
  return key ? tx(key) : type;
}

/** Folds by-type rows that share a friendly name (audiobook + chapter). */
export function foldTypes(rows: TypeCount[], tx: Translator = get(i18n)): { label: string; plays: number; ms: number }[] {
  const out = new Map<string, { label: string; plays: number; ms: number }>();
  for (const r of rows ?? []) {
    const group = TYPE_KEYS[r.type] ?? r.type;
    const label = typeLabel(r.type, tx);
    const cur = out.get(group) ?? { label, plays: 0, ms: 0 };
    cur.plays += r.plays;
    cur.ms += r.ms;
    out.set(group, cur);
  }
  return [...out.values()].sort((a, b) => b.ms - a.ms || b.plays - a.plays);
}

/** The years a Year in review can be opened for: this year back to 2000,
 *  newest first, capped to a short list. */
export function reviewYears(now = new Date(), count = 6): number[] {
  const y = now.getFullYear();
  return Array.from({ length: count }, (_, i) => y - i).filter((v) => v >= 2000);
}

/** A poster URL for a stored artwork path at width w. Paths are
 *  filesystem-derived, so each segment is encoded (# and ? in file names)
 *  while the / separators stay. */
export function artworkSrc(path: string, w: number): string {
  const encoded = path.split('/').map(encodeURIComponent).join('/');
  return assetUrl(`/artwork/${encoded}?w=${w}`);
}

/** Parses the /year/[year] route param; null when it isn't a usable year. */
export function parseYearParam(raw: string, now = new Date()): number | null {
  if (!/^\d{4}$/.test(raw)) return null;
  const y = Number(raw);
  return y >= 2000 && y <= now.getFullYear() ? y : null;
}
