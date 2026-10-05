// Which child a container's Play button starts. Pure (no Svelte, no network
// beyond the injected fetcher, randomness injected) so the picks are unit-
// tested. Mirrors the Android TV detail page:
//   - album / podcast / multi-file audiobook (and a season on a server
//     without up-next): DetailFragment's inProgressEpisode() ?:
//     firstUnwatchedEpisode() ?: firstEpisode();
//   - artist Play All / Shuffle: DetailViewModel.resolvePlayAllStart /
//     resolveShuffleStart. The player's auto-advance carries on from there,
//     so one start id is all these need: no queue.

import type { ChildItem } from './api/types';

/** A random source in [0, 1): Math.random in the app, seeded in tests. */
export type Rng = () => number;

/** Loads an item's children (GET /items/{id}/children). */
export type FetchChildren = (id: string) => Promise<ChildItem[]>;

type Ordered = Pick<ChildItem, 'disc_number' | 'index'>;

/**
 * Children in play order: disc (absent = disc 1), then number (absent =
 * after every numbered one), listing order breaking ties. The same order as
 * Android's ChildItem.PLAY_ORDER and the server's children query. The sort
 * is made stable by hand: unnumbered audiobook chapters and podcast episodes
 * must keep the server's order, and the TV's old Chromium shouldn't be
 * trusted to sort stably.
 */
export function inPlayOrder<T extends Ordered>(children: readonly T[]): T[] {
  return children
    .map((child, at) => ({ child, at }))
    .sort((a, b) => {
      const disc = (a.child.disc_number ?? 1) - (b.child.disc_number ?? 1);
      if (disc !== 0) return disc;
      const ia = a.child.index ?? Number.MAX_SAFE_INTEGER;
      const ib = b.child.index ?? Number.MAX_SAFE_INTEGER;
      if (ia !== ib) return ia - ib;
      return a.at - b.at;
    })
    .map((x) => x.child);
}

export interface ContainerStart {
  child: ChildItem;
  /** Where to start: the child's resume point when it's part-played, else 0. */
  startMs: number;
}

/**
 * Play on a container whose children are playable (album tracks, podcast
 * episodes, audiobook chapters): the part-played child, else the first one
 * not played yet, else the first (everything played: start over). Null when
 * there are no children.
 */
export function pickContainerStart(children: readonly ChildItem[]): ContainerStart | null {
  const ordered = inPlayOrder(children);
  const inProgress = ordered.find((c) => !c.watched && (c.view_offset_ms ?? 0) > 0);
  if (inProgress) return { child: inProgress, startMs: inProgress.view_offset_ms ?? 0 };
  const unplayed = ordered.find((c) => !c.watched);
  if (unplayed) return { child: unplayed, startMs: 0 };
  return ordered.length > 0 ? { child: ordered[0], startMs: 0 } : null;
}

/**
 * An artist's albums in Play All order: oldest first, albums without a year
 * after every dated one, then the album's own index. Only `album` children
 * count; an artist's loose music videos never start Play All.
 */
export function playAllAlbumOrder(artistChildren: readonly ChildItem[]): ChildItem[] {
  return artistChildren
    .filter((c) => c.type === 'album')
    .map((album, at) => ({ album, at }))
    .sort((a, b) => {
      const ya = a.album.year ?? Number.MAX_SAFE_INTEGER;
      const yb = b.album.year ?? Number.MAX_SAFE_INTEGER;
      if (ya !== yb) return ya - yb;
      const ia = a.album.index ?? Number.MAX_SAFE_INTEGER;
      const ib = b.album.index ?? Number.MAX_SAFE_INTEGER;
      if (ia !== ib) return ia - ib;
      return a.at - b.at;
    })
    .map((x) => x.album);
}

/** The first numbered track of an album in (disc, track) order, or null.
 *  Unnumbered tracks are skipped like Android's resolvePlayAllStart: with no
 *  number there's no telling which one opens the album. */
export function firstTrack(albumChildren: readonly ChildItem[]): ChildItem | null {
  const numbered = albumChildren.filter((c) => c.type === 'track' && c.index != null);
  return inPlayOrder(numbered)[0] ?? null;
}

/** A Fisher–Yates shuffled copy. */
export function shuffled<T>(list: readonly T[], rng: Rng): T[] {
  const out = list.slice();
  for (let i = out.length - 1; i > 0; i--) {
    const j = Math.min(i, Math.floor(rng() * (i + 1)));
    const t = out[i];
    out[i] = out[j];
    out[j] = t;
  }
  return out;
}

/** One element at random, or null for an empty list. */
export function pickRandom<T>(list: readonly T[], rng: Rng): T | null {
  if (list.length === 0) return null;
  return list[Math.min(list.length - 1, Math.floor(rng() * list.length))];
}

/**
 * Play All on an artist page: the first track of the chronologically first
 * album. Albums are fetched one at a time in that order, so an album with no
 * numbered tracks moves on to the next instead of leaving the button dead.
 * Null when no album has a track. Fetch errors propagate (the page maps them).
 */
export async function resolvePlayAll(
  artistChildren: readonly ChildItem[],
  fetchChildren: FetchChildren,
): Promise<ChildItem | null> {
  for (const album of playAllAlbumOrder(artistChildren)) {
    const track = firstTrack(await fetchChildren(album.id));
    if (track) return track;
  }
  return null;
}

/**
 * Shuffle on an artist page: a random track from a random album. Only one
 * album's tracks are fetched in the common case (Android fetches every
 * album, one request each, to pick uniformly; on the TV that's a visible
 * stall for a big discography). An album with no tracks moves on to the next
 * album in the shuffled order. Play then continues in order from that track.
 */
export async function resolveShuffle(
  artistChildren: readonly ChildItem[],
  fetchChildren: FetchChildren,
  rng: Rng = Math.random,
): Promise<ChildItem | null> {
  const albums = shuffled(artistChildren.filter((c) => c.type === 'album'), rng);
  for (const album of albums) {
    const tracks = (await fetchChildren(album.id)).filter((c) => c.type === 'track');
    const pick = pickRandom(tracks, rng);
    if (pick) return pick;
  }
  return null;
}
