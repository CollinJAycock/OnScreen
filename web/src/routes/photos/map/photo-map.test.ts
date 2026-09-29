import { fireEvent, render, screen, waitFor } from '@testing-library/svelte';
import { readable } from 'svelte/store';
import type { PhotoMapPoint } from '$lib/api';
import type { Cluster } from '$lib/photoMap';

const mockGoto = vi.hoisted(() => vi.fn());
const mockReplaceState = vi.hoisted(() => vi.fn());
const mockLibraries = vi.hoisted(() => vi.fn());
const mockMap = vi.hoisted(() => vi.fn());
const pageUrl = vi.hoisted(() => ({ href: 'http://localhost/photos/map' }));

// The Leaflet adapter is replaced by a recorder: the page's job is what it
// asks the map to draw and how it reacts to marker clicks and moves.
const fake = vi.hoisted(() => {
  const state = {
    opts: null as null | {
      thumbUrl: (p: string) => string;
      onViewChange: () => void;
      onClusterClick: (c: unknown) => void;
    },
    bounds: { south: -60, west: -180, north: 75, east: 180 },
    zoom: 2,
  };
  const handle = {
    showClusters: vi.fn(),
    fitBounds: vi.fn(),
    setView: vi.fn(),
    view: vi.fn(() => ({ lat: 10, lon: 20, zoom: state.zoom })),
    bounds: vi.fn(() => state.bounds),
    zoom: vi.fn(() => state.zoom),
    destroy: vi.fn(),
  };
  return { state, handle };
});

vi.mock('$app/navigation', () => ({ goto: mockGoto, replaceState: mockReplaceState }));
vi.mock('$app/stores', () => ({
  page: {
    subscribe: (fn: (v: unknown) => void) =>
      readable({ url: new URL(pageUrl.href), params: {}, state: {} }).subscribe(fn),
  },
}));
vi.mock('$lib/api', () => ({
  libraryApi: { list: mockLibraries },
  photoApi: { map: mockMap },
  assetUrl: (p: string) => `http://srv${p}`,
}));
vi.mock('$lib/photoMapLeaflet', () => ({
  createPhotoMap: vi.fn(async (_el: HTMLElement, opts: typeof fake.state.opts) => {
    fake.state.opts = opts;
    return fake.handle;
  }),
}));

import Page from './+page.svelte';

const libs = [
  { id: 'lib-1', name: 'Family', type: 'photo', scan_paths: [] },
  { id: 'lib-m', name: 'Movies', type: 'movie', scan_paths: [] },
  { id: 'lib-2', name: 'Travel', type: 'photo', scan_paths: [] },
];

const paris: PhotoMapPoint[] = [
  { id: 'p-1', library_id: 'lib-1', title: 'Louvre', poster_path: 'Family/louvre.jpg', lat: 48.8606, lon: 2.3376, taken_at: '2023-05-01T10:00:00Z', created_at: '' },
  { id: 'p-2', library_id: 'lib-1', title: 'Eiffel', poster_path: 'Family/eiffel.jpg', lat: 48.8584, lon: 2.2945, taken_at: '2024-05-02T10:00:00Z', created_at: '' },
];
const tokyo: PhotoMapPoint = { id: 'p-3', library_id: 'lib-1', title: 'Shibuya', lat: 35.6595, lon: 139.7005, created_at: '' };

function lastClusters(): Cluster<PhotoMapPoint>[] {
  const calls = fake.handle.showClusters.mock.calls;
  return calls[calls.length - 1][0];
}

beforeEach(() => {
  vi.clearAllMocks();
  vi.useRealTimers();
  localStorage.clear();
  sessionStorage.clear();
  localStorage.setItem('onscreen_user', JSON.stringify({ id: '1', username: 'u' }));
  pageUrl.href = 'http://localhost/photos/map';
  fake.state.opts = null;
  fake.state.bounds = { south: -60, west: -180, north: 75, east: 180 };
  fake.state.zoom = 2;
  mockLibraries.mockResolvedValue(libs);
  mockMap.mockResolvedValue({ items: [...paris, tokyo], total: 3 });
});

