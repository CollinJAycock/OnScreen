// Music library browsing: the Artists / Albums switch on a music library's
// page, the albums index's sort pills, and the URL that carries both.
//
// Artists are the music library's top-level rows; albums hang off them. The
// albums index lists every album in the library through the same items
// endpoint (`?type=album`), where each row also carries its artist
// (`parent_id` / `parent_title`). Genres and years live on the album rows —
// read from the files' tags — so genre/year browsing on a music library
// always lands on albums.

import type { SortField } from '$lib/api';

export type MusicView = 'artists' | 'albums';

// Sort pills per view. Artists keep the pills every other library shows;
// albums swap Rating (albums carry none) for Artist.
export const MUSIC_SORTS: Record<MusicView, ReadonlyArray<readonly [SortField, string]>> = {
  artists: [['title', 'Title'], ['year', 'Year'], ['rating', 'Rating'], ['created_at', 'Added']],
  albums: [['title', 'Title'], ['artist', 'Artist'], ['year', 'Year'], ['created_at', 'Added']],
};

// First click on a pill: A→Z for names, newest first for years and dates.
export function defaultSortAsc(field: SortField): boolean {
  return field === 'title' || field === 'artist';
}

export function sortOffered(view: MusicView, field: SortField): boolean {
  return MUSIC_SORTS[view].some(([f]) => f === field);
}

// The item type the grid lists, and the genre/year facets count, per view.
export function musicItemType(view: MusicView): 'artist' | 'album' {
  return view === 'albums' ? 'album' : 'artist';
}

// musicViewFromURL reads ?view=. A link that filters by genre or year but
// names no view — a bookmark from before the albums index — opens albums,
// where those facets live; artists carry neither.
export function musicViewFromURL(url: URL): MusicView {
  const sp = url.searchParams;
  const v = sp.get('view');
  if (v === 'albums' || v === 'artists') return v;
  return sp.has('genre') || sp.has('year_min') || sp.has('year_max') ? 'albums' : 'artists';
}

export interface MusicBrowseState {
  view: MusicView;
  sort: SortField;
  sortAsc: boolean;
  genre: string;
  yearMin: string;
  yearMax: string;
}

// musicBrowseURL writes the browse state onto a copy of `base`, leaving any
// other params alone. The default sort (title A→Z) is left out.
export function musicBrowseURL(base: URL, s: MusicBrowseState): URL {
  const u = new URL(base);
  const sp = u.searchParams;
  sp.set('view', s.view);
  if (s.sort === 'title' && s.sortAsc) {
    sp.delete('sort');
    sp.delete('sort_dir');
  } else {
    sp.set('sort', s.sort);
    sp.set('sort_dir', s.sortAsc ? 'asc' : 'desc');
  }
  setOrDelete(sp, 'genre', s.genre);
  setOrDelete(sp, 'year_min', s.yearMin);
  setOrDelete(sp, 'year_max', s.yearMax);
  return u;
}

function setOrDelete(sp: URLSearchParams, key: string, value: string) {
  if (value) sp.set(key, value);
  else sp.delete(key);
}

// The item type a library's genre / year browse pages count: albums on a
// music library, the library's root type (undefined → server default)
// everywhere else.
export function facetItemType(libraryType: string | undefined): 'album' | undefined {
  return libraryType === 'music' ? 'album' : undefined;
}

// libraryGridHref links into a library page's grid with filters or a sort
// preselected (genre/year browse tiles, the home page's Recently Added
// header). On a music library it opens the albums view: that is where the
// genres and years are, and what Recently Added shows.
export function libraryGridHref(
  libraryId: string,
  libraryType: string | undefined,
  params: Record<string, string | number>,
): string {
  const qs = new URLSearchParams();
  if (libraryType === 'music') qs.set('view', 'albums');
  for (const [k, v] of Object.entries(params)) qs.set(k, String(v));
  return `/libraries/${libraryId}?${qs.toString()}`;
}
