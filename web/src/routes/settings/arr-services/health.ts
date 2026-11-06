// Pure helpers for the arr-services health panel (GET
// /admin/arr-services/{id}/health). Kept out of the component so the
// summary / low-space rules are unit-tested directly.
import type { ArrDisk, ArrServiceHealth } from '$lib/api';
import { t, tn } from '$lib/i18n/index.svelte';

/** A disk with less than this fraction free is flagged low. */
export const LOW_SPACE_FRACTION = 0.1;

export type HealthTone = 'ok' | 'warn' | 'error';

export interface HealthSummary {
  tone: HealthTone;
  /** One compact line: "OK", "2 warnings · low disk space", "Unreachable — …". */
  label: string;
  /** Whether there's anything to expand (checks or disks). */
  hasDetails: boolean;
}

/** Free fraction of a disk (0..1), or null when its size is unknown. */
export function freeFraction(d: ArrDisk): number | null {
  if (!(d.total_bytes > 0)) return null;
  const f = d.free_bytes / d.total_bytes;
  return Math.min(1, Math.max(0, f));
}

/** Used percentage for the bar width (0..100); 0 when the size is unknown. */
export function usedPercent(d: ArrDisk): number {
  const f = freeFraction(d);
  return f == null ? 0 : Math.round((1 - f) * 1000) / 10;
}

/** True when a disk has under LOW_SPACE_FRACTION free. */
export function isLowSpace(d: ArrDisk): boolean {
  const f = freeFraction(d);
  return f != null && f < LOW_SPACE_FRACTION;
}

/** Summarises a health snapshot into a tone and one short line. */
export function summarizeHealth(h: ArrServiceHealth): HealthSummary {
  if (!h.reachable) {
    return {
      tone: 'error',
      label: h.error ? t('settings.arr.unreachableWith', { error: h.error }) : t('settings.arr.unreachable'),
      hasDetails: false,
    };
  }
  const errors = h.checks.filter((c) => c.type === 'error').length;
  const warnings = h.checks.filter((c) => c.type === 'warning').length;
  const notices = h.checks.filter((c) => c.type === 'notice').length;
  const low = h.disks.filter(isLowSpace).length;

  const parts: string[] = [];
  if (errors) parts.push(tn('settings.arr.errors', errors));
  if (warnings) parts.push(tn('settings.arr.warnings', warnings));
  if (low) parts.push(low === 1 ? t('settings.arr.lowDisk') : t('settings.arr.disksLow', { count: low }));
  if (!parts.length && notices) parts.push(tn('settings.arr.notices', notices));

  let tone: HealthTone = 'ok';
  if (errors) tone = 'error';
  else if (warnings || low) tone = 'warn';

  return {
    tone,
    label: parts.length ? (tone === 'ok' ? t('settings.arr.okWith', { details: parts.join(' · ') }) : parts.join(' · ')) : t('settings.arr.ok'),
    hasDetails: h.checks.length > 0 || h.disks.length > 0,
  };
}

const UNITS = ['B', 'KB', 'MB', 'GB', 'TB', 'PB'];

/** Human-readable bytes, binary units ("1.5 TB"). */
export function formatBytes(n: number): string {
  if (!Number.isFinite(n) || n <= 0) return '0 B';
  let v = n;
  let i = 0;
  while (v >= 1024 && i < UNITS.length - 1) {
    v /= 1024;
    i++;
  }
  const digits = v >= 100 || i === 0 ? 0 : 1;
  return `${v.toFixed(digits).replace(/\.0$/, '')} ${UNITS[i]}`;
}

/** Keeps a wiki link only when it's http(s) — it's rendered as an href. */
export function safeLink(url?: string): string | null {
  if (!url) return null;
  return /^https?:\/\//i.test(url.trim()) ? url.trim() : null;
}
