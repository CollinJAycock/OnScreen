import {
  contextKey,
  photoViewerHref,
  viewerCloseHref,
  viewerContextFromURL,
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
