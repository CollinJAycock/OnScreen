import {
  MUSIC_SORTS,
  defaultSortAsc,
  sortOffered,
  musicItemType,
  musicViewFromURL,
  musicBrowseURL,
  facetItemType,
  libraryGridHref,
  type MusicBrowseState,
} from './musicBrowse';

const u = (qs: string) => new URL(`http://localhost/libraries/lib-1${qs}`);

const base: MusicBrowseState = {
  view: 'albums',
  sort: 'title',
  sortAsc: true,
  genre: '',
  yearMin: '',
  yearMax: '',
};

describe('musicViewFromURL', () => {
  it('reads ?view=', () => {
    expect(musicViewFromURL(u('?view=albums'))).toBe('albums');
    expect(musicViewFromURL(u('?view=artists&genre=Rock'))).toBe('artists');
  });

  it('defaults to artists', () => {
    expect(musicViewFromURL(u(''))).toBe('artists');
    expect(musicViewFromURL(u('?view=bogus'))).toBe('artists');
    expect(musicViewFromURL(u('?sort=created_at&sort_dir=desc'))).toBe('artists');
  });

  it('opens albums for a genre or year link that names no view', () => {
    expect(musicViewFromURL(u('?genre=Rock'))).toBe('albums');
    expect(musicViewFromURL(u('?year_min=1990&year_max=1999'))).toBe('albums');
    expect(musicViewFromURL(u('?year_max=1969'))).toBe('albums');
  });
});

describe('musicBrowseURL', () => {
  it('writes the view and leaves the default sort out', () => {
    const out = musicBrowseURL(u('?sort=year&sort_dir=desc'), base);
    expect(out.pathname).toBe('/libraries/lib-1');
    expect(out.search).toBe('?view=albums');
  });

  it('writes a non-default sort with its direction', () => {
    const out = musicBrowseURL(u(''), { ...base, sort: 'artist', sortAsc: false });
    expect(out.searchParams.get('sort')).toBe('artist');
    expect(out.searchParams.get('sort_dir')).toBe('desc');
    // Title Z→A is not the default either.
    const zToA = musicBrowseURL(u(''), { ...base, sortAsc: false });
    expect(zToA.searchParams.get('sort')).toBe('title');
    expect(zToA.searchParams.get('sort_dir')).toBe('desc');
  });

  it('sets and clears genre and years, keeping unrelated params', () => {
    const set = musicBrowseURL(u('?foo=1'), { ...base, genre: 'Hip Hop', yearMin: '1990', yearMax: '1999' });
    expect(set.searchParams.get('genre')).toBe('Hip Hop');
    expect(set.searchParams.get('year_min')).toBe('1990');
    expect(set.searchParams.get('year_max')).toBe('1999');
    expect(set.searchParams.get('foo')).toBe('1');

    const cleared = musicBrowseURL(set, { ...base, view: 'artists' });
    expect(cleared.search).toBe('?foo=1&view=artists');
  });

  it('round-trips through musicViewFromURL', () => {
    for (const view of ['artists', 'albums'] as const) {
      expect(musicViewFromURL(musicBrowseURL(u('?genre=Jazz'), { ...base, view, genre: 'Jazz' }))).toBe(view);
    }
  });

  it('does not modify the URL it was given', () => {
    const src = u('?genre=Rock');
    musicBrowseURL(src, base);
    expect(src.search).toBe('?genre=Rock');
  });
});

describe('sort pills', () => {
  it('offers Artist on albums and Rating on artists', () => {
    expect(MUSIC_SORTS.albums.map(([f]) => f)).toEqual(['title', 'artist', 'year', 'created_at']);
    expect(MUSIC_SORTS.artists.map(([f]) => f)).toEqual(['title', 'year', 'rating', 'created_at']);
    expect(sortOffered('albums', 'artist')).toBe(true);
    expect(sortOffered('artists', 'artist')).toBe(false);
    expect(sortOffered('albums', 'rating')).toBe(false);
    expect(sortOffered('artists', 'created_at')).toBe(true);
  });

  it('sorts names A→Z and years / dates newest first on the first click', () => {
    expect(defaultSortAsc('title')).toBe(true);
    expect(defaultSortAsc('artist')).toBe(true);
    expect(defaultSortAsc('year')).toBe(false);
    expect(defaultSortAsc('created_at')).toBe(false);
  });
});

describe('facets and links', () => {
  it('counts music facets on albums only', () => {
    expect(musicItemType('albums')).toBe('album');
    expect(musicItemType('artists')).toBe('artist');
    expect(facetItemType('music')).toBe('album');
    expect(facetItemType('movie')).toBeUndefined();
    expect(facetItemType(undefined)).toBeUndefined();
  });

  it('opens a music library link on the albums view', () => {
    expect(libraryGridHref('lib-1', 'music', { genre: 'Drum & Bass' }))
      .toBe('/libraries/lib-1?view=albums&genre=Drum+%26+Bass');
    expect(libraryGridHref('lib-1', 'music', { sort: 'created_at', sort_dir: 'desc' }))
      .toBe('/libraries/lib-1?view=albums&sort=created_at&sort_dir=desc');
  });

  it('leaves other libraries as they were', () => {
    expect(libraryGridHref('lib-2', 'movie', { year_min: 1990, year_max: 1999 }))
      .toBe('/libraries/lib-2?year_min=1990&year_max=1999');
    expect(libraryGridHref('lib-2', undefined, { genre: 'Drama' })).toBe('/libraries/lib-2?genre=Drama');
  });
});
