import { describe, expect, it, vi } from 'vitest';
import { EMPTY_PAGES_PER_LOAD, Pager, appendUnique, nearEnd, openAtRow, refill, restoreStart, storedCardRow, uniqueBy } from './paging';

interface Row {
  id: string;
}
const rows = (from: number, n: number): Row[] => Array.from({ length: n }, (_, i) => ({ id: `r${from + i}` }));
const ids = (list: Row[]) => list.map((r) => r.id);

function deferred<T>() {
  let resolve!: (v: T) => void;
  let reject!: (e: unknown) => void;
  const promise = new Promise<T>((res, rej) => {
    resolve = res;
    reject = rej;
  });
  return { promise, resolve, reject };
}

describe('Pager', () => {
  it('appends pages at increasing offsets and ends on a short page', async () => {
    const pager = new Pager<Row>((r) => r.id, 3);
    const fetch = vi.fn(async (offset: number, limit: number) => rows(offset, offset < 6 ? limit : 1));
    expect(await pager.loadMore(fetch)).toBe(true);
    expect(await pager.loadMore(fetch)).toBe(true);
    expect(pager.done).toBe(false);
    expect(await pager.loadMore(fetch)).toBe(true);
    expect(pager.done).toBe(true);
    expect(fetch.mock.calls).toEqual([
      [0, 3],
      [3, 3],
      [6, 3],
    ]);
    expect(ids(pager.items)).toEqual(['r0', 'r1', 'r2', 'r3', 'r4', 'r5', 'r6']);
    // Done: no more requests.
    expect(await pager.loadMore(fetch)).toBe(false);
    expect(fetch).toHaveBeenCalledTimes(3);
  });

  it('with shortPageEnds off, only an empty page ends the list', async () => {
    const pager = new Pager<Row>((r) => r.id, 3, false);
    const pages = [rows(0, 2), rows(2, 1), []];
    const fetch = vi.fn(async (_offset: number, _limit: number) => pages.shift() ?? []);
    await pager.loadMore(fetch);
    expect(pager.done).toBe(false);
    await pager.loadMore(fetch);
    expect(pager.done).toBe(false);
    await pager.loadMore(fetch);
    expect(pager.done).toBe(true);
    expect(ids(pager.items)).toEqual(['r0', 'r1', 'r2']);
    // The offset follows the rows received: 0, then 2, then 3.
    expect(fetch.mock.calls.map((c) => c[0])).toEqual([0, 2, 3]);
  });

  it('drops a page that was in flight when the query changed', async () => {
    const pager = new Pager<Row>((r) => r.id, 2);
    const slow = deferred<Row[]>();
    const old = pager.loadMore(() => slow.promise);
    expect(pager.loading).toBe(true);
    pager.reset();
    expect(pager.loading).toBe(false);
    // The new query loads straight away.
    expect(await pager.loadMore(async () => [{ id: 'new' }])).toBe(true);
    slow.resolve(rows(0, 2));
    expect(await old).toBe(false);
    expect(ids(pager.items)).toEqual(['new']);
  });

  it('ignores a second request while one is in flight', async () => {
    const pager = new Pager<Row>((r) => r.id, 2);
    const slow = deferred<Row[]>();
    const fetch = vi.fn(() => slow.promise);
    const first = pager.loadMore(fetch);
    expect(await pager.loadMore(fetch)).toBe(false);
    slow.resolve(rows(0, 2));
    expect(await first).toBe(true);
    expect(fetch).toHaveBeenCalledTimes(1);
  });

  it('drops rows repeated across pages without stalling the offset', async () => {
    const pager = new Pager<Row>((r) => r.id, 2);
    const pages = [
      [{ id: 'a' }, { id: 'b' }],
      // The list shifted by one between requests: "b" comes round again.
      [{ id: 'b' }, { id: 'c' }],
      [{ id: 'd' }],
    ];
    const fetch = vi.fn(async (_offset: number, _limit: number) => pages.shift() ?? []);
    await pager.loadMore(fetch);
    await pager.loadMore(fetch);
    await pager.loadMore(fetch);
    expect(ids(pager.items)).toEqual(['a', 'b', 'c', 'd']);
    expect(fetch.mock.calls.map((c) => c[0])).toEqual([0, 2, 4]);
  });

  it('rethrows a failure for the current query and retries the same page', async () => {
    const pager = new Pager<Row>((r) => r.id, 2);
    await pager.loadMore(async () => rows(0, 2));
    await expect(pager.loadMore(async () => Promise.reject(new Error('boom')))).rejects.toThrow('boom');
    expect(pager.loading).toBe(false);
    expect(pager.done).toBe(false);
    const fetch = vi.fn(async (offset: number) => rows(offset, 1));
    await pager.loadMore(fetch);
    expect(fetch).toHaveBeenCalledWith(2, 2);
    expect(ids(pager.items)).toEqual(['r0', 'r1', 'r2']);
  });

  it('swallows a failure for a superseded query', async () => {
    const pager = new Pager<Row>((r) => r.id, 2);
    const slow = deferred<Row[]>();
    const old = pager.loadMore(() => slow.promise);
    pager.reset();
    slow.reject(new Error('late'));
    expect(await old).toBe(false);
    expect(pager.items).toEqual([]);
  });

  it('hands out a new items array on every change', async () => {
    const pager = new Pager<Row>((r) => r.id, 2);
    await pager.loadMore(async () => rows(0, 2));
    const before = pager.items;
    await pager.loadMore(async () => rows(2, 1));
    expect(pager.items).not.toBe(before);
    expect(ids(before)).toEqual(['r0', 'r1']);
  });
});

