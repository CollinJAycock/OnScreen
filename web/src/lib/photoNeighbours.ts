// Previous / Next for the photo viewer in a paged listing (a photo library,
// a person's photos): a bounded window of the listing around the photo,
// never the whole library. The grid passes the photo's position
// ($lib/photoViewerContext ?pos=), so the first fetch is the window around
// it; walking towards an edge fetches the next page there, and the far side
// is trimmed so the window stays bounded however far the viewer walks.
// Opened with no known position from a library (a hub row, search, the
// timeline, a shared link), the viewer asks the server where the photo sits
// in the library's listing (GET /libraries/{id}/items/position) and opens
// the window there. When the server can't say (one without the endpoint,
// a photo not in that listing), the viewer gets one first page; a photo
// outside it simply has no neighbours.

/** A contiguous slice of a listing: ids[i] sits at offset + i. */
export interface ListWindow {
  /** The listing it slices (photoViewerContext contextKey). */
  key: string;
  offset: number;
  ids: string[];
  /** The listing's length when last fetched. */
  total: number;
}

export type PageFetcher = (offset: number, limit: number) => Promise<{ items: { id: string }[]; total: number }>;

/** The first fetch around a known position. */
export const WINDOW = 100;
/** Each fetch at an edge. */
export const EDGE_PAGE = 50;
/** Extend once the photo is this close to an edge of the window. */
export const EDGE_MARGIN = 5;
/** The window never holds more ids than this. */
export const MAX_IDS = 400;
/** No known position: the one first page fetched. */
export const FALLBACK_PAGE = 200;

/** The window around pos (the first page when pos is unknown). Throws when
 *  the fetch fails. */
export async function openWindow(key: string, fetchPage: PageFetcher, pos?: number): Promise<ListWindow> {
  const from = pos === undefined ? 0 : Math.max(0, pos - WINDOW / 2);
  const r = await fetchPage(from, pos === undefined ? FALLBACK_PAGE : WINDOW);
  return { key, offset: from, ids: dedupe(r.items.map((i) => i.id)), total: r.total };
}

/** Asks the server for the photo's row in the listing; rejects (or gives
 *  a non-row) when it can't say. */
export type Locator = () => Promise<{ offset: number }>;

/** The window for a photo the loaded window of its listing (loaded, null
 *  when there is none) doesn't hold. A known pos opens around it. With no
 *  pos, locate (given for a library listing) asks where the photo sits and
 *  the window opens there; when it fails, the one fallback page — or null
 *  when loaded is already that page: refetching it would get the same page
 *  again, so the photo has no neighbours. Throws when a page fetch fails. */
export async function windowForPhoto(
  key: string,
  fetchPage: PageFetcher,
  loaded: ListWindow | null,
  pos?: number,
  locate?: Locator,
): Promise<ListWindow | null> {
  if (pos === undefined && locate) {
    let found: number | undefined;
    try {
      const at = (await locate()).offset;
      if (Number.isInteger(at) && at >= 0) found = at;
    } catch { /* an older server, or not in the listing: the fallback */ }
    if (found !== undefined) return openWindow(key, fetchPage, found);
  }
  if (pos === undefined && loaded) return null;
  return openWindow(key, fetchPage, pos);
}

/** The page that extends w towards the edge the photo at idx is near, or
 *  null when it is far from both or the listing ends there. */
export function edgeFetch(w: ListWindow, idx: number): { side: 'before' | 'after'; offset: number; limit: number } | null {
  if (idx < 0) return null;
  if (idx < EDGE_MARGIN && w.offset > 0) {
    const from = Math.max(0, w.offset - EDGE_PAGE);
    return { side: 'before', offset: from, limit: w.offset - from };
  }
  const end = w.offset + w.ids.length;
  if (w.ids.length - 1 - idx < EDGE_MARGIN && end < w.total) {
    return { side: 'after', offset: end, limit: EDGE_PAGE };
  }
  return null;
}

/** w with page added on side (ids it already has are skipped), trimmed on
 *  the other side to MAX_IDS. */
export function mergeEdge(
  w: ListWindow,
  side: 'before' | 'after',
  fetchedFrom: number,
  page: { items: { id: string }[]; total: number },
): ListWindow {
  const have = new Set(w.ids);
  const added = page.items.map((i) => i.id).filter((id) => !have.has(id));
  if (side === 'before') {
    const ids = dedupe([...added, ...w.ids]).slice(0, MAX_IDS);
    return { key: w.key, offset: Math.max(0, fetchedFrom + (page.items.length - added.length)), ids, total: page.total };
  }
  let ids = dedupe([...w.ids, ...added]);
  let offset = w.offset;
  if (ids.length > MAX_IDS) {
    const drop = ids.length - MAX_IDS;
    ids = ids.slice(drop);
    offset += drop;
  }
  return { key: w.key, offset, ids, total: page.total };
}

function dedupe(ids: string[]): string[] {
  return [...new Set(ids)];
}
