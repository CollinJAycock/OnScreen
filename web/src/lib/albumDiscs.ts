// An album's track list, disc by disc. Every disc restarts at track 1, so
// tracks sort by disc and then track number, and an album that spans more
// than one disc heads each disc's run of tracks ("Disc 2") so its numbering
// reads right. A track without a disc number is on disc 1.

import type { ChildItem } from '$lib/api';

type Ordered = Pick<ChildItem, 'disc_number' | 'index'>;

export function discOf(t: Pick<ChildItem, 'disc_number'>): number {
  return t.disc_number ?? 1;
}

// Play order: disc, then track number; tracks without a number go last on
// their disc. Returns a new array.
export function albumTrackOrder<T extends Ordered>(tracks: readonly T[]): T[] {
  return [...tracks].sort((a, b) => discOf(a) - discOf(b) || (a.index ?? 9999) - (b.index ?? 9999));
}

export interface DiscGroup<T> {
  disc: number;
  // Position of the disc's first track in the whole album, so a row can
  // still play "track i of the album".
  start: number;
  tracks: T[];
}

// Splits tracks (already in albumTrackOrder) into one group per disc.
export function discGroups<T extends Ordered>(tracks: readonly T[]): DiscGroup<T>[] {
  const groups: DiscGroup<T>[] = [];
  tracks.forEach((t, i) => {
    const last = groups[groups.length - 1];
    if (last && last.disc === discOf(t)) {
      last.tracks.push(t);
    } else {
      groups.push({ disc: discOf(t), start: i, tracks: [t] });
    }
  });
  return groups;
}
