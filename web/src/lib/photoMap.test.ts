import {
  MAP_POINT_LIMIT,
  bboxQueries,
  boundsOf,
  clusterCountLabel,
  clusterPoints,
  inView,
  isSinglePosition,
  loadMapSelection,
  loadMapView,
  mapStatusText,
  mergeById,
  newestFirst,
  padBounds,
  pickByIds,
  project,
  saveMapSelection,
  saveMapView,
  takenRangeLabel,
  wrapLon,
} from './photoMap';

const pt = (id: string, lat: number, lon: number) => ({ id, lat, lon });

describe('project', () => {
  it('maps the world to 256·2^z pixels with (0,0) at the centre', () => {
    expect(project(0, 0, 0)).toEqual({ x: 128, y: 128 });
    const z1 = project(0, 0, 1);
    expect(z1.x).toBeCloseTo(256);
    expect(z1.y).toBeCloseTo(256);
    expect(project(0, -180, 0).x).toBeCloseTo(0);
    expect(project(0, 180, 0).x).toBeCloseTo(256);
  });

  it('puts north up and clamps the poles to the Mercator limit', () => {
    expect(project(60, 0, 2).y).toBeLessThan(project(0, 0, 2).y);
    expect(project(90, 0, 0).y).toBeCloseTo(project(85.05112878, 0, 0).y);
    expect(project(85.05112878, 0, 0).y).toBeCloseTo(0, 3);
  });
});

describe('clusterPoints', () => {
  const paris = [pt('a', 48.8566, 2.3522), pt('b', 48.8606, 2.3376), pt('c', 48.853, 2.3499)];
  const tokyo = pt('t', 35.6762, 139.6503);

  it('merges nearby points at a low zoom and keeps distant ones apart', () => {
    const clusters = clusterPoints([...paris, tokyo], 4);
    expect(clusters).toHaveLength(2);
    const p = clusters.find((c) => c.points.length === 3)!;
    expect(p.points.map((x) => x.id)).toEqual(['a', 'b', 'c']);
    // Positioned at the members' mean, not a cell corner.
    expect(p.lat).toBeCloseTo((48.8566 + 48.8606 + 48.853) / 3);
    expect(p.lon).toBeCloseTo((2.3522 + 2.3376 + 2.3499) / 3);
    expect(clusters.find((c) => c.points.length === 1)!.points[0].id).toBe('t');
  });

  it('splits a group apart as the zoom rises', () => {
    expect(clusterPoints(paris, 4)).toHaveLength(1);
    expect(clusterPoints(paris, 18).length).toBe(3);
  });

  it('keeps photos taken at the same spot together at every zoom', () => {
    const same = [pt('x', 10, 10), pt('y', 10, 10)];
    for (const z of [0, 10, 19]) expect(clusterPoints(same, z)).toHaveLength(1);
  });

  it('gives each cluster a key unique within a zoom level', () => {
    const clusters = clusterPoints([...paris, tokyo], 18);
    expect(new Set(clusters.map((c) => c.key)).size).toBe(clusters.length);
    expect(clusters.every((c) => c.key.startsWith('18:'))).toBe(true);
  });

  it('skips points without a usable position', () => {
    expect(clusterPoints([pt('n', NaN, 1), pt('ok', 1, 1)], 3).map((c) => c.points[0].id)).toEqual(['ok']);
  });

  it('returns nothing for no points', () => {
    expect(clusterPoints([], 5)).toEqual([]);
  });
});

describe('boundsOf / isSinglePosition', () => {
  it('covers every point', () => {
    expect(boundsOf([pt('a', 10, -20), pt('b', -5, 30), pt('c', 2, 0)])).toEqual({
      south: -5, west: -20, north: 10, east: 30,
    });
  });

  it('is null for no (usable) points', () => {
    expect(boundsOf([])).toBeNull();
    expect(boundsOf([pt('n', NaN, NaN)])).toBeNull();
  });

  it('knows when every point sits at one position', () => {
    expect(isSinglePosition(boundsOf([pt('a', 1, 2), pt('b', 1, 2)])!)).toBe(true);
    expect(isSinglePosition(boundsOf([pt('a', 1, 2), pt('b', 1, 3)])!)).toBe(false);
  });
});

describe('wrapLon', () => {
  it('wraps into [-180, 180)', () => {
    expect(wrapLon(0)).toBe(0);
    expect(wrapLon(190)).toBe(-170);
    expect(wrapLon(-190)).toBe(170);
    expect(wrapLon(180)).toBe(-180);
    expect(wrapLon(540)).toBe(-180);
  });
});