describe('Pager opened part way down (the A–Z rail)', () => {
  it('startAt fetches from that row on, and on down from there', async () => {
    const pager = new Pager<Row>((r) => r.id, 3);
    pager.startAt(437);
    expect(pager.start).toBe(437);
    expect(pager.hasBefore).toBe(true);
    const fetch = vi.fn(async (offset: number, limit: number) => rows(offset, limit));
    await pager.loadMore(fetch);
    await pager.loadMore(fetch);
    expect(fetch.mock.calls).toEqual([
      [437, 3],
      [440, 3],
    ]);
    expect(ids(pager.items)).toEqual(['r437', 'r438', 'r439', 'r440', 'r441', 'r442']);
  });

  it('startAt drops what was loaded and any page in flight', async () => {
    const pager = new Pager<Row>((r) => r.id, 2);
    await pager.loadMore(async (o, l) => rows(o, l));
    const slow = deferred<Row[]>();
    const old = pager.loadMore(() => slow.promise);
    pager.startAt(10);
    expect(pager.items).toEqual([]);
    expect(await pager.loadMore(async (o, l) => rows(o, l))).toBe(true);
    slow.resolve(rows(2, 2));
    expect(await old).toBe(false);
    expect(ids(pager.items)).toEqual(['r10', 'r11']);
  });

  it('loadBefore puts the page above in front, never past the top', async () => {
    const pager = new Pager<Row>((r) => r.id, 3);
    pager.startAt(4);
    const fetch = vi.fn(async (offset: number, limit: number) => rows(offset, limit));
    await pager.loadMore(fetch);
    expect(await pager.loadBefore(fetch)).toBe(true);
    expect(pager.start).toBe(1);
    expect(await pager.loadBefore(fetch)).toBe(true);
    expect(pager.start).toBe(0);
    expect(pager.hasBefore).toBe(false);
    // At the top: nothing more to ask for.
    expect(await pager.loadBefore(fetch)).toBe(false);
    expect(fetch.mock.calls).toEqual([
      [4, 3],
      [1, 3],
      [0, 1],
    ]);
    expect(ids(pager.items)).toEqual(['r0', 'r1', 'r2', 'r3', 'r4', 'r5', 'r6']);
  });

  it('loadBefore keeps a row already loaded below where it is', async () => {
    const pager = new Pager<Row>((r) => r.id, 3);
    pager.startAt(3);
    await pager.loadMore(async () => rows(3, 3));
    // The list shifted: r3 comes back in the page above as well.
    await pager.loadBefore(async () => [{ id: 'r1' }, { id: 'r2' }, { id: 'r3' }]);
    expect(ids(pager.items)).toEqual(['r1', 'r2', 'r3', 'r4', 'r5']);
  });

  it('loadBefore runs one at a time, drops a superseded page, and retries after a failure', async () => {
    const pager = new Pager<Row>((r) => r.id, 2);
    pager.startAt(6);
    await pager.loadMore(async (o, l) => rows(o, l));
    const slow = deferred<Row[]>();
    const first = pager.loadBefore(() => slow.promise);
    expect(pager.loadingBefore).toBe(true);
    expect(await pager.loadBefore(async (o, l) => rows(o, l))).toBe(false);
    pager.startAt(20);
    slow.resolve(rows(4, 2));
    expect(await first).toBe(false);
    expect(pager.start).toBe(20);
    expect(pager.loadingBefore).toBe(false);

    await pager.loadMore(async (o, l) => rows(o, l));
    await expect(pager.loadBefore(async () => Promise.reject(new Error('offline')))).rejects.toThrow('offline');
    expect(pager.start).toBe(20);
    expect(await pager.loadBefore(async (o, l) => rows(o, l))).toBe(true);
    expect(pager.start).toBe(18);
  });

  it('reset starts over at the top', () => {
    const pager = new Pager<Row>((r) => r.id, 2);
    pager.startAt(50);
    pager.reset();
    expect(pager.start).toBe(0);
    expect(pager.hasBefore).toBe(false);
  });
});

