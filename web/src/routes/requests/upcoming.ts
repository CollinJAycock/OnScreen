// Calendar maths for the Requests page's Upcoming tab: which calendar day an
// entry belongs on, the Sunday-first month grid, the visible and fetched date
// ranges, the Movies / TV filter and the label formatting. Pulled out of
// Upcoming.svelte so the day-boundary rules can be unit-tested without
// mounting the component.
//
// Pure module — no DOM, no Svelte store dependencies.
//
// Day keys are "YYYY-MM-DD" strings. They compare correctly as plain strings,
// and all arithmetic on them goes through Date.UTC so it never trips over the
// viewer's timezone or a DST change.

import type { UpcomingItem, UpcomingReleaseType, UpcomingStatus } from '$lib/api';

export type UpcomingView = 'month' | 'agenda';
export type UpcomingFilter = 'all' | 'movie' | 'episode';

// Below this viewport width the month grid is too cramped to read, so the tab
// opens on the agenda instead.
export const NARROW_WIDTH = 700;

// The agenda covers today plus this many days — the server's default window.
export const AGENDA_DAYS = 30;

// A month grid is always six Sunday-first weeks, like Radarr / Sonarr.
export const GRID_DAYS = 42;

export const WEEKDAYS = ['Sun', 'Mon', 'Tue', 'Wed', 'Thu', 'Fri', 'Sat'] as const;

export const FILTERS: readonly { key: UpcomingFilter; label: string }[] = [
  { key: 'all', label: 'All' },
  { key: 'movie', label: 'Movies' },
  { key: 'episode', label: 'TV' },
];

export const STATUSES: readonly { key: UpcomingStatus; label: string; hint: string }[] = [
  { key: 'downloaded', label: 'Downloaded', hint: 'Already has a file' },
  { key: 'missing', label: 'Missing', hint: 'Date has passed and there is no file yet' },
  { key: 'upcoming', label: 'Upcoming', hint: 'Not released or aired yet' },
];

const RELEASE_LABELS: Record<UpcomingReleaseType, string> = {
  cinema: 'Cinema',
  digital: 'Digital',
  physical: 'Physical',
  airing: 'Airing',
};

function pad(n: number): string {
  return String(n).padStart(2, '0');
}

function utcKey(ms: number): string {
  const d = new Date(ms);
  return `${d.getUTCFullYear()}-${pad(d.getUTCMonth() + 1)}-${pad(d.getUTCDate())}`;
}

