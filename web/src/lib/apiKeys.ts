// Display helpers for personal API keys (the Account page and Settings ▸
// Security). Pure so they can be unit-tested without a DOM.
import type { ApiKey } from './api';
import { t, type MessageKey } from './i18n/index.svelte';

/** Expiry choices offered when creating a key, in days; 0 = never. */
export const EXPIRY_OPTIONS: { days: number; label: MessageKey }[] = [
  { days: 0, label: 'ui.apiKeys.expiry.never' },
  { days: 30, label: 'ui.apiKeys.expiry.30d' },
  { days: 90, label: 'ui.apiKeys.expiry.90d' },
  { days: 365, label: 'ui.apiKeys.expiry.1y' },
];

/** The expires_at to send for an expiry choice, or null for "never". */
export function expiryFromDays(days: number, now: number = Date.now()): string | null {
  if (!days || days <= 0) return null;
  return new Date(now + days * 24 * 60 * 60 * 1000).toISOString();
}

export function scopeLabel(scope: ApiKey['scope']): string {
  return scope === 'read_only' ? t('ui.apiKeys.readOnly') : t('ui.apiKeys.fullAccess');
}

/** "Expires 12 Mar 2027", "Expired", or "No expiry". */
export function expiryLabel(k: Pick<ApiKey, 'expires_at' | 'expired'>): string {
  if (k.expired) return t('ui.apiKeys.expired');
  if (!k.expires_at) return t('ui.apiKeys.noExpiry');
  return t('ui.apiKeys.expires', { date: new Date(k.expires_at).toLocaleDateString() });
}

/** Newest first. */
export function sortKeys(list: ApiKey[]): ApiKey[] {
  return [...list].sort((a, b) => Date.parse(b.created_at) - Date.parse(a.created_at));
}

/** A key name the server will accept (1–100 characters after trimming). */
export function validKeyName(name: string): boolean {
  const n = name.trim();
  return n.length > 0 && [...n].length <= 100;
}
