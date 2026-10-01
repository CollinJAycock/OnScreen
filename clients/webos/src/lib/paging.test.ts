import { describe, expect, it, vi } from 'vitest';
import { EMPTY_PAGES_PER_LOAD, Pager, appendUnique, nearEnd, refill, uniqueBy } from './paging';

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

  it('does nothing when enough is loaded already', async () => {
    const pager = new Pager<Row>((r) => r.id, 100);
    await pager.loadMore(async (offset, limit) => rows(offset, limit));
    const load = vi.fn(async () => true);
    expect(await refill(pager, load, 60)).toBe(0);
    expect(load).not.toHaveBeenCalled();
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
