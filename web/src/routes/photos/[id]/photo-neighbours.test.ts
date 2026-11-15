// Previous / Next in a big library: a bounded window around the photo's
// position in the grid, never the first N photos of the library, and never
// a refetch per photo.
import { fireEvent, render, screen, waitFor } from '@testing-library/svelte';
import { tick } from 'svelte';
import { readable, type Writable } from 'svelte/store';

const mockGoto = vi.hoisted(() => vi.fn());
const mockItemGet = vi.hoisted(() => vi.fn());
const mockListItems = vi.hoisted(() => vi.fn());
const mockPersonItems = vi.hoisted(() => vi.fn());

vi.mock('$app/navigation', () => ({ goto: mockGoto }));
vi.mock('$lib/stores/page.svelte', async () => {
  const { writable } = await import('svelte/store');
  return { page: writable({ url: new URL('http://localhost/photos/p1'), params: { id: 'p1' }, state: {} }) };
});
vi.mock('$lib/api', () => ({
  itemApi: { get: mockItemGet, exif: vi.fn().mockRejectedValue(new Error('no exif')) },
  mediaApi: { listItems: mockListItems },
  photoAlbumApi: { items: vi.fn(), list: vi.fn().mockResolvedValue([]) },
  photoPeopleApi: {
    itemFaces: vi.fn().mockResolvedValue({ faces: [], can_edit: false }),
    people: vi.fn().mockResolvedValue([]),
    personItems: mockPersonItems,
  },
  assetUrl: (p: string) => p,
}));
vi.mock('$lib/stores/capabilities', () => ({
  capabilities: { subscribe: (fn: (v: unknown) => void) => readable(null).subscribe(fn) },
}));
vi.mock('$lib/stores/toast', () => ({ toast: { success: vi.fn(), error: vi.fn() } }));

import { page } from '$lib/stores/page.svelte';
import Page from './+page.svelte';

const pageStore = page as unknown as Writable<{ url: URL; params: { id: string }; state: object }>;
function open(href: string) {
  const url = new URL(href, 'http://localhost');
  pageStore.set({ url, params: { id: url.pathname.split('/').pop()! }, state: {} });
}

const TOTAL = 5000;
// The library listing: photo pN sits at offset N.
function listing(offset: number, limit: number) {
  const n = Math.max(0, Math.min(limit, TOTAL - offset));
  return Promise.resolve({ items: Array.from({ length: n }, (_, i) => ({ id: `p${offset + i}` })), total: TOTAL });
}

async function settle() {
  for (let i = 0; i < 5; i++) {
    await new Promise((r) => setTimeout(r, 0));
    await tick();
  }
}

beforeEach(() => {
  vi.clearAllMocks();
  localStorage.setItem('onscreen_user', JSON.stringify({ user_id: '1', username: 'u' }));
  mockItemGet.mockImplementation(async (id: string) => ({
    id, library_id: 'lib-1', title: `Photo ${id}`, type: 'photo', poster_path: `P/${id}.jpg`,
    created_at: '', updated_at: '1', files: [],
  }));
  mockListItems.mockImplementation((_lib: string, limit: number, offset: number) => listing(offset, limit));
});

describe('photo viewer neighbours', () => {
  it('fetches a window around the grid position, with its sort, and walks on from it', async () => {
    open('/photos/p3000?pos=3000&sort=title&sort_dir=asc');
    render(Page);
    expect(await screen.findByText('3001 / 5000')).toBeTruthy();
    expect(mockListItems).toHaveBeenCalledTimes(1);
    expect(mockListItems).toHaveBeenCalledWith('lib-1', 100, 2950, { sort: 'title', sort_dir: 'asc' });

    await fireEvent.click(screen.getByTitle('Next (→)'));
    expect(mockGoto).toHaveBeenLastCalledWith('/photos/p3001?pos=3001&sort=title&sort_dir=asc');
    // The next photo is in the window: nothing is fetched for it.
    open('/photos/p3001?pos=3001&sort=title&sort_dir=asc');
    expect(await screen.findByText('3002 / 5000')).toBeTruthy();
    expect(mockListItems).toHaveBeenCalledTimes(1);
  });

  it('extends the window when the walk nears its edge', async () => {
    open('/photos/p3046?pos=3046&sort=title&sort_dir=asc');
    render(Page);
    // Window 2996..3095; 3046 is in the middle.
    await screen.findByText('3047 / 5000');
    open('/photos/p3092?pos=3092&sort=title&sort_dir=asc');
    await waitFor(() => expect(mockListItems).toHaveBeenCalledTimes(2));
    expect(mockListItems).toHaveBeenLastCalledWith('lib-1', 50, 3096, { sort: 'title', sort_dir: 'asc' });
  });

  it('opened with no position: one bounded page, not refetched for photos outside it', async () => {
    open('/photos/p4000');
    render(Page);
    await screen.findByText('Photo p4000');
    await settle();
    expect(mockListItems).toHaveBeenCalledTimes(1);
    expect(mockListItems).toHaveBeenCalledWith('lib-1', 200, 0, { sort: 'taken_at', sort_dir: 'desc' });
    expect(screen.queryByTitle('Next (→)')).toBeNull();

    open('/photos/p4001');
    await screen.findByText('Photo p4001');
    await settle();
    expect(mockListItems).toHaveBeenCalledTimes(1);
  });

  it("walks a person's photos from the grid position", async () => {
    mockPersonItems.mockImplementation((_p: string, limit: number, offset: number) => listing(offset, limit));
    open('/photos/p700?person=per-1&pos=700');
    render(Page);
    expect(await screen.findByText('701 / 5000')).toBeTruthy();
    expect(mockPersonItems).toHaveBeenCalledWith('per-1', 100, 650);
  });

  it('drops neighbour answers once the viewer is gone', async () => {
    // A short answer: p3000 ends up at the window's last slot, which a live
    // viewer extends at once.
    const short = () => listing(2950, 51);
    let release!: () => void;
    mockListItems.mockImplementationOnce(() => new Promise((resolve) => { release = () => resolve(short()); }));
    open('/photos/p3000?pos=3000&sort=title&sort_dir=asc');
    const { unmount } = render(Page);
    await waitFor(() => expect(mockListItems).toHaveBeenCalledTimes(1));
    unmount();
    release();
    await settle();
    expect(mockListItems).toHaveBeenCalledTimes(1);
  });

  it('extends at once when the photo lands at the edge of its window', async () => {
    mockListItems.mockImplementationOnce(() => listing(2950, 51));
    open('/photos/p3000?pos=3000&sort=title&sort_dir=asc');
    render(Page);
    await waitFor(() => expect(mockListItems).toHaveBeenCalledTimes(2));
    expect(mockListItems).toHaveBeenLastCalledWith('lib-1', 50, 3001, { sort: 'title', sort_dir: 'asc' });
    expect(await screen.findByTitle('Next (→)')).toBeTruthy();
  });
});
