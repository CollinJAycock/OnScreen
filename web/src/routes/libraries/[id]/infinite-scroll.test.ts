import { render, waitFor } from '@testing-library/svelte';
import type { MediaItem } from '$lib/api';

const mockListItems = vi.hoisted(() => vi.fn());
const mockLibraryGet = vi.hoisted(() => vi.fn());

// A page store the test can move to another library, as the sidebar does:
// same route, same component, new params.
const route = vi.hoisted(() => {
  let value = { url: new URL('http://localhost/libraries/lib-a'), params: { id: 'lib-a' }, state: {} };
  const subs = new Set<(v: unknown) => void>();
  return {
    subscribe(fn: (v: unknown) => void) { fn(value); subs.add(fn); return () => subs.delete(fn); },
    go(id: string) {
      value = { url: new URL(`http://localhost/libraries/${id}`), params: { id }, state: {} };
      for (const fn of subs) fn(value);
    },
  };
});

vi.mock('$app/navigation', () => ({ goto: vi.fn(), replaceState: vi.fn() }));
vi.mock('$lib/stores/page.svelte', () => ({ page: { subscribe: route.subscribe } }));
vi.mock('$lib/api', () => ({
  libraryApi: { get: mockLibraryGet, scan: vi.fn() },
  mediaApi: {
    listItems: mockListItems,
    genres: vi.fn().mockResolvedValue([]),
    randomItem: vi.fn(),
    eventCollections: vi.fn().mockResolvedValue([]),
    enrichItem: vi.fn(),
  },
  itemApi: { markWatched: vi.fn(), markUnwatched: vi.fn() },
  playlistApi: { list: vi.fn().mockResolvedValue([]) },
  assetUrl: (p: string) => p,
}));
vi.mock('$lib/stores/toast', () => ({ toast: { success: vi.fn(), error: vi.fn() } }));

import Page from './+page.svelte';

// lib-a fits on one screen (48 shows, nothing more to load); lib-b has 200.
const TOTALS: Record<string, number> = { 'lib-a': 48, 'lib-b': 200 };
function listing(lib: string, offset: number, limit: number) {
  const total = TOTALS[lib];
  const n = Math.max(0, Math.min(limit, total - offset));
  const items: MediaItem[] = Array.from({ length: n }, (_, i) => ({
    id: `${lib}-${offset + i}`, title: `Show ${offset + i}`, type: 'show', created_at: '', updated_at: '1',
  }));
  return { items, total };
}

// The sentinel sits on a screen taller than the rows loaded so far (a 4K
// window). A real IntersectionObserver reports changes, and a sentinel that
// stays in view reports nothing new; this one reports only when observe()
// is called, which is the same thing.
class InViewObserver {
  constructor(private cb: IntersectionObserverCallback) {}
  observe(node: Element) {
    queueMicrotask(() => this.cb([{ isIntersecting: true, target: node } as IntersectionObserverEntry], this as unknown as IntersectionObserver));
  }
  unobserve() {}
  disconnect() {}
  takeRecords() { return []; }
}

let releaseB: (() => void) | null = null;

beforeEach(() => {
  vi.clearAllMocks();
  localStorage.setItem('onscreen_user', JSON.stringify({ id: '1', username: 'u' }));
  vi.stubGlobal('IntersectionObserver', InViewObserver);
  releaseB = null;
  mockLibraryGet.mockImplementation((id: string) => Promise.resolve({ id, name: id, type: 'show', scan_paths: ['/tv'] }));
  mockListItems.mockImplementation((lib: string, limit: number, offset: number) => {
    // lib-b's first page is held until the test lets it go.
    if (lib === 'lib-b' && offset === 0 && releaseB === null) {
      return new Promise((resolve) => { releaseB = () => resolve(listing(lib, offset, limit)); });
    }
    return Promise.resolve(listing(lib, offset, limit));
  });
});

afterEach(() => vi.unstubAllGlobals());

const calls = (lib: string) => mockListItems.mock.calls.filter((c) => c[0] === lib).map((c) => c[2] as number);

describe('Library grid — auto-load', () => {
  it('keeps loading while the end of the grid is on screen', async () => {
    route.go('lib-b');
    releaseB = () => {}; // don't hold
    render(Page);
    await waitFor(() => expect(calls('lib-b')).toContain(192));
  });

  it('loads past the first page after switching libraries on a tall screen', async () => {
    route.go('lib-a');
    render(Page);
    await waitFor(() => expect(calls('lib-a')).toEqual([0]));

    // Sidebar to lib-b: same component; the end-of-grid sentinel stays
    // mounted and reports while lib-b's first page is still loading.
    route.go('lib-b');
    await waitFor(() => expect(releaseB).not.toBeNull());
    await new Promise((r) => setTimeout(r, 0));
    releaseB!();

    // The first page leaves the sentinel in view. Before the fix nothing
    // reported it again and the grid stopped at 48 of 200.
    await waitFor(() => expect(calls('lib-b')).toContain(48));
  });
});
