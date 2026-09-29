import { fireEvent, render, screen, waitFor } from '@testing-library/svelte';
import { readable } from 'svelte/store';

const mockLibraryGet = vi.hoisted(() => vi.fn());
const mockListItems = vi.hoisted(() => vi.fn());
const mockAlbumList = vi.hoisted(() => vi.fn());
const mockAddItem = vi.hoisted(() => vi.fn());
const mockToastSuccess = vi.hoisted(() => vi.fn());

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
  photoAlbumApi: { list: mockAlbumList, addItem: mockAddItem, create: vi.fn() },
  assetUrl: (p: string) => p,
}));
vi.mock('$lib/stores/toast', () => ({ toast: { success: mockToastSuccess, error: vi.fn() } }));

import Page from './+page.svelte';

const photoLib = { id: 'lib-1', name: 'Family', type: 'photo', scan_paths: ['/p'] };
const photos = ['Beach', 'Hike', 'Party'].map((t, i) => ({
  id: `p-${i + 1}`, title: t, type: 'photo', poster_path: `P/${i}.jpg`, created_at: '', updated_at: '1',
}));

beforeEach(() => {
  vi.clearAllMocks();
  localStorage.clear();
  localStorage.setItem('onscreen_user', JSON.stringify({ id: '1', username: 'u' }));
  mockLibraryGet.mockResolvedValue(photoLib);
  mockListItems.mockResolvedValue({ items: photos, total: photos.length });
  mockAlbumList.mockResolvedValue([{ id: 'a-1', name: 'Summer', item_count: 2, created_at: '', updated_at: '' }]);
  mockAddItem.mockResolvedValue(undefined);
});

describe('Photo library — map, albums and Select', () => {
  it('links to the map for this library and to the albums', async () => {
    render(Page);
    const map = await screen.findByRole('link', { name: 'Map' });
    expect(map.getAttribute('href')).toBe('/photos/map?library=lib-1');
    expect(screen.getByRole('link', { name: 'Albums' }).getAttribute('href')).toBe('/photos/albums');
  });

  it('selects photos and adds them to an album', async () => {
    render(Page);
    await screen.findByRole('link', { name: /Beach/ });

    await fireEvent.click(screen.getByRole('button', { name: 'Select' }));
    const add = screen.getByRole('button', { name: 'Add to album…' }) as HTMLButtonElement;
    expect(add.disabled).toBe(true);

    // A click toggles the photo instead of opening it.
    const beach = screen.getByRole('link', { name: 'Select Beach' });
    const clickBeach = new MouseEvent('click', { bubbles: true, cancelable: true });
    beach.dispatchEvent(clickBeach);
    expect(clickBeach.defaultPrevented).toBe(true);
    await fireEvent.click(screen.getByRole('link', { name: 'Select Party' }));
    await waitFor(() => expect(screen.getByText('2 selected')).toBeTruthy());

    await fireEvent.click(add);
    await fireEvent.click(await screen.findByRole('button', { name: /Summer/ }));

    await waitFor(() => expect(mockToastSuccess).toHaveBeenCalledWith('Added 2 photos to "Summer"'));
    expect(mockAddItem.mock.calls).toEqual([['a-1', 'p-1'], ['a-1', 'p-3']]);
    // Done: Select mode ends and the tiles open photos again.
    await waitFor(() => expect(screen.queryByRole('toolbar')).toBeNull());
    expect(screen.getByRole('link', { name: /Beach/ }).getAttribute('aria-label')).toBeNull();
  });

  it('leaves Select mode with Done, clearing the selection', async () => {
    render(Page);
    await screen.findByRole('link', { name: /Beach/ });
    await fireEvent.click(screen.getByRole('button', { name: 'Select' }));
    await fireEvent.click(screen.getByRole('link', { name: 'Select Hike' }));
    await fireEvent.click(screen.getByRole('button', { name: 'Done', pressed: true }));
    expect(screen.queryByRole('toolbar')).toBeNull();
    await fireEvent.click(screen.getByRole('button', { name: 'Select' }));
    expect(screen.getByText('0 selected')).toBeTruthy();
  });

  it('offers no Select, Map or Albums outside photo libraries', async () => {
    mockLibraryGet.mockResolvedValue({ ...photoLib, type: 'movie' });
    render(Page);
    await screen.findByRole('link', { name: /Beach/ });
    expect(screen.queryByRole('button', { name: 'Select' })).toBeNull();
    expect(screen.queryByRole('link', { name: 'Map' })).toBeNull();
    expect(screen.queryByRole('link', { name: 'Albums' })).toBeNull();
  });
});
