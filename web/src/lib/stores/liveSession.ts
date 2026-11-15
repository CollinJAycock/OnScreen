// The signed-in account's live feeds: the notifications SSE stream (bell,
// "Play on…", admin stop, cross-device resume) and the jobs poll (scan
// banner). They belong to one user: started on sign-in, stopped on sign-out,
// restarted when a different user signs in. The root layout calls
// syncLiveSession on every navigation with whoever is signed in now; a
// sign-in is an in-app goto from /login, so the layout never remounts and
// onMount alone would miss it.

import { initNotifications, stopNotifications } from './notifications';
import { startJobsPolling, stopJobsPolling } from './jobs';

let activeUserId: string | null = null;

/** The user whose feeds are running, or null. */
export function liveSessionUser(): string | null {
  return activeUserId;
}

/**
 * Run the feeds for userId (null: signed out). A no-op while they already
 * run for that user, so calling it on every navigation starts them once.
 */
export function syncLiveSession(userId: string | null): void {
  if (userId === activeUserId) return;
  if (activeUserId !== null) {
    stopNotifications();
    stopJobsPolling();
  }
  activeUserId = userId;
  if (userId !== null) {
    initNotifications();
    startJobsPolling();
  }
}
