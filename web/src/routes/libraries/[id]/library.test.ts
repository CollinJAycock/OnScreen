import { render, screen, waitFor, fireEvent } from '@testing-library/svelte';
import { readable } from 'svelte/store';
import type { MediaItem } from '$lib/api';

const mockGoto = vi.hoisted(() => vi.fn());
const mockReplaceState = vi.hoisted(() => vi.fn());
const mockLibraryGet = vi.hoisted(() => vi.fn());
const mockListItems = vi.hoisted(() => vi.fn());
const mockGenres = vi.hoisted(() => vi.fn());
const mockRandomItem = vi.hoisted(() => vi.fn());
const mockMarkWatched = vi.hoisted(() => vi.fn());
const mockMarkUnwatched = vi.hoisted(() => vi.fn());
const mockToastError = vi.hoisted(() => vi.fn());
// The page reads ?watch= etc. from $page.url; tests set this before render.
const pageUrl = vi.hoisted(() => ({ href: 'http://localhost/libraries/lib-1' }));

vi.mock('$app/navigation', () => ({ goto: mockGoto, replaceState: mockReplaceState }));
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
    genres: mockGenres,
    randomItem: mockRandomItem,
    eventCollections: vi.fn().mockResolvedValue([]),
    enrichItem: vi.fn(),
  },
  itemApi: { markWatched: mockMarkWatched, markUnwatched: mockMarkUnwatched },
  playlistApi: { list: vi.fn().mockResolvedValue([]) },
  assetUrl: (p: string) => p,
}));
vi.mock('$lib/stores/toast', () => ({
  toast: { success: vi.fn(), error: mockToastError },
}));

import Page from './+page.svelte';

const movieLib = { id: 'lib-1', name: 'Movies', type: 'movie', scan_paths: ['/m'] };

const items: MediaItem[] = [
  { id: 'm-1', title: 'Alien', type: 'movie', year: 1979, created_at: '', updated_at: '1', watch_state: 'watched' },
  { id: 'm-2', title: 'Heat', type: 'movie', year: 1995, created_at: '', updated_at: '1',
    watch_state: 'in_progress', view_offset_ms: 30 * 60_000, duration_ms: 120 * 60_000 },
  { id: 'm-3', title: 'Ran', type: 'movie', year: 1985, created_at: '', updated_at: '1' },
];

function list(itemsOut: MediaItem[] = items) {
  return { items: itemsOut, total: itemsOut.length };
}

beforeEach(() => {
  vi.clearAllMocks();
  localStorage.clear();
  localStorage.setItem('onscreen_user', JSON.stringify({ id: '1', username: 'u' }));
  pageUrl.href = 'http://localhost/libraries/lib-1';
  mockLibraryGet.mockResolvedValue(movieLib);
  mockGenres.mockResolvedValue([]);
  mockListItems.mockResolvedValue(list());
});

describe('Library grid — watch filter', () => {
  it('lists with the chosen watch filter and writes it to the URL', async () => {
    render(Page);
    const select = await screen.findByLabelText('Filter by watch state');
    expect(mockListItems.mock.calls[0][3].watch).toBeUndefined();

    mockListItems.mockResolvedValue(list([items[2]]));
    await fireEvent.change(select, { target: { value: 'unwatched' } });

    await waitFor(() => expect(mockListItems).toHaveBeenCalledTimes(2));
    const params = mockListItems.mock.calls[1][3];
    expect(params.watch).toBe('unwatched');
    // Existing sort is kept alongside it.
    expect(params.sort).toBe('title');
    const url = mockReplaceState.mock.calls[0][0] as URL;
    expect(url.searchParams.get('watch')).toBe('unwatched');
  });

  it('hydrates the filter from ?watch=', async () => {
    pageUrl.href = 'http://localhost/libraries/lib-1?watch=in_progress&genre=Drama';
    render(Page);
    await waitFor(() => expect(mockListItems).toHaveBeenCalled());
    expect(mockListItems.mock.calls[0][3]).toMatchObject({ watch: 'in_progress', genre: 'Drama' });
    const select = (await screen.findByLabelText('Filter by watch state')) as HTMLSelectElement;
    expect(select.value).toBe('in_progress');
  });

  it('says so when a watch filter matches nothing', async () => {
    pageUrl.href = 'http://localhost/libraries/lib-1?watch=watched';
    mockListItems.mockResolvedValue(list([]));
    render(Page);
    await waitFor(() => expect(screen.getByText('Nothing watched here')).toBeTruthy());
    expect(screen.queryByText('Library is empty')).toBeNull();
  });

  it('is not offered on music libraries', async () => {
    mockLibraryGet.mockResolvedValue({ ...movieLib, type: 'music' });
    mockListItems.mockResolvedValue(list([{ id: 'a-1', title: 'Artist', type: 'artist', created_at: '', updated_at: '1' }]));
    render(Page);
    await waitFor(() => expect(screen.getByText('Artist', { selector: '.item-title' })).toBeTruthy());
    expect(screen.queryByLabelText('Filter by watch state')).toBeNull();
    expect(screen.queryByText('Surprise me')).toBeNull();
    expect(screen.queryByRole('button', { name: /More actions/ })).toBeNull();
  });
});

