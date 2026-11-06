import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { api } from './api/client';
import { search as searchApi } from './api/endpoints';
import type { SearchResult } from './api/types';
import {
  LibrarySearch,
  SEARCH_DELAY_MS,
  SEARCH_LIMIT,
  searchResultKey,
  type SearchFn,
  type SearchState,
} from './librarySearch';

const hit = (id: string): SearchResult => ({ id, library_id: 'lib1', title: id, type: 'movie' });

function deferred<T>() {
  let resolve!: (v: T) => void;
  let reject!: (e: unknown) => void;
  const promise = new Promise<T>((res, rej) => {
    resolve = res;
    reject = rej;
  });
  return { promise, resolve, reject };
}

/** A search over library `lib1` whose states are recorded. */
function setup(search: SearchFn) {
  const states: SearchState[] = [];
  const s = new LibrarySearch({ libraryId: () => 'lib1', search, onstate: (st) => states.push(st) });
  const last = () => states[states.length - 1];
  return { s, states, last };
}

describe('LibrarySearch', () => {
  beforeEach(() => {
    vi.useFakeTimers();
  });
  afterEach(() => {
    vi.useRealTimers();
  });

  it('searches this library once typing pauses', async () => {
    const search = vi.fn<SearchFn>(async () => [hit('a')]);
    const { s, last } = setup(search);
    s.type('st');
    s.type('sta');
    s.type('star');
    await vi.advanceTimersByTimeAsync(SEARCH_DELAY_MS - 1);
    expect(search).not.toHaveBeenCalled();
    await vi.advanceTimersByTimeAsync(1);
    expect(search.mock.calls).toEqual([['star', SEARCH_LIMIT, 'lib1']]);
    expect(last()).toEqual({ kind: 'results', results: [hit('a')] });
  });

  it('asks for nothing under two characters, and clears what was showing', async () => {
    const search = vi.fn<SearchFn>(async () => [hit('a')]);
    const { s, last } = setup(search);
    s.type('s');
    s.type(' s ');
    await vi.advanceTimersByTimeAsync(SEARCH_DELAY_MS * 2);
    expect(search).not.toHaveBeenCalled();
    expect(last()).toEqual({ kind: 'idle' });
  });

  it('ignores an older search that answers after a newer one', async () => {
    const slow = deferred<SearchResult[]>();
    const fast = deferred<SearchResult[]>();
    const answers = [slow.promise, fast.promise];
    const { s, states, last } = setup(vi.fn<SearchFn>(() => answers.shift()!));
    s.type('dre');
    await vi.advanceTimersByTimeAsync(SEARCH_DELAY_MS);
    s.type('dream');
    await vi.advanceTimersByTimeAsync(SEARCH_DELAY_MS);
    fast.resolve([hit('dream')]);
    await vi.advanceTimersByTimeAsync(0);
    slow.resolve([hit('dre')]);
    await vi.advanceTimersByTimeAsync(0);
    expect(last()).toEqual({ kind: 'results', results: [hit('dream')] });
    expect(states.some((st) => st.kind === 'results' && st.results[0].id === 'dre')).toBe(false);
  });

  it('ignores an older search’s failure, and reports the newest one’s', async () => {
    const old = deferred<SearchResult[]>();
    const answers = [() => old.promise, () => Promise.reject(new Error('offline'))];
    const { s, last } = setup(vi.fn<SearchFn>(() => answers.shift()!()));
    s.type('dre');
    await vi.advanceTimersByTimeAsync(SEARCH_DELAY_MS);
    s.type('dream');
    await vi.advanceTimersByTimeAsync(SEARCH_DELAY_MS);
    expect(last().kind).toBe('failed');
    old.reject(new Error('timeout'));
    await vi.advanceTimersByTimeAsync(0);
    const st = last();
    expect(st.kind === 'failed' && (st.error as Error).message).toBe('offline');
  });

  it('drops an answer that was in flight when the text went under two characters', async () => {
    const slow = deferred<SearchResult[]>();
    const { s, last } = setup(vi.fn<SearchFn>(() => slow.promise));
    s.type('dr');
    await vi.advanceTimersByTimeAsync(SEARCH_DELAY_MS);
    s.type('d');
    slow.resolve([hit('dr')]);
    await vi.advanceTimersByTimeAsync(0);
    expect(last()).toEqual({ kind: 'idle' });
  });

  it('shows an empty answer as no results', async () => {
    const { s, last } = setup(async () => []);
    expect(await s.now('zzzz')).toBe(true);
    expect(last()).toEqual({ kind: 'results', results: [] });
  });

  it('searches straight away for Back to the panel, and not at all once closed', async () => {
    const search = vi.fn<SearchFn>(async () => [hit('a')]);
    const { s, states } = setup(search);
    expect(await s.now('alien')).toBe(true);
    expect(search).toHaveBeenCalledTimes(1);
    s.type('aliens');
    s.dispose();
    await vi.advanceTimersByTimeAsync(SEARCH_DELAY_MS * 2);
    expect(search).toHaveBeenCalledTimes(1);
    expect(await s.now('aliens')).toBe(false);
    expect(states.filter((st) => st.kind === 'results')).toHaveLength(1);
  });

  it('keys result cards apart from the grid’s cards', () => {
    expect(searchResultKey('abc')).toBe('search:abc');
  });
});

// ── The request (fetch mocked) ───────────────────────────────────────────

function memoryStorage() {
  const m = new Map<string, string>();
  return {
    getItem: (k: string) => (m.has(k) ? (m.get(k) as string) : null),
    setItem: (k: string, v: string) => void m.set(k, String(v)),
    removeItem: (k: string) => void m.delete(k),
    clear: () => m.clear(),
  };
}

describe('the library search request', () => {
  let fetchMock: ReturnType<typeof vi.fn>;

  beforeEach(() => {
    vi.stubGlobal('localStorage', memoryStorage());
    fetchMock = vi.fn(
      async () =>
        ({
          status: 200,
          ok: true,
          type: 'basic',
          redirected: false,
          url: '',
          statusText: '',
          json: async () => ({ data: [hit('m1')] }),
        }) as unknown as Response,
    );
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

  it('calls /search with the library_id', async () => {
    const states: SearchState[] = [];
    const s = new LibrarySearch({
      libraryId: () => 'lib 1',
      search: (q, limit, id) => searchApi.query(q, limit, id),
      onstate: (st) => states.push(st),
    });
    expect(await s.now('star wa')).toBe(true);
    expect(fetchMock.mock.calls.map((c) => String(c[0]))).toEqual([
      `https://onscreen.example/api/v1/search?q=star%20wa&limit=${SEARCH_LIMIT}&library_id=lib%201`,
    ]);
    expect(states[states.length - 1]).toEqual({ kind: 'results', results: [hit('m1')] });
  });
});
