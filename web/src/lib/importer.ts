// Helpers for Settings ▸ Import (importing watch history from Plex,
// Jellyfin or Emby).
//
// Wording comes from the i18n catalog. The display helpers take the
// translator as an optional last argument: the page passes $i18n so the text
// re-renders when the language changes; it defaults to the current one.

import { get } from 'svelte/store';
import { i18n, type MessageKey, type PluralKey, type Translator } from '$lib/i18n/index.svelte';
import type {
  ImportAccount,
  ImportAccountMapping,
  ImportCounts,
  ImportJob,
  ImportKind,
  ImportOptions
} from './api';

// label (a product name) and placeholder are never translated; the token
// field's wording comes from tokenField().
export const importKinds: { kind: ImportKind; label: string; placeholder: string }[] = [
  { kind: 'plex', label: 'Plex', placeholder: 'http://192.168.1.10:32400' },
  { kind: 'jellyfin', label: 'Jellyfin', placeholder: 'http://192.168.1.10:8096' },
  { kind: 'emby', label: 'Emby', placeholder: 'http://192.168.1.10:8096' }
];

export function kindLabel(kind: ImportKind): string {
  return importKinds.find((k) => k.kind === kind)?.label ?? kind;
}

/** The token field's label and help for a source. Plex's label is the
 *  header's own name (X-Plex-Token), so it isn't translated. */
export function tokenField(kind: ImportKind, tx: Translator = get(i18n)): { label: string; help: string } {
  switch (kind) {
    case 'plex':
      return { label: 'X-Plex-Token', help: tx('lib.importer.plexTokenHelp') };
    case 'jellyfin':
      return { label: tx('lib.importer.apiKey'), help: tx('lib.importer.jellyfinTokenHelp') };
    default:
      return { label: tx('lib.importer.apiKey'), help: tx('lib.importer.embyTokenHelp') };
  }
}

/** Each account's OnScreen user: the same-named user when there is one,
 *  otherwise '' (don't import). */
export function initialMapping(accounts: ImportAccount[]): Record<string, string> {
  const out: Record<string, string> = {};
  for (const a of accounts) out[a.id] = a.suggested_user_id ?? '';
  return out;
}

/** The mapped accounts, for the start request. */
export function mappingList(map: Record<string, string>): ImportAccountMapping[] {
  return Object.entries(map)
    .filter(([, user]) => user !== '')
    .map(([account_id, user_id]) => ({ account_id, user_id }));
}

/** Everything the source has. Plex has no favorites. */
export function defaultOptions(kind: ImportKind): ImportOptions {
  return { watched: true, ratings: true, favorites: kind !== 'plex', playlists: true, history: true };
}

export function anySelected(o: ImportOptions): boolean {
  return o.watched || o.ratings || o.favorites || o.playlists || o.history;
}

const countLabels: [keyof ImportCounts, PluralKey][] = [
  ['watched', 'lib.importer.countWatched'],
  ['resume_points', 'lib.importer.countResumePoints'],
  ['plays', 'lib.importer.countPlays'],
  ['ratings', 'lib.importer.countRatings'],
  ['favorites', 'lib.importer.countFavorites'],
  ['playlists', 'lib.importer.countPlaylists'],
  ['playlist_items', 'lib.importer.countPlaylistItems']
];

/** "12 watched · 1 resume point · 40 plays": the non-zero counts. */
export function countsSummary(c: ImportCounts | undefined, tx: Translator = get(i18n)): string {
  if (!c) return '';
  return countLabels
    .filter(([key]) => (c[key] ?? 0) > 0)
    .map(([key, label]) => tx.n(label, c[key]))
    .join(' · ');
}

/** "Matched 940 of 1,000 items" for a job's stats. */
export function matchSummary(job: ImportJob, tx: Translator = get(i18n)): string {
  const s = job.stats;
  if (!s || s.referenced === undefined) return '';
  return tx('lib.importer.matched', { matched: s.matched ?? 0, total: s.referenced });
}

const STATUS_KEYS: Record<string, [preview: MessageKey, run: MessageKey]> = {
  running: ['lib.importer.previewing', 'requests.dl.importing'],
  succeeded: ['settings.import.preview', 'lib.importer.imported'],
  failed: ['requests.status.failed', 'requests.status.failed'],
  cancelled: ['recordings.statusCancelled', 'recordings.statusCancelled'],
};

export function statusLabel(job: ImportJob, tx: Translator = get(i18n)): string {
  const keys = STATUS_KEYS[job.status];
  if (!keys) return job.status;
  return tx(job.dry_run ? keys[0] : keys[1]);
}
