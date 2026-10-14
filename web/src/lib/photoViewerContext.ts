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
// The context decides what Previous / Next walk (the library's photos, the
// album's, the photos at the map spot, the person's, the memory's) and
// where closing the viewer goes. The query values only ever become path
// segments or query values of our own routes (encoded), and anything that
// doesn't look like an id is ignored.

export type PhotoViewerContext =
  | { kind: 'library' }
  | { kind: 'album'; albumId: string }
  | { kind: 'map'; libraryId: string }
  | { kind: 'person'; personId: string }
  | { kind: 'memory'; key: string };

const ID_RE = /^[A-Za-z0-9-]{1,64}$/;
const MEMORY_KEY_RE = /^(\d{4}-\d{2}-\d{2})-(\d{1,2})$/;

export function viewerContextFromURL(url: URL): PhotoViewerContext {
  const album = url.searchParams.get('album');
  if (album && ID_RE.test(album)) return { kind: 'album', albumId: album };
  const map = url.searchParams.get('map');
  if (map && ID_RE.test(map)) return { kind: 'map', libraryId: map };
  const person = url.searchParams.get('person');
  if (person && ID_RE.test(person)) return { kind: 'person', personId: person };
  const memory = url.searchParams.get('memory');
  if (memory && MEMORY_KEY_RE.test(memory)) return { kind: 'memory', key: memory };
  return { kind: 'library' };
}

/** The viewer URL for a photo, keeping the context. */
export function photoViewerHref(photoId: string, ctx: PhotoViewerContext): string {
  const base = `/photos/${encodeURIComponent(photoId)}`;
  switch (ctx.kind) {
    case 'album':  return `${base}?album=${encodeURIComponent(ctx.albumId)}`;
    case 'map':    return `${base}?map=${encodeURIComponent(ctx.libraryId)}`;
    case 'person': return `${base}?person=${encodeURIComponent(ctx.personId)}`;
    case 'memory': return `${base}?memory=${encodeURIComponent(ctx.key)}`;
    default:       return base;
  }
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
 *  only when that changes (not on every Previous / Next). */
export function contextKey(ctx: PhotoViewerContext, libraryId: string): string {
  switch (ctx.kind) {
    case 'album':  return `album:${ctx.albumId}`;
    case 'map':    return `map:${ctx.libraryId}`;
    case 'person': return `person:${ctx.personId}`;
    case 'memory': return `memory:${ctx.key}`;
    default:       return `library:${libraryId}`;
  }
}
