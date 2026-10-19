import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { api } from './api/client';
import { watchStatus, watchlist } from './api/endpoints';
import type { WatchlistItem } from './api/endpoints';
import type { PageResult } from './paging';
import {
  WATCHLIST_EMPTY,
  WATCHLIST_STATUSES,
  WatchlistRow,
  episodeLabel,
  moveTargets,
  offersStatus,
  parseItemStatus,
  parseWatchlistCounts,
  readItemStatus,
  rowTitle,
  statusButtonLabel,
  statusChangedText,
  statusChoiceIndex,
  statusChoices,
  statusMenuMessage,
  statusOfFocusKey,
  statusesToLoad,
  visibleRows,
  watchlistCardText,
  watchlistPage,
  watchlistQueryString,
  withStatus,
  writeItemStatus,
  type ItemStatusApi,
} from './watchlist';

function item(id: string, over: Partial<WatchlistItem> = {}): WatchlistItem {
  return {
    id,
    library_id: 'lib-1',
    type: 'movie',
    title: `Title ${id}`,
    watch_state: 'unwatched',
    status: 'plan_to_watch',
    status_created_at: '2026-10-01T00:00:00Z',
    status_updated_at: '2026-10-01T00:00:00Z',
    ...over,
  };
}

const ids = (list: readonly WatchlistItem[]) => list.map((i) => i.id);

describe('watchlistQueryString', () => {
  it('asks for Plan to Watch, 50 from the top, by default', () => {
    expect(watchlistQueryString()).toBe('status=plan_to_watch&limit=50&offset=0');
  });

  it('sends the status, type, page and a non-default sort', () => {
    expect(watchlistQueryString({ status: 'on_hold', type: 'episode', sort: 'title', limit: 20, offset: 40 })).toBe(
      'status=on_hold&type=episode&sort=title&limit=20&offset=40',
    );
    expect(watchlistQueryString({ status: 'all', sort: 'recent' })).toBe('status=all&limit=50&offset=0');
  });
});

describe('watchlistPage', () => {
  it('reads the rows and meta.total', () => {
    const page = watchlistPage({ data: [item('a'), item('b')], meta: { total: 7 } });
    expect(ids(page.items)).toEqual(['a', 'b']);
    expect(page.total).toBe(7);
  });

  it('reads a missing or malformed body as an empty page with no total', () => {
    expect(watchlistPage(null)).toEqual({ items: [], total: null });
    expect(watchlistPage({ data: 'nope', meta: { total: 'x' } })).toEqual({ items: [], total: null });
    expect(watchlistPage({ data: [] })).toEqual({ items: [], total: null });
  });

  it('drops rows without an id or a type', () => {
    const page = watchlistPage({ data: [null, { title: 'x' }, { id: '', type: 'movie' }, item('ok')], meta: { total: 1 } });
    expect(ids(page.items)).toEqual(['ok']);
  });
});

describe('parseWatchlistCounts', () => {
  it('keeps known statuses and their non-zero types', () => {
    const c = parseWatchlistCounts({
      total: 5,
      statuses: {
        plan_to_watch: { total: 3, types: { movie: 2, episode: 1, show: 0 } },
        completed: { total: 2, types: { movie: 2 } },
        bogus: { total: 9, types: {} },
      },
    });
    expect(c.total).toBe(5);
    expect(c.statuses.plan_to_watch).toEqual({ total: 3, types: { movie: 2, episode: 1 } });
    expect(c.statuses.completed).toEqual({ total: 2, types: { movie: 2 } });
    expect(Object.keys(c.statuses)).toEqual(['plan_to_watch', 'completed']);
  });

  it('reads anything missing or malformed as zero', () => {
    expect(parseWatchlistCounts(null)).toEqual({ total: 0, statuses: {} });
    expect(parseWatchlistCounts({ total: 'x', statuses: { watching: { total: -1, types: null } } })).toEqual({
      total: 0,
      statuses: { watching: { total: 0, types: {} } },
    });
  });
});

