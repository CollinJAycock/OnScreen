// Music discovery (v2.6) on an artist page and the Instant mix: what the
// server's /music endpoints answer, shaped for the TV. Pure; unit-tested.

import type { ArtistAppearance, MixTrack } from './api/types';
import type { QueueEntry } from './player/queue';

/** An album by another artist that this one appears on. */
export interface AppearsOnAlbum {
  id: string;
  title: string;
  year?: number;
  posterPath?: string;
  /** The album's own artist ("Various Artists" for a compilation). */
  artist?: string;
}

/**
 * The "Appears on" row: the albums behind the artist's guest tracks, each
 * once, in the server's order (newest album first). An album of the
 * artist's own never shows here (the server lists only other artists'), and
 * one credited on several of its tracks is still one card.
 */
export function appearsOnAlbums(tracks: readonly ArtistAppearance[] | null | undefined): AppearsOnAlbum[] {
  if (!Array.isArray(tracks)) return [];
  const out: AppearsOnAlbum[] = [];
  const seen = new Set<string>();
  for (const t of tracks) {
    if (!t || !t.album_id || seen.has(t.album_id)) continue;
    seen.add(t.album_id);
    const album: AppearsOnAlbum = { id: t.album_id, title: t.album_title || 'Unknown album' };
    if (t.album_year != null) album.year = t.album_year;
    if (t.album_poster_path) album.posterPath = t.album_poster_path;
    if (t.album_artist_title) album.artist = t.album_artist_title;
    out.push(album);
  }
  return out;
}

/** An appears-on card's second line: the album's artist, else its year. */
export function appearsOnSubtitle(a: AppearsOnAlbum): string | undefined {
  return a.artist || (a.year != null ? String(a.year) : undefined);
}

/** An Instant mix as a play queue, in the server's order. */
export function mixQueue(tracks: readonly MixTrack[] | null | undefined): QueueEntry[] {
  if (!Array.isArray(tracks)) return [];
  return tracks.filter((t) => !!t && !!t.id).map((t) => ({ id: t.id, title: t.title, type: 'track' }));
}

/** The types an Instant mix can start from. */
export function canMix(type: string | null | undefined): boolean {
  return type === 'artist' || type === 'album' || type === 'track';
}
