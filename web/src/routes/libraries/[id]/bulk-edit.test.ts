import { fireEvent, render, screen, waitFor, within } from '@testing-library/svelte';
import { readable } from 'svelte/store';

const mockLibraryGet = vi.hoisted(() => vi.fn());
const mockListItems = vi.hoisted(() => vi.fn());
const mockBulkEdit = vi.hoisted(() => vi.fn());
const mockMarkWatched = vi.hoisted(() => vi.fn());
const mockGenres = vi.hoisted(() => vi.fn());

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
    genres: mockGenres,
    randomItem: vi.fn(),
    eventCollections: vi.fn().mockResolvedValue([]),
    enrichItem: vi.fn(),
  },
  itemApi: { markWatched: mockMarkWatched, markUnwatched: vi.fn(), bulkEdit: mockBulkEdit },
  labelApi: { list: vi.fn().mockResolvedValue([]), apply: vi.fn() },
  playlistApi: { list: vi.fn().mockResolvedValue([]) },
  photoAlbumApi: { list: vi.fn().mockResolvedValue([]), addItem: vi.fn(), create: vi.fn() },
  collectionApi: {
    list: vi.fn().mockResolvedValue([]),
    addItems: vi.fn(),
    createManual: vi.fn(),
    forLibrary: vi.fn().mockResolvedValue([]),
  },
  assetUrl: (p: string) => p,
  normalizeLabel: (s: string) => s.trim().toLowerCase(),
}));
vi.mock('$lib/stores/toast', () => ({ toast: { success: vi.fn(), error: vi.fn() } }));

import Page from './+page.svelte';

const movieLib = { id: 'lib-1', name: 'Movies', type: 'movie', scan_paths: ['/m'] };
const movies = ['Alien', 'Brazil', 'Cube', 'Dune'].map((t, i) => ({
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
  mockGenres.mockResolvedValue([]);
  mockMarkWatched.mockResolvedValue(undefined);
  mockBulkEdit.mockResolvedValue({ matched: 2, updated: 1, labels_added: 0, labels_removed: 0, skipped: [
    { item_id: 'm-3', title: 'Cube', field: 'content_rating', reason: 'locked' },
  ] });
});

afterEach(() => localStorage.clear());

describe('Library grid — bulk selection', () => {
  it('shift-click selects a range, and Select all covers the page', async () => {
    signIn(true);
    render(Page);
    await screen.findByRole('link', { name: /Alien/ });
    await fireEvent.click(screen.getByRole('button', { name: 'Select' }));

    await fireEvent.click(screen.getByRole('link', { name: 'Select Alien' }));
    await fireEvent.click(screen.getByRole('link', { name: 'Select Cube' }), { shiftKey: true });
    await waitFor(() => expect(screen.getByText('3 selected')).toBeTruthy());
    expect(screen.getByRole('link', { name: 'Deselect Brazil' })).toBeTruthy();

    await fireEvent.click(screen.getByRole('button', { name: 'Select all on page' }));
    await waitFor(() => expect(screen.getByText('4 selected')).toBeTruthy());
    await fireEvent.click(screen.getByRole('button', { name: 'Clear page' }));
    await waitFor(() => expect(screen.getByText('0 selected')).toBeTruthy());
  });

  it('the hover checkbox starts Select mode with that tile', async () => {
    signIn(true);
    render(Page);
    await screen.findByRole('link', { name: /Alien/ });
    await fireEvent.click(screen.getByRole('button', { name: 'Select Dune' }));
    await waitFor(() => expect(screen.getByText('1 selected')).toBeTruthy());
    expect(screen.getByRole('link', { name: 'Deselect Dune' })).toBeTruthy();
  });

  it('Edit metadata… sends one bulk edit and shows what was skipped', async () => {
    signIn(true);
    render(Page);
    await screen.findByRole('link', { name: /Alien/ });
    await fireEvent.click(screen.getByRole('button', { name: 'Select' }));
    await fireEvent.click(screen.getByRole('link', { name: 'Select Brazil' }));
    await fireEvent.click(screen.getByRole('link', { name: 'Select Cube' }));
    await fireEvent.click(screen.getByRole('button', { name: 'Edit metadata…' }));

    const dialog = await screen.findByRole('dialog');
    const apply = within(dialog).getByRole('button', { name: 'Apply to 2' }) as HTMLButtonElement;
    expect(apply.disabled).toBe(true); // everything starts at "leave unchanged"

    await fireEvent.change(within(dialog).getByLabelText('Content rating change'), { target: { value: 'set' } });
    await fireEvent.input(await within(dialog).findByLabelText('Content rating'), { target: { value: 'PG' } });
    await fireEvent.change(within(dialog).getByLabelText('Genre change'), { target: { value: 'add' } });
    await fireEvent.input(await within(dialog).findByLabelText('Genres, comma-separated'), { target: { value: 'Sci-Fi' } });
    await fireEvent.click(apply);

    await waitFor(() => expect(mockBulkEdit).toHaveBeenCalledWith({
      item_ids: ['m-2', 'm-3'],
      set: { content_rating: 'PG' },
      add_genres: ['Sci-Fi'],
    }));
    expect(await within(dialog).findByText(/Updated 1 of 2 items/)).toBeTruthy();
    expect(within(dialog).getByText('Cube')).toBeTruthy();
  });

  it('marks the selection watched', async () => {
    signIn(true);
    render(Page);
    await screen.findByRole('link', { name: /Alien/ });
    await fireEvent.click(screen.getByRole('button', { name: 'Select' }));
    await fireEvent.click(screen.getByRole('link', { name: 'Select Alien' }));
    await fireEvent.click(screen.getByRole('link', { name: 'Select Dune' }));
    await fireEvent.click(screen.getByRole('button', { name: 'Mark watched' }));
    await waitFor(() => expect(mockMarkWatched).toHaveBeenCalledTimes(2));
    expect(mockMarkWatched.mock.calls.map((c) => c[0]).sort()).toEqual(['m-1', 'm-4']);
  });

  it('non-admins get no hover checkbox or edit actions', async () => {
    signIn(false);
    render(Page);
    await screen.findByRole('link', { name: /Alien/ });
    expect(screen.queryByRole('button', { name: 'Select Alien' })).toBeNull();
    expect(screen.queryByRole('button', { name: 'Edit metadata…' })).toBeNull();
  });
});
