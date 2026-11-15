import { describe, expect, it, vi } from 'vitest';
import { EDGE_MARGIN, EDGE_PAGE, FALLBACK_PAGE, MAX_IDS, WINDOW, edgeFetch, mergeEdge, openWindow, windowForPhoto, type ListWindow } from './photoNeighbours';

const ids = (from: number, n: number) => Array.from({ length: n }, (_, i) => `p${from + i}`);
const page = (from: number, n: number, total: number) => ({ items: ids(from, n).map((id) => ({ id })), total });

describe('openWindow', () => {
  it('fetches a window centred on a known position', async () => {
    const fetchPage = vi.fn().mockResolvedValue(page(950, WINDOW, 5000));
    const w = await openWindow('k', fetchPage, 1000);
    expect(fetchPage).toHaveBeenCalledWith(1000 - WINDOW / 2, WINDOW);
    expect(w.offset).toBe(950);
    expect(w.ids.indexOf('p1000')).toBe(50);
    expect(w.total).toBe(5000);
  });

  it('starts at 0 near the top', async () => {
    const fetchPage = vi.fn().mockResolvedValue(page(0, WINDOW, 5000));
    await openWindow('k', fetchPage, 3);
    expect(fetchPage).toHaveBeenCalledWith(0, WINDOW);
  });

  it('fetches one bounded first page when the position is unknown', async () => {
    const fetchPage = vi.fn().mockResolvedValue(page(0, FALLBACK_PAGE, 5000));
    const w = await openWindow('k', fetchPage);
    expect(fetchPage).toHaveBeenCalledTimes(1);
    expect(fetchPage).toHaveBeenCalledWith(0, FALLBACK_PAGE);
    expect(w.ids).toHaveLength(FALLBACK_PAGE);
  });
});

describe('windowForPhoto', () => {
  const loaded: ListWindow = { key: 'k', offset: 0, ids: ids(0, FALLBACK_PAGE), total: 5000 };

  it('opens around a known position without asking the server', async () => {
    const fetchPage = vi.fn().mockResolvedValue(page(950, WINDOW, 5000));
    const locate = vi.fn();
    const w = await windowForPhoto('k', fetchPage, loaded, 1000, locate);
    expect(locate).not.toHaveBeenCalled();
    expect(fetchPage).toHaveBeenCalledWith(950, WINDOW);
    expect(w?.ids.indexOf('p1000')).toBe(50);
  });

  it('with no position, opens around where the server says the photo sits', async () => {
    const fetchPage = vi.fn().mockResolvedValue(page(3950, WINDOW, 5000));
    const locate = vi.fn().mockResolvedValue({ offset: 4000, total: 5000 });
    const w = await windowForPhoto('k', fetchPage, loaded, undefined, locate);
    expect(locate).toHaveBeenCalledTimes(1);
    expect(fetchPage).toHaveBeenCalledTimes(1);
    expect(fetchPage).toHaveBeenCalledWith(3950, WINDOW);
    expect(w?.offset).toBe(3950);
    expect(w?.ids.indexOf('p4000')).toBe(50);
  });

  it("falls back to the first page when the server can't say", async () => {
    for (const locate of [
      vi.fn().mockRejectedValue(Object.assign(new Error('not found'), { status: 404 })),
      vi.fn().mockRejectedValue(new Error('network')),
      vi.fn().mockResolvedValue({ offset: -1, total: 0 }),
      vi.fn().mockResolvedValue({}),
    ]) {
      const fetchPage = vi.fn().mockResolvedValue(page(0, FALLBACK_PAGE, 5000));
      const w = await windowForPhoto('k', fetchPage, null, undefined, locate);
      expect(fetchPage).toHaveBeenCalledWith(0, FALLBACK_PAGE);
      expect(w?.offset).toBe(0);
    }
  });

  it('does not refetch the fallback page it already has', async () => {
    const fetchPage = vi.fn();
    const failed = vi.fn().mockRejectedValue(new Error('not found'));
    expect(await windowForPhoto('k', fetchPage, loaded, undefined, failed)).toBeNull();
    expect(await windowForPhoto('k', fetchPage, loaded)).toBeNull();
    expect(fetchPage).not.toHaveBeenCalled();
  });

  it('with nothing to locate by (not a library listing), the first page', async () => {
    const fetchPage = vi.fn().mockResolvedValue(page(0, FALLBACK_PAGE, 5000));
    await windowForPhoto('k', fetchPage, null);
    expect(fetchPage).toHaveBeenCalledWith(0, FALLBACK_PAGE);
  });

  it('a failed page fetch throws, as openWindow does', async () => {
    const fetchPage = vi.fn().mockRejectedValue(new Error('down'));
    await expect(windowForPhoto('k', fetchPage, null, undefined, vi.fn().mockResolvedValue({ offset: 7, total: 9 }))).rejects.toThrow('down');
  });
});

describe('edgeFetch', () => {
  const w: ListWindow = { key: 'k', offset: 100, ids: ids(100, 100), total: 1000 };

  it('asks for nothing in the middle of the window', () => {
    expect(edgeFetch(w, 50)).toBeNull();
    expect(edgeFetch(w, -1)).toBeNull();
  });

  it('extends forwards near the end, backwards near the start', () => {
    expect(edgeFetch(w, 99 - (EDGE_MARGIN - 1))).toEqual({ side: 'after', offset: 200, limit: EDGE_PAGE });
    expect(edgeFetch(w, EDGE_MARGIN - 1)).toEqual({ side: 'before', offset: 100 - EDGE_PAGE, limit: EDGE_PAGE });
  });

  it('stops at the ends of the listing', () => {
    expect(edgeFetch({ ...w, offset: 0 }, 0)).toBeNull();
    expect(edgeFetch({ ...w, offset: 900 }, 99)).toBeNull();
    expect(edgeFetch({ ...w, offset: 20 }, 0)).toEqual({ side: 'before', offset: 0, limit: 20 });
  });
});

describe('mergeEdge', () => {
  it('appends after and prepends before, keeping positions', () => {
    const w: ListWindow = { key: 'k', offset: 100, ids: ids(100, 100), total: 1000 };
    const after = mergeEdge(w, 'after', 200, page(200, EDGE_PAGE, 1000));
    expect(after.offset).toBe(100);
    expect(after.ids[after.ids.length - 1]).toBe('p249');
    const before = mergeEdge(w, 'before', 50, page(50, EDGE_PAGE, 1000));
    expect(before.offset).toBe(50);
    expect(before.ids[0]).toBe('p50');
    expect(before.ids.indexOf('p100')).toBe(50);
  });

  it('stays bounded however far the viewer walks', () => {
    let w: ListWindow = { key: 'k', offset: 0, ids: ids(0, MAX_IDS), total: 100_000 };
    for (let i = 0; i < 10; i++) {
      const from = w.offset + w.ids.length;
      w = mergeEdge(w, 'after', from, page(from, EDGE_PAGE, 100_000));
    }
    expect(w.ids).toHaveLength(MAX_IDS);
    expect(w.ids[0]).toBe(`p${w.offset}`);
    expect(w.ids[w.ids.length - 1]).toBe(`p${MAX_IDS + 10 * EDGE_PAGE - 1}`);
  });

  it('never lists a photo twice when the listing shifted', () => {
    const w: ListWindow = { key: 'k', offset: 0, ids: ids(0, 10), total: 20 };
    const merged = mergeEdge(w, 'after', 10, page(8, 5, 20));
    expect(new Set(merged.ids).size).toBe(merged.ids.length);
    expect(merged.ids.slice(-3)).toEqual(['p10', 'p11', 'p12']);
  });
});
