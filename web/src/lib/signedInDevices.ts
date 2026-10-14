// Display helpers for the signed-in devices list (Settings ▸ Security and the
// admin Users page). Pure so they can be unit-tested without a DOM.
import type { SignedInSession } from './api';

/** The row's title: the device label the server recorded, or a fallback for
 *  sign-ins made before devices were named. */
export function deviceName(s: SignedInSession): string {
  return s.client_name?.trim() || 'Unknown device';
}

/** "just now", "5 minutes ago", "3 hours ago", "2 days ago", then a date. */
export function relativeTime(iso: string, now: number = Date.now()): string {
  const t = Date.parse(iso);
  if (Number.isNaN(t)) return '';
  const secs = Math.max(0, Math.round((now - t) / 1000));
  if (secs < 60) return 'just now';
  const mins = Math.round(secs / 60);
  if (mins < 60) return `${mins} minute${mins === 1 ? '' : 's'} ago`;
  const hours = Math.round(mins / 60);
  if (hours < 24) return `${hours} hour${hours === 1 ? '' : 's'} ago`;
  const days = Math.round(hours / 24);
  if (days < 30) return `${days} day${days === 1 ? '' : 's'} ago`;
  return new Date(t).toLocaleDateString();
}

/** Current device first, then most recently used. */
export function sortSessions(list: SignedInSession[]): SignedInSession[] {
  return [...list].sort((a, b) => {
    if (a.current !== b.current) return a.current ? -1 : 1;
    return Date.parse(b.last_seen_at) - Date.parse(a.last_seen_at);
  });
}
