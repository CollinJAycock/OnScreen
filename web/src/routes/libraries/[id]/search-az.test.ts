import { render, screen, waitFor, fireEvent, within } from '@testing-library/svelte';
import { readable } from 'svelte/store';
import type { MediaItem, SearchResult, ItemLetter } from '$lib/api';

const mockLibraryGet = vi.hoisted(() => vi.fn());
const mockListItems = vi.hoisted(() => vi.fn());
const mockItemLetters = vi.hoisted(() => vi.fn());
const mockSearch = vi.hoisted(() => vi.fn());
const pageUrl = vi.hoisted(() => ({ href: 'http://localhost/libraries/lib-1' }));

vi.mock('$app/navigation', () => ({ goto: vi.fn(), replaceState: vi.fn() }));
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
    itemLetters: mockItemLetters,
    genres: vi.fn().mockResolvedValue([]),
    randomItem: vi.fn(),
    eventCollections: vi.fn().mockResolvedValue([]),
    enrichItem: vi.fn(),
  },
  itemApi: { markWatched: vi.fn(), markUnwatched: vi.fn() },
  searchApi: { search: mockSearch },
  playlistApi: { list: vi.fn().mockResolvedValue([]) },
  assetUrl: (p: string) => p,
}));
vi.mock('$lib/stores/toast', () => ({ toast: { success: vi.fn(), error: vi.fn() } }));

import Page from './+page.svelte';

const movieLib = { id: 'lib-1', name: 'Movies', type: 'movie', scan_paths: ['/m'] };

const movie = (id: string, title: string): MediaItem =>
  ({ id, title, type: 'movie', created_at: '', updated_at: '1' });
const items = [movie('m-1', 'Alien'), movie('m-2', 'Heat'), movie('m-3', 'Ran')];
const hit = (id: string, title: string, type = 'movie', extra: Partial<SearchResult> = {}): SearchResult =>
  ({ id, library_id: 'lib-1', title, type, ...extra });

function deferred<T>() {
  let resolve!: (v: T) => void;
  const promise = new Promise<T>((r) => { resolve = r; });
  return { promise, resolve };
}

const searchBox = () => screen.getByPlaceholderText('Search this library…') as HTMLInputElement;
const gridTitles = () =>
  [...document.querySelectorAll('[data-item-id] .item-title')].map((el) => el.textContent);

beforeEach(() => {
  vi.clearAllMocks();
  localStorage.clear();
  localStorage.setItem('onscreen_user', JSON.stringify({ id: '1', username: 'u' }));
  pageUrl.href = 'http://localhost/libraries/lib-1';
  mockLibraryGet.mockResolvedValue(movieLib);
  mockListItems.mockResolvedValue({ items, total: items.length });
  mockItemLetters.mockResolvedValue([]);
  mockSearch.mockResolvedValue([]);
});

