// A user's own stats (My stats, Year in review) and the digest email
// settings: API calls, response types and the small pure helpers the pages
// chart with. Kept out of api.ts so the pages' logic is testable on its own.
import { api, assetUrl } from './api';

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
export function fmtDuration(ms: number): string {
  if (!ms || ms < 0) return '0 min';
  const minutes = Math.round(ms / 60000);
  if (minutes < 60) return `${minutes} min`;
  const hours = ms / 3_600_000;
  return `${hours >= 100 ? Math.round(hours) : Math.round(hours * 10) / 10} h`;
}

/** Hours as a short number for a big stat tile ("12.5", "1,204"). */
export function fmtHours(h: number): string {
  if (h >= 100) return Math.round(h).toLocaleString();
  return String(Math.round(h * 10) / 10);
}

/** "Oct" / "Oct 2026" for a YYYY-MM month key. */
export function monthLabel(month: string, withYear = false): string {
  const [y, m] = month.split('-').map(Number);
  if (!y || !m) return month;
  const d = new Date(Date.UTC(y, m - 1, 1));
  return d.toLocaleString(undefined, { month: 'short', ...(withYear ? { year: 'numeric' } : {}), timeZone: 'UTC' });
}

/** A local calendar date ("2026-03-04") for display. */
export function dayLabel(date: string): string {
  const [y, m, d] = date.split('-').map(Number);
  if (!y || !m || !d) return date;
  return new Date(Date.UTC(y, m - 1, d)).toLocaleDateString(undefined, {
    weekday: 'short', month: 'short', day: 'numeric', timeZone: 'UTC',
  });
}

export const WEEKDAYS = ['Mon', 'Tue', 'Wed', 'Thu', 'Fri', 'Sat', 'Sun'];

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

/** Friendly media-type names for the by-type breakdown. */
export function typeLabel(type: string): string {
  const names: Record<string, string> = {
    movie: 'Movies', episode: 'TV episodes', track: 'Music', audiobook: 'Audiobooks',
    audiobook_chapter: 'Audiobooks', podcast_episode: 'Podcasts', music_video: 'Music videos',
    home_video: 'Home videos', book: 'Books', photo: 'Photos',
  };
  return names[type] ?? type;
}

/** Folds by-type rows whose friendly name is the same (audiobook + chapter). */
export function foldTypes(rows: TypeCount[]): { label: string; plays: number; ms: number }[] {
  const out = new Map<string, { label: string; plays: number; ms: number }>();
  for (const r of rows ?? []) {
    const label = typeLabel(r.type);
    const cur = out.get(label) ?? { label, plays: 0, ms: 0 };
    cur.plays += r.plays;
    cur.ms += r.ms;
    out.set(label, cur);
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