describe('bboxQueries', () => {
  it('sends a plain view as one box', () => {
    expect(bboxQueries({ south: 40, west: -10, north: 50, east: 20 })).toEqual([
      { min_lat: 40, max_lat: 50, min_lon: -10, max_lon: 20 },
    ]);
  });

  it('splits a view across the antimeridian into two boxes', () => {
    expect(bboxQueries({ south: -20, west: 170, north: 10, east: 190 })).toEqual([
      { min_lat: -20, max_lat: 10, min_lon: 170, max_lon: 180 },
      { min_lat: -20, max_lat: 10, min_lon: -180, max_lon: -170 },
    ]);
    // The same view seen from the western copy of the world.
    expect(bboxQueries({ south: -20, west: -190, north: 10, east: -170 })).toEqual([
      { min_lat: -20, max_lat: 10, min_lon: 170, max_lon: 180 },
      { min_lat: -20, max_lat: 10, min_lon: -180, max_lon: -170 },
    ]);
  });

  it('accepts a box written west > east as crossing the antimeridian', () => {
    expect(bboxQueries({ south: 0, west: 170, north: 1, east: -170 })).toHaveLength(2);
  });

  it('shifts a view on another copy of the world back into range', () => {
    expect(bboxQueries({ south: 0, west: 370, north: 1, east: 380 })).toEqual([
      { min_lat: 0, max_lat: 1, min_lon: 10, max_lon: 20 },
    ]);
  });

  it('drops the longitude edges for a view as wide as the world', () => {
    expect(bboxQueries({ south: -100, west: -200, north: 100, east: 200 })).toEqual([
      { min_lat: -90, max_lat: 90 },
    ]);
  });

  it('never sends an out-of-range edge (the server 400s those)', () => {
    const views = [
      { south: -95, west: -179.9, north: 95, east: 179.9 },
      { south: 10, west: 100, north: 20, east: 300 },
      { south: 10, west: -500, north: 20, east: -400 },
    ];
    for (const v of views) {
      for (const b of bboxQueries(v)) {
        expect(b.min_lat).toBeGreaterThanOrEqual(-90);
        expect(b.max_lat).toBeLessThanOrEqual(90);
        if (b.min_lon != null) {
          expect(b.min_lon).toBeGreaterThanOrEqual(-180);
          expect(b.max_lon!).toBeLessThanOrEqual(180);
          expect(b.min_lon).toBeLessThanOrEqual(b.max_lon!);
        }
      }
    }
  });
});

describe('inView / padBounds', () => {
  const view = { south: 40, west: -10, north: 50, east: 20 };

  it('matches points inside the view only', () => {
    expect(inView(pt('in', 45, 0), view)).toBe(true);
    expect(inView(pt('north', 55, 0), view)).toBe(false);
    expect(inView(pt('east', 45, 30), view)).toBe(false);
  });

  it('matches across the antimeridian and on other copies of the world', () => {
    const dateline = { south: -30, west: 170, north: 0, east: 190 };
    expect(inView(pt('fiji', -17.7, 178), dateline)).toBe(true);
    expect(inView(pt('samoa', -13.8, -172), dateline)).toBe(true);
    expect(inView(pt('perth', -31.9, 115.9), dateline)).toBe(false);
    expect(inView(pt('p', 45, 0), { south: 40, west: 350, north: 50, east: 380 })).toBe(true);
  });

  it('matches every longitude for a world-wide view', () => {
    expect(inView(pt('p', 0, 179), { south: -90, west: -200, north: 90, east: 200 })).toBe(true);
  });

  it('pads a view on every side and clamps the latitude', () => {
    expect(padBounds(view, 0.5)).toEqual({ south: 35, west: -25, north: 55, east: 35 });
    const tall = padBounds({ south: -80, west: 0, north: 80, east: 10 }, 1);
    expect(tall.south).toBe(-90);
    expect(tall.north).toBe(90);
  });
});

describe('mergeById / pickByIds / newestFirst', () => {
  it('merges lists keeping the first copy of each id', () => {
    expect(mergeById([pt('a', 1, 1), pt('b', 2, 2)], [pt('b', 9, 9), pt('c', 3, 3)]).map((p) => [p.id, p.lat]))
      .toEqual([['a', 1], ['b', 2], ['c', 3]]);
  });

  it('picks points in the ids order and skips unknown ids', () => {
    const pts = [pt('a', 1, 1), pt('b', 2, 2), pt('c', 3, 3)];
    expect(pickByIds(pts, ['c', 'x', 'a']).map((p) => p.id)).toEqual(['c', 'a']);
  });

  it('sorts newest taken first, falling back to the added date, undated last', () => {
    const rows = [
      { id: 'old', taken_at: '2020-01-01T00:00:00Z' },
      { id: 'none' },
      { id: 'new', taken_at: '2024-06-01T00:00:00Z' },
      { id: 'added', created_at: '2022-01-01T00:00:00Z' },
    ];
    expect(newestFirst(rows).map((r) => r.id)).toEqual(['new', 'added', 'old', 'none']);
  });
});

