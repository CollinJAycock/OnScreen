import { fireEvent, render, screen, waitFor } from '@testing-library/svelte';
import { readable } from 'svelte/store';

const mockReplaceState = vi.hoisted(() => vi.fn());
const mockLibraryGet = vi.hoisted(() => vi.fn());
const mockListItems = vi.hoisted(() => vi.fn());
const mockForLibrary = vi.hoisted(() => vi.fn());
const pageUrl = vi.hoisted(() => ({ href: 'http://localhost/libraries/lib-1' }));

vi.mock('$app/navigation', () => ({ goto: vi.fn(), replaceState: mockReplaceState }));
vi.mock('$app/stores', () => ({
  page: {
    subscribe: (fn: (v: unknown) => void) =>
      readable({ url: new URL(pageUrl.href), params: { id: 'lib-1' }, state: {} }).subscribe(fn),
  },
}));
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
  collectionApi: { forLibrary: mockForLibrary },
  assetUrl: (p: string) => p,
}));
vi.mock('$lib/stores/toast', () => ({ toast: { success: vi.fn(), error: vi.fn() } }));

import Page from './+page.svelte';

const movieLib = { id: 'lib-1', name: 'Movies', type: 'movie', scan_paths: ['/m'] };
const items = [{ id: 'm-1', title: 'Alien', type: 'movie', year: 1979, created_at: '', updated_at: '1' }];

beforeEach(() => {
  vi.clearAllMocks();
  localStorage.clear();
  localStorage.setItem('onscreen_user', JSON.stringify({ id: '1', username: 'u' }));
  pageUrl.href = 'http://localhost/libraries/lib-1';
  mockLibraryGet.mockResolvedValue(movieLib);
  mockListItems.mockResolvedValue({ items, total: 1 });
  mockForLibrary.mockResolvedValue([
    { id: 'c-1', name: 'Alien Collection', type: 'franchise', item_count: 2 },
  ]);
});

describe('Library page — Collections tab', () => {
  it('switches a movie library to its franchise collections and records ?tab=', async () => {
    render(Page);
    const tab = await screen.findByRole('tab', { name: 'Collections' });
    expect(screen.getByRole('tab', { name: 'Library' }).getAttribute('aria-selected')).toBe('true');
    expect(mockForLibrary).not.toHaveBeenCalled();

    await fireEvent.click(tab);

    expect(await screen.findByRole('link', { name: /Alien Collection/ })).toBeTruthy();
    expect(mockForLibrary).toHaveBeenCalledWith('lib-1');
    expect(tab.getAttribute('aria-selected')).toBe('true');
    // The grid controls are replaced by the tab content.
    expect(screen.queryByPlaceholderText('Filter…')).toBeNull();
    const url = mockReplaceState.mock.calls.at(-1)?.[0] as URL;
    expect(url.searchParams.get('tab')).toBe('collections');

    await fireEvent.click(screen.getByRole('tab', { name: 'Library' }));
    expect(await screen.findByPlaceholderText('Filter…')).toBeTruthy();
    expect((mockReplaceState.mock.calls.at(-1)?.[0] as URL).searchParams.has('tab')).toBe(false);
  });

  it('opens straight on the Collections tab from ?tab=collections', async () => {
    pageUrl.href = 'http://localhost/libraries/lib-1?tab=collections';
    render(Page);
    expect(await screen.findByRole('link', { name: /Alien Collection/ })).toBeTruthy();
  });

  it('has no Collections tab outside movie libraries', async () => {
    mockLibraryGet.mockResolvedValue({ ...movieLib, type: 'show' });
    render(Page);
    await waitFor(() => expect(mockListItems).toHaveBeenCalled());
    await screen.findByPlaceholderText('Filter…');
    expect(screen.queryByRole('tab', { name: 'Collections' })).toBeNull();
  });
});
