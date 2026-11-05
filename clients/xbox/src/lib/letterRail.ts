// The library grid's A–Z rail. Pure: the fetch is injected, so the rules
// for when the rail shows and where a letter jumps to are unit-tested.
//
// The server gives, for the listing the grid pages (same sort and filters),
// each letter that has items with the offset of its first one
// (GET /libraries/{id}/items/letters). A jump opens the grid at that offset
// (Pager.startAt) rather than paging through every row before it: a library
// can hold 10,000 titles.

import type { LetterJump } from './api/types';
import type { LibrarySort } from './librarySort';
import { uniqueBy } from './paging';

/** The rail only means something in title order (the server returns no
 *  letters for any other sort, so it isn't asked). */
export function railSort(sort: LibrarySort): boolean {
  return sort.sort === 'title';
}

/**
 * The letters for the rail: none (and no request) when the grid isn't in
 * title order, and none when the request fails: a server before the index
 * answers 404, and the rail is a shortcut the grid works without. Rows that
 * don't parse are dropped.
 */
export async function loadLetters(
  sort: LibrarySort,
  fetchLetters: () => Promise<LetterJump[]>,
): Promise<LetterJump[]> {
  if (!railSort(sort)) return [];
  let rows: unknown;
  try {
    rows = await fetchLetters();
  } catch {
    return [];
  }
  if (!Array.isArray(rows)) return [];
  const valid = rows.filter(
    (r): r is LetterJump =>
      !!r &&
      typeof r.letter === 'string' &&
      r.letter !== '' &&
      typeof r.offset === 'number' &&
      Number.isFinite(r.offset) &&
      r.offset >= 0 &&
      !(typeof r.count === 'number' && r.count <= 0),
  );
  // One button per letter (the rail is keyed by it).
  return uniqueBy(valid, (r) => r.letter);
}

/** The index in `letters` of the letter the listing's row `row` falls
 *  under (the last one starting at or before it); -1 before the first. */
export function letterAt(letters: readonly LetterJump[], row: number): number {
  let hit = -1;
  for (let i = 0; i < letters.length; i++) {
    if (letters[i].offset <= row) hit = i;
    else break;
  }
  return hit;
}

/** What a letter's OK does: focus its first item, when that row is already
 *  in the grid (`start` is the row the grid's first card is, `loaded` how
 *  many it has), or open the grid at its offset. */
export type JumpPlan = { kind: 'focus'; index: number } | { kind: 'load'; offset: number };

export function planJump(letter: LetterJump, start: number, loaded: number): JumpPlan {
  const index = letter.offset - start;
  if (index >= 0 && index < loaded) return { kind: 'focus', index };
  return { kind: 'load', offset: letter.offset };
}