describe('mapStatusText', () => {
  it('counts the photos when everything is on the map', () => {
    expect(mapStatusText(1, 1)).toBe('1 geotagged photo');
    expect(mapStatusText(1234, 1234)).toBe(`${(1234).toLocaleString()} geotagged photos`);
  });

  it('says so when a library has none', () => {
    expect(mapStatusText(0, 0)).toBe('No geotagged photos');
  });

  it('asks to zoom in when the answer was cut off', () => {
    const t = mapStatusText(MAP_POINT_LIMIT, 40000, false);
    expect(t).toContain('Showing');
    expect(t).toContain((40000).toLocaleString());
    expect(t).toMatch(/zoom in/);
  });

  it('says the area is complete when a per-view answer was not cut off', () => {
    expect(mapStatusText(300, 40000, true)).toMatch(/every one in this area/);
  });
});

describe('clusterCountLabel', () => {
  it('abbreviates large counts', () => {
    expect(clusterCountLabel(7)).toBe('7');
    expect(clusterCountLabel(999)).toBe('999');
    expect(clusterCountLabel(1000)).toBe('1k');
    expect(clusterCountLabel(1250)).toBe('1.2k');
    expect(clusterCountLabel(9999)).toBe('9.9k');
    expect(clusterCountLabel(12345)).toBe('12k');
  });
});

describe('takenRangeLabel', () => {
  it('is empty without EXIF dates', () => {
    expect(takenRangeLabel([{}, { taken_at: 'nope' }])).toBe('');
  });

  it('shows one date for one day and a range for several', () => {
    const one = takenRangeLabel([{ taken_at: '2024-03-12T10:00:00Z' }]);
    expect(one).not.toContain('–');
    const range = takenRangeLabel([
      { taken_at: '2024-04-03T10:00:00Z' },
      { taken_at: '2023-03-12T10:00:00Z' },
      {},
    ]);
    expect(range).toContain('–');
    expect(range).toContain('2023');
    expect(range).toContain('2024');
    expect(range.indexOf('2023')).toBeLessThan(range.indexOf('2024'));
  });
});

describe('per-tab map state', () => {
  beforeEach(() => sessionStorage.clear());

  it('remembers the view per library', () => {
    expect(loadMapView('lib-1')).toBeNull();
    saveMapView('lib-1', { lat: 48.8, lon: 2.3, zoom: 12 });
    expect(loadMapView('lib-1')).toEqual({ lat: 48.8, lon: 2.3, zoom: 12 });
    expect(loadMapView('lib-2')).toBeNull();
  });

  it('ignores a corrupt stored view', () => {
    sessionStorage.setItem('onscreen_photo_map_view:lib-1', '{"lat":"x"}');
    expect(loadMapView('lib-1')).toBeNull();
    sessionStorage.setItem('onscreen_photo_map_view:lib-1', 'not json');
    expect(loadMapView('lib-1')).toBeNull();
  });

  it('stores and clears the selection handed to the viewer', () => {
    saveMapSelection({ libraryId: 'lib-1', ids: ['a', 'b'] });
    expect(loadMapSelection()).toEqual({ libraryId: 'lib-1', ids: ['a', 'b'] });
    saveMapSelection(null);
    expect(loadMapSelection()).toBeNull();
    saveMapSelection({ libraryId: 'lib-1', ids: [] });
    expect(loadMapSelection()).toBeNull();
  });

  it('survives storage that throws', () => {
    const blocked = () => { throw new Error('blocked'); };
    vi.stubGlobal('sessionStorage', { getItem: blocked, setItem: blocked, removeItem: blocked });
    try {
      expect(() => saveMapView('lib-1', { lat: 0, lon: 0, zoom: 1 })).not.toThrow();
      expect(loadMapView('lib-1')).toBeNull();
      expect(() => saveMapSelection({ libraryId: 'l', ids: ['a'] })).not.toThrow();
      expect(() => saveMapSelection(null)).not.toThrow();
      expect(loadMapSelection()).toBeNull();
    } finally {
      vi.unstubAllGlobals();
    }
  });
});