describe('Library page — search this library', () => {
  it('searches the server for the whole library, debounced', async () => {
    mockSearch.mockResolvedValue([hit('m-9', 'Alien³', 'movie', { year: 1992 })]);
    render(Page);
    await screen.findByText('Heat', { selector: '.item-title' });

    await fireEvent.input(searchBox(), { target: { value: 'ali' } });
    await fireEvent.input(searchBox(), { target: { value: 'alie' } });
    await fireEvent.input(searchBox(), { target: { value: 'alien' } });

    expect(await screen.findByText('Alien³', { selector: '.item-title' })).toBeTruthy();
    // One request, for the last text, scoped to this library.
    expect(mockSearch).toHaveBeenCalledTimes(1);
    expect(mockSearch).toHaveBeenCalledWith('alien', 'lib-1', 100);
    // The results replace the grid: not a filter of what's loaded.
    expect(gridTitles()).toEqual([]);
    expect(screen.getByText('1 match')).toBeTruthy();
  });

  it('drops a response to an older query', async () => {
    const first = deferred<SearchResult[]>();
    const second = deferred<SearchResult[]>();
    mockSearch.mockReturnValueOnce(first.promise).mockReturnValueOnce(second.promise);
    render(Page);
    await screen.findByText('Heat', { selector: '.item-title' });

    await fireEvent.input(searchBox(), { target: { value: 'hea' } });
    await waitFor(() => expect(mockSearch).toHaveBeenCalledTimes(1));
    await fireEvent.input(searchBox(), { target: { value: 'ran' } });
    await waitFor(() => expect(mockSearch).toHaveBeenCalledTimes(2));

    second.resolve([hit('m-3', 'Ran')]);
    expect(await screen.findByRole('link', { name: /Ran/ })).toBeTruthy();
    first.resolve([hit('m-2', 'Heat')]);
    await new Promise((r) => setTimeout(r, 0));
    expect(screen.queryByRole('link', { name: /Heat/ })).toBeNull();
    expect(screen.getByRole('link', { name: /Ran/ })).toBeTruthy();
  });

  it('says so when nothing matches, and clearing restores the grid as it was', async () => {
    render(Page);
    await screen.findByText('Heat', { selector: '.item-title' });
    const listCalls = mockListItems.mock.calls.length;

    await fireEvent.input(searchBox(), { target: { value: 'zzz' } });
    expect(await screen.findByText('No results for "zzz" in Movies')).toBeTruthy();

    await fireEvent.click(screen.getByLabelText('Clear search'));
    await waitFor(() => expect(gridTitles()).toEqual(['Alien', 'Heat', 'Ran']));
    expect(searchBox().value).toBe('');
    // The listing was kept, not reloaded.
    expect(mockListItems.mock.calls.length).toBe(listCalls);
  });

  it('opens an episode or a season where the global search does', async () => {
    mockLibraryGet.mockResolvedValue({ ...movieLib, type: 'show', name: 'TV' });
    mockSearch.mockResolvedValue([
      hit('s-1', 'Lost', 'show', { year: 2004 }),
      hit('e-1', 'Pilot', 'episode', { thumb_path: 'e/1.jpg' }),
    ]);
    render(Page);
    await screen.findByText('Heat', { selector: '.item-title' });
    await fireEvent.input(searchBox(), { target: { value: 'lost' } });

    const episode = await screen.findByRole('link', { name: /Pilot/ });
    expect(episode.getAttribute('href')).toBe('/watch/e-1');
    // Mixed types say what each result is.
    expect(within(episode).getByText('Episode')).toBeTruthy();
    expect(screen.getByRole('link', { name: /Lost/ }).textContent).toMatch(/Show · 2004/);
  });

  it('clears with Escape', async () => {
    mockSearch.mockResolvedValue([hit('m-3', 'Ran')]);
    render(Page);
    await screen.findByText('Heat', { selector: '.item-title' });
    await fireEvent.input(searchBox(), { target: { value: 'ran' } });
    await screen.findByRole('link', { name: /Ran/ });
    await fireEvent.keyDown(searchBox(), { key: 'Escape' });
    await waitFor(() => expect(gridTitles()).toEqual(['Alien', 'Heat', 'Ran']));
  });
});