describe('Pager with a reported total (collection items)', () => {
  it('ends on the total, even when the last page is full', async () => {
    const pager = new Pager<Row>((r) => r.id, 3, false);
    const fetch = vi.fn(async (offset: number, limit: number) => ({ items: rows(offset, limit), total: 6 }));
    await pager.loadMore(fetch);
    expect(pager.done).toBe(false);
    await pager.loadMore(fetch);
    expect(pager.done).toBe(true);
    expect(ids(pager.items)).toEqual(['r0', 'r1', 'r2', 'r3', 'r4', 'r5']);
    expect(await pager.loadMore(fetch)).toBe(false);
    expect(fetch).toHaveBeenCalledTimes(2);
  });

  it('pages past a page the server emptied of hidden rows', async () => {
    // A playlist of 9 rows, 3 a page; rows 3-5 come from a library the user
    // can't see, so the server's middle page is empty and the total is 6.
    const visible = (offset: number, limit: number) =>
      rows(offset, limit).filter((r) => !['r3', 'r4', 'r5'].includes(r.id));
    const pager = new Pager<Row>((r) => r.id, 3, false);
    const fetch = vi.fn(async (offset: number, limit: number) => ({ items: visible(offset, limit), total: 6 }));
    await pager.loadMore(fetch);
    expect(pager.done).toBe(false);
    // One call: the empty page is followed by the next straight away.
    expect(await pager.loadMore(fetch)).toBe(true);
    expect(pager.done).toBe(true);
    expect(ids(pager.items)).toEqual(['r0', 'r1', 'r2', 'r6', 'r7', 'r8']);
    // The offset moves by the full page, not by the rows that survived.
    expect(fetch.mock.calls.map((c) => c[0])).toEqual([0, 3, 6]);
  });

  it('advances by the full page when a page comes back short mid-list', async () => {
    const pager = new Pager<Row>((r) => r.id, 3, false);
    const pages = [
      { items: [{ id: 'a' }], total: 4 }, // two of three rows hidden
      { items: [{ id: 'b' }, { id: 'c' }, { id: 'd' }], total: 4 },
    ];
    const fetch = vi.fn(async (_offset: number, _limit: number) => pages.shift()!);
    await pager.loadMore(fetch);
    expect(pager.done).toBe(false);
    await pager.loadMore(fetch);
    expect(pager.done).toBe(true);
    expect(fetch.mock.calls.map((c) => c[0])).toEqual([0, 3]);
  });

  it('stops a run of empty pages after a few and goes on from there next call', async () => {
    const pager = new Pager<Row>((r) => r.id, 3, false);
    // Rows 3 to 11 are hidden (three empty pages at 3 a page); the last
    // three visible rows sit after them.
    const hiddenUpTo = 3 + 3 * EMPTY_PAGES_PER_LOAD;
    const fetch = vi.fn(async (offset: number, limit: number) => ({
      items: offset === 0 || offset >= hiddenUpTo ? rows(offset, limit) : [],
      total: 6,
    }));
    await pager.loadMore(fetch);
    // One call: a short burst of empty pages, then it hands back.
    expect(await pager.loadMore(fetch)).toBe(true);
    expect(fetch).toHaveBeenCalledTimes(1 + EMPTY_PAGES_PER_LOAD);
    expect(pager.done).toBe(false);
    expect(pager.stalled).toBe(true);
    // The next focus near the end (or Load more) carries on where it stopped.
    expect(await pager.loadMore(fetch)).toBe(true);
    expect(fetch.mock.calls[fetch.mock.calls.length - 1][0]).toBe(hiddenUpTo);
    expect(pager.stalled).toBe(false);
    expect(pager.done).toBe(true);
    expect(ids(pager.items)).toEqual(['r0', 'r1', 'r2', `r${hiddenUpTo}`, `r${hiddenUpTo + 1}`, `r${hiddenUpTo + 2}`]);
  });

  it('is never done below the total, however long the hidden run', async () => {
    const pager = new Pager<Row>((r) => r.id, 3, false);
    // The total promises more than the rows deliver so far.
    const fetch = vi.fn(async (offset: number) => ({ items: offset === 0 ? rows(0, 3) : [], total: 10 }));
    await pager.loadMore(fetch);
    for (let i = 0; i < 4; i++) {
      expect(await pager.loadMore(fetch)).toBe(true);
      expect(pager.done).toBe(false);
      expect(pager.stalled).toBe(true);
    }
    // Never more than the small burst per call.
    expect(fetch).toHaveBeenCalledTimes(1 + 4 * EMPTY_PAGES_PER_LOAD);
    // Offsets keep moving on by the full page.
    expect(fetch.mock.calls.map((c) => c[0])).toEqual(
      Array.from({ length: 1 + 4 * EMPTY_PAGES_PER_LOAD }, (_, i) => i * 3),
    );
    // A new query starts unstalled.
    pager.reset();
    expect(pager.stalled).toBe(false);
  });

  it('stays stalled while the follow-up load runs', async () => {
    const pager = new Pager<Row>((r) => r.id, 3, false);
    const gate = deferred<{ items: Row[]; total: number }>();
    let calls = 0;
    const fetch = vi.fn((offset: number) => {
      calls++;
      if (offset === 0) return Promise.resolve({ items: rows(0, 3), total: 9 });
      if (calls <= 1 + EMPTY_PAGES_PER_LOAD) return Promise.resolve({ items: [] as Row[], total: 9 });
      return gate.promise;
    });
    await pager.loadMore(fetch);
    await pager.loadMore(fetch);
    expect(pager.stalled).toBe(true);
    const next = pager.loadMore(fetch);
    // The Load more button must not vanish from under the focus mid-load.
    expect(pager.stalled).toBe(true);
    expect(pager.loading).toBe(true);
    gate.resolve({ items: rows(12, 3), total: 9 });
    expect(await next).toBe(true);
    expect(pager.stalled).toBe(false);
  });

  it('is done at once for an empty list', async () => {
    const pager = new Pager<Row>((r) => r.id, 3, false);
    expect(await pager.loadMore(async () => ({ items: [], total: 0 }))).toBe(true);
    expect(pager.done).toBe(true);
    expect(pager.items).toEqual([]);
  });

  it('falls back to the empty-page rule when the total is missing', async () => {
    const pager = new Pager<Row>((r) => r.id, 3, false);
    const pages = [{ items: rows(0, 2), total: null }, { items: [] as Row[], total: null }];
    const fetch = vi.fn(async (_offset: number, _limit: number) => pages.shift()!);
    await pager.loadMore(fetch);
    expect(pager.done).toBe(false);
    await pager.loadMore(fetch);
    expect(pager.done).toBe(true);
    expect(fetch.mock.calls.map((c) => c[0])).toEqual([0, 2]);
  });

  it('ends on an empty page for a list filtered in SQL, below the total', async () => {
    // An auto-genre collection whose total drifted: a row inserted above the
    // offset mid-browse repeats one row (deduped) and the list can never
    // reach the total. Its first empty page is its end.
    const pager = new Pager<Row>((r) => r.id, 3, false);
    pager.emptyPageEnds = true;
    const pages = [
      { items: rows(0, 3), total: 6 },
      { items: [{ id: 'r2' }, { id: 'r3' }, { id: 'r4' }], total: 6 },
      { items: [] as Row[], total: 6 },
    ];
    const fetch = vi.fn(async (_offset: number, _limit: number) => pages.shift()!);
    await pager.loadMore(fetch);
    await pager.loadMore(fetch);
    expect(pager.done).toBe(false);
    expect(await pager.loadMore(fetch)).toBe(true);
    expect(pager.done).toBe(true);
    expect(pager.stalled).toBe(false);
    expect(ids(pager.items)).toEqual(['r0', 'r1', 'r2', 'r3', 'r4']);
    // Done: no Load more, no more requests past the end.
    expect(await pager.loadMore(fetch)).toBe(false);
    expect(fetch).toHaveBeenCalledTimes(3);
  });

  it('applies emptyPageEnds from the next page once it is set', async () => {
    // The collection's type arrives alongside its first page.
    const pager = new Pager<Row>((r) => r.id, 3, false);
    const fetch = vi.fn(async (offset: number) => ({ items: offset === 0 ? rows(0, 3) : [], total: 10 }));
    await pager.loadMore(fetch);
    pager.emptyPageEnds = true;
    expect(await pager.loadMore(fetch)).toBe(true);
    expect(pager.done).toBe(true);
    expect(fetch).toHaveBeenCalledTimes(2);
  });

  it('keeps the never-done-below-total rule for playlists (emptyPageEnds off)', async () => {
    const pager = new Pager<Row>((r) => r.id, 3, false);
    expect(pager.emptyPageEnds).toBe(false);
    const fetch = vi.fn(async (offset: number) => ({ items: offset === 0 ? rows(0, 3) : [], total: 10 }));
    await pager.loadMore(fetch);
    await pager.loadMore(fetch);
    expect(pager.done).toBe(false);
    expect(pager.stalled).toBe(true);
  });

  it('drops pages of a superseded query mid-run', async () => {
    const pager = new Pager<Row>((r) => r.id, 3, false);
    const gate = deferred<{ items: Row[]; total: number }>();
    const fetch = vi.fn((offset: number) =>
      offset === 0 ? Promise.resolve({ items: [] as Row[], total: 5 }) : gate.promise,
    );
    const run = pager.loadMore(fetch);
    await new Promise((r) => setTimeout(r, 0));
    pager.reset();
    gate.resolve({ items: rows(3, 2), total: 5 });
    expect(await run).toBe(false);
    expect(pager.items).toEqual([]);
  });
});

