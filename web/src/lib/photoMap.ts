// Pure helpers for the web photo map (/photos/map): marker clustering,
// bounds math, the bounding-box queries sent to GET /photos/map, and the
// small bits of per-tab state the map hands to the photo viewer. Nothing
// here touches Leaflet or the DOM, so it's all unit-tested directly.

/** Anything with a position and an id: a PhotoMapPoint, or a test stub. */
export interface GeoPoint {
  id: string;
  lat: number;
  lon: number;
}

/** A group of points drawn as one marker. A single photo is a cluster of 1. */
export interface Cluster<T extends GeoPoint> {
  /** Stable for a given zoom: the grid cell the members fell into. */
  key: string;
  lat: number;
  lon: number;
  points: T[];
}

/** The visible map area, as Leaflet reports it. west/east may run past
 *  ±180 when the view spans a wrap of the world. */
export interface ViewBounds {
  south: number;
  west: number;
  north: number;
  east: number;
}

/** One GET /photos/map bounding box. Longitude edges are absent when the
 *  view covers every longitude. */
export interface MapBBox {
  min_lat: number;
  max_lat: number;
  min_lon?: number;
  max_lon?: number;
}

export interface MapView {
  lat: number;
  lon: number;
  zoom: number;
}

/** The server's hard cap on one /photos/map answer (maxMapPointLimit). */
export const MAP_POINT_LIMIT = 25000;

/** Marker grid cell, in screen pixels: points closer than this merge. */
export const CLUSTER_CELL_PX = 64;

// Web Mercator can't draw the poles; tiles stop at this latitude.
const MAX_MERCATOR_LAT = 85.05112878;
const TILE_SIZE = 256;

/** Web Mercator world-pixel coordinates of a point at a zoom level — the
 *  same projection Leaflet and the OSM tiles use. */
export function project(lat: number, lon: number, zoom: number): { x: number; y: number } {
  const scale = TILE_SIZE * Math.pow(2, zoom);
  const clampedLat = Math.max(-MAX_MERCATOR_LAT, Math.min(MAX_MERCATOR_LAT, lat));
  const sin = Math.sin((clampedLat * Math.PI) / 180);
  const x = ((lon + 180) / 360) * scale;
  const y = (0.5 - Math.log((1 + sin) / (1 - sin)) / (4 * Math.PI)) * scale;
  return { x, y };
}

/**
 * Groups points into clusters by the screen-grid cell they project into at
 * `zoom`. A cluster sits at its members' mean position, so it lands among
 * them rather than on a cell corner. Photos taken at the same spot share a
 * cell at every zoom, which is what lets one marker stand for "the photos
 * here". Clusters come out in first-seen order, and members keep the input
 * order (the server sends newest first).
 */
export function clusterPoints<T extends GeoPoint>(
  points: readonly T[],
  zoom: number,
  cellPx: number = CLUSTER_CELL_PX,
): Cluster<T>[] {
  const z = Math.max(0, Math.round(zoom));
  const cells = new Map<string, { latSum: number; lonSum: number; points: T[] }>();
  for (const p of points) {
    if (!Number.isFinite(p.lat) || !Number.isFinite(p.lon)) continue;
    const { x, y } = project(p.lat, p.lon, z);
    const key = `${z}:${Math.floor(x / cellPx)}:${Math.floor(y / cellPx)}`;
    let cell = cells.get(key);
    if (!cell) {
      cell = { latSum: 0, lonSum: 0, points: [] };
      cells.set(key, cell);
    }
    cell.latSum += p.lat;
    cell.lonSum += p.lon;
    cell.points.push(p);
  }
  const out: Cluster<T>[] = [];
  for (const [key, cell] of cells) {
    const n = cell.points.length;
    out.push({ key, lat: cell.latSum / n, lon: cell.lonSum / n, points: cell.points });
  }
  return out;
}

/** The smallest box holding every point, or null for none. */
export function boundsOf(points: readonly GeoPoint[]): ViewBounds | null {
  let south = Infinity;
  let west = Infinity;
  let north = -Infinity;
  let east = -Infinity;
  for (const p of points) {
    if (!Number.isFinite(p.lat) || !Number.isFinite(p.lon)) continue;
    south = Math.min(south, p.lat);
    north = Math.max(north, p.lat);
    west = Math.min(west, p.lon);
    east = Math.max(east, p.lon);
  }
  return Number.isFinite(south) ? { south, west, north, east } : null;
}

