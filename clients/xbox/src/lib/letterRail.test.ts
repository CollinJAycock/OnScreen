import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { api, ApiError } from './api/client';
import { libraries } from './api/endpoints';
import type { LetterJump, MediaItem } from './api/types';
import { letterAt, loadLetters, planJump, railSort } from './letterRail';
import { Pager } from './paging';

const TITLE = { sort: 'title', dir: 'asc' } as const;
const L = (letter: string, offset: number, count = 1): LetterJump => ({ letter, offset, count });

describe('railSort', () => {
  it('shows the rail in title order only', () => {
    expect(railSort({ sort: 'title', dir: 'asc' })).toBe(true);
    expect(railSort({ sort: 'title', dir: 'desc' })).toBe(true);
    for (const sort of ['created_at', 'year', 'rating']) {
      expect(railSort({ sort, dir: 'desc' })).toBe(false);
    }
  });
});

describe('loadLetters', () => {
  it('asks for nothing when the grid is not in title order', async () => {
    const fetch = vi.fn(async () => [L('A', 0)]);
    expect(await loadLetters({ sort: 'created_at', dir: 'desc' }, fetch)).toEqual([]);
    expect(await loadLetters({ sort: 'year', dir: 'asc' }, fetch)).toEqual([]);
    expect(fetch).not.toHaveBeenCalled();
  });

  it('has no rail on a server without the index (404) or when the request fails', async () => {
    const missing = new ApiError(404, 'NOT_FOUND', 'not found');
    expect(await loadLetters(TITLE, async () => Promise.reject(missing))).toEqual([]);
    expect(await loadLetters(TITLE, async () => Promise.reject(new Error('offline')))).toEqual([]);
  });

  it('keeps the letters in listing order, and drops rows that do not parse or repeat', async () => {
    const rows = [
      L('#', 0, 3),
      L('A', 3, 2),
      { letter: '', offset: 5, count: 1 },
      { letter: 'B', offset: -1, count: 1 },
      { letter: 'C', offset: Number.NaN, count: 1 },
      L('D', 5, 0),
      L('E', 5, 4),
      L('E', 9, 1),
      null,
    ] as unknown as LetterJump[];
    expect(await loadLetters(TITLE, async () => rows)).toEqual([L('#', 0, 3), L('A', 3, 2), L('E', 5, 4)]);
    expect(await loadLetters(TITLE, async () => ({}) as unknown as LetterJump[])).toEqual([]);
  });

  it('is empty when the listing has no letters', async () => {
    expect(await loadLetters(TITLE, async () => [])).toEqual([]);
  });
});

describe('letterAt', () => {
  const letters = [L('#', 0), L('A', 4), L('B', 10), L('Z', 200)];

  it('finds the letter a listing row falls under', () => {
    expect(letterAt(letters, 0)).toBe(0);
    expect(letterAt(letters, 3)).toBe(0);
    expect(letterAt(letters, 4)).toBe(1);
    expect(letterAt(letters, 199)).toBe(2);
    expect(letterAt(letters, 5000)).toBe(3);
  });

  it('is -1 before the first letter or with no letters', () => {
    expect(letterAt([L('A', 2)], 1)).toBe(-1);
    expect(letterAt([], 0)).toBe(-1);
  });
});

describe('planJump', () => {
  it('focuses the letter’s first item when it is already in the grid', () => {
    expect(planJump(L('M', 120), 0, 300, 6)).toEqual({ kind: 'focus', index: 120 });
    expect(planJump(L('M', 120), 100, 50, 6)).toEqual({ kind: 'focus', index: 20 });
  });

  it('opens the grid at the start of the letter’s row when it is not, focusing the letter', () => {
    // 4370 is the third card of the row starting at 4368: the two before it
    // fill the row instead of blank space.
    expect(planJump(L('S', 4370), 0, 200, 6)).toEqual({ kind: 'load', offset: 4368, index: 2 });
    // Above a grid that was itself opened at a letter; 12 starts a row.
    expect(planJump(L('A', 12), 4368, 200, 6)).toEqual({ kind: 'load', offset: 12, index: 0 });
    // Nothing loaded yet.
    expect(planJump(L('A', 0), 0, 0, 6)).toEqual({ kind: 'load', offset: 0, index: 0 });
  });
});

// ── The requests (fetch mocked) ──────────────────────────────────────────

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

describe('the rail’s requests', () => {
  let fetchMock: ReturnType<typeof vi.fn>;
  let answer: Response;
  const urls = () => fetchMock.mock.calls.map((c) => String(c[0]));

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

  it('asks for the index with the grid’s sort and filters', async () => {
    answer = respond(200, { data: [L('A', 0, 2), L('B', 2, 5)] });
    const rows = await loadLetters(TITLE, () => libraries.letters('lib1', 'title', 'asc', 'unwatched', 'Sci-Fi & Fantasy'));
    expect(rows).toEqual([L('A', 0, 2), L('B', 2, 5)]);
    expect(urls()).toEqual([
      'https://onscreen.example/api/v1/libraries/lib1/items/letters?sort=title&sort_dir=asc&watch=unwatched&genre=Sci-Fi%20%26%20Fantasy',
    ]);
  });

  it('hides the rail when the server answers 404', async () => {
    answer = respond(404, { error: { code: 'NOT_FOUND', message: 'not found' } });
    expect(await loadLetters(TITLE, () => libraries.letters('lib1'))).toEqual([]);
    expect(urls()).toEqual(['https://onscreen.example/api/v1/libraries/lib1/items/letters?sort=title&sort_dir=asc']);
  });

  it('a letter opens the listing at its row, then pages on from there', async () => {
    const page = (from: number, n: number) =>
      Array.from({ length: n }, (_, i) => ({ id: `m${from + i}`, title: `T${from + i}`, type: 'movie' }));
    const pager = new Pager<MediaItem>((i) => i.id, 100);
    const plan = planJump(L('S', 4370, 250), pager.start, pager.items.length, 6);
    expect(plan).toEqual({ kind: 'load', offset: 4368, index: 2 });
    if (plan.kind !== 'load') return;
    pager.startAt(plan.offset);
    const fetchPage = (offset: number, limit: number) =>
      libraries.listItems('lib1', 'title', 'asc', '', '', limit, offset);
    answer = respond(200, { data: page(4368, 100) });
    await pager.loadMore(fetchPage);
    expect(pager.items[plan.index].id).toBe('m4370');
    answer = respond(200, { data: page(4468, 100) });
    await pager.loadMore(fetchPage);
    answer = respond(200, { data: page(4268, 100) });
    await pager.loadBefore(fetchPage);
    expect(urls()).toEqual([
      'https://onscreen.example/api/v1/libraries/lib1/items?sort=title&sort_dir=asc&limit=100&offset=4368',
      'https://onscreen.example/api/v1/libraries/lib1/items?sort=title&sort_dir=asc&limit=100&offset=4468',
      'https://onscreen.example/api/v1/libraries/lib1/items?sort=title&sort_dir=asc&limit=100&offset=4268',
    ]);
    expect(pager.start).toBe(4268);
    expect(pager.items[100].id).toBe('m4368');
  });
});
