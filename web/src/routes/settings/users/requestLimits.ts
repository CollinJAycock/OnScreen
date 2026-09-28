// Request limits on Settings ▸ Users: per-user can-request + quota overrides
// and the server-wide quota defaults. Pure helpers so the page stays small
// and the parsing/wording is unit-tested.

import type { User } from '$lib/api';

/** Largest per-window quota the server accepts (settings.MaxRequestQuota). */
export const MAX_REQUEST_QUOTA = 1000;
export const MIN_WINDOW_DAYS = 1;
export const MAX_WINDOW_DAYS = 90;
export const DEFAULT_WINDOW_DAYS = 7;

/** Server-wide quota defaults as the page edits them. */
export interface QuotaDefaults {
  quota_movies: number;
  quota_tv: number;
  quota_window_days: number;
}

export type ParseResult<T> = { ok: true; value: T } | { ok: false; error: string };

function parseWhole(raw: string): number | null {
  const s = raw.trim();
  if (!/^\d+$/.test(s)) return null;
  return Number(s);
}

/** A per-user quota field: blank = server default (null), 0 = unlimited,
 *  1..MAX = that many per window. */
export function parseUserQuota(raw: string, label: string): ParseResult<number | null> {
  if (raw.trim() === '') return { ok: true, value: null };
  const n = parseWhole(raw);
  if (n === null || n > MAX_REQUEST_QUOTA) {
    return { ok: false, error: `${label}: enter a whole number from 0 to ${MAX_REQUEST_QUOTA}, or leave blank for the default` };
  }
  return { ok: true, value: n };
}

/** A server-default quota field: 0 = unlimited, 1..MAX. Blank means 0. */
export function parseDefaultQuota(raw: string, label: string): ParseResult<number> {
  if (raw.trim() === '') return { ok: true, value: 0 };
  const n = parseWhole(raw);
  if (n === null || n > MAX_REQUEST_QUOTA) {
    return { ok: false, error: `${label}: enter a whole number from 0 (unlimited) to ${MAX_REQUEST_QUOTA}` };
  }
  return { ok: true, value: n };
}

export function parseWindowDays(raw: string): ParseResult<number> {
  const n = parseWhole(raw);
  if (n === null || n < MIN_WINDOW_DAYS || n > MAX_WINDOW_DAYS) {
    return { ok: false, error: `Window: enter a number of days from ${MIN_WINDOW_DAYS} to ${MAX_WINDOW_DAYS}` };
  }
  return { ok: true, value: n };
}

/** Normalise the settings response (older servers omit the quota fields). */
export function quotaDefaultsFrom(r: Partial<QuotaDefaults> | null | undefined): QuotaDefaults {
  return {
    quota_movies: Math.max(0, Number(r?.quota_movies) || 0),
    quota_tv: Math.max(0, Number(r?.quota_tv) || 0),
    quota_window_days: Number(r?.quota_window_days) || DEFAULT_WINDOW_DAYS,
  };
}

/** "3" / "no limit" for a concrete limit. */
export function limitText(n: number): string {
  return n === 0 ? 'no limit' : String(n);
}

/** Placeholder for a per-user quota input: what blank means right now. */
export function quotaPlaceholder(def: number | undefined): string {
  return `Default (${limitText(def ?? 0)})`;
}

/** Input text for a stored per-user quota (null = blank = default). */
export function quotaInputValue(v: number | null | undefined): string {
  return v === null || v === undefined ? '' : String(v);
}

/** Compact per-row summary shown on the "Request limits" button. */
export function limitsSummary(user: Pick<User, 'can_request' | 'request_quota_movies' | 'request_quota_tv'>): string {
  if (user.can_request === false) return 'Requests off';
  const m = user.request_quota_movies;
  const t = user.request_quota_tv;
  if ((m === null || m === undefined) && (t === null || t === undefined)) return 'Default limits';
  const fmt = (v: number | null | undefined) => (v === null || v === undefined ? 'default' : limitText(v));
  return `Movies ${fmt(m)} · TV ${fmt(t)}`;
}

/** Hint under the per-user inputs, phrased for the current window. */
export function windowHint(days: number): string {
  const per = days === 1 ? 'per day' : days === 7 ? 'per week' : `per ${days} days`;
  return `Requests ${per}. Blank = server default, 0 = no limit. Past the limit, requests wait for approval.`;
}