describe('statusesToLoad', () => {
  it('loads the statuses with something under them, in row order', () => {
    const c = parseWatchlistCounts({
      total: 4,
      statuses: { dropped: { total: 1 }, plan_to_watch: { total: 2 }, watching: { total: 0 }, on_hold: { total: 1 } },
    });
    expect(statusesToLoad(c)).toEqual(['plan_to_watch', 'on_hold', 'dropped']);
  });

  it('loads every status when the counts are unknown', () => {
    expect(statusesToLoad(null)).toEqual([...WATCHLIST_STATUSES]);
  });

  it('loads nothing for an empty watchlist (statuses missing)', () => {
    expect(statusesToLoad(parseWatchlistCounts({ total: 0, statuses: {} }))).toEqual([]);
  });
});

describe('labels', () => {
  it('heads a row with its count', () => {
    expect(rowTitle('plan_to_watch', 12)).toBe('Plan to Watch · 12');
    expect(rowTitle('on_hold', null)).toBe('On Hold');
  });

  it('labels an episode "S1 E2", dropping a missing half', () => {
    expect(episodeLabel(1, 2)).toBe('S1 E2');
    expect(episodeLabel(null, 2)).toBe('E2');
    expect(episodeLabel(3, 0)).toBe('S3');
    expect(episodeLabel(undefined, undefined)).toBe('');
  });

  it('puts the show and episode under an episode card', () => {
    const ep = item('e', {
      type: 'episode',
      title: 'Pilot',
      index: 2,
      parent_index: 1,
      parent_title: 'Season 1',
      grandparent_title: 'Show',
    });
    expect(watchlistCardText(ep)).toEqual({ title: 'Pilot', subtitle: 'Show · S1 E2' });
    expect(watchlistCardText({ ...ep, grandparent_title: undefined })).toEqual({ title: 'Pilot', subtitle: 'S1 E2' });
  });

  it('labels seasons, tracks, albums and the rest', () => {
    expect(watchlistCardText(item('s', { type: 'season', title: 'Season 2', parent_title: 'Show' }))).toEqual({
      title: 'Season 2',
      subtitle: 'Show',
    });
    expect(
      watchlistCardText(item('t', { type: 'track', title: 'Song', parent_title: 'Album', grandparent_title: 'Artist' })),
    ).toEqual({ title: 'Song', subtitle: 'Artist · Album' });
    expect(watchlistCardText(item('a', { type: 'album', title: 'Album', parent_title: 'Artist', year: 1999 }))).toEqual({
      title: 'Album',
      subtitle: 'Artist · 1999',
    });
    expect(watchlistCardText(item('m', { year: 2024 }))).toEqual({ title: 'Title m', subtitle: '2024' });
    expect(watchlistCardText(item('m'))).toEqual({ title: 'Title m', subtitle: undefined });
  });

  it('offers every other status as a move', () => {
    expect(moveTargets('watching')).toEqual(['plan_to_watch', 'on_hold', 'completed', 'dropped']);
  });

  it('reads the status from a card focus key', () => {
    expect(statusOfFocusKey('on_hold:abc')).toBe('on_hold');
    expect(statusOfFocusKey('trending:abc')).toBeNull();
    expect(statusOfFocusKey('abc')).toBeNull();
    expect(statusOfFocusKey(null)).toBeNull();
  });

  it('stamps a moved card with its new status', () => {
    const moved = withStatus(item('a'), 'completed', new Date('2026-10-17T12:00:00Z'));
    expect(moved.status).toBe('completed');
    expect(moved.status_updated_at).toBe('2026-10-17T12:00:00.000Z');
    expect(moved.status_created_at).toBe('2026-10-01T00:00:00Z');
  });
});

/** A server list of `n` cards, paged by offset / limit, with its total. */
function serverList(n: number, prefix = 'i') {
  const all = Array.from({ length: n }, (_, k) => item(`${prefix}${k}`));
  const calls: Array<[number, number]> = [];
  const fetch = async (offset: number, limit: number): Promise<PageResult<WatchlistItem>> => {
    calls.push([offset, limit]);
    return { items: all.slice(offset, offset + limit), total: all.length };
  };
  return { all, calls, fetch };
}

