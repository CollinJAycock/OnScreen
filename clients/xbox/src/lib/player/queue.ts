// A play queue (v2.6 Instant mix): tracks from all over the library, played
// in the order given. While the item playing is in it, the player steps
// through it (auto-advance, the previous / next keys, "Track 3 of 50")
// instead of the item's album. nav.playItem starts a queue or drops the one
// there was; the player moving on inside it (nav.replaceTo) keeps it.

import type { ChildItem } from '../api/types';

/** One queued track: enough for the player's Up Next and its next route. */
export interface QueueEntry {
  id: string;
  title: string;
  type: string;
}

let entries: QueueEntry[] = [];

/** Start a queue (a copy; repeats of a track keep their first place). */
export function setQueue(list: readonly QueueEntry[]): void {
  const seen = new Set<string>();
  entries = list.filter((e) => {
    if (!e || !e.id || seen.has(e.id)) return false;
    seen.add(e.id);
    return true;
  });
}

export function clearQueue(): void {
  entries = [];
}

export interface QueueNeighbours {
  prev: ChildItem | null;
  next: ChildItem | null;
  /** 1-based place in the queue. */
  position: number;
  total: number;
}

function asChild(e: QueueEntry | undefined): ChildItem | null {
  return e ? { id: e.id, title: e.title, type: e.type } : null;
}

/** Where `id` sits in the queue, null when it isn't queued (the player then
 *  steps through the item's album as before). The end of the queue is the
 *  end: it never runs on into an album. */
export function queueNeighbours(id: string): QueueNeighbours | null {
  const at = entries.findIndex((e) => e.id === id);
  if (at < 0) return null;
  return {
    prev: asChild(entries[at - 1]),
    next: asChild(entries[at + 1]),
    position: at + 1,
    total: entries.length,
  };
}
