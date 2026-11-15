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
  // Where `items` starts in the server's list: 0, unless the list was opened
  // part way down (startAt: the library's A–Z rail), and then the earlier
  // rows come in a page at a time above it (loadBefore).
  private first = 0;
  private inflightBefore = false;

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
    this.first = 0;
    this.inflightBefore = false;
  }

  /** Start a new query at row `offset` instead of the top (reset, then the
   *  next loadMore fetches from there): the A–Z rail's jump to a letter,
   *  which mustn't load the thousands of rows before it. */
  startAt(offset: number): void {
    this.reset();
    const at = Number.isFinite(offset) ? Math.max(0, Math.floor(offset)) : 0;
    this.first = at;
    this.offset = at;
  }

  /** The server row `items[0]` is (0 unless opened with startAt). */
  get start(): number {
    return this.first;
  }

  /** Rows before `items[0]` are still to load (loadBefore). */
  get hasBefore(): boolean {
    return this.first > 0;
  }

  get loading(): boolean {
    return this.inflight;
  }

  get loadingBefore(): boolean {
    return this.inflightBefore;
  }

  /**
   * Fetch the page just above `items[0]` (up to `limit` rows, never past the
   * top) and put it in front. For a list opened with startAt, whose rows
   * before the start the user reaches by going up. Resolves true when it
   * changed `items`; false when there's nothing above, a load above is
   * already on its way, or the query changed while it was in flight. A row
   * of that page already loaded further down (the list shifted) keeps its
   * place there. Failures as loadMore's: the next call retries.
   */
  async loadBefore(fetchPage: FetchPage<T>): Promise<boolean> {
    if (this.inflightBefore || this.first <= 0) return false;
    const gen = this.generation;
    const from = Math.max(0, this.first - this.limit);
    this.inflightBefore = true;
    let result: T[] | PageResult<T>;
    try {
      result = await fetchPage(from, this.first - from);
    } catch (e) {
      if (gen !== this.generation) return false;
      this.inflightBefore = false;
      throw e;
    }
    if (gen !== this.generation) return false;
    this.inflightBefore = false;
    const page = Array.isArray(result) ? result : result.items;
    this.first = from;
    const loaded = new Set(this.items.map(this.keyOf));
    const above = uniqueBy(
      page.filter((item) => !loaded.has(this.keyOf(item))),
      this.keyOf,
    );
    this.items = above.concat(this.items);
    return true;
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
      this.done = this.reachedTotal(total, page.length) || (this.emptyPageEnds && page.length === 0);
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

  /** Every row `total` counts is in, the last page having brought `got`.
   *  Opened part way down (startAt), the rows above `items[0]` not loaded
   *  yet count as `first` rows: exact for a list filtered in the SQL that
   *  pages it, but one that drops hidden rows after paging can have fewer
   *  there, so then it also takes an empty page (past the server's last
   *  row) to end, or the last rows of the list would never be asked for. */
  private reachedTotal(total: number, got: number): boolean {
    if (this.first + this.items.length < total) return false;
    return this.first === 0 || got === 0;
  }
}

/** Most pages refill fetches (100 a page in the library grid). */
export const REFILL_MAX_PAGES = 50;

/**
 * Loads pages until `count` rows are in: Back to a paged grid fetches again
 * what it had when the user left, so the card they opened (far down a big
 * list) is there to focus. Stops early when the list ends, a load fails
 * or does nothing, or a page brings no new row; when `afterPage` (run after
 * each page: the page focuses the card as soon as it has arrived) answers
 * true, i.e. the card is in, as nothing below it is needed; and after
 * `maxPages`, so a huge grid can't keep the TV fetching for long. Resolves
 * with the number of pages loaded. The library grid needs none of it: it
 * opens at the card's row (restoreStart). A collection or a Watchlist row
 * opens there too, and needs it only when the card wasn't there (openAtRow).
 */
export async function refill(
  pager: { readonly items: readonly unknown[]; readonly done: boolean },
  load: () => Promise<boolean>,
  count: number,
  afterPage: () => boolean | void | Promise<boolean | void> = () => {},
  maxPages = REFILL_MAX_PAGES,
): Promise<number> {
  let pages = 0;
  while (pager.items.length < count && !pager.done && pages < maxPages) {
    const before = pager.items.length;
    if (!(await load()) || pager.items.length === before) break;
    pages++;
    if ((await afterPage()) === true) break;
  }
  return pages;
}

/**
 * Where Back opens a paged grid for the card at listing row `row`
 * (Pager.startAt, as the A–Z rail does): the page around it, from the start
 * of a grid row, so the card is in the first page fetched; 0 when the first
 * page has it. The rows above come in a page at a time as focus goes up
 * (Pager.loadBefore). It used to fetch again every page down to the card,
 * one after another (up to 50 x 100 rows), and render every card.
 */
export function restoreStart(row: number, pageSize: number, columns: number): number {
  if (!Number.isFinite(row) || row < pageSize) return 0;
  const at = Math.max(0, Math.floor(row) - Math.floor(pageSize / 2));
  return at - (at % columns);
}

/**
 * Back to a list whose rows can move while the user is away (a collection:
 * a title taken out of it on its page; a Watchlist row: a title given
 * another status there): open it at `at`, the page around the card's stored
 * row (restoreStart), and read that page (`load`). When the card isn't in it
 * (`hasCard`: the list moved more than half a page meanwhile, or now ends
 * above that row), start over at the top and read the first page, so the
 * caller refills until the card is in as it did before. With `at` 0 only the
 * first page is read. Resolves true when it opened part way down with the
 * card in; a failed read rejects.
 */
export async function openAtRow(
  list: { startAt(offset: number): void; reset(): void },
  at: number,
  load: () => Promise<unknown>,
  hasCard: () => boolean,
): Promise<boolean> {
  if (!(at > 0)) {
    await load();
    return false;
  }
  list.startAt(at);
  await load();
  if (hasCard()) return true;
  list.reset();
  await load();
  return false;
}

/** The card a grid was left from, stored as "<row>:<id>": its row, when it
 *  is the card focus comes back to (`focusedId`); else -1. */
export function storedCardRow(stored: string | null | undefined, focusedId: string | null | undefined): number {
  if (!stored || !focusedId) return -1;
  const at = stored.indexOf(':');
  if (at <= 0 || stored.slice(at + 1) !== focusedId) return -1;
  const row = Number(stored.slice(0, at));
  return Number.isInteger(row) && row >= 0 ? row : -1;
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
