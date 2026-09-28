import { writable } from 'svelte/store';
import { requestsAdminApi } from '$lib/api';

/** Number of media requests waiting for approval — the admin nav badge.
 *  Only meaningful for admins; stays 0 for everyone else. */
export const pendingRequestCount = writable(0);

// Navigation refreshes are throttled to one GET per NAV_REFRESH_MS; forced
// refreshes (a request_pending notification, an approve/decline) always go.
const NAV_REFRESH_MS = 15_000;
let lastFetch = 0;
let inflight: Promise<void> | null = null;
let fetchSeq = 0;
// Bumped by reset() so a response that lands after logout / user switch is
// dropped instead of resurrecting the previous admin's count.
let generation = 0;

/** Re-read the pending count. `force` skips the navigation throttle. Errors
 *  (not an admin, older server, offline) leave the current value alone. */
export function refreshPendingRequests(force = false): Promise<void> {
  const now = Date.now();
  if (!force && now - lastFetch < NAV_REFRESH_MS) return inflight ?? Promise.resolve();
  if (inflight && !force) return inflight;
  lastFetch = now;
  const gen = generation;
  const seq = ++fetchSeq;
  inflight = (async () => {
    try {
      const res = await requestsAdminApi.pendingCount();
      if (gen === generation) pendingRequestCount.set(Math.max(0, Number(res?.count) || 0));
    } catch {
      /* keep the last known value */
    } finally {
      if (seq === fetchSeq) inflight = null;
    }
  })();
  return inflight;
}

/** Clear the badge (logout, switching to a non-admin user). */
export function resetPendingRequests(): void {
  generation++;
  lastFetch = 0;
  inflight = null;
  pendingRequestCount.set(0);
}

/** Notification types that mean the approval queue changed for an admin. */
export function changesPendingQueue(type: string | undefined | null): boolean {
  return type === 'request_pending';
}

/** Badge text: the count, capped at "99+". Empty for zero. */
export function pendingBadgeText(n: number): string {
  if (!n || n < 0) return '';
  return n > 99 ? '99+' : String(n);
}
