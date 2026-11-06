// Request limits on Settings ▸ Users: per-user can-request + quota overrides
// and the server-wide quota defaults. Pure helpers so the page stays small
// and the parsing/wording is unit-tested.
//
// Wording comes from the i18n catalog (settings-a). The display helpers take
// the translator as an optional last argument: the page passes $tr so the
// text re-renders when the language changes; it defaults to the current one.

import { get } from 'svelte/store';
import type { User } from '$lib/api';
import { t, tr, type Translator } from '$lib/i18n/index.svelte';

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
    return { ok: false, error: t('settings.users.quotaUserError', { label, max: String(MAX_REQUEST_QUOTA) }) };
  }
  return { ok: true, value: n };
}

/** A server-default quota field: 0 = unlimited, 1..MAX. Blank means 0. */
export function parseDefaultQuota(raw: string, label: string): ParseResult<number> {
  if (raw.trim() === '') return { ok: true, value: 0 };
  const n = parseWhole(raw);
  if (n === null || n > MAX_REQUEST_QUOTA) {
    return { ok: false, error: t('settings.users.quotaDefaultError', { label, max: String(MAX_REQUEST_QUOTA) }) };
  }
  return { ok: true, value: n };
}

export function parseWindowDays(raw: string): ParseResult<number> {
  const n = parseWhole(raw);
  if (n === null || n < MIN_WINDOW_DAYS || n > MAX_WINDOW_DAYS) {
    return { ok: false, error: t('settings.users.windowError', { min: MIN_WINDOW_DAYS, max: MAX_WINDOW_DAYS }) };
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
export function limitText(n: number, tx: Translator = get(tr)): string {
  return n === 0 ? tx('settings.users.noLimit') : String(n);
}

/** Placeholder for a per-user quota input: what blank means right now. */
export function quotaPlaceholder(def: number | undefined, tx: Translator = get(tr)): string {
  return tx('settings.users.quotaPlaceholder', { value: limitText(def ?? 0, tx) });
}

/** Input text for a stored per-user quota (null = blank = default). */
export function quotaInputValue(v: number | null | undefined): string {
  return v === null || v === undefined ? '' : String(v);
}

/** Compact per-row summary shown on the "Request limits" button. */
export function limitsSummary(
  user: Pick<User, 'can_request' | 'request_quota_movies' | 'request_quota_tv'>,
  tx: Translator = get(tr),
): string {
  if (user.can_request === false) return tx('settings.users.requestsOff');
  const m = user.request_quota_movies;
  const tv = user.request_quota_tv;
  if ((m === null || m === undefined) && (tv === null || tv === undefined)) return tx('settings.users.defaultLimits');
  const fmt = (v: number | null | undefined) => (v === null || v === undefined ? tx('settings.users.limitDefault') : limitText(v, tx));
  return tx('settings.users.limitsSummary', { movies: fmt(m), tv: fmt(tv) });
}

/** Hint under the per-user inputs, phrased for the current window. */
export function windowHint(days: number, tx: Translator = get(tr)): string {
  if (days === 1) return tx('settings.users.windowHintDay');
  if (days === 7) return tx('settings.users.windowHintWeek');
  return tx.n('settings.users.windowHintDays', days);
}
