// Pure helpers for the admin Library health page (Settings ▸ Library ▸
// Library health). Server contract: internal/api/v1/issues.go and
// items_regrab.go.

import type { AdminMediaIssue, DamagedFile, IssueStatus, RegrabResult } from '$lib/api';

export const ISSUE_FILTERS: readonly { value: IssueStatus | 'all'; label: string }[] = [
  { value: 'open', label: 'Open' },
  { value: 'resolved', label: 'Resolved' },
  { value: 'dismissed', label: 'Dismissed' },
  { value: 'all', label: 'All' },
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
  let msg = `${app} (${r.service_name}) is searching for ${r.title}.`;
  if (r.blocklisted) {
    msg += r.blocklisted_release ? ` Blocklisted ${r.blocklisted_release}.` : ' Blocklisted the last grab.';
  }
  if (r.has_file) {
    msg += ` ${app} still has a file for it, so only an upgrade will be grabbed — delete the file in ${app} for a same-quality replacement.`;
  }
  return msg;
}

/** Resolution note stored when an issue is resolved by re-grabbing. The
 *  reporter sees it in their notification. */
export function regrabResolutionNote(r: RegrabResult, adminNote = ''): string {
  const base = r.blocklisted ? 'Blocklisted the bad release and requested a new download.' : 'Requested a new download.';
  const extra = adminNote.trim();
  return extra ? `${base} ${extra}` : base;
}

export function regrabErrorMessage(err: unknown): string {
  const e = err as { status?: number; code?: string; message?: string } | null;
  if (e?.code === 'NOT_MANAGED') {
    return e.message ? `Not managed by Radarr/Sonarr: ${e.message}` : 'No Radarr/Sonarr instance manages this title.';
  }
  if (e?.code === 'ARR_UNAVAILABLE') {
    return e.message || 'Radarr/Sonarr could not be reached.';
  }
  return e?.message ? `Re-grab failed: ${e.message}` : 'Re-grab failed.';
}
