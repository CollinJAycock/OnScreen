import { fireEvent, render, screen, waitFor } from '@testing-library/svelte';
import { readable } from 'svelte/store';

const mockGoto = vi.hoisted(() => vi.fn());
const mockList = vi.hoisted(() => vi.fn());
const mockItems = vi.hoisted(() => vi.fn());
const mockRename = vi.hoisted(() => vi.fn());
const mockDelete = vi.hoisted(() => vi.fn());
const mockRemoveItem = vi.hoisted(() => vi.fn());
const mockToastSuccess = vi.hoisted(() => vi.fn());
const mockToastError = vi.hoisted(() => vi.fn());

vi.mock('$app/navigation', () => ({ goto: mockGoto }));
vi.mock('$app/stores', () => ({
  page: {
    subscribe: (fn: (v: unknown) => void) =>
      readable({ url: new URL('http://localhost/photos/albums/a-1'), params: { id: 'a-1' }, state: {} }).subscribe(fn),
  },
}));
vi.mock('$lib/api', () => ({
  photoAlbumApi: {
    list: mockList,
    items: mockItems,
    rename: mockRename,
    delete: mockDelete,
    removeItem: mockRemoveItem,
  },
  assetUrl: (p: string) => `http://srv${p}`,
}));
vi.mock('$lib/stores/toast', () => ({
  toast: { success: mockToastSuccess, error: mockToastError },
}));
const mockConfirm = vi.hoisted(() => vi.fn());
vi.mock('$lib/native', async (importOriginal) => ({
  ...(await importOriginal<typeof import('$lib/native')>()),
  confirmAction: mockConfirm,
}));

import Page from './+page.svelte';

const album = { id: 'a-1', name: 'Alps', item_count: 3, created_at: '', updated_at: '' };
const photos = [
  { id: 'p-1', library_id: 'lib-1', title: 'Summit', poster_path: 'P/summit.jpg', added_at: '' },
  { id: 'p-2', library_id: 'lib-1', title: 'Lake', poster_path: 'P/lake.jpg', added_at: '' },
  { id: 'p-3', library_id: 'lib-1', title: 'Hut', added_at: '' },
];

beforeEach(() => {
  vi.clearAllMocks();
  localStorage.clear();
  localStorage.setItem('onscreen_user', JSON.stringify({ id: '1', username: 'u' }));
  mockList.mockResolvedValue([{ ...album }, { id: 'a-2', name: 'Other', item_count: 0, created_at: '', updated_at: '' }]);
  mockItems.mockResolvedValue({ items: photos, total: 3 });
  mockRemoveItem.mockResolvedValue(undefined);
});