describe('Pager with a total, opened part way down (Back to a collection)', () => {
  it('ends at the end of the list, and the rows above come in with no refetch', async () => {
    const pager = new Pager<Row>((r) => r.id, 3, false);
    const fetch = vi.fn(async (offset: number, limit: number) => ({ items: rows(offset, Math.min(limit, 8 - offset)), total: 8 }));
    pager.startAt(4);
    await pager.loadMore(fetch);
    expect(ids(pager.items)).toEqual(['r4', 'r5', 'r6']);
    expect(pager.done).toBe(false);
    // r7 is the last row: every row is counted, but with rows above still to
    // load, an empty page past the end is what ends it.
    await pager.loadMore(fetch);
    expect(ids(pager.items)).toEqual(['r4', 'r5', 'r6', 'r7']);
    expect(pager.done).toBe(false);
    await pager.loadMore(fetch);
    expect(pager.done).toBe(true);
    expect(pager.stalled).toBe(false);
    expect(fetch.mock.calls.map((c) => c[0])).toEqual([4, 7, 10]);
    await pager.loadBefore(fetch);
    await pager.loadBefore(fetch);
    expect(ids(pager.items)).toEqual(rows(0, 8).map((r) => r.id));
    expect(pager.hasBefore).toBe(false);
  });

  it('reads on past rows hidden above it, as they leave more below', async () => {
    // 12 rows, r1 and r2 hidden (dropped after paging): total 10. Opened at
    // 6, the 6 rows above count as 6 while only 4 of them are visible.
    const hidden = new Set(['r1', 'r2']);
    const pager = new Pager<Row>((r) => r.id, 3, false);
    const fetch = vi.fn(async (offset: number, limit: number) => ({
      items: rows(offset, Math.max(0, Math.min(limit, 12 - offset))).filter((r) => !hidden.has(r.id)),
      total: 10,
    }));
    pager.startAt(6);
    while (!pager.done) await pager.loadMore(fetch);
    expect(ids(pager.items)).toEqual(['r6', 'r7', 'r8', 'r9', 'r10', 'r11']);
  });

  it('at the top, is done on the total as before', async () => {
    const pager = new Pager<Row>((r) => r.id, 3, false);
    const fetch = vi.fn(async (offset: number, limit: number) => ({ items: rows(offset, limit), total: 6 }));
    pager.startAt(0);
    await pager.loadMore(fetch);
    await pager.loadMore(fetch);
    expect(pager.done).toBe(true);
    expect(fetch).toHaveBeenCalledTimes(2);
  });
});