describe('Library page — A-Z rail', () => {
  const letters: ItemLetter[] = [
    { letter: '#', count: 2, offset: 0 },
    { letter: 'A', count: 40, offset: 2 },
    { letter: 'M', count: 60, offset: 240 },
    { letter: 'Z', count: 5, offset: 495 },
  ];
  const page = (from: number, n: number) =>
    Array.from({ length: n }, (_, i) => movie(`m-${from + i}`, `Title ${from + i}`));

  beforeEach(() => {
    mockItemLetters.mockResolvedValue(letters);
    mockListItems.mockImplementation((_id: string, limit: number, offset: number) =>
      Promise.resolve({ items: page(offset, Math.min(limit, 500 - offset)), total: 500 }));
  });

  it('lists every letter with the empty ones disabled', async () => {
    render(Page);
    const rail = await screen.findByRole('navigation', { name: 'Jump to letter' });
    expect(mockItemLetters).toHaveBeenCalledWith('lib-1', expect.objectContaining({ sort: 'title', sort_dir: 'asc' }));
    const buttons = within(rail).getAllByRole('button');
    expect(buttons.map((b) => b.textContent)).toEqual(['#', ...'ABCDEFGHIJKLMNOPQRSTUVWXYZ']);
    expect((within(rail).getByRole('button', { name: /starting with M \(60\)/ }) as HTMLButtonElement).disabled).toBe(false);
    expect((within(rail).getByRole('button', { name: 'No titles start with B' }) as HTMLButtonElement).disabled).toBe(true);
  });

  it('is one tab stop, moved along with the arrow keys', async () => {
    render(Page);
    const rail = await screen.findByRole('navigation', { name: 'Jump to letter' });
    const hash = within(rail).getByRole('button', { name: /number or symbol/ });
    expect(rail.querySelectorAll('button[tabindex="0"]')).toHaveLength(1);
    expect(hash.getAttribute('tabindex')).toBe('0');
    hash.focus();
    await fireEvent.keyDown(hash, { key: 'ArrowDown' });
    expect(document.activeElement?.textContent).toBe('A');
    // B-L are empty: the next stop is M.
    await fireEvent.keyDown(document.activeElement!, { key: 'ArrowDown' });
    expect(document.activeElement?.textContent).toBe('M');
    await fireEvent.keyDown(document.activeElement!, { key: 'End' });
    expect(document.activeElement?.textContent).toBe('Z');
    expect(within(rail).getByRole('button', { name: /starting with Z/ }).getAttribute('tabindex')).toBe('0');
  });

  it('jumps to a letter by loading the page it starts on, then loads earlier rows', async () => {
    render(Page);
    const rail = await screen.findByRole('navigation', { name: 'Jump to letter' });
    await fireEvent.click(within(rail).getByRole('button', { name: /starting with M/ }));

    await waitFor(() => expect(gridTitles()[0]).toBe('Title 240'));
    // One request, at the letter's offset — nothing before it is loaded.
    const jump = mockListItems.mock.calls.at(-1)!;
    expect(jump.slice(0, 3)).toEqual(['lib-1', 48, 240]);
    expect(jump[3]).toMatchObject({ sort: 'title', sort_dir: 'asc' });
    expect(gridTitles()).toHaveLength(48);

    await fireEvent.click(screen.getByRole('button', { name: 'Load earlier titles' }));
    await waitFor(() => expect(gridTitles()[0]).toBe('Title 192'));
    expect(mockListItems.mock.calls.at(-1)!.slice(0, 3)).toEqual(['lib-1', 48, 192]);
    expect(gridTitles()).toHaveLength(96);
    expect(gridTitles()[48]).toBe('Title 240');
  });

  it('scrolls to a letter already loaded instead of fetching it', async () => {
    render(Page);
    const rail = await screen.findByRole('navigation', { name: 'Jump to letter' });
    const calls = mockListItems.mock.calls.length;
    await fireEvent.click(within(rail).getByRole('button', { name: /starting with A/ }));
    await new Promise((r) => setTimeout(r, 0));
    expect(mockListItems.mock.calls.length).toBe(calls);
    expect(screen.queryByRole('button', { name: 'Load earlier titles' })).toBeNull();
  });

  it('is hidden on any sort but title', async () => {
    render(Page);
    await screen.findByRole('navigation', { name: 'Jump to letter' });
    const letterCalls = mockItemLetters.mock.calls.length;

    await fireEvent.click(screen.getByRole('button', { name: 'Year' }));
    await waitFor(() => expect(mockListItems.mock.calls.at(-1)![3]).toMatchObject({ sort: 'year' }));
    expect(screen.queryByRole('navigation', { name: 'Jump to letter' })).toBeNull();
    // No index is asked for a sort that has none.
    expect(mockItemLetters.mock.calls.length).toBe(letterCalls);
  });

  it('is re-fetched with the listing when a filter changes', async () => {
    render(Page);
    await screen.findByRole('navigation', { name: 'Jump to letter' });
    await fireEvent.change(screen.getByLabelText('Filter by watch state'), { target: { value: 'unwatched' } });
    await waitFor(() => expect(mockItemLetters).toHaveBeenLastCalledWith('lib-1', expect.objectContaining({ watch: 'unwatched' })));
  });

  it('is hidden when the server has no index (404)', async () => {
    mockItemLetters.mockRejectedValue(Object.assign(new Error('not found'), { status: 404 }));
    render(Page);
    await screen.findByText('Title 0', { selector: '.item-title' });
    await new Promise((r) => setTimeout(r, 0));
    expect(screen.queryByRole('navigation', { name: 'Jump to letter' })).toBeNull();
  });

  it('is hidden while searching', async () => {
    mockSearch.mockResolvedValue([hit('m-3', 'Ran')]);
    render(Page);
    await screen.findByRole('navigation', { name: 'Jump to letter' });
    await fireEvent.input(searchBox(), { target: { value: 'ran' } });
    await screen.findByRole('link', { name: /Ran/ });
    expect(screen.queryByRole('navigation', { name: 'Jump to letter' })).toBeNull();
  });
});