describe('Photo album page', () => {
  it('shows the album\'s photos, each opening the viewer in album context', async () => {
    render(Page);
    expect(await screen.findByRole('heading', { name: 'Alps' })).toBeTruthy();
    expect(mockItems).toHaveBeenCalledWith('a-1');
    expect(screen.getByText('3 photos')).toBeTruthy();
    const summit = screen.getByRole('link', { name: 'Summit' });
    expect(summit.getAttribute('href')).toBe('/photos/p-1?album=a-1');
    expect(summit.querySelector('img')?.getAttribute('src')).toBe('http://srv/artwork/P/summit.jpg?w=300');
  });

  it('counts the photos it could load, not a server total that includes hidden ones', async () => {
    mockItems.mockResolvedValue({ items: photos.slice(0, 2), total: 3 });
    render(Page);
    expect(await screen.findByText('2 photos')).toBeTruthy();
    expect(screen.queryByText(/Showing the newest/)).toBeNull();
  });

  it('says so for an album that isn\'t the caller\'s (or doesn\'t exist)', async () => {
    mockItems.mockRejectedValue(Object.assign(new Error('Not found'), { status: 404 }));
    render(Page);
    expect(await screen.findByText('Album not found')).toBeTruthy();
  });

  it('says so for an empty album', async () => {
    mockItems.mockResolvedValue({ items: [], total: 0 });
    render(Page);
    expect(await screen.findByText('This album is empty')).toBeTruthy();
    expect(screen.queryByRole('button', { name: 'Select' })).toBeNull();
  });

  it('removes the selected photos', async () => {
    render(Page);
    await screen.findByRole('heading', { name: 'Alps' });

    await fireEvent.click(screen.getByRole('button', { name: 'Select' }));
    const remove = screen.getByRole('button', { name: 'Remove from album' }) as HTMLButtonElement;
    expect(remove.disabled).toBe(true);

    // In Select mode a tile toggles instead of navigating.
    await fireEvent.click(screen.getByRole('link', { name: 'Select Summit' }));
    await fireEvent.click(screen.getByRole('link', { name: 'Select Hut' }));
    await fireEvent.click(screen.getByRole('link', { name: 'Deselect Hut' }));
    await fireEvent.click(screen.getByRole('link', { name: 'Select Lake' }));
    expect(screen.getByText('2 selected')).toBeTruthy();

    await fireEvent.click(remove);
    await waitFor(() => expect(mockToastSuccess).toHaveBeenCalledWith('Removed 2 photos from "Alps"'));
    expect(mockRemoveItem.mock.calls).toEqual([['a-1', 'p-1'], ['a-1', 'p-2']]);
    expect(screen.queryByRole('link', { name: /Summit/ })).toBeNull();
    expect(screen.getByRole('link', { name: 'Hut' })).toBeTruthy();
    expect(screen.getByText('1 photo')).toBeTruthy();
    // Select mode ends once nothing is left selected.
    expect(screen.queryByRole('toolbar')).toBeNull();
  });

  it('keeps the photos that failed to remove selected', async () => {
    mockRemoveItem.mockResolvedValueOnce(undefined).mockRejectedValueOnce(new Error('HTTP 500'));
    render(Page);
    await screen.findByRole('heading', { name: 'Alps' });
    await fireEvent.click(screen.getByRole('button', { name: 'Select' }));
    await fireEvent.click(screen.getByRole('link', { name: 'Select Summit' }));
    await fireEvent.click(screen.getByRole('link', { name: 'Select Lake' }));
    await fireEvent.click(screen.getByRole('button', { name: 'Remove from album' }));

    await waitFor(() => expect(mockToastError).toHaveBeenCalledWith('Removed 1 of 2 photos from "Alps"; 1 failed: HTTP 500'));
    expect(screen.getByRole('link', { name: 'Deselect Lake' })).toBeTruthy();
    expect(screen.getByText('1 selected')).toBeTruthy();
  });

  it('renames the album', async () => {
    mockRename.mockResolvedValue({ id: 'a-1', name: 'Alps 24', created_at: '', updated_at: '' });
    render(Page);
    await screen.findByRole('heading', { name: 'Alps' });
    await fireEvent.click(screen.getByRole('button', { name: 'Rename' }));
    const input = screen.getByLabelText('Album name');
    await fireEvent.input(input, { target: { value: 'Alps 24' } });
    await fireEvent.submit(input.closest('form')!);
    await waitFor(() => expect(mockRename).toHaveBeenCalledWith('a-1', 'Alps 24'));
    expect(await screen.findByRole('heading', { name: 'Alps 24' })).toBeTruthy();
  });

  it('deletes the album after confirming and goes back to the list', async () => {
    mockDelete.mockResolvedValue(undefined);
    mockConfirm.mockResolvedValue(true);
    render(Page);
    await screen.findByRole('heading', { name: 'Alps' });
    await fireEvent.click(screen.getByRole('button', { name: 'Delete' }));
    await waitFor(() => expect(mockDelete).toHaveBeenCalledWith('a-1'));
    expect(mockGoto).toHaveBeenCalledWith('/photos/albums');
  });
});