describe('WatchlistRow', () => {
  it('pages by what it has loaded until the total is in', async () => {
    const s = serverList(120);
    const row = new WatchlistRow('plan_to_watch');
    expect(await row.loadMore(s.fetch)).toBe(true);
    expect(row.items).toHaveLength(50);
    expect(row.total).toBe(120);
    expect(row.done).toBe(false);
    await row.loadMore(s.fetch);
    await row.loadMore(s.fetch);
    expect(row.items).toHaveLength(120);
    expect(row.done).toBe(true);
    expect(await row.loadMore(s.fetch)).toBe(false);
    expect(s.calls).toEqual([
      [0, 50],
      [50, 50],
      [100, 50],
    ]);
  });

  it('ends on a short page when the server sends no total', async () => {
    const row = new WatchlistRow('watching', 10);
    await row.loadMore(async () => ({ items: [item('a'), item('b')], total: null }));
    expect(row.done).toBe(true);
    expect(row.total).toBeNull();
  });

  it('ends on an empty page even below the total', async () => {
    const row = new WatchlistRow('watching', 2);
    await row.loadMore(async () => ({ items: [item('a'), item('b')], total: 5 }));
    expect(row.done).toBe(false);
    await row.loadMore(async () => ({ items: [], total: 5 }));
    expect(row.done).toBe(true);
  });

  it('drops duplicates, and stops on a page of nothing new', async () => {
    const row = new WatchlistRow('watching', 2);
    await row.loadMore(async () => ({ items: [item('a'), item('b')], total: 9 }));
    await row.loadMore(async () => ({ items: [item('b'), item('c')], total: 9 }));
    expect(ids(row.items)).toEqual(['a', 'b', 'c']);
    expect(row.done).toBe(false);
    await row.loadMore(async () => ({ items: [item('c')], total: 9 }));
    expect(row.done).toBe(true);
  });

  it('asks once at a time', async () => {
    const s = serverList(100);
    const row = new WatchlistRow('plan_to_watch');
    const first = row.loadMore(s.fetch);
    expect(row.loading).toBe(true);
    expect(await row.loadMore(s.fetch)).toBe(false);
    await first;
    expect(s.calls).toHaveLength(1);
    expect(row.loading).toBe(false);
  });

  it('keeps the row after a failed page, and asks for it again', async () => {
    const s = serverList(100);
    const row = new WatchlistRow('plan_to_watch');
    await row.loadMore(s.fetch);
    await expect(row.loadMore(async () => Promise.reject(new Error('offline')))).rejects.toThrow('offline');
    expect(row.items).toHaveLength(50);
    expect(row.loading).toBe(false);
    await row.loadMore(s.fetch);
    expect(s.calls[1]).toEqual([50, 50]);
  });

  it('drops a page that lands after a reset', async () => {
    let resolve!: (p: PageResult<WatchlistItem>) => void;
    const row = new WatchlistRow('plan_to_watch');
    const pending = row.loadMore(() => new Promise((r) => (resolve = r)));
    row.reset();
    resolve({ items: [item('late')], total: 1 });
    expect(await pending).toBe(false);
    expect(row.items).toEqual([]);
    expect(row.done).toBe(false);
  });

  it('lines the next page up with the server after a card leaves', async () => {
    const s = serverList(120);
    const row = new WatchlistRow('plan_to_watch');
    await row.loadMore(s.fetch);
    expect(row.remove('i3')).toBe(3);
    expect(row.remove('missing')).toBe(-1);
    expect(row.total).toBe(119);
    // The server's list lost i3 too: its offset 49 is i50.
    s.all.splice(3, 1);
    await row.loadMore(s.fetch);
    expect(s.calls[1]).toEqual([49, 50]);
    expect(row.items).toHaveLength(99);
    expect(new Set(ids(row.items)).size).toBe(99);
  });

  it('puts a card that joins at the front, counting it once', () => {
    const row = new WatchlistRow('completed');
    row.items = [item('a'), item('b')];
    row.total = 5;
    row.addFront(item('c'));
    expect(ids(row.items)).toEqual(['c', 'a', 'b']);
    expect(row.total).toBe(6);
    row.addFront(item('a'));
    expect(ids(row.items)).toEqual(['a', 'c', 'b']);
    expect(row.total).toBe(6);
    expect(row.done).toBe(false);
  });

  it('starts a status that had nothing with the one card', () => {
    const row = WatchlistRow.empty('dropped');
    row.addFront(item('x'));
    expect(row.total).toBe(1);
    expect(row.done).toBe(true);
  });

  it('leaves an unknown total unknown when a card joins', () => {
    const row = new WatchlistRow('on_hold');
    row.addFront(item('x'));
    expect(row.total).toBeNull();
    expect(row.done).toBe(false);
  });
});

