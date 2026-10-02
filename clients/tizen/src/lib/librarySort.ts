// Library grid sort options. Pure; mirrors the Android TV LibraryFragment
// (SORT_OPTIONS) and LibraryViewModel (LibrarySort.defaultFor) so a user
// moving between clients sees the same six choices with the same names.

export interface LibrarySort {
  /** Server sort field: title | created_at | year | rating. */
  sort: string;
  dir: 'asc' | 'desc';
}

export const SORT_OPTIONS: ReadonlyArray<LibrarySort & { label: string }> = [
  { sort: 'title', dir: 'asc', label: 'Title (A–Z)' },
  { sort: 'title', dir: 'desc', label: 'Title (Z–A)' },
  { sort: 'created_at', dir: 'desc', label: 'Recently added' },
  { sort: 'year', dir: 'desc', label: 'Newest year' },
  { sort: 'year', dir: 'asc', label: 'Oldest year' },
  { sort: 'rating', dir: 'desc', label: 'Highest rated' },
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
  return SORT_OPTIONS.find((o) => o.sort === s.sort && o.dir === s.dir)?.label ?? 'Sort';
}

export function sameSort(a: LibrarySort, b: LibrarySort): boolean {
  return a.sort === b.sort && a.dir === b.dir;
}
