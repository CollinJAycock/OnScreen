// Offset paging for the TV grids (library, collection). Pure: the fetch is
// injected, so the stale-page guard and the end-of-list rules are unit-tested.
// Mirrors Android's LibraryViewModel / CollectionViewModel: a page at a time,
// the next one requested as focus nears the end, and a query generation
// counter so a page that was in flight when the sort / filter changed can't
// land in the new list.

/** A page plus the server's meta.total: how many rows the caller can see in
 *  the whole list. null when the endpoint doesn't report one. */
export interface PageResult<T> {
  items: T[];
  total: number | null;
}

/** Fetches `limit` rows starting at `offset` for the current query: the rows
 *  alone, or the rows with the list's total (see Pager.loadMore). */
export type FetchPage<T> = (offset: number, limit: number) => Promise<T[] | PageResult<T>>;

/** In a list with a total, how many pages in a row that bring nothing new
 *  one loadMore fetches before it hands back (see Pager.loadMore). Small:
 *  these go out back to back with no input from the user. */
export const EMPTY_PAGES_PER_LOAD = 3;

export class Pager<T> {
  /** Everything loaded so far for the current query, without duplicates. A
   *  new array after every change, so a Svelte `$state.raw` assignment sees it. */
  items: T[] = [];
  /** No more pages for the current query. */
  done = false;
  /** A list with a total whose last loadMore stopped on its run of empty
   *  pages with rows still owed: the next loadMore carries on from there.
   *  Nothing new appeared for focus to ask with, so the page offers a way to
   *  ask (Load more). Stays set while that next load runs, so the control
   *  doesn't vanish from under the focus. */
  stalled = false;
  /**
   * With a total: an empty page ends the list anyway. For an endpoint that
   * filters in the same SQL it pages (an auto-genre collection: the rows and
   * the count apply the same access and rating filters), where nothing past
   * an empty page can turn up. Without it the total alone ends the list, and
   * a total that drifted above what paging can deliver (a row inserted above
   * the offset mid-browse) would leave the list stalled for good, with every
   * load sending its run of empty pages. Settable once the caller learns
   * which kind of list it is; it applies from the next page.
   */
  emptyPageEnds = false;

  private generation = 0;
  // Server rows consumed so far. Advances by the raw page length, not by
  // what survived the de-duplication, so a duplicate can't stall paging.
  private offset = 0;
  private inflight = false;

  /**
   * @param keyOf          identity of a row (duplicates across pages are
   *                       dropped: a keyed {#each} throws on a repeated key,
   *                       and offset paging repeats a row whenever the list
   *                       shifts between two requests).
   * @param limit          page size.
   * @param shortPageEnds  a page shorter than `limit` means the end. Only
   *                       safe when the server pages and filters in the same
   *                       SQL (the library listing). Off for endpoints that
   *                       drop rows after paging, where only an empty page
   *                       ends, unless the fetch reports a total (below).
   */
  constructor(
    private readonly keyOf: (item: T) => string,
    readonly limit: number,
    private readonly shortPageEnds = true,
  ) {}

  /** Start a new query: drops what's loaded and orphans any page in flight. */
  reset(): void {
    this.generation++;
    this.items = [];
    this.done = false;
    this.stalled = false;
    this.offset = 0;
    this.inflight = false;
  }

  get loading(): boolean {
    return this.inflight;
  }