describe('visibleRows', () => {
  it('draws the rows with cards in row order, headed with their counts', () => {
    const done = new WatchlistRow('completed');
    done.items = [item('c')];
    done.total = 30;
    const plan = new WatchlistRow('plan_to_watch');
    plan.items = [item('p')];
    const empty = WatchlistRow.empty('watching');
    expect(visibleRows([done, empty, plan])).toEqual([
      { status: 'plan_to_watch', title: 'Plan to Watch · 1', items: [plan.items[0]] },
      { status: 'completed', title: 'Completed · 30', items: [done.items[0]] },
    ]);
  });
});

// ── The API client (fetch mocked) ────────────────────────────────────────

function memoryStorage() {
  const m = new Map<string, string>();
  return {
    getItem: (k: string) => (m.has(k) ? (m.get(k) as string) : null),
    setItem: (k: string, v: string) => void m.set(k, String(v)),
    removeItem: (k: string) => void m.delete(k),
    clear: () => m.clear(),
  };
}

function respond(status: number, body: unknown = {}): Response {
  return {
    status,
    ok: status >= 200 && status < 300,
    type: 'basic',
    redirected: false,
    url: '',
    statusText: '',
    json: async () => body,
  } as unknown as Response;
}

describe('endpoints.watchlist', () => {
  let fetchMock: ReturnType<typeof vi.fn>;
  let answer: Response;

  beforeEach(() => {
    vi.stubGlobal('localStorage', memoryStorage());
    answer = respond(200, { data: [] });
    fetchMock = vi.fn(async () => answer);
    vi.stubGlobal('fetch', fetchMock);
    api.setOrigin('https://onscreen.example');
    api.setTokens({
      access_token: 'access',
      refresh_token: 'refresh',
      asset_token: 'asset',
      expires_at: '2099-01-01T00:00:00Z',
      user_id: 'u1',
      username: 'u1',
      is_admin: false,
    });
  });

  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it('lists a page of one status with its total', async () => {
    answer = respond(200, { data: [item('a'), item('b')], meta: { total: 61, cursor: '' } });
    const page = await watchlist.list({ status: 'watching', limit: 50, offset: 50 });
    expect(ids(page.items)).toEqual(['a', 'b']);
    expect(page.total).toBe(61);
    const [url, init] = fetchMock.mock.calls[0];
    expect(url).toBe('https://onscreen.example/api/v1/watchlist?status=watching&limit=50&offset=50');
    expect((init as RequestInit).method ?? 'GET').toBe('GET');
  });

  it('reads the counts from under data', async () => {
    answer = respond(200, {
      data: { total: 3, statuses: { plan_to_watch: { total: 2, types: { movie: 2 } }, dropped: { total: 1, types: { show: 1 } } } },
    });
    const c = await watchlist.counts();
    expect(fetchMock.mock.calls[0][0]).toBe('https://onscreen.example/api/v1/watchlist/counts');
    expect(statusesToLoad(c)).toEqual(['plan_to_watch', 'dropped']);
  });

  it('rejects with the status on a server without the watchlist', async () => {
    answer = respond(404, { error: { code: 'NOT_FOUND', message: 'not found' } });
    await expect(watchlist.counts()).rejects.toMatchObject({ status: 404 });
  });
});

