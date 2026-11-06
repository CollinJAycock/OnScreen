import type { RequestQuota, RequestQuotaUsage } from '$lib/api';
import { t, tn } from '$lib/i18n/index.svelte';

/** Message shown when a created request went over the requester's quota:
 *  it exists, but waits for an admin instead of being auto-approved.
 *  English only: kept for callers not yet translated; use overQuotaMessage(). */
export const OVER_QUOTA_MESSAGE = "You've reached your limit — this request needs admin approval";

/** Explanation shown (and used as the disabled button's title) when an admin
 *  has turned requesting off for the account.
 *  English only: kept for callers not yet translated; use requestsDisabledMessage(). */
export const REQUESTS_DISABLED_MESSAGE =
  'Requesting is turned off for your account. Ask an admin if you need something added.';

/** OVER_QUOTA_MESSAGE in the active language. */
export function overQuotaMessage(): string {
  return t('search.overQuota');
}

/** REQUESTS_DISABLED_MESSAGE in the active language. */
export function requestsDisabledMessage(): string {
  return t('search.requestsDisabled');
}

/** Human phrase for the rolling quota window: "today", "this week",
 *  "this month", otherwise "in this 14-day period". */
export function windowPhrase(days: number): string {
  if (days === 1) return t('search.windowToday');
  if (days === 7) return t('search.windowWeek');
  if (days === 30) return t('search.windowMonth');
  return t('search.windowDays', { days });
}

function usageLine(u: RequestQuotaUsage, kind: 'movie' | 'tv', phrase: string): string | null {
  if (u.remaining === null || u.remaining === undefined) return null; // unlimited
  if (u.remaining <= 0) {
    return kind === 'movie'
      ? t('search.quotaMoviesNone', { window: phrase })
      : t('search.quotaTvNone', { window: phrase });
  }
  return kind === 'movie'
    ? tn('search.quotaMoviesLeft', u.remaining, { window: phrase })
    : tn('search.quotaTvLeft', u.remaining, { window: phrase });
}

/** One line per limited media type ("2 movie requests left this week"), or
 *  the disabled explanation. Empty when requesting is unlimited or the quota
 *  couldn't be loaded (older server). */
export function quotaLines(q: RequestQuota | null | undefined): string[] {
  if (!q) return [];
  if (!q.can_request) return [requestsDisabledMessage()];
  const phrase = windowPhrase(q.window_days || 7);
  return [usageLine(q.movies, 'movie', phrase), usageLine(q.tv, 'tv', phrase)].filter(
    (l): l is string => l !== null,
  );
}

/** True when the server refused a request because the account can't request. */
export function isRequestsDisabledError(e: unknown): boolean {
  return typeof e === 'object' && e !== null && (e as { code?: unknown }).code === 'REQUESTS_DISABLED';
}
