// Display helpers for the signed-in devices list (Settings ▸ Security and the
// admin Users page). Pure so they can be unit-tested without a DOM.
import type { SignedInSession } from './api';
import { t as translate, tn } from './i18n/index.svelte';

/** The row's title: the device label the server recorded, or a fallback for
 *  sign-ins made before devices were named. */
export function deviceName(s: SignedInSession): string {
  return s.client_name?.trim() || translate('ui.devices.unknown');
}

/** "just now", "5 minutes ago", "3 hours ago", "2 days ago", then a date. */
export function relativeTime(iso: string, now: number = Date.now()): string {
  const t = Date.parse(iso);
  if (Number.isNaN(t)) return '';
  const secs = Math.max(0, Math.round((now - t) / 1000));
  if (secs < 60) return translate('ui.relative.justNow');
  const mins = Math.round(secs / 60);
  if (mins < 60) return tn('ui.relative.minutes', mins);
  const hours = Math.round(mins / 60);
  if (hours < 24) return tn('ui.relative.hours', hours);
  const days = Math.round(hours / 24);
  if (days < 30) return tn('ui.relative.days', days);
  return new Date(t).toLocaleDateString();
}

/** Current device first, then most recently used. */
export function sortSessions(list: SignedInSession[]): SignedInSession[] {
  return [...list].sort((a, b) => {
    if (a.current !== b.current) return a.current ? -1 : 1;
    return Date.parse(b.last_seen_at) - Date.parse(a.last_seen_at);
  });
}
