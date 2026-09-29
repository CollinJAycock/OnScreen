// Where the photo viewer (/photos/[id]) was opened from, carried in its
// query string so a reload or a shared link keeps it:
//
//   /photos/{id}               a photo library (the default)
//   /photos/{id}?album={id}    a photo album
//   /photos/{id}?map={libId}   the photo map
//
// The context decides what Previous / Next walk (the library's photos, the
// album's, or the photos at the map spot) and where closing the viewer
// goes. The query values only ever become path segments of our own routes
// (encoded), and anything that doesn't look like an id is ignored.

export type PhotoViewerContext =
  | { kind: 'library' }
  | { kind: 'album'; albumId: string }
  | { kind: 'map'; libraryId: string };

const ID_RE = /^[A-Za-z0-9-]{1,64}$/;

export function viewerContextFromURL(url: URL): PhotoViewerContext {
  const album = url.searchParams.get('album');
  if (album && ID_RE.test(album)) return { kind: 'album', albumId: album };
  const map = url.searchParams.get('map');
  if (map && ID_RE.test(map)) return { kind: 'map', libraryId: map };
  return { kind: 'library' };
}

/** The viewer URL for a photo, keeping the context. */
export function photoViewerHref(photoId: string, ctx: PhotoViewerContext): string {
  const base = `/photos/${encodeURIComponent(photoId)}`;
  switch (ctx.kind) {
    case 'album': return `${base}?album=${encodeURIComponent(ctx.albumId)}`;
    case 'map':   return `${base}?map=${encodeURIComponent(ctx.libraryId)}`;
    default:      return base;
  }
}

/** Where closing the viewer goes: back to the album, the map, or the
 *  photo's library (home when that isn't known yet). */
export function viewerCloseHref(ctx: PhotoViewerContext, libraryId?: string): string {
  switch (ctx.kind) {
    case 'album': return `/photos/albums/${encodeURIComponent(ctx.albumId)}`;
    case 'map':   return `/photos/map?library=${encodeURIComponent(ctx.libraryId)}`;
    default:      return libraryId ? `/libraries/${encodeURIComponent(libraryId)}` : '/';
  }
}

/** Identifies the sibling list a context walks, so the viewer refetches
 *  only when that changes (not on every Previous / Next). */
export function contextKey(ctx: PhotoViewerContext, libraryId: string): string {
  switch (ctx.kind) {
    case 'album': return `album:${ctx.albumId}`;
    case 'map':   return `map:${ctx.libraryId}`;
    default:      return `library:${libraryId}`;
  }
}
