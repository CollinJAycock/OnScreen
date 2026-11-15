// Where the photo viewer (/photos/[id]) was opened from, carried in its
// query string so a reload or a shared link keeps it:
//
//   /photos/{id}                   a photo library (the default)
//   /photos/{id}?album={id}        a photo album
//   /photos/{id}?map={libId}       the photo map
//   /photos/{id}?person={id}       a person's photos
//   /photos/{id}?memory={key}      a memory ("3 years ago today"); key is
//                                  YYYY-MM-DD-N, the date and group index
//
// A library or person grid also passes where the photo sits in its listing
// (?pos=, and for a library the sort and filters: sort, sort_dir, genre,
// year_min, year_max, rating_min), so the viewer fetches a bounded window of
// neighbours around it ($lib/photoNeighbours) instead of a whole library.
//
// The context decides what Previous / Next walk (the library's photos, the
// album's, the photos at the map spot, the person's, the memory's) and
// where closing the viewer goes. The query values only ever become path
// segments or query values of our own routes (encoded), and anything that
// doesn't look like an id (or a number, a sort field) is ignored.

import type { ListItemsParams, SortField } from './api';

export type PhotoViewerContext =
  | { kind: 'library'; pos?: number; params?: ListItemsParams }
  | { kind: 'album'; albumId: string }
  | { kind: 'map'; libraryId: string }
  | { kind: 'person'; personId: string; pos?: number }
  | { kind: 'memory'; key: string };

const ID_RE = /^[A-Za-z0-9-]{1,64}$/;
const MEMORY_KEY_RE = /^(\d{4}-\d{2}-\d{2})-(\d{1,2})$/;
const POS_RE = /^\d{1,7}$/;
const YEAR_RE = /^\d{4}$/;
const RATING_RE = /^\d{1,2}(\.\d{1,2})?$/;
const SORT_FIELDS: ReadonlySet<string> = new Set<SortField>(['title', 'year', 'rating', 'created_at', 'taken_at', 'artist']);

function readPos(url: URL): number | undefined {
  const pos = url.searchParams.get('pos');
  return pos && POS_RE.test(pos) ? Number(pos) : undefined;
}

// The library listing's sort and filters, as the grid passed them.
function readListParams(url: URL): ListItemsParams | undefined {
  const q = url.searchParams;
  const p: ListItemsParams = {};
  const sort = q.get('sort');
  if (sort && SORT_FIELDS.has(sort)) p.sort = sort as SortField;
  const dir = q.get('sort_dir');
  if (dir === 'asc' || dir === 'desc') p.sort_dir = dir;
  const genre = q.get('genre');
  if (genre && genre.length <= 100) p.genre = genre;
  const yearMin = q.get('year_min');
  if (yearMin && YEAR_RE.test(yearMin)) p.year_min = Number(yearMin);
  const yearMax = q.get('year_max');
  if (yearMax && YEAR_RE.test(yearMax)) p.year_max = Number(yearMax);
  const rating = q.get('rating_min');
  if (rating && RATING_RE.test(rating)) p.rating_min = Number(rating);
  return Object.keys(p).length ? p : undefined;
}

export function viewerContextFromURL(url: URL): PhotoViewerContext {
  const album = url.searchParams.get('album');
  if (album && ID_RE.test(album)) return { kind: 'album', albumId: album };
  const map = url.searchParams.get('map');
  if (map && ID_RE.test(map)) return { kind: 'map', libraryId: map };
  const person = url.searchParams.get('person');
  if (person && ID_RE.test(person)) {
    const pos = readPos(url);
    return pos === undefined ? { kind: 'person', personId: person } : { kind: 'person', personId: person, pos };
  }
  const memory = url.searchParams.get('memory');
  if (memory && MEMORY_KEY_RE.test(memory)) return { kind: 'memory', key: memory };
  const pos = readPos(url);
  if (pos === undefined) return { kind: 'library' };
  const params = readListParams(url);
  return params ? { kind: 'library', pos, params } : { kind: 'library', pos };
}

// The listing params as query pairs ('' when there are none).
function listQuery(params?: ListItemsParams): string {
  if (!params) return '';
  const q = new URLSearchParams();
  if (params.sort) q.set('sort', params.sort);
  if (params.sort_dir) q.set('sort_dir', params.sort_dir);
  if (params.genre) q.set('genre', params.genre);
  if (params.year_min != null) q.set('year_min', String(params.year_min));
  if (params.year_max != null) q.set('year_max', String(params.year_max));
  if (params.rating_min != null) q.set('rating_min', String(params.rating_min));
  return q.toString();
}

/** The viewer URL for a photo, keeping the context. */
export function photoViewerHref(photoId: string, ctx: PhotoViewerContext): string {
  const base = `/photos/${encodeURIComponent(photoId)}`;
  switch (ctx.kind) {
    case 'album':  return `${base}?album=${encodeURIComponent(ctx.albumId)}`;
    case 'map':    return `${base}?map=${encodeURIComponent(ctx.libraryId)}`;
    case 'person':
      return `${base}?person=${encodeURIComponent(ctx.personId)}${ctx.pos !== undefined ? `&pos=${ctx.pos}` : ''}`;
    case 'memory': return `${base}?memory=${encodeURIComponent(ctx.key)}`;
    default: {
      if (ctx.pos === undefined) return base;
      const q = listQuery(ctx.params);
      return `${base}?pos=${ctx.pos}${q ? `&${q}` : ''}`;
    }
  }
}

/** ctx with a photo's position in its listing (library and person only). */
export function withListPosition(ctx: PhotoViewerContext, pos: number): PhotoViewerContext {
  if (ctx.kind === 'library' || ctx.kind === 'person') return { ...ctx, pos };
  return ctx;
}

/** The memory key for a date's group: YYYY-MM-DD-N. */
export function memoryKey(date: string, index: number): string {
  return `${date}-${index}`;
}

/** The date a memory key names. */
export function memoryKeyDate(key: string): string {
  return MEMORY_KEY_RE.exec(key)?.[1] ?? '';
}

/** Where closing the viewer goes: back to the album, the map, the person,
 *  the memories, or the photo's library (home when that isn't known yet). */
export function viewerCloseHref(ctx: PhotoViewerContext, libraryId?: string): string {
  switch (ctx.kind) {
    case 'album':  return `/photos/albums/${encodeURIComponent(ctx.albumId)}`;
    case 'map':    return `/photos/map?library=${encodeURIComponent(ctx.libraryId)}`;
    case 'person': return `/photos/people/${encodeURIComponent(ctx.personId)}`;
    case 'memory': return `/photos/memories?date=${encodeURIComponent(memoryKeyDate(ctx.key))}`;
    default:       return libraryId ? `/libraries/${encodeURIComponent(libraryId)}` : '/';
  }
}

/** Identifies the sibling list a context walks, so the viewer refetches
 *  only when that changes (not on every Previous / Next). A library sorted
 *  or filtered differently is a different list; the position isn't part of
 *  the key. */
export function contextKey(ctx: PhotoViewerContext, libraryId: string): string {
  switch (ctx.kind) {
    case 'album':  return `album:${ctx.albumId}`;
    case 'map':    return `map:${ctx.libraryId}`;
    case 'person': return `person:${ctx.personId}`;
    case 'memory': return `memory:${ctx.key}`;
    default: {
      const q = listQuery(ctx.params);
      return q ? `library:${libraryId}?${q}` : `library:${libraryId}`;
    }
  }
}
