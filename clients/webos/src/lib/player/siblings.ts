// Neighbours of the playing item for auto-advance (Up Next) and the
// Previous / Next track keys, picked from its parent's /children listing.
// Pure so the ordering rules are checkable without mounting the player.

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