/** True when every point in the box sits at one position (zooming in
 *  would never pull them apart). */
export function isSinglePosition(b: ViewBounds): boolean {
  return b.south === b.north && b.west === b.east;
}

/** Wraps a longitude into [-180, 180). */
export function wrapLon(lon: number): number {
  return ((((lon + 180) % 360) + 360) % 360) - 180;
}

function clampLat(lat: number): number {
  return Math.max(-90, Math.min(90, lat));
}

/** A view's longitude span in degrees. A box given west > east (the
 *  GeoJSON way of writing one across the antimeridian) spans the gap. */
function lonSpan(view: ViewBounds): number {
  const span = view.east - view.west;
  return span < 0 ? span + 360 : span;
}

/**
 * The /photos/map queries that cover a view. The server's filter is a
 * straight BETWEEN, so a view across the antimeridian becomes two boxes
 * (west of it and east of it), and a view as wide as the world drops the
 * longitude edges.
 */
export function bboxQueries(view: ViewBounds): MapBBox[] {
  const min_lat = clampLat(Math.min(view.south, view.north));
  const max_lat = clampLat(Math.max(view.south, view.north));
  const span = lonSpan(view);
  // NaN fails the comparison too: no usable longitude edges, so drop them.
  if (!(span < 360)) return [{ min_lat, max_lat }];
  const west = wrapLon(view.west);
  const east = west + span;
  if (east <= 180) return [{ min_lat, max_lat, min_lon: west, max_lon: east }];
  return [
    { min_lat, max_lat, min_lon: west, max_lon: 180 },
    { min_lat, max_lat, min_lon: -180, max_lon: east - 360 },
  ];
}

/** Grows a view by `ratio` of its size on every side, so markers just off
 *  screen are ready before a pan reveals them. */
export function padBounds(view: ViewBounds, ratio: number): ViewBounds {
  const dLat = (view.north - view.south) * ratio;
  const dLon = (view.east - view.west) * ratio;
  return {
    south: clampLat(view.south - dLat),
    north: clampLat(view.north + dLat),
    west: view.west - dLon,
    east: view.east + dLon,
  };
}

/** Whether a point falls in a view, accounting for a view that runs past
 *  ±180 (it's matched against every wrap of its longitude). */
export function inView(p: GeoPoint, view: ViewBounds): boolean {
  if (p.lat < view.south || p.lat > view.north) return false;
  const span = lonSpan(view);
  if (!(span < 360)) return true;
  const west = wrapLon(view.west);
  const east = west + span;
  const lon = wrapLon(p.lon);
  return (lon >= west && lon <= east) || (lon + 360 >= west && lon + 360 <= east);
}

/** Concatenates point lists, keeping the first copy of each id. */
export function mergeById<T extends { id: string }>(...lists: ReadonlyArray<readonly T[]>): T[] {
  const seen = new Set<string>();
  const out: T[] = [];
  for (const list of lists) {
    for (const p of list) {
      if (seen.has(p.id)) continue;
      seen.add(p.id);
      out.push(p);
    }
  }
  return out;
}

/** The points with the given ids, in the ids' order; unknown ids are skipped. */
export function pickByIds<T extends { id: string }>(points: readonly T[], ids: readonly string[]): T[] {
  const byId = new Map(points.map((p) => [p.id, p]));
  const out: T[] = [];
  for (const id of ids) {
    const p = byId.get(id);
    if (p) out.push(p);
  }
  return out;
}

/**
 * The status line above the map. `loaded` is how many points the map holds,
 * `total` the library's geotagged count. When a library has more than one
 * answer can carry, the map reloads per view; `complete` says that view's
 * answer wasn't cut off, i.e. every photo in the area is on the map.
 */
export function mapStatusText(loaded: number, total: number, complete: boolean = loaded >= total): string {
  const fmt = (n: number) => n.toLocaleString();
  if (total <= 0 && loaded <= 0) return 'No geotagged photos';
  if (loaded >= total) return `${fmt(loaded)} geotagged photo${loaded === 1 ? '' : 's'}`;
  if (complete) return `${fmt(total)} geotagged photos · every one in this area is shown`;
  return `Showing ${fmt(loaded)} of ${fmt(total)} geotagged photos — zoom in to see the rest`;
}

