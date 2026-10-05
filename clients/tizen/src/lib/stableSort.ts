// A sort that keeps equal elements in their original order on every engine.
// Array.prototype.sort is stable from Chromium 70 (ES2019), but this app's
// floor is Tizen 5.5's Chromium 69, whose V8 sorts arrays longer than 10
// with an unstable QuickSort: unnumbered audiobook chapters came out
// shuffled, and subtitle cues that start together in the wrong order.

/** A new array of `items` sorted by `cmp`, ties in their original order. */
export function stableSort<T>(items: readonly T[], cmp: (a: T, b: T) => number): T[] {
  return items
    .map((item, i) => ({ item, i }))
    .sort((a, b) => cmp(a.item, b.item) || a.i - b.i)
    .map((x) => x.item);
}
