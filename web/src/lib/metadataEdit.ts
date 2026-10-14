// Admin metadata editor — pure helpers for ItemMetadataEditor. Server
// contract: PATCH /items/{id} (internal/api/v1/items.go UpdateMetadata).
// Every field the request sets is locked against refreshes; unlock_fields
// releases locks. Lock names are the media_items column names.

import type { ItemDetail, MetadataEdit } from './api';

export type LockableField =
  | 'title'
  | 'sort_title'
  | 'original_title'
  | 'year'
  | 'originally_available_at'
  | 'summary'
  | 'tagline'
  | 'rating'
  | 'content_rating'
  | 'genres';

/** The editor's fields in display order, with their labels. */
export const METADATA_FIELDS: readonly { field: LockableField; label: string }[] = [
  { field: 'title', label: 'Title' },
  { field: 'sort_title', label: 'Sort title' },
  { field: 'original_title', label: 'Original title' },
  { field: 'year', label: 'Year' },
  { field: 'originally_available_at', label: 'Release date' },
  { field: 'content_rating', label: 'Content rating' },
  { field: 'rating', label: 'Critic rating' },
  { field: 'genres', label: 'Genres' },
  { field: 'tagline', label: 'Tagline' },
  { field: 'summary', label: 'Summary' },
];

const LOCKABLE = new Set<string>(METADATA_FIELDS.map((f) => f.field));

/** Suggestions for the content-rating box. Free text is allowed; a rating
 *  the parental ceiling doesn't know ranks as most restrictive. */
export const CONTENT_RATING_SUGGESTIONS: readonly string[] = [
  'G', 'PG', 'PG-13', 'R', 'NC-17',
  'TV-Y', 'TV-Y7', 'TV-G', 'TV-PG', 'TV-14', 'TV-MA', 'NR',
];

/** The form's values, all strings as the inputs hold them. sort_title is ''
 *  while it just follows the title; genres is comma-separated. */
export interface MetadataForm {
  title: string;
  sort_title: string;
  original_title: string;
  year: string;
  originally_available_at: string; // YYYY-MM-DD
  summary: string;
  tagline: string;
  rating: string;
  content_rating: string;
  genres: string;
}

export function formFromItem(item: ItemDetail): MetadataForm {
  const sort = item.sort_title ?? '';
  return {
    title: item.title ?? '',
    sort_title: sort === '' || sort === item.title ? '' : sort,
    original_title: item.original_title ?? '',
    year: item.year != null ? String(item.year) : '',
    originally_available_at: item.taken_at ? item.taken_at.slice(0, 10) : '',
    summary: item.summary ?? '',
    tagline: item.tagline ?? '',
    rating: item.rating != null ? String(item.rating) : '',
    content_rating: item.content_rating ?? '',
    genres: (item.genres ?? []).join(', '),
  };
}

/** The item's locked fields that the editor knows about. */
export function lockedFields(item: Pick<ItemDetail, 'locked_fields'>): Set<LockableField> {
  return new Set((item.locked_fields ?? []).filter((f) => LOCKABLE.has(f)) as LockableField[]);
}

/** Splits the genres box on commas: trimmed, blanks and case-insensitive
 *  repeats dropped (the server does the same). */
export function parseGenres(s: string): string[] {
  const out: string[] = [];
  const seen = new Set<string>();
  for (const raw of s.split(',')) {
    const g = raw.trim();
    const k = g.toLowerCase();
    if (!g || seen.has(k)) continue;
    seen.add(k);
    out.push(g);
  }
  return out;
}

function sameGenres(a: string, b: string): boolean {
  const x = parseGenres(a);
  const y = parseGenres(b);
  return x.length === y.length && x.every((g, i) => g === y[i]);
}

/** Fields whose form value differs from the item's. */
export function changedFields(initial: MetadataForm, form: MetadataForm): LockableField[] {
  return METADATA_FIELDS.map((f) => f.field).filter((f) =>
    f === 'genres' ? !sameGenres(initial.genres, form.genres) : initial[f].trim() !== form[f].trim(),
  );
}

/** '' when the form can be saved, else what to fix. */
export function validateForm(form: MetadataForm): string {
  if (!form.title.trim()) return 'Title is required.';
  const year = form.year.trim();
  if (year && !/^\d{4}$/.test(year)) return 'Year must be a four-digit year.';
  const rating = form.rating.trim();
  if (rating) {
    const n = Number(rating);
    if (!Number.isFinite(n) || n < 0 || n > 10) return 'Critic rating must be between 0 and 10.';
  }
  return '';
}

/** The PATCH body: the changed fields (each will be locked) and the locks
 *  to release. A field that is both changed and marked for unlock is
 *  edited — the server refuses both at once. */
export function buildMetadataEdit(
  initial: MetadataForm,
  form: MetadataForm,
  unlock: ReadonlySet<LockableField>,
): MetadataEdit {
  const body: MetadataEdit = {};
  const changed = changedFields(initial, form);
  for (const f of changed) {
    const v = form[f].trim();
    switch (f) {
      case 'year':
        body.year = v ? Number(v) : null;
        break;
      case 'rating':
        body.rating = v ? Number(v) : null;
        break;
      case 'genres':
        body.genres = parseGenres(form.genres);
        break;
      case 'originally_available_at':
        body.taken_at = v;
        break;
      default:
        body[f] = v;
    }
  }
  const unlocks = [...unlock].filter((f) => !changed.includes(f));
  if (unlocks.length) body.unlock_fields = unlocks;
  return body;
}

/** Lock state a field's icon shows: 'locked' (refreshes keep it),
 *  'unlocking' (lock released on save), 'locking' (edited: locked on save),
 *  or 'open'. */
export type LockState = 'locked' | 'unlocking' | 'locking' | 'open';

export function lockState(
  field: LockableField,
  locked: ReadonlySet<LockableField>,
  unlock: ReadonlySet<LockableField>,
  changed: readonly LockableField[],
): LockState {
  if (changed.includes(field)) return 'locking';
  if (locked.has(field)) return unlock.has(field) ? 'unlocking' : 'locked';
  return 'open';
}

export function editErrorMessage(err: unknown): string {
  const e = err as { status?: number; message?: string } | null;
  if (e?.status === 409) return 'Another item in this library already has this title and year.';
  if (e?.status === 403) return 'Only admins can edit metadata.';
  if (e?.status === 404) return 'This item is no longer available.';
  return e?.message ? `Couldn't save: ${e.message}` : "Couldn't save.";
}
