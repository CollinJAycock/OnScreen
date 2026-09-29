// Smoke test of the Leaflet adapter against the real Leaflet build. happy-dom
// has no layout, so this covers wiring (markers, icons, events, teardown),
// not what the map looks like.
import type { PhotoMapPoint } from './api';
import { createPhotoMap, OSM_TILE_URL } from './photoMapLeaflet';
import { clusterPoints } from './photoMap';

const points: PhotoMapPoint[] = [
  { id: 'p-1', library_id: 'l', title: 'Louvre', poster_path: 'F/l.jpg', lat: 48.8606, lon: 2.3376, created_at: '' },
  { id: 'p-2', library_id: 'l', title: 'Eiffel <b>tower</b>', lat: 48.8584, lon: 2.2945, created_at: '' },
  { id: 'p-3', library_id: 'l', title: 'Shibuya', lat: 35.6595, lon: 139.7005, created_at: '' },
];

describe('createPhotoMap', () => {
  it('draws one marker per cluster and reports clicks and Enter', async () => {
    const el = document.createElement('div');
    document.body.appendChild(el);
    const onClusterClick = vi.fn();
    const handle = await createPhotoMap(el, {
      thumbUrl: (p) => `/artwork/${p}?w=96`,
      onViewChange: vi.fn(),
      onClusterClick,
    });

    const clusters = clusterPoints(points, 2);
    handle.showClusters(clusters);

    const icons = [...el.querySelectorAll<HTMLElement>('.leaflet-marker-icon')];
    expect(icons).toHaveLength(2);
    const group = icons.find((i) => i.title === '2 photos')!;
    expect(group.getAttribute('role')).toBe('button');
    expect(group.tabIndex).toBe(0);
    expect(group.querySelector('.pm-cluster .pm-count')?.textContent).toBe('2');
    expect(group.querySelector('img')?.getAttribute('src')).toBe('/artwork/F/l.jpg?w=96');
    const single = icons.find((i) => i.title === 'Shibuya')!;
    expect(single.querySelector('.pm-blank')).toBeTruthy();

    group.dispatchEvent(new MouseEvent('click', { bubbles: true }));
    expect(onClusterClick).toHaveBeenCalledWith(clusters.find((c) => c.points.length === 2));
    single.dispatchEvent(new KeyboardEvent('keypress', { key: 'Enter', keyCode: 13, bubbles: true }));
    expect(onClusterClick).toHaveBeenLastCalledWith(clusters.find((c) => c.points.length === 1));

    // Redrawing replaces the markers.
    handle.showClusters(clusterPoints(points.slice(2), 2));
    expect(el.querySelectorAll('.leaflet-marker-icon')).toHaveLength(1);

    // Titles never become markup.
    handle.showClusters(clusterPoints([points[1]], 18));
    const eiffel = el.querySelector<HTMLElement>('.leaflet-marker-icon')!;
    expect(eiffel.title).toBe('Eiffel <b>tower</b>');
    expect(eiffel.querySelector('b')).toBeNull();

    handle.setView({ lat: 10, lon: 20, zoom: 5 });
    expect(handle.view()).toEqual({ lat: 10, lon: 20, zoom: 5 });
    expect(handle.zoom()).toBe(5);

    handle.destroy();
    expect(el.querySelector('.leaflet-marker-icon')).toBeNull();
    el.remove();
  });

  it('uses the OpenStreetMap tile server over https', () => {
    expect(OSM_TILE_URL).toBe('https://tile.openstreetmap.org/{z}/{x}/{y}.png');
  });
});