describe('openAtRow (Back to a collection or a Watchlist row)', () => {
  /** A list of `n` rows, 10 a page, as a Pager reads it. */
  function list(n: number) {
    const pager = new Pager<Row>((r) => r.id, 10, false);
    const fetch = vi.fn(async (offset: number, limit: number) => ({
      items: rows(offset, Math.max(0, Math.min(limit, n - offset))),
      total: n,
    }));
    const load = () => pager.loadMore(fetch);
    return { pager, fetch, load };
  }

  it('opens at the stored row when the card is still around it', async () => {
    const { pager, fetch, load } = list(500);
    expect(await openAtRow(pager, 300, load, () => ids(pager.items).includes('r305'))).toBe(true);
    expect(pager.start).toBe(300);
    expect(fetch.mock.calls).toEqual([[300, 10]]);
  });

  it('a card moved away from its row: back to the top, for the refill to find it', async () => {
    const { pager, fetch, load } = list(500);
    expect(await openAtRow(pager, 300, load, () => ids(pager.items).includes('r420'))).toBe(false);
    expect(pager.start).toBe(0);
    expect(ids(pager.items)).toEqual(rows(0, 10).map((r) => r.id));
    expect(fetch.mock.calls).toEqual([
      [300, 10],
      [0, 10],
    ]);
  });

  it('a list that now ends above the row starts at the top', async () => {
    const { pager, load } = list(40);
    expect(await openAtRow(pager, 300, load, () => ids(pager.items).includes('r305'))).toBe(false);
    expect(ids(pager.items)).toHaveLength(10);
    expect(pager.start).toBe(0);
  });

  it('with no row to open at, reads the first page only', async () => {
    const { pager, fetch, load } = list(500);
    const hasCard = vi.fn(() => true);
    expect(await openAtRow(pager, 0, load, hasCard)).toBe(false);
    expect(fetch.mock.calls).toEqual([[0, 10]]);
    expect(hasCard).not.toHaveBeenCalled();
  });

  it('rejects when the read fails', async () => {
    const pager = new Pager<Row>((r) => r.id, 10, false);
    await expect(
      openAtRow(pager, 300, () => pager.loadMore(async () => Promise.reject(new Error('offline'))), () => false),
    ).rejects.toThrow('offline');
  });
});

