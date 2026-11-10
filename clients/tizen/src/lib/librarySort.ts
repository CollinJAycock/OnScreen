// Library grid sort options. Pure; mirrors the Android TV LibraryFragment
// (SORT_OPTIONS) and LibraryViewModel (LibrarySort.defaultFor) so a user
// moving between clients sees the same six choices with the same names.

import { t, type MessageKey } from './i18n/index.svelte';

export interface LibrarySort {
  /** Server sort field: title | created_at | year | rating. */
  sort: string;
  dir: 'asc' | 'desc';
}

/** An option whose label is read in the app's language each time it's
 *  shown, never kept (lib/i18n). */
function option(sort: string, dir: 'asc' | 'desc', key: MessageKey): LibrarySort & { readonly label: string } {
  return {
    sort,
    dir,
    get label() {
      return t(key);
    },
  };
}

export const SORT_OPTIONS: ReadonlyArray<LibrarySort & { readonly label: string }> = [
  option('title', 'asc', 'library.sortTitleAsc'),
  option('title', 'desc', 'library.sortTitleDesc'),
  option('created_at', 'desc', 'library.sortRecent'),
  option('year', 'desc', 'library.sortNewest'),
  option('year', 'asc', 'library.sortOldest'),
  option('rating', 'desc', 'library.sortRating'),
];

/**
 * The sort a library opens with. Home videos, photos and DVR recordings are
 * date-driven ("what I shot / recorded last" belongs on top), so they open
 * newest-added first; everything else browses alphabetically. created_at is
 * the closest the server's sort enum gets to a capture date.
 */
export function defaultSortFor(libraryType: string | null | undefined): LibrarySort {
  switch (libraryType) {
    case 'home_video':
    case 'photo':
    case 'dvr':
      return { sort: 'created_at', dir: 'desc' };
    default:
      return { sort: 'title', dir: 'asc' };
  }
}

/** The stored form of a sort ("title:asc"), for sessionStorage. */
export function sortKey(s: LibrarySort): string {
  return `${s.sort}:${s.dir}`;
}

/** A stored sort back to one of the six options; null for anything else
 *  (missing, corrupted, or an option a later build dropped). */
export function parseSort(stored: string | null | undefined): LibrarySort | null {
  if (!stored) return null;
  const hit = SORT_OPTIONS.find((o) => sortKey(o) === stored);
  return hit ? { sort: hit.sort, dir: hit.dir } : null;
}

export function sortLabel(s: LibrarySort): string {
  return SORT_OPTIONS.find((o) => o.sort === s.sort && o.dir === s.dir)?.label ?? t('library.sortFallback');
}

export function sameSort(a: LibrarySort, b: LibrarySort): boolean {
  return a.sort === b.sort && a.dir === b.dir;
}
