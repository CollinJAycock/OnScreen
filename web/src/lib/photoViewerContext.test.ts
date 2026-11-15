import {
  contextKey,
  photoViewerHref,
  viewerCloseHref,
  viewerContextFromURL,
  withListPosition,
} from './photoViewerContext';

const url = (qs: string) => new URL(`http://localhost/photos/p-1${qs}`);

describe('viewerContextFromURL', () => {
  it('defaults to the library', () => {
    expect(viewerContextFromURL(url(''))).toEqual({ kind: 'library' });
  });

  it('reads ?album= and ?map=', () => {
    expect(viewerContextFromURL(url('?album=alb-1'))).toEqual({ kind: 'album', albumId: 'alb-1' });
    expect(viewerContextFromURL(url('?map=lib-1'))).toEqual({ kind: 'map', libraryId: 'lib-1' });
  });

  it('prefers the album when both are present', () => {
    expect(viewerContextFromURL(url('?map=lib-1&album=alb-1')).kind).toBe('album');
  });

  it('ignores values that are not ids', () => {
    expect(viewerContextFromURL(url('?album=../../settings'))).toEqual({ kind: 'library' });
    expect(viewerContextFromURL(url('?map=https://evil.example'))).toEqual({ kind: 'library' });
    expect(viewerContextFromURL(url('?album='))).toEqual({ kind: 'library' });
  });
});

describe('photoViewerHref', () => {
  it('keeps the context on the next photo', () => {
    expect(photoViewerHref('p-2', { kind: 'library' })).toBe('/photos/p-2');
    expect(photoViewerHref('p-2', { kind: 'album', albumId: 'alb-1' })).toBe('/photos/p-2?album=alb-1');
    expect(photoViewerHref('p-2', { kind: 'map', libraryId: 'lib-1' })).toBe('/photos/p-2?map=lib-1');
  });
});

describe('viewerCloseHref', () => {
  it('returns to where the viewer was opened from', () => {
    expect(viewerCloseHref({ kind: 'library' }, 'lib-1')).toBe('/libraries/lib-1');
    expect(viewerCloseHref({ kind: 'library' })).toBe('/');
    expect(viewerCloseHref({ kind: 'album', albumId: 'alb-1' }, 'lib-1')).toBe('/photos/albums/alb-1');
    expect(viewerCloseHref({ kind: 'map', libraryId: 'lib-9' }, 'lib-1')).toBe('/photos/map?library=lib-9');
  });
});

describe('contextKey', () => {
  it('names the sibling list, per library for the library context', () => {
    expect(contextKey({ kind: 'library' }, 'lib-1')).toBe('library:lib-1');
    expect(contextKey({ kind: 'library' }, 'lib-2')).not.toBe(contextKey({ kind: 'library' }, 'lib-1'));
    expect(contextKey({ kind: 'album', albumId: 'a' }, 'lib-1')).toBe('album:a');
    expect(contextKey({ kind: 'map', libraryId: 'lib-1' }, 'lib-1')).toBe('map:lib-1');
  });
});

describe('the position in a listing', () => {
  it('round-trips a library grid position with its sort and filters', () => {
    const ctx = { kind: 'library' as const, pos: 3000, params: { sort: 'taken_at' as const, sort_dir: 'desc' as const, year_min: 2019 } };
    const href = photoViewerHref('p-2', ctx);
    expect(href).toBe('/photos/p-2?pos=3000&sort=taken_at&sort_dir=desc&year_min=2019');
    expect(viewerContextFromURL(new URL(`http://localhost${href}`))).toEqual(ctx);
  });

  it("round-trips a person's grid position", () => {
    const href = photoViewerHref('p-2', { kind: 'person', personId: 'per-1', pos: 12 });
    expect(href).toBe('/photos/p-2?person=per-1&pos=12');
    expect(viewerContextFromURL(new URL(`http://localhost${href}`))).toEqual({ kind: 'person', personId: 'per-1', pos: 12 });
  });

  it('ignores positions and sorts that are not what the grid sends', () => {
    expect(viewerContextFromURL(url('?pos=-1'))).toEqual({ kind: 'library' });
    expect(viewerContextFromURL(url('?pos=1e9'))).toEqual({ kind: 'library' });
    expect(viewerContextFromURL(url('?pos=5&sort=drop_table&sort_dir=sideways&year_min=19999'))).toEqual({ kind: 'library', pos: 5 });
  });

  it('adds a position only where the context has a listing', () => {
    expect(withListPosition({ kind: 'library' }, 4)).toEqual({ kind: 'library', pos: 4 });
    expect(withListPosition({ kind: 'album', albumId: 'a' }, 4)).toEqual({ kind: 'album', albumId: 'a' });
  });

  it('keys a library list by its sort and filters, not the position', () => {
    const a = contextKey({ kind: 'library', pos: 1, params: { sort: 'title', sort_dir: 'asc' } }, 'lib-1');
    const b = contextKey({ kind: 'library', pos: 900, params: { sort: 'title', sort_dir: 'asc' } }, 'lib-1');
    expect(a).toBe(b);
    expect(a).not.toBe(contextKey({ kind: 'library', pos: 1, params: { sort: 'taken_at', sort_dir: 'desc' } }, 'lib-1'));
  });
});