function takenTime(p: { taken_at?: string; created_at?: string }): number {
  const t = Date.parse(p.taken_at ?? p.created_at ?? '');
  return Number.isNaN(t) ? -Infinity : t;
}

/** Newest taken first (added date when there's no EXIF date), undated last. */
export function newestFirst<T extends { taken_at?: string; created_at?: string }>(points: readonly T[]): T[] {
  return [...points].sort((a, b) => takenTime(b) - takenTime(a));
}

/** "12 Mar 2024" or "12 Mar 2024 – 3 Apr 2024" for the photos' EXIF dates;
 *  "" when none has one. */
export function takenRangeLabel(points: ReadonlyArray<{ taken_at?: string }>): string {
  let min = Infinity;
  let max = -Infinity;
  for (const p of points) {
    const t = Date.parse(p.taken_at ?? '');
    if (Number.isNaN(t)) continue;
    min = Math.min(min, t);
    max = Math.max(max, t);
  }
  if (!Number.isFinite(min)) return '';
  const fmt = (t: number) => new Date(t).toLocaleDateString(undefined, { dateStyle: 'medium' });
  const a = fmt(min);
  const b = fmt(max);
  return a === b ? a : `${a} – ${b}`;
}

/** Marker badge text: exact below 1000, then "1.2k" / "12k". */
export function clusterCountLabel(n: number): string {
  if (n < 1000) return String(n);
  if (n < 10000) return `${(Math.floor(n / 100) / 10).toFixed(1).replace(/\.0$/, '')}k`;
  return `${Math.floor(n / 1000)}k`;
}

// ── Per-tab state shared between the map and the photo viewer ─────────────
//
// sessionStorage, so it's per tab and gone when the tab closes. Every access
// is guarded: storage can be missing or throw (private mode, blocked site
// data), and the map simply starts fresh then.

const VIEW_KEY_PREFIX = 'onscreen_photo_map_view:';
const SELECTION_KEY = 'onscreen_photo_map_selection';

/** The photos open in the map's side panel, handed to the viewer so its
 *  Previous / Next walk that spot's photos. */
export interface MapSelection {
  libraryId: string;
  ids: string[];
}

function storage(): Storage | null {
  try {
    return typeof sessionStorage === 'undefined' ? null : sessionStorage;
  } catch {
    return null;
  }
}

export function saveMapView(libraryId: string, view: MapView): void {
  try {
    storage()?.setItem(VIEW_KEY_PREFIX + libraryId, JSON.stringify(view));
  } catch { /* storage full or blocked — the view just isn't remembered */ }
}

export function loadMapView(libraryId: string): MapView | null {
  try {
    const raw = storage()?.getItem(VIEW_KEY_PREFIX + libraryId);
    if (!raw) return null;
    const v = JSON.parse(raw) as Partial<MapView>;
    if (
      typeof v?.lat === 'number' && Number.isFinite(v.lat) &&
      typeof v.lon === 'number' && Number.isFinite(v.lon) &&
      typeof v.zoom === 'number' && Number.isFinite(v.zoom)
    ) {
      return { lat: v.lat, lon: v.lon, zoom: v.zoom };
    }
  } catch { /* unreadable — start fresh */ }
  return null;
}

export function saveMapSelection(sel: MapSelection | null): void {
  try {
    const s = storage();
    if (!s) return;
    if (sel && sel.ids.length > 0) s.setItem(SELECTION_KEY, JSON.stringify(sel));
    else s.removeItem(SELECTION_KEY);
  } catch { /* not remembered */ }
}

export function loadMapSelection(): MapSelection | null {
  try {
    const raw = storage()?.getItem(SELECTION_KEY);
    if (!raw) return null;
    const v = JSON.parse(raw) as Partial<MapSelection>;
    if (typeof v?.libraryId === 'string' && Array.isArray(v.ids)) {
      const ids = v.ids.filter((x): x is string => typeof x === 'string');
      if (ids.length > 0) return { libraryId: v.libraryId, ids };
    }
  } catch { /* unreadable */ }
  return null;
}