describe('Status on a title page', () => {
  it('is offered on the title types the watchlist lists', () => {
    for (const t of ['movie', 'show', 'season', 'episode', 'album', 'artist', 'audiobook', 'book']) {
      expect(offersStatus(t)).toBe(true);
    }
    for (const t of ['track', 'photo', 'book_author', 'book_series', 'podcast', 'audiobook_chapter', '', null, undefined]) {
      expect(offersStatus(t)).toBe(false);
    }
  });

  it('reads the status, "" and anything unknown as none', () => {
    expect(parseItemStatus({ status: 'on_hold', created_at: 'x', updated_at: 'y' })).toBe('on_hold');
    expect(parseItemStatus({ status: '' })).toBeNull();
    expect(parseItemStatus({ status: 'finished' })).toBeNull();
    expect(parseItemStatus(null)).toBeNull();
    expect(parseItemStatus(undefined)).toBeNull();
    expect(parseItemStatus('watching')).toBeNull();
  });

  it('labels the button by the status', () => {
    expect(statusButtonLabel(null)).toBe('Add to Watchlist');
    expect(statusButtonLabel('watching')).toBe('Status: Watching');
    expect(statusButtonLabel('plan_to_watch')).toBe('Status: Plan to Watch');
    expect(statusButtonLabel(undefined)).toBe('Watchlist');
  });

  it('lists the five statuses, the current one checked, then Remove', () => {
    expect(statusChoices('on_hold')).toEqual([
      { status: 'plan_to_watch', label: 'Plan to Watch' },
      { status: 'watching', label: 'Watching' },
      { status: 'on_hold', label: '✓ On Hold' },
      { status: 'completed', label: 'Completed' },
      { status: 'dropped', label: 'Dropped' },
      { status: null, label: 'Remove from Watchlist', danger: true },
    ]);
    expect(statusChoiceIndex('on_hold')).toBe(2);
    expect(statusMenuMessage('on_hold')).toBe('On your watchlist as On Hold');
  });

  it('has nothing to remove with no status', () => {
    const choices = statusChoices(null);
    expect(choices.map((c) => c.status)).toEqual([...WATCHLIST_STATUSES]);
    expect(choices.some((c) => c.label.startsWith('✓'))).toBe(false);
    expect(statusChoiceIndex(null)).toBe(0);
    expect(statusMenuMessage(null)).toBe('Not on your watchlist');
  });

  it('offers Remove when the status is unknown, none checked', () => {
    const choices = statusChoices(undefined);
    expect(choices).toHaveLength(6);
    expect(choices[5]).toEqual({ status: null, label: 'Remove from Watchlist', danger: true });
    expect(choices.some((c) => c.label.startsWith('✓'))).toBe(false);
    expect(statusChoiceIndex(undefined)).toBe(0);
    expect(statusMenuMessage(undefined)).toBeUndefined();
  });

  it('says what a saved pick did', () => {
    expect(statusChangedText('completed')).toBe('Status: Completed');
    expect(statusChangedText(null)).toBe('Removed from your watchlist');
  });

  it("points the empty Watchlist at Status on a title's page, keeping the hold-OK hint", () => {
    expect(WATCHLIST_EMPTY).toContain('Status');
    expect(WATCHLIST_EMPTY).toContain('page');
    expect(WATCHLIST_EMPTY).toContain('hold OK');
    expect(WATCHLIST_EMPTY).not.toContain('web or phone');
  });
});

describe('readItemStatus / writeItemStatus', () => {
  function fake(over: Partial<ItemStatusApi> = {}) {
    const calls: string[] = [];
    const api: ItemStatusApi = {
      get: async (id) => {
        calls.push(`get ${id}`);
        return { status: 'watching' };
      },
      set: async (id, s) => {
        calls.push(`set ${id} ${s}`);
        return { status: s };
      },
      clear: async (id) => {
        calls.push(`clear ${id}`);
        return undefined;
      },
      ...over,
    };
    return { api, calls };
  }

  it("reads the title's status", async () => {
    const { api, calls } = fake();
    expect(await readItemStatus(api, 'm1')).toBe('watching');
    expect(calls).toEqual(['get m1']);
  });

  it('reads a 404 as none', async () => {
    const { api } = fake({ get: async () => Promise.reject(Object.assign(new Error('nf'), { status: 404 })) });
    expect(await readItemStatus(api, 'm1')).toBeNull();
  });

  it('rejects any other failure', async () => {
    const { api } = fake({ get: async () => Promise.reject(Object.assign(new Error('boom'), { status: 500 })) });
    await expect(readItemStatus(api, 'm1')).rejects.toMatchObject({ status: 500 });
    const offline = fake({ get: async () => Promise.reject(new TypeError('Failed to fetch')) });
    await expect(readItemStatus(offline.api, 'm1')).rejects.toBeInstanceOf(TypeError);
  });

  it('PUTs a status and resolves to what the server saved', async () => {
    const { api, calls } = fake();
    expect(await writeItemStatus(api, 'm1', 'completed')).toBe('completed');
    expect(calls).toEqual(['set m1 completed']);
  });

  it('falls back to the pick when the PUT answers without a status', async () => {
    const { api } = fake({ set: async () => undefined });
    expect(await writeItemStatus(api, 'm1', 'dropped')).toBe('dropped');
  });

  it('DELETEs for Remove', async () => {
    const { api, calls } = fake();
    expect(await writeItemStatus(api, 'm1', null)).toBeNull();
    expect(calls).toEqual(['clear m1']);
  });

  it('rejects a failed write, so the page keeps the old status', async () => {
    const { api } = fake({ set: async () => Promise.reject(Object.assign(new Error('slow down'), { status: 429 })) });
    await expect(writeItemStatus(api, 'm1', 'watching')).rejects.toMatchObject({ status: 429 });
  });
});

