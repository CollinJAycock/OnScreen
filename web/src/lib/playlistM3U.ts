// Display helpers for M3U playlist import (the Playlists page). Pure so they
// can be unit-tested without a DOM.
//
// Wording comes from the i18n catalog. The display helpers take the
// translator as an optional last argument: Svelte callers pass $i18n so the
// text re-renders when the language changes; it defaults to the current one.
import { get } from 'svelte/store';
import type { M3UImportResult, M3UMatchMethod } from './api';
import { i18n, type MessageKey, type Translator } from '$lib/i18n/index.svelte';

/** Largest playlist file the server takes (1 MB). */
export const MAX_PLAYLIST_BYTES = 1 << 20;

/** How each match method reads in the summary ("{count} by path"), as
 *  catalog keys. */
export const MATCH_METHOD_LABELS: Record<M3UMatchMethod, MessageKey> = {
  path: 'lib.playlistM3U.byPath',
  relative: 'lib.playlistM3U.byRelativePath',
  suffix: 'lib.playlistM3U.bySuffix',
  tags: 'lib.playlistM3U.byTags',
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
export function importSummary(
  r: Pick<M3UImportResult, 'total' | 'matched' | 'by_method'>,
  tx: Translator = get(i18n),
): string {
  const head = tx.n('lib.playlistM3U.matched', r.total, { matched: r.matched });
  const parts = (Object.keys(MATCH_METHOD_LABELS) as M3UMatchMethod[])
    .filter((m) => (r.by_method?.[m] ?? 0) > 0)
    .map((m) => tx(MATCH_METHOD_LABELS[m], { count: r.by_method[m] }));
  if (!parts.length) return head;
  return tx('lib.playlistM3U.withMethods', { summary: head, methods: parts.join(tx('ui.bulkEdit.listSeparator')) });
}

/** Why a line didn't match, in words. */
export function unmatchedReason(reason: 'url' | 'not_found', tx: Translator = get(i18n)): string {
  return reason === 'url' ? tx('lib.playlistM3U.reasonUrl') : tx('lib.playlistM3U.reasonNotFound');
}