  /**
   * Fetch the next page. Resolves true when it changed `items` / `done` for
   * the current query; false when it did nothing (already loading, already
   * done) or the query changed while it was in flight (the page is dropped).
   * A failure for the current query rejects and leaves the offset where it
   * was, so the next call retries the same page; a failure for a superseded
   * query is swallowed.
   *
   * A fetch that reports the list's total (PageResult) pages on it, the way
   * Android's CollectionViewModel does: the list ends once every row the
   * total counts has loaded, not on an empty page. That is for endpoints that
   * page in SQL and only then drop the rows the caller can't see (a playlist
   * with items from a library the user lost access to): a page there can come
   * back short, or empty, with visible rows still after it. So the offset
   * moves on by the full `limit` (the server consumed that many rows; `limit`
   * must not exceed the server's own page cap), and a page that brings
   * nothing new is followed by the next one straight away, since no new card
   * appears for focus to ask again. The list is never done below the total
   * (unless `emptyPageEnds`, for lists filtered in SQL): a run of hidden rows
   * can be any length (a playlist whose first 1,100 rows come from a library
   * the user lost access to). Instead one call stops
   * after EMPTY_PAGES_PER_LOAD empty pages, sets `stalled`, and the next call
   * (focus near the end again, or Load more) goes on from the same offset, so
   * the requests that go out without the user asking stay few.
   */
  async loadMore(fetchPage: FetchPage<T>): Promise<boolean> {
    if (this.inflight || this.done) return false;
    const gen = this.generation;
    this.inflight = true;
    let emptyRun = 0;
    for (;;) {
      let result: T[] | PageResult<T>;
      try {
        result = await fetchPage(this.offset, this.limit);
      } catch (e) {
        if (gen !== this.generation) return false;
        this.inflight = false;
        throw e;
      }
      if (gen !== this.generation) return false;
      const page = Array.isArray(result) ? result : result.items;
      const total = Array.isArray(result) ? null : result.total;
      if (total === null || !Number.isFinite(total)) {
        this.inflight = false;
        this.stalled = false;
        this.offset += page.length;
        this.items = appendUnique(this.items, page, this.keyOf);
        this.done = page.length === 0 || (this.shortPageEnds && page.length < this.limit);
        return true;
      }
      this.offset += this.limit;
      const merged = appendUnique(this.items, page, this.keyOf);
      const grew = merged.length > this.items.length;
      if (grew) this.items = merged;
      this.done = this.items.length >= total || (this.emptyPageEnds && page.length === 0);
      if (grew || this.done) {
        this.inflight = false;
        this.stalled = false;
        return true;
      }
      if (++emptyRun >= EMPTY_PAGES_PER_LOAD) {
        this.inflight = false;
        this.stalled = true;
        return true;
      }
    }
  }
}

/** Most pages refill fetches (100 a page in the library grid). */
export const REFILL_MAX_PAGES = 50;

/**
 * Loads pages until `count` rows are in: Back to a paged grid fetches again
 * what it had when the user left, so the card they opened (far down a big
 * library) is there to focus. Stops early when the list ends, a load fails
 * or does nothing, or a page brings no new row; and after `maxPages`, so a
 * huge grid can't keep the TV fetching for long. `afterPage` runs after
 * each page (the page focuses the card as soon as it has arrived). Resolves
 * with the number of pages loaded.
 */
export async function refill(
  pager: { readonly items: readonly unknown[]; readonly done: boolean },
  load: () => Promise<boolean>,
  count: number,
  afterPage: () => void | Promise<void> = () => {},
  maxPages = REFILL_MAX_PAGES,
): Promise<number> {
  let pages = 0;
  while (pager.items.length < count && !pager.done && pages < maxPages) {
    const before = pager.items.length;
    if (!(await load()) || pager.items.length === before) break;
    pages++;
    await afterPage();
  }
  return pages;
}

/** `list` + the rows of `more` whose key isn't already present (first wins). */
export function appendUnique<T>(list: readonly T[], more: readonly T[], keyOf: (item: T) => string): T[] {
  const seen = new Set(list.map(keyOf));
  const out = list.slice();
  for (const item of more) {
    const k = keyOf(item);
    if (seen.has(k)) continue;
    seen.add(k);
    out.push(item);
  }
  return out;
}

/** The first row per key, in order (e.g. History: one card per title). */
export function uniqueBy<T>(list: readonly T[], keyOf: (item: T) => string): T[] {
  return appendUnique([], list, keyOf);
}

/**
 * True when the card at `index` sits in the last `rows` rows of a grid of
 * `count` cards laid out `columns` wide: the moment to ask for the next page,
 * so it lands before the user reaches the bottom edge.
 */
export function nearEnd(index: number, count: number, columns: number, rows = 2): boolean {
  if (count <= 0) return false;
  return index >= count - Math.max(1, columns) * Math.max(1, rows);
}
