// Helpers for Settings ▸ Import (importing watch history from Plex,
// Jellyfin or Emby).

import type {
  ImportAccount,
  ImportAccountMapping,
  ImportCounts,
  ImportJob,
  ImportKind,
  ImportOptions
} from './api';

export const importKinds: { kind: ImportKind; label: string; placeholder: string; tokenLabel: string; tokenHelp: string }[] = [
  {
    kind: 'plex',
    label: 'Plex',
    placeholder: 'http://192.168.1.10:32400',
    tokenLabel: 'X-Plex-Token',
    tokenHelp:
      "The server owner's token. In Plex Web, open any item's ⋯ menu ▸ Get Info ▸ View XML: the token is the X-Plex-Token at the end of the address."
  },
  {
    kind: 'jellyfin',
    label: 'Jellyfin',
    placeholder: 'http://192.168.1.10:8096',
    tokenLabel: 'API key',
    tokenHelp: 'Create one in Dashboard ▸ API Keys.'
  },
  {
    kind: 'emby',
    label: 'Emby',
    placeholder: 'http://192.168.1.10:8096',
    tokenLabel: 'API key',
    tokenHelp: 'Create one in Settings ▸ Advanced ▸ API Keys.'
  }
];

export function kindLabel(kind: ImportKind): string {
  return importKinds.find((k) => k.kind === kind)?.label ?? kind;
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

const countLabels: [keyof ImportCounts, string, string][] = [
  ['watched', 'watched', 'watched'],
  ['resume_points', 'resume point', 'resume points'],
  ['plays', 'play', 'plays'],
  ['ratings', 'rating', 'ratings'],
  ['favorites', 'favorite', 'favorites'],
  ['playlists', 'playlist', 'playlists'],
  ['playlist_items', 'playlist item', 'playlist items']
];

/** "12 watched · 1 resume point · 40 plays": the non-zero counts. */
export function countsSummary(c: ImportCounts | undefined): string {
  if (!c) return '';
  return countLabels
    .filter(([key]) => (c[key] ?? 0) > 0)
    .map(([key, one, many]) => `${c[key].toLocaleString()} ${c[key] === 1 ? one : many}`)
    .join(' · ');
}

/** "Matched 940 of 1,000 items" for a job's stats. */
export function matchSummary(job: ImportJob): string {
  const s = job.stats;
  if (!s || s.referenced === undefined) return '';
  return `Matched ${(s.matched ?? 0).toLocaleString()} of ${s.referenced.toLocaleString()} items`;
}

export function statusLabel(job: ImportJob): string {
  switch (job.status) {
    case 'running':
      return job.dry_run ? 'Previewing' : 'Importing';
    case 'succeeded':
      return job.dry_run ? 'Preview' : 'Imported';
    case 'failed':
      return 'Failed';
    case 'cancelled':
      return 'Cancelled';
  }
  return job.status;
}
