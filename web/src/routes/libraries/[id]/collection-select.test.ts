import { fireEvent, render, screen, waitFor } from '@testing-library/svelte';
import { readable } from 'svelte/store';

const mockLibraryGet = vi.hoisted(() => vi.fn());
const mockListItems = vi.hoisted(() => vi.fn());
const mockCollectionList = vi.hoisted(() => vi.fn());
const mockAddItems = vi.hoisted(() => vi.fn());
const mockCreateManual = vi.hoisted(() => vi.fn());

vi.mock('$app/navigation', () => ({ goto: vi.fn(), replaceState: vi.fn() }));
vi.mock('$app/stores', () => ({
  page: {
    subscribe: (fn: (v: unknown) => void) =>
      readable({ url: new URL('http://localhost/libraries/lib-1'), params: { id: 'lib-1' }, state: {} }).subscribe(fn),
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
  photoAlbumApi: { list: vi.fn().mockResolvedValue([]), addItem: vi.fn(), create: vi.fn() },
  collectionApi: {
    list: mockCollectionList,
    addItems: mockAddItems,
    createManual: mockCreateManual,
    forLibrary: vi.fn().mockResolvedValue([]),
  },
  assetUrl: (p: string) => p,
}));
vi.mock('$lib/stores/toast', () => ({ toast: { success: vi.fn(), error: vi.fn() } }));

import Page from './+page.svelte';

const movieLib = { id: 'lib-1', name: 'Movies', type: 'movie', scan_paths: ['/m'] };
const movies = ['Halloween', 'Scream', 'Coraline'].map((t, i) => ({
  id: `m-${i + 1}`, title: t, type: 'movie', poster_path: `M/${i}.jpg`, created_at: '', updated_at: '1',
}));

function signIn(isAdmin: boolean) {
  localStorage.setItem('onscreen_user', JSON.stringify({ user_id: '1', username: 'u', is_admin: isAdmin }));
}

beforeEach(() => {
  vi.clearAllMocks();
  localStorage.clear();
  mockLibraryGet.mockResolvedValue(movieLib);
  mockListItems.mockResolvedValue({ items: movies, total: movies.length });
  mockCollectionList.mockResolvedValue([
    { id: 'c-1', name: 'Spooky', type: 'manual', item_count: 4, created_at: '' },
    { id: 'p-1', name: 'My playlist', type: 'playlist', created_at: '' },
  ]);
  mockAddItems.mockResolvedValue(undefined);
});

afterEach(() => {
  localStorage.clear();
});

describe('Movie library — Select into a collection', () => {
  it('lets an admin select movies and add them to a collection', async () => {
    signIn(true);
    render(Page);
    await screen.findByRole('link', { name: /Halloween/ });

    await fireEvent.click(screen.getByRole('button', { name: 'Select' }));
    const add = screen.getByRole('button', { name: 'Add to collection…' }) as HTMLButtonElement;
    expect(add.disabled).toBe(true);

    // A click toggles the movie instead of opening it.
    const halloween = screen.getByRole('link', { name: 'Select Halloween' });
    const click = new MouseEvent('click', { bubbles: true, cancelable: true });
    halloween.dispatchEvent(click);
    expect(click.defaultPrevented).toBe(true);
    await fireEvent.click(screen.getByRole('link', { name: 'Select Coraline' }));
    await waitFor(() => expect(screen.getByText('2 selected')).toBeTruthy());

    await fireEvent.click(add);
    // Only shared collections are offered, not playlists.
    const spooky = await screen.findByRole('button', { name: /Spooky/ });
    expect(screen.queryByRole('button', { name: /My playlist/ })).toBeNull();
    await fireEvent.click(spooky);
    await waitFor(() => expect(mockAddItems).toHaveBeenCalledWith('c-1', ['m-1', 'm-3']));
  });

  it('creates a collection from the selection', async () => {
    signIn(true);
    mockCreateManual.mockResolvedValue({ id: 'c-new', name: 'Halloween night', type: 'manual', created_at: '' });
    render(Page);
    await screen.findByRole('link', { name: /Halloween/ });
    await fireEvent.click(screen.getByRole('button', { name: 'Select' }));
    await fireEvent.click(screen.getByRole('link', { name: 'Select Scream' }));
    await fireEvent.click(screen.getByRole('button', { name: 'Add to collection…' }));
    await fireEvent.click(await screen.findByRole('button', { name: '+ New Collection' }));
    await fireEvent.input(screen.getByLabelText('New collection name'), { target: { value: 'Halloween night' } });
    await fireEvent.click(screen.getByRole('button', { name: 'Create & Add' }));
    await waitFor(() => expect(mockCreateManual).toHaveBeenCalledWith('Halloween night'));
    expect(mockAddItems).toHaveBeenCalledWith('c-new', ['m-2']);
  });

  it('gives non-admins no Select mode in a movie library', async () => {
    signIn(false);
    render(Page);
    await screen.findByRole('link', { name: /Halloween/ });
    expect(screen.queryByRole('button', { name: 'Select' })).toBeNull();
  });
});