/** The viewer's local calendar day for a Date, as "YYYY-MM-DD". */
export function localDateKey(d: Date): string {
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}`;
}

function parseKey(key: string): { y: number; m: number; d: number } {
  const [y, m, d] = key.split('-').map(Number);
  return { y, m: m - 1, d };
}

/** Calendar-day arithmetic on a "YYYY-MM-DD" key. */
export function addDays(key: string, n: number): string {
  const { y, m, d } = parseKey(key);
  return utcKey(Date.UTC(y, m, d + n));
}

/**
 * The calendar day an entry belongs on, or null when its date doesn't parse.
 *
 * Movie release dates (all_day) are date-only values the server sends as UTC
 * midnight, so they take the UTC date part — reading them in local time would
 * move every release back a day anywhere west of Greenwich. Episode air times
 * are real instants, so they land on the viewer's local day.
 */
export function entryDayKey(item: Pick<UpcomingItem, 'date' | 'all_day'>): string | null {
  const t = Date.parse(item.date);
  if (Number.isNaN(t)) return null;
  return item.all_day ? utcKey(t) : localDateKey(new Date(t));
}

export interface GridDay {
  key: string;      // YYYY-MM-DD
  day: number;      // day of the month, 1–31
  inMonth: boolean; // false for the leading / trailing days of the neighbouring months
}

/** Normalises a (year, month) pair whose month has run past either end of the year. */
export function shiftMonth(
  cursor: { year: number; month: number },
  delta: number,
): { year: number; month: number } {
  const d = new Date(Date.UTC(cursor.year, cursor.month + delta, 1));
  return { year: d.getUTCFullYear(), month: d.getUTCMonth() };
}

/**
 * The six-week, Sunday-first grid for a month (month is 0-based, as in Date):
 * it starts on the Sunday on or before the 1st and always holds GRID_DAYS days,
 * so the layout doesn't jump between five- and six-week months.
 */
export function buildMonthGrid(year: number, month: number): GridDay[] {
  const { year: y, month: m } = shiftMonth({ year, month }, 0);
  const lead = new Date(Date.UTC(y, m, 1)).getUTCDay();
  const days: GridDay[] = [];
  for (let i = 0; i < GRID_DAYS; i++) {
    const d = new Date(Date.UTC(y, m, 1 - lead + i));
    days.push({ key: utcKey(d.getTime()), day: d.getUTCDate(), inMonth: d.getUTCMonth() === m });
  }
  return days;
}

/** The inclusive range of days a view shows. */
export function visibleRange(
  view: UpcomingView,
  cursor: { year: number; month: number },
  today: string,
): { from: string; to: string } {
  if (view === 'agenda') return { from: today, to: addDays(today, AGENDA_DAYS) };
  const grid = buildMonthGrid(cursor.year, cursor.month);
  return { from: grid[0].key, to: grid[grid.length - 1].key };
}

/**
 * The API range for a visible range, padded by a day at each end. The server
 * selects by UTC calendar day, but an episode is shown on the viewer's local
 * day, which can be the UTC day before or after (an evening airing in the
 * Americas is already tomorrow in UTC). The extras are dropped again by
 * groupByDay. 42 + 2 days stays well inside the server's 62-day cap.
 */
export function fetchRange(range: { from: string; to: string }): { from: string; to: string } {
  return { from: addDays(range.from, -1), to: addDays(range.to, 1) };
}

export function filterItems(items: UpcomingItem[], filter: UpcomingFilter): UpcomingItem[] {
  return filter === 'all' ? items : items.filter(i => i.kind === filter);
}

// Within a day: all-day releases first (as calendars show them), then air
// times in order, then by title.
function compareInDay(a: UpcomingItem, b: UpcomingItem): number {
  if (a.all_day !== b.all_day) return a.all_day ? -1 : 1;
  const dt = Date.parse(a.date) - Date.parse(b.date);
  if (dt !== 0) return dt;
  return a.title.localeCompare(b.title);
}

/**
 * Buckets entries by the day they belong on (see entryDayKey), keeping only
 * the days in [from, to] when a range is given. Days with nothing are absent.
 */
export function groupByDay(
  items: UpcomingItem[],
  range?: { from: string; to: string },
): Map<string, UpcomingItem[]> {
  const out = new Map<string, UpcomingItem[]>();
  for (const item of items) {
    const key = entryDayKey(item);
    if (key === null) continue;
    if (range && (key < range.from || key > range.to)) continue;
    const bucket = out.get(key);
    if (bucket) bucket.push(item); else out.set(key, [item]);
  }
  for (const bucket of out.values()) bucket.sort(compareInDay);
  return out;
}

/** Agenda sections: one per day that has entries, in date order. */
export function agendaDays(byDay: Map<string, UpcomingItem[]>): { key: string; items: UpcomingItem[] }[] {
  return [...byDay.keys()].sort().map(key => ({ key, items: byDay.get(key)! }));
}

export function initialView(viewportWidth: number): UpcomingView {
  return viewportWidth < NARROW_WIDTH ? 'agenda' : 'month';
}

/** Posters are only ever rendered from https URLs. */
export function safePosterUrl(url: string | null | undefined): string | null {
  return url && /^https:\/\//i.test(url) ? url : null;
}

export function releaseTypeLabel(t: UpcomingReleaseType): string {
  return RELEASE_LABELS[t] ?? t;
}

export function statusLabel(s: UpcomingStatus): string {
  return STATUSES.find(x => x.key === s)?.label ?? s;
}

/** Full text of an entry, for the title attribute behind its truncated label. */
export function entryText(item: UpcomingItem): string {
  if (item.kind === 'episode') return item.subtitle ? `${item.title} — ${item.subtitle}` : item.title;
  return `${item.title} (${releaseTypeLabel(item.release_type)} release)`;
}

// Day keys become local-midnight Dates only for display, so the weekday and
// day number shown are the key's own.
function keyToLocalDate(key: string): Date {
  const { y, m, d } = parseKey(key);
  return new Date(y, m, d);
}

export function monthTitle(year: number, month: number): string {
  return new Date(year, month, 1).toLocaleDateString(undefined, { month: 'long', year: 'numeric' });
}

/** Agenda day heading, e.g. "Mon, Sep 28". */
export function formatDayKey(key: string): string {
  return keyToLocalDate(key).toLocaleDateString(undefined, { weekday: 'short', month: 'short', day: 'numeric' });
}

export function relativeDay(key: string, today: string): string {
  if (key === today) return 'Today';
  if (key === addDays(today, 1)) return 'Tomorrow';
  return '';
}

/** Local air time for an episode; empty for all-day releases. */
export function formatTime(item: Pick<UpcomingItem, 'date' | 'all_day'>): string {
  if (item.all_day) return '';
  const t = Date.parse(item.date);
  if (Number.isNaN(t)) return '';
  return new Date(t).toLocaleTimeString(undefined, { hour: 'numeric', minute: '2-digit' });
}

/** Detail-panel date: date only for releases (read in UTC), local date and time for airings. */
export function formatWhen(item: Pick<UpcomingItem, 'date' | 'all_day'>): string {
  const t = Date.parse(item.date);
  if (Number.isNaN(t)) return '';
  const date: Intl.DateTimeFormatOptions = { weekday: 'short', year: 'numeric', month: 'long', day: 'numeric' };
  if (item.all_day) return new Date(t).toLocaleDateString(undefined, { ...date, timeZone: 'UTC' });
  return new Date(t).toLocaleString(undefined, { ...date, hour: 'numeric', minute: '2-digit' });
}
