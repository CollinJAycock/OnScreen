// Neighbours of the playing item: the Previous / Next track keys and the
// "Track N of M" line (findSiblings, from its parent's /children listing),
// and what auto-advance plays next (resolveNext, which can cross into the
// next season or album). Pure so the ordering rules are checkable without
// mounting the player.

import { stableSort } from '$lib/stableSort';
import type { ChildItem } from '../api/types';

/** Play order within a container: disc, then index. No disc_number means
 *  disc 1 (single-disc albums, every non-track, older servers), so for
 *  anything but a track this is plain index order. The server lists an
 *  album's tracks in this order, and the web album page sorts the same. */
export function playOrder(a: ChildItem, b: ChildItem): number {
  return (a.disc_number ?? 1) - (b.disc_number ?? 1) || (a.index ?? 0) - (b.index ?? 0);
}

export interface Siblings {
  next: ChildItem | null;
  prev: ChildItem | null;
  /** 1-indexed slot among the same-type siblings; 0 = not in the listing. */
  position: number;
  total: number;
}

export function findSiblings(
  kids: ChildItem[],
  current: { id: string; type: string; index?: number }
): Siblings {
  const sorted = kids
    .filter((k) => k.type === current.type && k.index != null)
    .sort(playOrder);
  const pos = sorted.findIndex((k) => k.id === current.id);
  let next: ChildItem | null;
  let prev: ChildItem | null;
  if (current.type === 'track') {
    // A multi-disc album numbers each disc from 1, so index +/- 1 never
    // crosses a disc boundary and can land on the other disc's track. Step
    // by position in (disc, index) order instead.
    next = pos >= 0 ? (sorted[pos + 1] ?? null) : null;
    prev = pos > 0 ? sorted[pos - 1] : null;
  } else {
    const myIdx = current.index ?? -1;
    next = sorted.find((k) => (k.index ?? -1) === myIdx + 1) ?? null;
    prev = sorted.find((k) => (k.index ?? -1) === myIdx - 1) ?? null;
  }
  return { next, prev, position: pos + 1, total: sorted.length };
}

// ── Auto-advance: what plays after this item ─────────────────────────
//
// The Android TV client's NextSiblingResolver + AudiobookChapters, with the
// server reads injected so the rules are tested with fake fetchers.

export type FetchChildren = (parentId: string) => Promise<ChildItem[]>;
export type FetchItem = (id: string) => Promise<{ id: string; parent_id?: string }>;

/** The item resolveNext looks from (the player's ItemDetail fits). */
export interface PlayingItem {
  id: string;
  type: string;
  parent_id?: string;
  index?: number;
}

/** The `type` row of `children` after `currentId`: the nearest one after it
 *  in (disc, index) order. Next-GREATER, not exactly index + 1: a library
 *  missing one file (episode 4 of 10, a track ripped out of an album) leaves
 *  a gap, and an exact-successor match stopped the chain there (or jumped to
 *  the next season). The current row's disc comes from the listing (the item
 *  endpoint carries none); without one it is disc 1, as every episode is. */
export function nextInContainer(
  children: ChildItem[],
  currentId: string,
  type: string,
  currentIndex: number
): ChildItem | null {
  const disc = children.find((k) => k.id === currentId)?.disc_number ?? 1;
  const sorted = children.filter((k) => k.type === type && k.index != null).sort(playOrder);
  for (const k of sorted) {
    const d = k.disc_number ?? 1;
    if (d > disc || (d === disc && (k.index as number) > currentIndex)) return k;
  }
  return null;
}

/** A book's chapters in listening order: numbered ones by number, then the
 *  unnumbered ones as the server lists them (by title). The scanner can't
 *  number every chapter file, so chapters can't be chained on index + 1. */
export function chaptersInOrder(children: ChildItem[]): ChildItem[] {
  const seen = new Set<string>();
  const chapters = children.filter((k) => {
    if (k.type !== 'audiobook_chapter' || seen.has(k.id)) return false;
    seen.add(k.id);
    return true;
  });
  // Stably (lib/stableSort: Tizen 5.5 runs Chromium 69): unnumbered
  // chapters keep the server's order behind the numbered ones.
  return stableSort(chapters,
    (a, b) =>
      (a.disc_number ?? 1) - (b.disc_number ?? 1) ||
      (a.index ?? Number.MAX_SAFE_INTEGER) - (b.index ?? Number.MAX_SAFE_INTEGER)
  );
}

/** The chapter after `currentId` in its book, null at the book's end (a book
 *  never runs on into the next one) or when it is no longer listed. */
export function nextChapter(children: ChildItem[], currentId: string): ChildItem | null {
  const ordered = chaptersInOrder(children);
  const at = ordered.findIndex((k) => k.id === currentId);
  return at < 0 ? null : (ordered[at + 1] ?? null);
}

/**
 * What auto-advance plays after `item`, or null:
 *  - episode: the next-greater episode in its season, else the first episode
 *    of the next season (the show's seasons by index);
 *  - track: the next track in (disc, index) order, else the first track of
 *    the artist's next album (albums by year, then index);
 *  - audiobook_chapter: the next chapter in the book's listing, numbered or
 *    not, never crossing into another book;
 *  - podcast_episode: the next-greater episode in its feed (a feed missing
 *    one download, 41 → 43, still chains; Android's NextSiblingResolver
 *    treats every indexed type this way), never crossing into another feed;
 *  - anything else (movies, standalone tracks, single-file books): null.
 * `children` is the parent's listing when the caller already has it. Never
 * throws: a failed read means no next item.
 */
export async function resolveNext(
  item: PlayingItem,
  fetchChildren: FetchChildren,
  fetchItem: FetchItem,
  children?: ChildItem[]
): Promise<ChildItem | null> {
  const parentId = item.parent_id;
  if (!parentId) return null;
  try {
    const kids = children ?? (await fetchChildren(parentId));
    if (item.type === 'audiobook_chapter') return nextChapter(kids, item.id);
    if (item.index == null) return null;
    if (item.type === 'podcast_episode') return nextInContainer(kids, item.id, item.type, item.index);
    if (item.type !== 'episode' && item.type !== 'track') return null;

    const inPlace = nextInContainer(kids, item.id, item.type, item.index);
    if (inPlace) return inPlace;

    // Off the end of the season / album: the next container of the same
    // show / artist.
    const parent = await fetchItem(parentId);
    const grandparentId = parent.parent_id;
    if (!grandparentId) return null;
    const containerType = item.type === 'track' ? 'album' : 'season';
    const big = Number.MAX_SAFE_INTEGER;
    const containers = stableSort(
      (await fetchChildren(grandparentId)).filter((k) => k.type === containerType),
        item.type === 'track'
          ? (a, b) => (a.year ?? big) - (b.year ?? big) || (a.index ?? big) - (b.index ?? big)
          : (a, b) => (a.index ?? big) - (b.index ?? big)
      );
    const at = containers.findIndex((k) => k.id === parentId);
    const nextContainer = at >= 0 ? containers[at + 1] : undefined;
    if (!nextContainer) return null;
    const first = (await fetchChildren(nextContainer.id))
      .filter((k) => k.type === item.type && k.index != null)
      .sort(playOrder);
    return first[0] ?? null;
  } catch {
    return null;
  }
}