describe('endpoints.watchStatus', () => {
  let fetchMock: ReturnType<typeof vi.fn>;
  let answer: Response;

  beforeEach(() => {
    vi.stubGlobal('localStorage', memoryStorage());
    answer = respond(200, {});
    fetchMock = vi.fn(async () => answer);
    vi.stubGlobal('fetch', fetchMock);
    api.setOrigin('https://onscreen.example');
    api.setTokens({
      access_token: 'access',
      refresh_token: 'refresh',
      asset_token: 'asset',
      expires_at: '2099-01-01T00:00:00Z',
      user_id: 'u1',
      username: 'u1',
      is_admin: false,
    });
  });

  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it('GETs the status from the body the server sends (fields at the top and under data)', async () => {
    const st = { status: 'watching', created_at: '2026-10-01T00:00:00Z', updated_at: '2026-10-02T00:00:00Z' };
    answer = respond(200, { ...st, data: st });
    expect(await readItemStatus(watchStatus, 'm1')).toBe('watching');
    const [url, init] = fetchMock.mock.calls[0];
    expect(url).toBe('https://onscreen.example/api/v1/items/m1/watch-status');
    expect((init as RequestInit).method ?? 'GET').toBe('GET');
  });

  it('reads status "" as none', async () => {
    const st = { status: '', created_at: '0001-01-01T00:00:00Z', updated_at: '0001-01-01T00:00:00Z' };
    answer = respond(200, { ...st, data: st });
    expect(await readItemStatus(watchStatus, 'm1')).toBeNull();
  });

  it('reads a 404 as none', async () => {
    answer = respond(404, { error: { code: 'NOT_FOUND', message: 'not found' } });
    expect(await readItemStatus(watchStatus, 'm1')).toBeNull();
  });

  it('PUTs the status as JSON', async () => {
    const st = { status: 'on_hold', created_at: 'a', updated_at: 'b' };
    answer = respond(200, { ...st, data: st });
    expect(await writeItemStatus(watchStatus, 'm1', 'on_hold')).toBe('on_hold');
    const [url, init] = fetchMock.mock.calls[0];
    expect(url).toBe('https://onscreen.example/api/v1/items/m1/watch-status');
    expect((init as RequestInit).method).toBe('PUT');
    expect(JSON.parse((init as RequestInit).body as string)).toEqual({ status: 'on_hold' });
  });

  it('DELETEs to remove', async () => {
    answer = respond(204);
    expect(await writeItemStatus(watchStatus, 'm1', null)).toBeNull();
    const [url, init] = fetchMock.mock.calls[0];
    expect(url).toBe('https://onscreen.example/api/v1/items/m1/watch-status');
    expect((init as RequestInit).method).toBe('DELETE');
  });

  it('rejects a refused PUT with its status', async () => {
    answer = respond(500, { error: { code: 'INTERNAL', message: 'nope' } });
    await expect(writeItemStatus(watchStatus, 'm1', 'watching')).rejects.toMatchObject({ status: 500 });
  });
});
