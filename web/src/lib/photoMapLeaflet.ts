// The Leaflet side of the web photo map: a thin adapter the map page drives
// through PhotoMapHandle, so the page (and its tests) never touch Leaflet.
// Clustering and bounds math live in photoMap.ts.
//
// Leaflet is an npm dependency, bundled like hls.js / epub.js, and imported
// lazily here so its code only loads when a map opens. Its stylesheet is
// bundled with this module. Tiles are raster PNGs from the OpenStreetMap
// tile server, which Leaflet loads as <img> elements: the server's CSP
// already allows https: images (img-src), and nothing is fetched with
// fetch/XHR, so connect-src needs no tile host.

import 'leaflet/dist/leaflet.css';
import type { LeafletKeyboardEvent } from 'leaflet';
import type { PhotoMapPoint } from './api';
import { clusterCountLabel, type Cluster, type MapView, type ViewBounds } from './photoMap';

export const OSM_TILE_URL = 'https://tile.openstreetmap.org/{z}/{x}/{y}.png';
const OSM_ATTRIBUTION =
  '&copy; <a href="https://www.openstreetmap.org/copyright" target="_blank" rel="noopener noreferrer">OpenStreetMap</a> contributors';
const MAX_ZOOM = 19;
const MARKER_PX = 44;

export interface PhotoMapOptions {
  /** URL for a marker thumbnail from a photo's poster_path. */
  thumbUrl: (posterPath: string) => string;
  /** After every pan / zoom settles (Leaflet's moveend). */
  onViewChange: () => void;
  /** A marker was clicked (or activated with Enter). */
  onClusterClick: (cluster: Cluster<PhotoMapPoint>) => void;
}

export interface PhotoMapHandle {
  /** Replaces every marker with one per cluster. */
  showClusters(clusters: readonly Cluster<PhotoMapPoint>[]): void;
  fitBounds(b: ViewBounds, maxZoom?: number): void;
  setView(v: MapView): void;
  view(): MapView;
  bounds(): ViewBounds;
  zoom(): number;
  destroy(): void;
}

export async function createPhotoMap(el: HTMLElement, opts: PhotoMapOptions): Promise<PhotoMapHandle> {
  const L = await import('leaflet/dist/leaflet-src.esm.js');

  // worldCopyJump keeps markers on the copy of the world being looked at
  // when a pan crosses the antimeridian.
  const map = L.map(el, { worldCopyJump: true, minZoom: 1, maxZoom: MAX_ZOOM }).setView([20, 0], 2);
  L.tileLayer(OSM_TILE_URL, { maxZoom: MAX_ZOOM, attribution: OSM_ATTRIBUTION }).addTo(map);
  const markers = L.layerGroup().addTo(map);
  map.on('moveend', () => opts.onViewChange());

  // The icon is built as DOM rather than an HTML string, so a title or path
  // can never be parsed as markup.
  function iconFor(c: Cluster<PhotoMapPoint>) {
    const n = c.points.length;
    const el = document.createElement('div');
    el.className = n > 1 ? 'pm-marker pm-cluster' : 'pm-marker';
    const cover = c.points.find((p) => p.poster_path);
    if (cover?.poster_path) {
      const img = document.createElement('img');
      img.src = opts.thumbUrl(cover.poster_path);
      img.alt = '';
      img.draggable = false;
      el.appendChild(img);
    } else {
      el.classList.add('pm-blank');
    }
    if (n > 1) {
      const badge = document.createElement('span');
      badge.className = 'pm-count';
      badge.textContent = clusterCountLabel(n);
      el.appendChild(badge);
    }
    return L.divIcon({
      html: el,
      className: 'pm-icon',
      iconSize: [MARKER_PX, MARKER_PX],
      iconAnchor: [MARKER_PX / 2, MARKER_PX / 2],
    });
  }

  return {
    showClusters(clusters) {
      markers.clearLayers();
      for (const c of clusters) {
        const n = c.points.length;
        const m = L.marker([c.lat, c.lon], {
          icon: iconFor(c),
          // keyboard makes the marker a focusable role=button; the title is
          // its accessible name.
          title: n > 1 ? `${n.toLocaleString()} photos` : c.points[0]?.title || 'Photo',
          keyboard: true,
          riseOnHover: true,
        });
        m.on('click', () => opts.onClusterClick(c));
        // A focused marker is a div, so Enter doesn't click it natively;
        // Leaflet relays the key event instead.
        m.on('keypress', (e) => {
          if ((e as LeafletKeyboardEvent).originalEvent.key === 'Enter') opts.onClusterClick(c);
        });
        markers.addLayer(m);
      }
    },
    fitBounds(b, maxZoom = 16) {
      map.fitBounds(
        [
          [b.south, b.west],
          [b.north, b.east],
        ],
        { padding: [48, 48], maxZoom },
      );
    },
    setView(v) {
      map.setView([v.lat, v.lon], v.zoom);
    },
    view() {
      const c = map.getCenter();
      return { lat: c.lat, lon: c.lng, zoom: map.getZoom() };
    },
    bounds() {
      const b = map.getBounds();
      return { south: b.getSouth(), west: b.getWest(), north: b.getNorth(), east: b.getEast() };
    },
    zoom() {
      return map.getZoom();
    },
    destroy() {
      map.remove();
    },
  };
}
