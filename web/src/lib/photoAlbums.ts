// Album-edit logic for the photo albums UI: name validation, the bulk
// add / remove loop behind multi-select, the toast text it reports, and
// the immutable list updates the pages apply after a create / rename /
// delete. Kept free of Svelte and the API client so it's tested directly.
//
// Wording comes from the i18n catalog. The display helpers take the
// translator as an optional last argument: Svelte callers pass $i18n so the
// text re-renders when the language changes; it defaults to the current one.

import { get } from 'svelte/store';
import type { PhotoAlbum } from './api';
import { t, i18n, type Translator } from '$lib/i18n/index.svelte';

/** How many photos GET /photo-albums/{id}/items returns at most (the
 *  server's photoAlbumPageDefault). */
export const ALBUM_PAGE_LIMIT = 5000;

/** A usable album name (trimmed), or null for a blank one. */
export function normalizeAlbumName(raw: string): string | null {
  const name = raw.trim();
  return name ? name : null;
}

export function photoCountLabel(n: number, tx: Translator = get(i18n)): string {
  return tx.n('photos.albums.photoCount', n);
}

export interface BulkResult {
  /** Ids the call succeeded for. */
  done: number;
  /** Ids it failed for. */
  failed: number;
  /** The first failure's message, for the toast. */
  firstError: string;
}

/**
 * Runs `op` for each id (deduplicated), one request at a time: gentle on
 * the server, and each add gets its own position in the album. A failure
 * doesn't stop the rest; the result counts both. `onProgress` fires after
 * each id with the number handled so far.
 */
export async function runBulk(
  ids: readonly string[],
  op: (id: string) => Promise<unknown>,
  onProgress?: (handled: number, total: number) => void,
): Promise<BulkResult> {
  const unique = [...new Set(ids)];
  const result: BulkResult = { done: 0, failed: 0, firstError: '' };
  for (let i = 0; i < unique.length; i++) {
    try {
      await op(unique[i]);
      result.done++;
    } catch (e) {
      result.failed++;
      if (!result.firstError) result.firstError = e instanceof Error && e.message ? e.message : t('ui.seasons.requestFailed');
    }
    onProgress?.(i + 1, unique.length);
  }
  return result;
}

/** Toast text for a bulk add / remove. ok=false means use an error toast. */
export function bulkMessage(
  kind: 'add' | 'remove',
  r: BulkResult,
  albumName: string,
  tx: Translator = get(i18n),
): { ok: boolean; message: string } {
  const total = r.done + r.failed;
  const add = kind === 'add';
  const album = albumName;
  if (r.failed === 0) {
    if (total === 1) {
      return { ok: true, message: tx(add ? 'lib.photoAlbums.addedOne' : 'lib.photoAlbums.removedOne', { album }) };
    }
    return { ok: true, message: tx.n(add ? 'lib.photoAlbums.addedMany' : 'lib.photoAlbums.removedMany', total, { album }) };
  }
  let message: string;
  if (r.done === 0) {
    message = total === 1
      ? tx(add ? 'lib.photoAlbums.addFailedOne' : 'lib.photoAlbums.removeFailedOne', { album })
      : tx(add ? 'lib.photoAlbums.addFailedMany' : 'lib.photoAlbums.removeFailedMany', { album });
  } else {
    message = tx.n(add ? 'lib.photoAlbums.addedPartial' : 'lib.photoAlbums.removedPartial', total, {
      album,
      done: r.done,
      failed: r.failed,
    });
  }
  if (r.firstError) message = tx('lib.photoAlbums.withError', { message, error: r.firstError });
  return { ok: false, message };
}

/** The list with a newly created album first (the server lists by last
 *  update), with the count / cover the create response leaves out. */
export function withCreated(albums: readonly PhotoAlbum[], created: PhotoAlbum): PhotoAlbum[] {
  return [{ ...created, item_count: created.item_count ?? 0 }, ...albums.filter((a) => a.id !== created.id)];
}

/** The list after a rename. The update response carries no count or cover,
 *  so only the name (and timestamp) are taken from it. */
export function withRenamed(albums: readonly PhotoAlbum[], updated: Pick<PhotoAlbum, 'id' | 'name'> & { updated_at?: string }): PhotoAlbum[] {
  return albums.map((a) =>
    a.id === updated.id ? { ...a, name: updated.name, updated_at: updated.updated_at ?? a.updated_at } : a,
  );
}

export function withoutAlbum(albums: readonly PhotoAlbum[], id: string): PhotoAlbum[] {
  return albums.filter((a) => a.id !== id);
}

/** Toggles one id in a selection, returning a new Set (so a reactive
 *  assignment sees the change). */
export function toggleSelected(sel: ReadonlySet<string>, id: string): Set<string> {
  const next = new Set(sel);
  if (next.has(id)) next.delete(id);
  else next.add(id);
  return next;
}
