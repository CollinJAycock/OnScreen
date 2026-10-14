// Bulk metadata edit — pure helpers for BulkMetadataEditor. Server contract:
// PATCH /items/bulk (internal/api/v1/items_bulk.go). Every field written is
// locked, as in the single-item editor; a field an item has locked is skipped
// and reported unless the same request unlocks it.

import { normalizeLabel, type BulkEditRequest, type BulkEditResult } from './api';
import { parseGenres, type LockableField } from './metadataEdit';

export const MAX_BULK_ITEMS = 500;

export type RatingMode = 'keep' | 'set' | 'clear';
export type GenreMode = 'keep' | 'add' | 'remove' | 'replace';
export type LockChoice = 'keep' | 'lock' | 'unlock';

/** The dialog's state; every field starts at "leave unchanged". */
export interface BulkEditForm {
  ratingMode: RatingMode;
  rating: string;
  genreMode: GenreMode;
  genres: string; // comma-separated
  addLabels: string[];
  removeLabels: string[];
  locks: Partial<Record<LockableField, LockChoice>>;
}

export function emptyBulkForm(): BulkEditForm {
  return {
    ratingMode: 'keep', rating: '',
    genreMode: 'keep', genres: '',
    addLabels: [], removeLabels: [],
    locks: {},
  };
}

/** Item types each bulk-editable field applies to (mirrors
 *  internal/bulkedit.applicable); other selected items are skipped. */
export const BULK_FIELD_TYPES: Record<'content_rating' | 'genres', readonly string[]> = {
  content_rating: ['movie', 'show', 'episode', 'music_video', 'home_video', 'audiobook', 'book', 'podcast'],
  genres: ['movie', 'show', 'album', 'artist', 'music_video', 'audiobook', 'book', 'podcast'],
};

/** How many of the selected item types a field applies to. */
export function applicableCount(field: 'content_rating' | 'genres', types: readonly string[]): number {
  return types.filter((t) => BULK_FIELD_TYPES[field].includes(t)).length;
}

/** A client-side message for a form the server would reject, or ''. */
export function validateBulkForm(f: BulkEditForm): string {
  if (f.ratingMode === 'set' && !f.rating.trim()) return 'Enter a content rating, or choose Clear.';
  if (f.ratingMode === 'set' && f.rating.trim().length > 32) return 'Content rating is at most 32 characters.';
  if ((f.genreMode === 'add' || f.genreMode === 'remove') && parseGenres(f.genres).length === 0) {
    return 'Enter at least one genre.';
  }
  if (parseGenres(f.genres).length > 50) return 'At most 50 genres.';
  return '';
}

/** The PATCH body for ids, or null when the form changes nothing. */
export function buildBulkRequest(ids: readonly string[], f: BulkEditForm): BulkEditRequest | null {
  const req: BulkEditRequest = { item_ids: [...ids] };
  const set: NonNullable<BulkEditRequest['set']> = {};
  if (f.ratingMode === 'set') set.content_rating = f.rating.trim();
  if (f.ratingMode === 'clear') set.content_rating = null;
  const genres = parseGenres(f.genres);
  switch (f.genreMode) {
    case 'replace': set.genres = genres; break;
    case 'add': if (genres.length) req.add_genres = genres; break;
    case 'remove': if (genres.length) req.remove_genres = genres; break;
  }
  if (Object.keys(set).length) req.set = set;

  const add = uniqLabels(f.addLabels);
  const remove = uniqLabels(f.removeLabels);
  if (add.length) req.add_labels = add;
  if (remove.length) req.remove_labels = remove;

  const lock: string[] = [];
  const unlock: string[] = [];
  for (const [field, choice] of Object.entries(f.locks)) {
    if (choice === 'lock') lock.push(field);
    if (choice === 'unlock') unlock.push(field);
  }
  if (lock.length) req.lock = lock;
  if (unlock.length) req.unlock = unlock;

  const changes = Object.keys(req).length > 1;
  return changes && ids.length > 0 ? req : null;
}

function uniqLabels(list: readonly string[]): string[] {
  return [...new Set(list.map(normalizeLabel).filter(Boolean))];
}

/** One line for the result: what changed and what was skipped, and why. */
export function summarizeBulkResult(res: BulkEditResult, requested: number): string {
  const parts = [`Updated ${res.updated} of ${requested} item${requested === 1 ? '' : 's'}`];
  if (res.labels_added) parts.push(`labelled ${res.labels_added}`);
  if (res.labels_removed) parts.push(`unlabelled ${res.labels_removed}`);
  const counts = { locked: 0, not_applicable: 0, not_found: 0 };
  for (const s of res.skipped ?? []) counts[s.reason] = (counts[s.reason] ?? 0) + 1;
  const skipped: string[] = [];
  if (counts.locked) skipped.push(`${counts.locked} locked`);
  if (counts.not_applicable) skipped.push(`${counts.not_applicable} not applicable`);
  if (counts.not_found) skipped.push(`${counts.not_found} no longer there`);
  let line = parts.join(', ') + '.';
  if (skipped.length) line += ` Skipped: ${skipped.join(', ')}.`;
  return line;
}

const REASON_TEXT: Record<string, string> = {
  locked: 'locked',
  not_applicable: "doesn't apply to this type",
  not_found: 'no longer in the library',
};

export function skipReasonText(reason: string): string {
  return REASON_TEXT[reason] ?? reason;
}
