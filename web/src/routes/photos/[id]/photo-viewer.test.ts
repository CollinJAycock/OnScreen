import { fireEvent, render, screen, waitFor } from '@testing-library/svelte';
import { readable } from 'svelte/store';

const mockGoto = vi.hoisted(() => vi.fn());
const mockItemGet = vi.hoisted(() => vi.fn());
const mockExif = vi.hoisted(() => vi.fn());
const mockListItems = vi.hoisted(() => vi.fn());
const mockAlbumItems = vi.hoisted(() => vi.fn());
const mockAlbumList = vi.hoisted(() => vi.fn());
const mockAddItem = vi.hoisted(() => vi.fn());
const mockRemoveItem = vi.hoisted(() => vi.fn());
const mockToastSuccess = vi.hoisted(() => vi.fn());
const mockToastError = vi.hoisted(() => vi.fn());
const mockItemFaces = vi.hoisted(() => vi.fn());
const mockNameFace = vi.hoisted(() => vi.fn());
const mockDeleteFace = vi.hoisted(() => vi.fn());
const mockPeople = vi.hoisted(() => vi.fn());
const mockPersonItems = vi.hoisted(() => vi.fn());
const caps = vi.hoisted(() => ({ value: null as unknown }));
const pageUrl = vi.hoisted(() => ({ href: 'http://localhost/photos/p-2' }));

vi.mock('$app/navigation', () => ({ goto: mockGoto }));
vi.mock('$app/stores', () => ({
  page: {
    subscribe: (fn: (v: unknown) => void) => {
      const url = new URL(pageUrl.href);
      const id = url.pathname.split('/').pop();
      return readable({ url, params: { id }, state: {} }).subscribe(fn);
    },
  },
}));
vi.mock('$lib/api', () => ({
  itemApi: { get: mockItemGet, exif: mockExif },
  mediaApi: { listItems: mockListItems },
  photoAlbumApi: { items: mockAlbumItems, list: mockAlbumList, addItem: mockAddItem, removeItem: mockRemoveItem },
  photoPeopleApi: {
    itemFaces: mockItemFaces, nameFace: mockNameFace, deleteFace: mockDeleteFace,
    people: mockPeople, personItems: mockPersonItems,
  },
  assetUrl: (p: string) => p,
}));
vi.mock('$lib/stores/capabilities', () => ({
  capabilities: { subscribe: (fn: (v: unknown) => void) => readable(caps.value).subscribe(fn) },
}));
vi.mock('$lib/stores/toast', () => ({
  toast: { success: mockToastSuccess, error: mockToastError },
}));

import Page from './+page.svelte';

const photo = (id: string) => ({
  id, library_id: 'lib-1', title: `Photo ${id}`, type: 'photo', poster_path: `P/${id}.jpg`,
  created_at: '', updated_at: '1', files: [],
});

beforeEach(() => {
  vi.clearAllMocks();
  localStorage.clear();
  sessionStorage.clear();
  localStorage.setItem('onscreen_user', JSON.stringify({ id: '1', username: 'u' }));
  pageUrl.href = 'http://localhost/photos/p-2';
  mockItemGet.mockImplementation(async (id: string) => photo(id));
  mockExif.mockRejectedValue(new Error('no exif'));
  mockListItems.mockResolvedValue({ items: ['p-1', 'p-2', 'p-3'].map(photo), total: 3 });
  mockAlbumItems.mockResolvedValue({ items: [{ id: 'p-9' }, { id: 'p-2' }, { id: 'p-7' }], total: 3 });
  mockAlbumList.mockResolvedValue([{ id: 'a-1', name: 'Alps', item_count: 3, created_at: '', updated_at: '' }]);
  mockAddItem.mockResolvedValue(undefined);
  mockRemoveItem.mockResolvedValue(undefined);
  mockItemFaces.mockResolvedValue({ faces: [], can_edit: false });
  mockPeople.mockResolvedValue([]);
  caps.value = null;
});

