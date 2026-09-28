import { render, screen, waitFor, fireEvent, within } from '@testing-library/svelte';
import { tick } from 'svelte';
import type { MediaItem } from '$lib/api';

const mockLibraryGet = vi.hoisted(() => vi.fn());
const mockListItems = vi.hoisted(() => vi.fn());
const mockGenres = vi.hoisted(() => vi.fn());
// The page store is writable here so a test can navigate under the mounted
// page (Back/Forward), and goto updates it the way SvelteKit would.
const nav = vi.hoisted(() => ({ set: (_href: string) => {} }));
const mockGoto = vi.hoisted(() => vi.fn());

vi.mock('$app/navigation', () => ({ goto: mockGoto, replaceState: vi.fn() }));
vi.mock('$app/stores', async () => {
  const { writable } = await import('svelte/store');
  const page = writable({ url: new URL('http://localhost/libraries/lib-1'), params: { id: 'lib-1' }, state: {} });
  nav.set = (href) => {
    const url = new URL(href);
    page.set({ url, params: { id: url.pathname.split('/')[2] }, state: {} });
  };
  return { page: { subscribe: page.subscribe } };
});
vi.mock('$lib/api', () => ({
  libraryApi: { get: mockLibraryGet, scan: vi.fn() },
  mediaApi: {
    listItems: mockListItems,
    genres: mockGenres,
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

const musicLib = { id: 'lib-1', name: 'Music', type: 'music', scan_paths: ['/music'] };

const artists: MediaItem[] = [
  { id: 'ar-1', title: 'ABBA', type: 'artist', created_at: '', updated_at: '1' },
  { id: 'ar-2', title: 'The Beatles', type: 'artist', created_at: '', updated_at: '1' },
];
const albums: MediaItem[] = [
  { id: 'al-1', title: 'Arrival', type: 'album', year: 1976, parent_id: 'ar-1', parent_title: 'ABBA', created_at: '', updated_at: '1' },
  { id: 'al-2', title: 'Revolver', type: 'album', year: 1966, parent_id: 'ar-2', parent_title: 'The Beatles', created_at: '', updated_at: '1', poster_path: 'b/revolver.jpg' },
];

// Artists for the default listing, albums when the page asks for them; the
// music-video shelf is empty.
function listByType(_id: string, _limit: number, _offset: number, params: { type?: string } = {}) {
  const out = params.type === 'album' ? albums : params.type === 'music_video' ? [] : artists;
  return Promise.resolve({ items: out, total: out.length });
}

// The params of the most recent grid listing (not the music-video shelf's).
const lastList = () =>
  mockListItems.mock.calls.filter((c) => c[3]?.type !== 'music_video').at(-1)?.[3];
const gridCalls = () => mockListItems.mock.calls.filter((c) => c[3]?.type !== 'music_video').length;
// The URL of the most recent goto, and whether it replaced the entry.
const lastGoto = () => {
  const [url, opts] = mockGoto.mock.calls.at(-1) ?? [];
  return { url: new URL(String(url), 'http://localhost'), replace: !!opts?.replaceState };
};

function open(qs = '') {
  nav.set(`http://localhost/libraries/lib-1${qs}`);
  return render(Page);
}

beforeEach(() => {
  vi.clearAllMocks();
  localStorage.clear();
  localStorage.setItem('onscreen_user', JSON.stringify({ id: '1', username: 'u' }));
  mockLibraryGet.mockResolvedValue(musicLib);
  mockListItems.mockImplementation(listByType);
  mockGenres.mockImplementation((_id: string, type?: string) =>
    Promise.resolve(type === 'album' ? [{ name: 'Pop', count: 1 }, { name: 'Rock', count: 1 }] : []));
  mockGoto.mockImplementation((url: string | URL) => {
    nav.set(new URL(String(url), 'http://localhost').href);
    return Promise.resolve();
  });
});

describe('Music library — Artists / Albums', () => {
  it('opens on the artist grid, unchanged', async () => {
    open();
    expect(await screen.findByText('The Beatles', { selector: '.item-title' })).toBeTruthy();
    expect(screen.getByRole('tab', { name: 'Artists' }).getAttribute('aria-selected')).toBe('true');
    expect(lastList().type).toBeUndefined();
    expect(mockGenres).toHaveBeenCalledWith('lib-1', undefined);
    // Artists carry no genres: no genre filter on this view.
    expect(screen.queryByLabelText('Filter by genre')).toBeNull();
    expect(document.querySelector('.circle-poster')).toBeTruthy();
  });

  it('lists every album with its artist and year, and records the view', async () => {
    open();
    await screen.findByText('The Beatles', { selector: '.item-title' });

    await fireEvent.click(screen.getByRole('tab', { name: 'Albums' }));

    expect(await screen.findByText('Revolver', { selector: '.item-title' })).toBeTruthy();
    expect(lastList()).toMatchObject({ type: 'album', sort: 'title', sort_dir: 'asc' });
    expect(screen.getByRole('tab', { name: 'Albums' }).getAttribute('aria-selected')).toBe('true');
    // Title → album page; artist → artist page, as a separate link.
    const cell = screen.getByText('Revolver', { selector: '.item-title' }).closest('.item-cell') as HTMLElement;
    expect(within(cell).getByRole('link', { name: /Revolver/ }).getAttribute('href')).toBe('/albums/al-2');
    expect(within(cell).getByRole('link', { name: 'The Beatles' }).getAttribute('href')).toBe('/artists/ar-2');
    expect(within(cell).getByText('1966')).toBeTruthy();
    expect(screen.getByText('2 albums')).toBeTruthy();
    // Album genres feed the genre filter.
    expect(mockGenres).toHaveBeenLastCalledWith('lib-1', 'album');
    expect(await screen.findByLabelText('Filter by genre')).toBeTruthy();
    // Switching views is a history entry of its own.
    const g = lastGoto();
    expect(g.url.searchParams.get('view')).toBe('albums');
    expect(g.replace).toBe(false);
  });

  it('sorts albums by artist and keeps the sort in the URL', async () => {
    open('?view=albums');
    await screen.findByText('Revolver', { selector: '.item-title' });
    const pills = [...document.querySelectorAll('.sort-pill')].map((b) => b.textContent?.trim());
    expect(pills).toEqual(['Title ↑', 'Artist', 'Year', 'Added']);

    await fireEvent.click(screen.getByRole('button', { name: 'Artist' }));
    await waitFor(() => expect(lastList()).toMatchObject({ type: 'album', sort: 'artist', sort_dir: 'asc' }));
    let g = lastGoto();
    expect(g.url.search).toBe('?view=albums&sort=artist&sort_dir=asc');
    expect(g.replace).toBe(true);

    await fireEvent.click(screen.getByRole('button', { name: /Artist/ }));
    await waitFor(() => expect(lastList()).toMatchObject({ sort: 'artist', sort_dir: 'desc' }));
    g = lastGoto();
    expect(g.url.searchParams.get('sort_dir')).toBe('desc');

    // Year's first click is newest first.
    await fireEvent.click(screen.getByRole('button', { name: 'Year' }));
    await waitFor(() => expect(lastList()).toMatchObject({ sort: 'year', sort_dir: 'desc' }));
  });

  it('restores view, sort and filters from a deep link', async () => {
    open('?view=albums&sort=artist&sort_dir=desc&genre=Rock&year_min=1960&year_max=1969');
    await screen.findByText('Revolver', { selector: '.item-title' });
    expect(lastList()).toMatchObject({
      type: 'album', sort: 'artist', sort_dir: 'desc', genre: 'Rock', year_min: 1960, year_max: 1969,
    });
    expect(screen.getByRole('button', { name: 'Artist ↓' })).toBeTruthy();
    expect(((await screen.findByLabelText('Filter by genre')) as HTMLSelectElement).value).toBe('Rock');
    expect(screen.getByText('1960–1969')).toBeTruthy();
  });

  it('treats a genre link without a view as albums', async () => {
    open('?genre=Pop');
    await screen.findByText('Arrival', { selector: '.item-title' });
    expect(lastList()).toMatchObject({ type: 'album', genre: 'Pop' });
  });

  it('writes a genre pick to the URL', async () => {
    open('?view=albums');
    const select = await screen.findByLabelText('Filter by genre');
    await fireEvent.change(select, { target: { value: 'Pop' } });
    await waitFor(() => expect(lastList()).toMatchObject({ type: 'album', genre: 'Pop' }));
    expect(lastGoto().url.search).toBe('?view=albums&genre=Pop');
  });

  it('clears a year filter from its chip', async () => {
    open('?view=albums&year_min=1990&year_max=1999');
    await screen.findByText('Revolver', { selector: '.item-title' });
    await fireEvent.click(screen.getByRole('button', { name: 'Clear year filter' }));
    await waitFor(() => expect(lastList().year_min).toBeUndefined());
    expect(lastList().year_max).toBeUndefined();
    expect(lastGoto().url.search).toBe('?view=albums');
    expect(screen.queryByText('1990–1999')).toBeNull();
  });

  it('says when no album matches the filters', async () => {
    mockListItems.mockResolvedValue({ items: [], total: 0 });
    open('?view=albums&genre=Polka');
    expect(await screen.findByText('No albums match these filters')).toBeTruthy();
    expect(screen.queryByText('Library is empty')).toBeNull();
    mockListItems.mockImplementation(listByType);
    await fireEvent.click(screen.getByRole('button', { name: 'Show all albums' }));
    await waitFor(() => expect(lastList().genre).toBeUndefined());
    expect(await screen.findByText('Revolver', { selector: '.item-title' })).toBeTruthy();
  });

  it('drops album-only sort and filters on the way back to artists', async () => {
    open('?view=albums&sort=artist&genre=Rock');
    await screen.findByText('Revolver', { selector: '.item-title' });

    await fireEvent.click(screen.getByRole('tab', { name: 'Artists' }));

    expect(await screen.findByText('The Beatles', { selector: '.item-title' })).toBeTruthy();
    expect(lastList()).toMatchObject({ sort: 'title', sort_dir: 'asc' });
    expect(lastList().type).toBeUndefined();
    expect(lastList().genre).toBeUndefined();
    expect(lastGoto().url.search).toBe('?view=artists');
  });

  it('keeps a sort both views offer', async () => {
    open('?view=albums&sort=created_at&sort_dir=desc');
    await screen.findByText('Revolver', { selector: '.item-title' });
    await fireEvent.click(screen.getByRole('tab', { name: 'Artists' }));
    await waitFor(() => expect(lastList().type).toBeUndefined());
    expect(lastList()).toMatchObject({ sort: 'created_at', sort_dir: 'desc' });
  });

  it('follows Back and Forward between the two views', async () => {
    open();
    await screen.findByText('The Beatles', { selector: '.item-title' });
    await fireEvent.click(screen.getByRole('tab', { name: 'Albums' }));
    await screen.findByText('Revolver', { selector: '.item-title' });
    const calls = gridCalls();

    // Back: the router puts the artists URL back under the mounted page.
    nav.set('http://localhost/libraries/lib-1');
    await tick();
    expect(await screen.findByText('The Beatles', { selector: '.item-title' })).toBeTruthy();
    expect(screen.getByRole('tab', { name: 'Artists' }).getAttribute('aria-selected')).toBe('true');
    expect(gridCalls()).toBe(calls + 1);
    expect(lastList().type).toBeUndefined();

    // Forward.
    nav.set('http://localhost/libraries/lib-1?view=albums&sort=artist&sort_dir=desc');
    await tick();
    expect(await screen.findByText('Revolver', { selector: '.item-title' })).toBeTruthy();
    expect(lastList()).toMatchObject({ type: 'album', sort: 'artist', sort_dir: 'desc' });
  });

  it('leaves a move to another library to the library reload', async () => {
    open('?view=albums&sort=artist');
    await screen.findByText('Revolver', { selector: '.item-title' });

    nav.set('http://localhost/libraries/lib-2?genre=Jazz');
    await waitFor(() => expect(mockListItems.mock.calls.some((c) => c[0] === 'lib-2')).toBe(true));
    await new Promise((r) => setTimeout(r, 0));
    // lib-1 is never listed with lib-2's filters; lib-2 opens from its own URL.
    expect(mockListItems.mock.calls.filter((c) => c[0] === 'lib-1' && c[3]?.genre)).toEqual([]);
    expect(mockListItems.mock.calls.filter((c) => c[0] === 'lib-2' && c[3]?.type !== 'music_video').map((c) => c[3]))
      .toEqual([expect.objectContaining({ type: 'album', genre: 'Jazz', sort: 'title', sort_dir: 'asc' })]);
  });

  it('does not reload for its own URL writes', async () => {
    open('?view=albums');
    await screen.findByText('Revolver', { selector: '.item-title' });
    const before = gridCalls();
    await fireEvent.click(screen.getByRole('button', { name: 'Year' }));
    await waitFor(() => expect(gridCalls()).toBe(before + 1));
    await tick();
    await new Promise((r) => setTimeout(r, 0));
    expect(gridCalls()).toBe(before + 1);
  });

  it('ignores a listing that a view switch superseded', async () => {
    let releaseArtists: (v: { items: MediaItem[]; total: number }) => void = () => {};
    mockListItems.mockImplementationOnce(
      () => new Promise((r) => { releaseArtists = r; }),
    );
    open();
    await screen.findByRole('tab', { name: 'Albums' });
    await fireEvent.click(screen.getByRole('tab', { name: 'Albums' }));
    expect(await screen.findByText('Revolver', { selector: '.item-title' })).toBeTruthy();

    // The first (artists) request answers late: it must not replace albums.
    releaseArtists({ items: artists, total: artists.length });
    await tick();
    await new Promise((r) => setTimeout(r, 0));
    expect(screen.queryByText('The Beatles', { selector: '.item-title' })).toBeNull();
    expect(screen.getByText('Revolver', { selector: '.item-title' })).toBeTruthy();
  });

  it('matches the text filter on the artist too', async () => {
    open('?view=albums');
    await screen.findByText('Revolver', { selector: '.item-title' });
    await fireEvent.input(screen.getByPlaceholderText('Filter…'), { target: { value: 'beatles' } });
    expect(screen.getByText('Revolver', { selector: '.item-title' })).toBeTruthy();
    expect(screen.queryByText('Arrival', { selector: '.item-title' })).toBeNull();
  });

  it('shows no view switch outside music libraries', async () => {
    mockLibraryGet.mockResolvedValue({ ...musicLib, type: 'movie' });
    mockListItems.mockResolvedValue({ items: [{ id: 'm-1', title: 'Alien', type: 'movie', created_at: '', updated_at: '1' }], total: 1 });
    open('?view=albums');
    await screen.findByText('Alien', { selector: '.item-title' });
    expect(screen.queryByRole('tab', { name: 'Albums' })).toBeNull();
    expect(lastList().type).toBeUndefined();
    await fireEvent.click(screen.getByRole('button', { name: 'Year' }));
    await waitFor(() => expect(lastList()).toMatchObject({ sort: 'year' }));
    // Other libraries don't navigate on a sort change.
    expect(mockGoto).not.toHaveBeenCalled();
  });
});
