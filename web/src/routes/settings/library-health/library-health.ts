// Pure helpers for the admin Library health page (Settings ▸ Library ▸
// Library health). Server contract: internal/api/v1/issues.go and
// items_regrab.go.

import type { AdminMediaIssue, DamagedFile, IssueStatus, RegrabResult } from '$lib/api';
import { t, type MessageKey } from '$lib/i18n/index.svelte';

/** label is a catalog key. */
export const ISSUE_FILTERS: readonly { value: IssueStatus | 'all'; label: MessageKey }[] = [
  { value: 'open', label: 'settings.libraryHealth.filterOpen' },
  { value: 'resolved', label: 'settings.libraryHealth.filterResolved' },
  { value: 'dismissed', label: 'settings.libraryHealth.filterDismissed' },
  { value: 'all', label: 'settings.libraryHealth.filterAll' },
];

/** Item types Radarr/Sonarr can re-grab. */
const REGRABBABLE = new Set(['movie', 'show', 'season', 'episode']);

export function canRegrab(itemType: string): boolean {
  return REGRABBABLE.has(itemType);
}

function pad2(n: number): string {
  return String(n).padStart(2, '0');
}

interface TitleParts {
  item_type: string;
  title: string;
  year?: number;
  show_title?: string;
  season_number?: number;
  episode_number?: number;
}

/** "Andor — S02E05 · Harvest", "Andor — Season 2", "Heat (1995)". */
export function displayTitle(p: TitleParts): string {
  if (p.item_type === 'episode' && p.show_title) {
    const code =
      p.season_number != null && p.episode_number != null
        ? `S${pad2(p.season_number)}E${pad2(p.episode_number)} · `
        : '';
    return `${p.show_title} — ${code}${p.title}`;
  }
  if (p.item_type === 'season' && p.show_title) {
    return `${p.show_title} — ${p.title}`;
  }
  return p.year ? `${p.title} (${p.year})` : p.title;
}

export function issueTitle(row: AdminMediaIssue): string {
  return displayTitle({ ...row, title: row.item_title, year: row.item_year });
}

export function damagedTitle(row: DamagedFile): string {
  return displayTitle(row);
}

/** Toast after a successful re-grab. */
export function regrabSummary(r: RegrabResult): string {
  const app = r.service_kind === 'sonarr' ? 'Sonarr' : 'Radarr';
  // Separate sentences, joined with a space.
  const parts = [t('settings.libraryHealth.regrabSearching', { app, service: r.service_name, title: r.title })];
  if (r.blocklisted) {
    parts.push(
      r.blocklisted_release
        ? t('settings.libraryHealth.regrabBlocklisted', { release: r.blocklisted_release })
        : t('settings.libraryHealth.regrabBlocklistedLast'),
    );
  }
  if (r.has_file) {
    parts.push(t('settings.libraryHealth.regrabHasFile', { app }));
  }
  return parts.join(' ');
}

/** Resolution note stored when an issue is resolved by re-grabbing. The
 *  reporter sees it in their notification. */
export function regrabResolutionNote(r: RegrabResult, adminNote = ''): string {
  const base = r.blocklisted ? t('settings.libraryHealth.noteBlocklisted') : t('settings.libraryHealth.noteRequested');
  const extra = adminNote.trim();
  return extra ? `${base} ${extra}` : base;
}

export function regrabErrorMessage(err: unknown): string {
  const e = err as { status?: number; code?: string; message?: string } | null;
  if (e?.code === 'NOT_MANAGED') {
    return e.message ? t('settings.libraryHealth.notManagedWith', { message: e.message }) : t('settings.libraryHealth.notManaged');
  }
  if (e?.code === 'ARR_UNAVAILABLE') {
    return e.message || t('settings.libraryHealth.arrUnreachable');
  }
  return e?.message ? t('settings.libraryHealth.regrabFailedWith', { message: e.message }) : t('settings.libraryHealth.regrabFailed');
}