describe('Library grid — Surprise me', () => {
  it('opens a random item matching the current filters', async () => {
    pageUrl.href = 'http://localhost/libraries/lib-1?watch=unwatched&genre=Drama';
    mockRandomItem.mockResolvedValue({ id: 'm-3', type: 'movie' });
    render(Page);
    const btn = await screen.findByRole('button', { name: /Surprise me/ });
    await fireEvent.click(btn);
    await waitFor(() => expect(mockGoto).toHaveBeenCalledWith('/watch/m-3'));
    expect(mockRandomItem).toHaveBeenCalledWith('lib-1', expect.objectContaining({ watch: 'unwatched', genre: 'Drama' }));
  });

  it('routes containers to their own page', async () => {
    mockRandomItem.mockResolvedValue({ id: 'b-1', type: 'book' });
    render(Page);
    await fireEvent.click(await screen.findByRole('button', { name: /Surprise me/ }));
    await waitFor(() => expect(mockGoto).toHaveBeenCalledWith('/books/b-1'));
  });

  it('shows a friendly message when nothing matches', async () => {
    mockRandomItem.mockRejectedValue(Object.assign(new Error('not found'), { status: 404 }));
    render(Page);
    await fireEvent.click(await screen.findByRole('button', { name: /Surprise me/ }));
    await waitFor(() => expect(screen.getByRole('status').textContent).toMatch(/Nothing matches these filters/));
    expect(mockGoto).not.toHaveBeenCalled();
  });
});

describe('Library grid — watch badges and mark menu', () => {
  it('renders watched check, progress bar and unwatched count', async () => {
    mockListItems.mockResolvedValue(list([
      ...items,
      { id: 's-1', title: 'Lost', type: 'show', created_at: '', updated_at: '1', leaf_count: 20, unwatched_count: 7 },
    ]));
    render(Page);
    await waitFor(() => expect(screen.getByLabelText('Watched')).toBeTruthy());
    expect(screen.getByRole('progressbar', { name: 'In progress, 25% watched' })).toBeTruthy();
    const count = screen.getByLabelText('7 unwatched episodes');
    expect(count.textContent).toBe('7');
    // Ran has no watch state at all → no badge.
    expect(document.querySelectorAll('.wb-check, .wb-count, .wb-progress')).toHaveLength(3);
  });

  it('marks watched optimistically', async () => {
    let resolve: () => void = () => {};
    mockMarkWatched.mockReturnValue(new Promise<void>((r) => { resolve = r; }));
    render(Page);
    const trigger = await screen.findByRole('button', { name: 'More actions for Ran' });
    await fireEvent.click(trigger);
    const item = await screen.findByRole('menuitem', { name: 'Mark watched' });
    await fireEvent.click(item);
    expect(mockMarkWatched).toHaveBeenCalledWith('m-3');
    // Badge flips before the server answers: Alien + Ran both watched now.
    await waitFor(() => expect(screen.getAllByLabelText('Watched')).toHaveLength(2));
    resolve();
  });

  it('rolls back when marking fails', async () => {
    mockMarkUnwatched.mockRejectedValue(new Error('nope'));
    render(Page);
    await fireEvent.click(await screen.findByRole('button', { name: 'More actions for Alien' }));
    await fireEvent.click(await screen.findByRole('menuitem', { name: 'Mark unwatched' }));
    expect(mockMarkUnwatched).toHaveBeenCalledWith('m-1');
    await waitFor(() => expect(mockToastError).toHaveBeenCalledWith('nope'));
    await waitFor(() => expect(screen.getAllByLabelText('Watched')).toHaveLength(1));
  });

  it('offers both actions for an in-progress item and closes on Escape', async () => {
    render(Page);
    const trigger = await screen.findByRole('button', { name: 'More actions for Heat' });
    await fireEvent.click(trigger);
    const menu = await screen.findByRole('menu');
    const labels = screen.getAllByRole('menuitem').map((el) => el.textContent?.trim());
    expect(labels).toEqual(['Mark watched', 'Mark unwatched']);
    expect(trigger.getAttribute('aria-expanded')).toBe('true');
    await fireEvent.keyDown(menu, { key: 'Escape' });
    await waitFor(() => expect(screen.queryByRole('menu')).toBeNull());
    expect(trigger.getAttribute('aria-expanded')).toBe('false');
  });
});