describe('refill', () => {
  it('loads pages until the remembered count is in', async () => {
    const pager = new Pager<Row>((r) => r.id, 100);
    const fetch = vi.fn(async (offset: number, limit: number) => rows(offset, limit));
    const after = vi.fn();
    await pager.loadMore(fetch);
    const pages = await refill(pager, () => pager.loadMore(fetch), 350, after);
    expect(pages).toBe(3);
    expect(pager.items).toHaveLength(400);
    expect(after).toHaveBeenCalledTimes(3);
  });

  it('stops at the end of the list', async () => {
    const pager = new Pager<Row>((r) => r.id, 100);
    const fetch = vi.fn(async (offset: number, limit: number) => rows(offset, Math.max(0, Math.min(limit, 150 - offset))));
    await pager.loadMore(fetch);
    // The library shrank while the user was away: 150 rows, not 500.
    expect(await refill(pager, () => pager.loadMore(fetch), 500)).toBe(1);
    expect(pager.done).toBe(true);
    expect(pager.items).toHaveLength(150);
  });

  it('stops on a failed or idle load, and on a page with nothing new', async () => {
    const pager = new Pager<Row>((r) => r.id, 100);
    await pager.loadMore(async (offset, limit) => rows(offset, limit));
    expect(await refill(pager, async () => false, 500)).toBe(0);
    // Every row repeated (the list shifted): no progress, so no loop.
    expect(await refill(pager, () => pager.loadMore(async () => rows(0, 100)), 500)).toBe(0);
  });

  it('caps the pages it fetches', async () => {
    const pager = new Pager<Row>((r) => r.id, 10);
    const fetch = vi.fn(async (offset: number, limit: number) => rows(offset, limit));
    expect(await refill(pager, () => pager.loadMore(fetch), 10_000, undefined, 4)).toBe(4);
    expect(fetch).toHaveBeenCalledTimes(4);
  });

  it('stops once the card is in (nothing below it is needed)', async () => {
    const pager = new Pager<Row>((r) => r.id, 100);
    const fetch = vi.fn(async (offset: number, limit: number) => rows(offset, limit));
    await pager.loadMore(fetch);
    let calls = 0;
    expect(await refill(pager, () => pager.loadMore(fetch), 900, () => ++calls === 2)).toBe(2);
    expect(fetch).toHaveBeenCalledTimes(3);
  });

  it('does nothing when enough is loaded already', async () => {
    const pager = new Pager<Row>((r) => r.id, 100);
    await pager.loadMore(async (offset, limit) => rows(offset, limit));
    const load = vi.fn(async () => true);
    expect(await refill(pager, load, 60)).toBe(0);
    expect(load).not.toHaveBeenCalled();
  });
});

