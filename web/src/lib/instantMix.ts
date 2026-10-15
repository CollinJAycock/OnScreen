// Instant mix, "Appears on" and ReplayGain-source helpers for the music
// pages. The pure parts (queue mapping, grouping, labels) are unit-tested
// in instantMix.test.ts; startInstantMix wires the server's mix into the
// existing music player queue.

import { musicApi, type ArtistAppearance, type ItemFile, type MixTrack } from './api';
import { audio, type AudioTrack } from './stores/audio';
import { replayGainFromFile } from './replaygain';

/** Turn the server's mix into player queue entries. Each track's ReplayGain
 *  rides along (the mix response carries it), so the player needs no
 *  per-track lookup before a track starts. */
export function mixQueue(tracks: MixTrack[]): AudioTrack[] {
  return tracks
    .filter((t) => !!t.file_id)
    .map((t) => ({
      id: t.id,
      fileId: t.file_id,
      title: t.title,
      durationMS: t.duration_ms,
      index: t.index,
      album: t.album_title,
      albumId: t.album_id,
      artist: t.artist_title,
      artistId: t.artist_id,
      posterPath: t.album_poster_path,
      replayGain: replayGainFromFile(t)
    }));
}

/** Fetch a fresh mix seeded with a track, album or artist and start playing
 *  it, replacing the queue. Resolves to the number of tracks queued; 0 when
 *  the server had nothing to mix (the queue is left alone then). */
export async function startInstantMix(seedId: string): Promise<number> {
  const queue = mixQueue(await musicApi.mix(seedId));
  if (queue.length === 0) return 0;
  audio.play(queue, 0);
  return queue.length;
}

export interface AppearanceGroup {
  albumId: string;
  albumTitle: string;
  albumYear?: number;
  albumPosterPath?: string;
  albumUpdatedAt: number;
  albumArtistId?: string;
  albumArtistTitle?: string;
  tracks: ArtistAppearance[];
}

/** Group "Appears on" tracks by album, keeping the server's order (newest
 *  album first, tracks in album order). */
export function groupAppearances(list: ArtistAppearance[]): AppearanceGroup[] {
  const groups: AppearanceGroup[] = [];
  const byAlbum = new Map<string, AppearanceGroup>();
  for (const a of list) {
    let g = byAlbum.get(a.album_id);
    if (!g) {
      g = {
        albumId: a.album_id,
        albumTitle: a.album_title,
        albumYear: a.album_year,
        albumPosterPath: a.album_poster_path,
        albumUpdatedAt: a.album_updated_at,
        albumArtistId: a.album_artist_id,
        albumArtistTitle: a.album_artist_title,
        tracks: []
      };
      byAlbum.set(a.album_id, g);
      groups.push(g);
    }
    g.tracks.push(a);
  }
  return groups;
}

/** How a file's ReplayGain was obtained, for the track info panel:
 *  "from tags", "analyzed", or '' when unknown (no values, or a server
 *  that predates the field). */
export function replayGainSourceLabel(file: Pick<ItemFile, 'replaygain_source'> | null | undefined): string {
  switch (file?.replaygain_source) {
    case 'tags':
      return 'from tags';
    case 'analyzed':
      return 'analyzed';
    default:
      return '';
  }
}
