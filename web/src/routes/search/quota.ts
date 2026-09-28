import type { RequestQuota, RequestQuotaUsage } from '$lib/api';

/** Message shown when a created request went over the requester's quota:
 *  it exists, but waits for an admin instead of being auto-approved. */
export const OVER_QUOTA_MESSAGE = "You've reached your limit — this request needs admin approval";

/** Explanation shown (and used as the disabled button's title) when an admin
 *  has turned requesting off for the account. */
export const REQUESTS_DISABLED_MESSAGE =
  'Requesting is turned off for your account. Ask an admin if you need something added.';

/** Human phrase for the rolling quota window: "today", "this week",
 *  "this month", otherwise "in this 14-day period". */
export function windowPhrase(days: number): string {
  if (days === 1) return 'today';
  if (days === 7) return 'this week';
  if (days === 30) return 'this month';
  return `in this ${days}-day period`;
}

function usageLine(u: RequestQuotaUsage, noun: string, phrase: string): string | null {
  if (u.remaining === null || u.remaining === undefined) return null; // unlimited
  if (u.remaining <= 0) return `No ${noun} requests left ${phrase} — new ones need admin approval`;
  return `${u.remaining} ${noun} request${u.remaining === 1 ? '' : 's'} left ${phrase}`;
}

/** One line per limited media type ("2 movie requests left this week"), or
 *  the disabled explanation. Empty when requesting is unlimited or the quota
 *  couldn't be loaded (older server). */
export function quotaLines(q: RequestQuota | null | undefined): string[] {
  if (!q) return [];
  if (!q.can_request) return [REQUESTS_DISABLED_MESSAGE];
  const phrase = windowPhrase(q.window_days || 7);
  return [usageLine(q.movies, 'movie', phrase), usageLine(q.tv, 'TV', phrase)].filter(
    (l): l is string => l !== null,
  );
}

/** True when the server refused a request because the account can't request. */
export function isRequestsDisabledError(e: unknown): boolean {
  return typeof e === 'object' && e !== null && (e as { code?: unknown }).code === 'REQUESTS_DISABLED';
}
