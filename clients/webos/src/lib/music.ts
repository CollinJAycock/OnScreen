// Music discovery (v2.6) on an artist page and the Instant mix: what the
// server's /music endpoints answer, shaped for the TV. Pure; unit-tested.

import { t } from './i18n/index.svelte';
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
  for (const a of tracks) {
    if (!a || !a.album_id || seen.has(a.album_id)) continue;
    seen.add(a.album_id);
    const album: AppearsOnAlbum = { id: a.album_id, title: a.album_title || t('music.unknownAlbum') };
    if (a.album_year != null) album.year = a.album_year;
    if (a.album_poster_path) album.posterPath = a.album_poster_path;
    if (a.album_artist_title) album.artist = a.album_artist_title;
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
