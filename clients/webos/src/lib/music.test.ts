import { describe, expect, it } from 'vitest';
import type { ArtistAppearance, MixTrack } from './api/types';
import { appearsOnAlbums, appearsOnSubtitle, canMix, mixQueue } from './music';

function appearance(id: string, album: string, extra: Partial<ArtistAppearance> = {}): ArtistAppearance {
  return {
    id,
    title: `Track ${id}`,
    role: 'featured',
    album_id: album,
    album_title: `Album ${album}`,
    album_updated_at: 0,
    ...extra,
  };
}

describe('appearsOnAlbums', () => {
  it('shows each album once, in the order the server lists them', () => {
    const albums = appearsOnAlbums([
      appearance('t1', 'new', { album_year: 2024, album_artist_title: 'Someone Else', album_poster_path: 'p/new' }),
      appearance('t2', 'old', { album_year: 1999 }),
      appearance('t3', 'new'),
    ]);
    expect(albums).toEqual([
      { id: 'new', title: 'Album new', year: 2024, posterPath: 'p/new', artist: 'Someone Else' },
      { id: 'old', title: 'Album old', year: 1999 },
    ]);
  });

  it('names a card by the album artist, else its year', () => {
    expect(appearsOnSubtitle({ id: 'a', title: 'A', artist: 'Various Artists', year: 2001 })).toBe('Various Artists');
    expect(appearsOnSubtitle({ id: 'a', title: 'A', year: 2001 })).toBe('2001');
    expect(appearsOnSubtitle({ id: 'a', title: 'A' })).toBeUndefined();
  });

  it('is empty for nothing', () => {
    expect(appearsOnAlbums([])).toEqual([]);
    expect(appearsOnAlbums(undefined)).toEqual([]);
  });
});

describe('mixQueue / canMix', () => {
  const track = (id: string): MixTrack => ({ id, title: `T${id}`, album_id: 'a', album_title: 'A', file_id: `f${id}` });

  it('queues the mix as tracks, in its order', () => {
    expect(mixQueue([track('2'), track('1')])).toEqual([
      { id: '2', title: 'T2', type: 'track' },
      { id: '1', title: 'T1', type: 'track' },
    ]);
    expect(mixQueue([])).toEqual([]);
    expect(mixQueue(null)).toEqual([]);
  });

  it('mixes from an artist, an album or a track only', () => {
    expect(['artist', 'album', 'track'].every(canMix)).toBe(true);
    expect(canMix('movie')).toBe(false);
    expect(canMix('music_video')).toBe(false);
    expect(canMix(undefined)).toBe(false);
  });
});