describe('Photo viewer', () => {
  it('walks the library and closes to it by default', async () => {
    render(Page);
    expect(await screen.findByText('2 / 3')).toBeTruthy();
    expect(mockAlbumItems).not.toHaveBeenCalled();

    await fireEvent.click(screen.getByTitle('Next (→)'));
    expect(mockGoto).toHaveBeenLastCalledWith('/photos/p-3');
    await fireEvent.click(screen.getByRole('button', { name: 'Close' }));
    expect(mockGoto).toHaveBeenLastCalledWith('/libraries/lib-1');
    expect(screen.queryByRole('button', { name: 'Remove from album' })).toBeNull();
  });

  it('walks the album and closes to it when opened from an album', async () => {
    pageUrl.href = 'http://localhost/photos/p-2?album=a-1';
    render(Page);
    expect(await screen.findByText('2 / 3')).toBeTruthy();
    expect(mockAlbumItems).toHaveBeenCalledWith('a-1');
    expect(mockListItems).not.toHaveBeenCalled();

    await fireEvent.click(screen.getByTitle('Previous (←)'));
    expect(mockGoto).toHaveBeenLastCalledWith('/photos/p-9?album=a-1');
    await fireEvent.keyDown(window, { key: 'Escape' });
    expect(mockGoto).toHaveBeenLastCalledWith('/photos/albums/a-1');
  });

  it('walks the map spot\'s photos and closes to the map when opened from the map', async () => {
    pageUrl.href = 'http://localhost/photos/p-2?map=lib-1';
    sessionStorage.setItem('onscreen_photo_map_selection', JSON.stringify({ libraryId: 'lib-1', ids: ['p-2', 'p-5'] }));
    render(Page);
    expect(await screen.findByText('1 / 2')).toBeTruthy();
    expect(mockListItems).not.toHaveBeenCalled();
    await fireEvent.click(screen.getByTitle('Next (→)'));
    expect(mockGoto).toHaveBeenLastCalledWith('/photos/p-5?map=lib-1');
    await fireEvent.click(screen.getByRole('button', { name: 'Close' }));
    expect(mockGoto).toHaveBeenLastCalledWith('/photos/map?library=lib-1');
  });

  it('falls back to the library when the map selection doesn\'t hold the photo', async () => {
    pageUrl.href = 'http://localhost/photos/p-2?map=lib-1';
    sessionStorage.setItem('onscreen_photo_map_selection', JSON.stringify({ libraryId: 'lib-1', ids: ['p-8'] }));
    render(Page);
    expect(await screen.findByText('2 / 3')).toBeTruthy();
    expect(mockListItems).toHaveBeenCalled();
  });

  it('adds the photo to an album', async () => {
    render(Page);
    await screen.findByText('2 / 3');
    await fireEvent.click(screen.getByRole('button', { name: 'Add to album' }));
    const dialog = await screen.findByRole('dialog', { name: 'Add photo to album' });
    await fireEvent.click(await screen.findByRole('button', { name: /Alps/ }));
    await waitFor(() => expect(mockAddItem).toHaveBeenCalledWith('a-1', 'p-2'));
    expect(mockToastSuccess).toHaveBeenCalledWith('Added to "Alps"');
    await waitFor(() => expect(dialog.isConnected).toBe(false));
  });

  it('leaves the viewer keys alone while the album dialog is open', async () => {
    render(Page);
    await screen.findByText('2 / 3');
    await fireEvent.click(screen.getByRole('button', { name: 'Add to album' }));
    await screen.findByRole('dialog');
    await fireEvent.keyDown(window, { key: 'ArrowRight' });
    expect(mockGoto).not.toHaveBeenCalled();
    // Escape closes the dialog, not the viewer.
    await fireEvent.keyDown(window, { key: 'Escape' });
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull());
    expect(mockGoto).not.toHaveBeenCalled();
  });

  it('removes the photo from the album and moves on to the next one', async () => {
    pageUrl.href = 'http://localhost/photos/p-2?album=a-1';
    render(Page);
    await screen.findByText('2 / 3');
    await fireEvent.click(screen.getByRole('button', { name: 'Remove from album' }));
    await waitFor(() => expect(mockRemoveItem).toHaveBeenCalledWith('a-1', 'p-2'));
    expect(mockToastSuccess).toHaveBeenCalledWith('Removed from the album');
    expect(mockGoto).toHaveBeenLastCalledWith('/photos/p-7?album=a-1');
  });

  it('goes back to the album after removing its last photo', async () => {
    pageUrl.href = 'http://localhost/photos/p-2?album=a-1';
    mockAlbumItems.mockResolvedValue({ items: [{ id: 'p-2' }], total: 1 });
    render(Page);
    await screen.findByText('1 / 1');
    await fireEvent.click(screen.getByRole('button', { name: 'Remove from album' }));
    await waitFor(() => expect(mockGoto).toHaveBeenLastCalledWith('/photos/albums/a-1'));
  });

  it('reports a failed remove and stays put', async () => {
    pageUrl.href = 'http://localhost/photos/p-2?album=a-1';
    mockRemoveItem.mockRejectedValue(new Error('HTTP 500'));
    render(Page);
    await screen.findByText('2 / 3');
    await fireEvent.click(screen.getByRole('button', { name: 'Remove from album' }));
    await waitFor(() => expect(mockToastError).toHaveBeenCalledWith('HTTP 500'));
    expect(mockGoto).not.toHaveBeenCalled();
  });

  it('shows face boxes with names and opens a named person', async () => {
    mockItemFaces.mockResolvedValue({
      can_edit: false,
      faces: [
        { id: 'f-1', item_id: 'p-2', x: 0.1, y: 0.1, w: 0.2, h: 0.2, score: 9, person_id: 'per-1', person_name: 'Alice' },
        { id: 'f-2', item_id: 'p-2', x: 0.5, y: 0.1, w: 0.2, h: 0.2, score: 8 },
      ],
    });
    render(Page);
    const alice = await screen.findByRole('button', { name: 'Alice' });
    expect(screen.getByRole('button', { name: 'Unnamed face' })).toBeTruthy();
    await fireEvent.click(alice);
    expect(mockGoto).toHaveBeenLastCalledWith('/photos/people/per-1');
  });

  it('names a face', async () => {
    mockItemFaces.mockResolvedValue({
      can_edit: true,
      faces: [{ id: 'f-2', item_id: 'p-2', x: 0.5, y: 0.1, w: 0.2, h: 0.2, score: 8 }],
    });
    mockPeople.mockResolvedValue([{ id: 'per-1', library_id: 'lib-1', name: 'Alice', photo_count: 3, can_edit: true }]);
    mockNameFace.mockResolvedValue({ id: 'f-2', item_id: 'p-2', x: 0.5, y: 0.1, w: 0.2, h: 0.2, score: 8, person_id: 'per-2', person_name: 'Bob' });
    render(Page);
    await fireEvent.click(await screen.findByRole('button', { name: 'Unnamed face' }));
    const input = await screen.findByLabelText('Who is this?');
    // Typing in the name box must not drive the viewer's keys.
    await fireEvent.keyDown(input, { key: 'ArrowRight' });
    expect(mockGoto).not.toHaveBeenCalled();
    await fireEvent.input(input, { target: { value: 'Bob' } });
    await fireEvent.click(screen.getByRole('button', { name: 'Save' }));
    await waitFor(() => expect(mockNameFace).toHaveBeenCalledWith('f-2', 'Bob'));
    expect(await screen.findByRole('button', { name: 'Bob' })).toBeTruthy();
  });

  it("walks a person's photos and closes to the person", async () => {
    pageUrl.href = 'http://localhost/photos/p-2?person=per-1';
    mockPersonItems.mockResolvedValue({ items: [{ id: 'p-2' }, { id: 'p-4' }], total: 2 });
    render(Page);
    expect(await screen.findByText('1 / 2')).toBeTruthy();
    expect(mockPersonItems).toHaveBeenCalledWith('per-1', 500);
    await fireEvent.click(screen.getByTitle('Next (→)'));
    expect(mockGoto).toHaveBeenLastCalledWith('/photos/p-4?person=per-1');
    await fireEvent.click(screen.getByRole('button', { name: 'Close' }));
    expect(mockGoto).toHaveBeenLastCalledWith('/photos/people/per-1');
  });

  it('walks a memory and closes to the memories', async () => {
    pageUrl.href = 'http://localhost/photos/p-2?memory=2026-10-14-0';
    sessionStorage.setItem('onscreen_memory:2026-10-14-0', JSON.stringify(['p-1', 'p-2']));
    render(Page);
    expect(await screen.findByText('2 / 2')).toBeTruthy();
    expect(mockListItems).not.toHaveBeenCalled();
    await fireEvent.click(screen.getByRole('button', { name: 'Close' }));
    expect(mockGoto).toHaveBeenLastCalledWith('/photos/memories?date=2026-10-14');
  });

  it('downloads the RAW of a RAW + JPEG pair as the original', async () => {
    caps.value = { features: { web_downloads: true } };
    mockItemGet.mockImplementation(async (id: string) => ({
      ...photo(id),
      files: [
        { id: 'jpg-1', container: 'jpg', stream_token: 't1' },
        { id: 'raw-1', container: 'cr3', stream_token: 't2' },
      ],
    }));
    render(Page);
    const link = await screen.findByRole('link', { name: 'Download original' });
    expect(link.getAttribute('href')).toBe('/media/download/raw-1?token=t2');
    expect(link.getAttribute('title')).toBe('Download original (CR3)');
  });
});
