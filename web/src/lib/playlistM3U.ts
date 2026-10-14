// Display helpers for M3U playlist import (the Playlists page). Pure so they
// can be unit-tested without a DOM.
import type { M3UImportResult, M3UMatchMethod } from './api';

/** Largest playlist file the server takes (1 MB). */
export const MAX_PLAYLIST_BYTES = 1 << 20;

/** How each match method reads in the summary. */
export const MATCH_METHOD_LABELS: Record<M3UMatchMethod, string> = {
  path: 'by path',
  relative: 'by relative path',
  suffix: 'by folder and file name',
  tags: 'by artist and title',
};

/** Whether a picked file looks like a playlist the server accepts. */
export function isPlaylistFileName(name: string): boolean {
  return /\.m3u8?$/i.test(name.trim());
}

/** The default playlist name for a file: its name without the extension. */
export function playlistNameFromFile(name: string): string {
  return name.trim().replace(/\.m3u8?$/i, '').trim();
}

/** "Matched 42 of 50 entries." plus the per-method breakdown. */
export function importSummary(r: Pick<M3UImportResult, 'total' | 'matched' | 'by_method'>): string {
  const head = `Matched ${r.matched} of ${r.total} ${r.total === 1 ? 'entry' : 'entries'}.`;
  const parts = (Object.keys(MATCH_METHOD_LABELS) as M3UMatchMethod[])
    .filter((m) => (r.by_method?.[m] ?? 0) > 0)
    .map((m) => `${r.by_method[m]} ${MATCH_METHOD_LABELS[m]}`);
  return parts.length ? `${head} ${parts.join(', ')}.` : head;
}

/** Why a line didn't match, in words. */
export function unmatchedReason(reason: 'url' | 'not_found'): string {
  return reason === 'url' ? 'web address (not followed)' : 'no match in your libraries';
}