describe('Photo map page', () => {
  it('loads the first photo library, fits the map to its photos and clusters them', async () => {
    render(Page);

    await waitFor(() => expect(fake.handle.showClusters).toHaveBeenCalled());
    expect(mockMap).toHaveBeenCalledWith('lib-1', { limit: 25000 });
    expect(fake.handle.fitBounds).toHaveBeenCalledWith(
      expect.objectContaining({ south: tokyo.lat, north: paris[0].lat, east: tokyo.lon }),
    );
    // Paris' two photos share a marker at world zoom; Tokyo has its own.
    const sizes = lastClusters().map((c) => c.points.length).sort();
    expect(sizes).toEqual([1, 2]);
    expect(screen.getByRole('status').textContent).toContain('3 geotagged photos');
    // Only photo libraries are offered.
    const select = screen.getByLabelText('Photo library') as HTMLSelectElement;
    expect([...select.options].map((o) => o.textContent)).toEqual(['Family', 'Travel']);
  });

  it('uses ?library= when it names a photo library', async () => {
    pageUrl.href = 'http://localhost/photos/map?library=lib-2';
    render(Page);
    await waitFor(() => expect(mockMap).toHaveBeenCalledWith('lib-2', { limit: 25000 }));
  });

  it('lists the photos at a clicked marker, newest first, linked into the viewer', async () => {
    render(Page);
    await waitFor(() => expect(fake.handle.showClusters).toHaveBeenCalled());
    const cluster = lastClusters().find((c) => c.points.length === 2)!;

    fake.state.opts!.onClusterClick(cluster);

    const panel = await screen.findByRole('complementary', { name: 'Photos at this spot' });
    expect(panel.querySelector('h2')?.textContent).toBe('2 photos');
    const links = [...panel.querySelectorAll('a.thumb')] as HTMLAnchorElement[];
    expect(links.map((a) => a.getAttribute('href'))).toEqual(['/photos/p-2?map=lib-1', '/photos/p-1?map=lib-1']);
    expect(links[0].querySelector('img')?.getAttribute('src')).toBe('http://srv/artwork/Family/eiffel.jpg?w=300');
    // The viewer walks these photos.
    expect(JSON.parse(sessionStorage.getItem('onscreen_photo_map_selection')!)).toEqual({ libraryId: 'lib-1', ids: ['p-2', 'p-1'] });

    await fireEvent.click(screen.getByRole('button', { name: 'Zoom in' }));
    expect(fake.handle.fitBounds).toHaveBeenLastCalledWith(
      expect.objectContaining({ south: 48.8584, north: 48.8606 }),
      18,
    );

    await fireEvent.click(screen.getByRole('button', { name: 'Close' }));
    expect(screen.queryByRole('complementary')).toBeNull();
    expect(sessionStorage.getItem('onscreen_photo_map_selection')).toBeNull();
  });

  it('shows a single photo by its title, without Zoom in', async () => {
    render(Page);
    await waitFor(() => expect(fake.handle.showClusters).toHaveBeenCalled());
    fake.state.opts!.onClusterClick(lastClusters().find((c) => c.points.length === 1)!);
    const panel = await screen.findByRole('complementary');
    expect(panel.querySelector('h2')?.textContent).toBe('Shibuya');
    expect(screen.queryByRole('button', { name: 'Zoom in' })).toBeNull();
    // No poster: a letter tile instead of a broken image.
    expect(panel.querySelector('img')).toBeNull();
  });

  it('only draws markers for photos in view', async () => {
    render(Page);
    await waitFor(() => expect(fake.handle.showClusters).toHaveBeenCalled());
    fake.state.bounds = { south: 48, west: 2, north: 49, east: 3 };
    fake.state.zoom = 17;
    fake.state.opts!.onViewChange();
    const ids = lastClusters().flatMap((c) => c.points.map((p) => p.id)).sort();
    expect(ids).toEqual(['p-1', 'p-2']);
    // The view is remembered for the way back from the viewer.
    expect(JSON.parse(sessionStorage.getItem('onscreen_photo_map_view:lib-1')!)).toEqual({ lat: 10, lon: 20, zoom: 17 });
  });

  it('restores the remembered view and open spot on the way back', async () => {
    sessionStorage.setItem('onscreen_photo_map_view:lib-1', JSON.stringify({ lat: 48.86, lon: 2.3, zoom: 15 }));
    sessionStorage.setItem('onscreen_photo_map_selection', JSON.stringify({ libraryId: 'lib-1', ids: ['p-1'] }));
    render(Page);
    await waitFor(() => expect(fake.handle.setView).toHaveBeenCalledWith({ lat: 48.86, lon: 2.3, zoom: 15 }));
    expect(fake.handle.fitBounds).not.toHaveBeenCalled();
    const panel = await screen.findByRole('complementary');
    expect(panel.querySelector('h2')?.textContent).toBe('Louvre');
  });

  it('reloads per view when the library has more photos than one answer holds', async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    mockMap.mockResolvedValueOnce({ items: paris, total: 40000 });
    render(Page);
    await waitFor(() => expect(fake.handle.showClusters).toHaveBeenCalled());
    expect(screen.getByRole('status').textContent).toMatch(/Showing 2 of 40,000.*zoom in/);

    // A view across the antimeridian is asked for as two boxes.
    mockMap.mockResolvedValueOnce({ items: [tokyo], total: 40000 });
    mockMap.mockResolvedValueOnce({ items: [], total: 40000 });
    fake.state.bounds = { south: 0, west: 130, north: 50, east: 200 };
    fake.state.zoom = 4;
    fake.state.opts!.onViewChange();
    await vi.advanceTimersByTimeAsync(500);

    await waitFor(() => expect(mockMap).toHaveBeenCalledTimes(3));
    const boxes = mockMap.mock.calls.slice(1).map((c) => c[1]);
    expect(boxes[0]).toMatchObject({ max_lon: 180, limit: 25000 });
    expect(boxes[1]).toMatchObject({ min_lon: -180, limit: 25000 });
    await waitFor(() => expect(screen.getByRole('status').textContent).toMatch(/every one in this area/));
    expect(lastClusters().flatMap((c) => c.points.map((p) => p.id))).toEqual(['p-3']);
    vi.useRealTimers();
  });

  it('says so when the library has no geotagged photos', async () => {
    mockMap.mockResolvedValue({ items: [], total: 0 });
    render(Page);
    expect(await screen.findByText('No geotagged photos', { selector: 'strong' })).toBeTruthy();
    expect(fake.handle.setView).toHaveBeenCalledWith({ lat: 20, lon: 0, zoom: 2 });
  });

  it('explains when there is no photo library', async () => {
    mockLibraries.mockResolvedValue([libs[1]]);
    render(Page);
    expect(await screen.findByText('No photo libraries')).toBeTruthy();
    expect(mockMap).not.toHaveBeenCalled();
  });

  it('switches library from the picker', async () => {
    render(Page);
    await waitFor(() => expect(mockMap).toHaveBeenCalledTimes(1));
    const select = screen.getByLabelText('Photo library') as HTMLSelectElement;
    await fireEvent.change(select, { target: { value: 'lib-2' } });
    await waitFor(() => expect(mockMap).toHaveBeenLastCalledWith('lib-2', { limit: 25000 }));
  });

  it('surfaces a load failure', async () => {
    mockMap.mockRejectedValue(new Error('HTTP 403'));
    render(Page);
    expect((await screen.findByRole('alert')).textContent).toContain('HTTP 403');
  });

  it('sends a signed-out visitor to login', async () => {
    localStorage.clear();
    render(Page);
    expect(mockGoto).toHaveBeenCalledWith('/login');
    expect(mockLibraries).not.toHaveBeenCalled();
  });
});