describe('restoreStart (Back to a card far down a grid)', () => {
  it('opens at the page around the card, from the start of a grid row', () => {
    expect(restoreStart(4_321, 100, 6)).toBe(4_266);
    expect(4_266 % 6).toBe(0);
    expect(4_321 - restoreStart(4_321, 100, 6)).toBeLessThan(100);
  });

  it('starts at the top when the first page has the card', () => {
    expect(restoreStart(0, 100, 6)).toBe(0);
    expect(restoreStart(99, 100, 6)).toBe(0);
    expect(restoreStart(-1, 100, 6)).toBe(0);
    expect(restoreStart(Number.NaN, 100, 6)).toBe(0);
  });

  it('the stored row counts only for the card focus comes back to', () => {
    expect(storedCardRow('4321:abc', 'abc')).toBe(4321);
    expect(storedCardRow('4321:abc', 'def')).toBe(-1);
    expect(storedCardRow('4321:abc', null)).toBe(-1);
    expect(storedCardRow('', 'abc')).toBe(-1);
    expect(storedCardRow('x:abc', 'abc')).toBe(-1);
    expect(storedCardRow(null, 'abc')).toBe(-1);
  });
});

describe('appendUnique / uniqueBy', () => {
  it('keeps the first row per key, in order', () => {
    expect(ids(appendUnique([{ id: 'a' }], [{ id: 'b' }, { id: 'a' }, { id: 'b' }], (r) => r.id))).toEqual(['a', 'b']);
    const history = [
      { id: 'e3', media_id: 'm1' },
      { id: 'e2', media_id: 'm2' },
      { id: 'e1', media_id: 'm1' },
    ];
    expect(uniqueBy(history, (h) => h.media_id).map((h) => h.id)).toEqual(['e3', 'e2']);
  });
});

describe('nearEnd', () => {
  it('is true for cards in the last two rows', () => {
    // 6 columns, 24 cards: rows start at 0, 6, 12, 18 → the last two rows are 12..23.
    expect(nearEnd(11, 24, 6)).toBe(false);
    expect(nearEnd(12, 24, 6)).toBe(true);
    expect(nearEnd(23, 24, 6)).toBe(true);
  });

  it('is true everywhere in a grid shorter than the window', () => {
    expect(nearEnd(0, 5, 6)).toBe(true);
    expect(nearEnd(0, 0, 6)).toBe(false);
  });

  it('honours a custom row count', () => {
    expect(nearEnd(17, 24, 6, 1)).toBe(false);
    expect(nearEnd(18, 24, 6, 1)).toBe(true);
  });
});
