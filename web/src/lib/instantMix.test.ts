import { describe, it, expect, beforeEach, vi } from 'vitest';
import { get } from 'svelte/store';

const { mixMock } = vi.hoisted(() => ({ mixMock: vi.fn() }));
vi.mock('./api', () => ({ musicApi: { mix: mixMock } }));

import { mixQueue, startInstantMix, groupAppearances, replayGainSourceLabel } from './instantMix';
import { audio } from './stores/audio';
import type { ArtistAppearance, MixTrack } from './api';

const track = (id: string, extra: Partial<MixTrack> = {}): MixTrack => ({
  id,
  title: `Title ${id}`,
  album_id: `album-${id}`,
  album_title: `Album ${id}`,
  file_id: `file-${id}`,
  ...extra
});

beforeEach(() => {
  audio.clear();
  mixMock.mockReset();
});

describe('mixQueue', () => {
  it('maps mix tracks to player entries with their ReplayGain', () => {
    const [q] = mixQueue([
      track('a', {
        artist_id: 'artist-a',
        artist_title: 'Artist A',
        album_poster_path: 'a/poster.jpg',
        duration_ms: 1000,
        replaygain_track_gain: -6.5,
        replaygain_track_peak: 0.98,
        replaygain_album_gain: -7
      })
    ]);
    expect(q).toMatchObject({
      id: 'a',
      fileId: 'file-a',
      title: 'Title a',
      album: 'Album a',
      albumId: 'album-a',
      artist: 'Artist A',
      artistId: 'artist-a',
      posterPath: 'a/poster.jpg',
      durationMS: 1000
    });
    expect(q.replayGain).toEqual({ trackGain: -6.5, trackPeak: 0.98, albumGain: -7 });
  });

  it('gives a track without ReplayGain an empty (unity) entry and drops fileless tracks', () => {
    const q = mixQueue([track('a'), track('b', { file_id: '' })]);
    expect(q).toHaveLength(1);
    expect(q[0].replayGain).toEqual({});
  });
});

describe('startInstantMix', () => {
  it('replaces the queue with the mix and starts at its first track', async () => {
    mixMock.mockResolvedValue([track('seed'), track('b'), track('c')]);
    const n = await startInstantMix('seed');
    expect(mixMock).toHaveBeenCalledWith('seed');
    expect(n).toBe(3);
    const s = get(audio);
    expect(s.queue.map((t) => t.id)).toEqual(['seed', 'b', 'c']);
    expect(s.index).toBe(0);
    expect(s.playing).toBe(true);
  });

  it('leaves the current queue alone when the mix is empty', async () => {
    audio.play([{ id: 'x', fileId: 'fx', title: 'X' }], 0);
    mixMock.mockResolvedValue([]);
    expect(await startInstantMix('seed')).toBe(0);
    expect(get(audio).queue.map((t) => t.id)).toEqual(['x']);
  });
});

describe('groupAppearances', () => {
  const app = (id: string, album: string, extra: Partial<ArtistAppearance> = {}): ArtistAppearance => ({
    id,
    title: id,
    role: 'featured',
    album_id: album,
    album_title: `Album ${album}`,
    album_updated_at: 1,
    ...extra
  });

  it('groups by album in server order', () => {
    const groups = groupAppearances([
      app('t1', 'new', { album_year: 2020, album_artist_title: 'Host' }),
      app('t2', 'old'),
      app('t3', 'new')
    ]);
    expect(groups.map((g) => [g.albumId, g.tracks.map((t) => t.id)])).toEqual([
      ['new', ['t1', 't3']],
      ['old', ['t2']]
    ]);
    expect(groups[0]).toMatchObject({ albumTitle: 'Album new', albumYear: 2020, albumArtistTitle: 'Host' });
  });

  it('is empty for no appearances', () => {
    expect(groupAppearances([])).toEqual([]);
  });
});

describe('replayGainSourceLabel', () => {
  it('names where the values came from', () => {
    expect(replayGainSourceLabel({ replaygain_source: 'tags' })).toBe('from tags');
    expect(replayGainSourceLabel({ replaygain_source: 'analyzed' })).toBe('analyzed');
    expect(replayGainSourceLabel({})).toBe('');
    expect(replayGainSourceLabel(undefined)).toBe('');
  });
});
