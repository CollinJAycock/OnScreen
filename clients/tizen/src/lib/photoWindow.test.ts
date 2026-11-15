import { describe, expect, it, vi } from 'vitest';
import {
  PHOTO_EDGE,
  PHOTO_PAGE,
  PhotoWindow,
  offerPhotoSource,
  takePhotoSource,
  type PhotoPager,
} from './photoWindow';

/** A listing of `n` photos p0..p{n-1}, with its total, counting reads. */
function listing(n: number, withTotal = true) {
  const reads: Array<[number, number]> = [];
  const page = vi.fn<PhotoPager>(async (offset, limit) => {
    reads.push([offset, limit]);
    const items = [];
    for (let i = offset; i < Math.min(n, offset + limit); i++) items.push({ id: `p${i}`, posterPath: '' });
    return { items, total: withTotal ? n : null };
  });
  return { page, reads };
}

describe('PhotoWindow', () => {
  it('reads one page around the photo, not the whole library', async () => {
    const { page, reads } = listing(50_000);
    const w = new PhotoWindow(page);
    expect(await w.open('p20000', 20_000)).toBe(true);
    expect(reads).toEqual([[20_000 - PHOTO_PAGE / 2, PHOTO_PAGE]]);
    expect(w.start + w.indexOf('p20000')).toBe(20_000);
    expect(w.total).toBe(50_000);
    expect(w.complete).toBe(false);
  });

  it('reads the next page only near the edge, one read at a time', async () => {
    const { page, reads } = listing(50_000);
    const w = new PhotoWindow(page);
    await w.open('p1000', 1000);
    const last = w.items.length - 1;
    expect(await w.extendAfter(last - PHOTO_EDGE - 1)).toBe(false);
    expect(reads).toHaveLength(1);
    const a = w.extendAfter(last);
    const b = w.extendAfter(last);
    expect(await a).toBe(true);
    expect(await b).toBe(true);
    expect(reads).toHaveLength(2);
    expect(w.items).toHaveLength(2 * PHOTO_PAGE);
    expect(w.items[PHOTO_PAGE].id).toBe(`p${1000 - PHOTO_PAGE / 2 + PHOTO_PAGE}`);
  });

  it('puts the page before in front, and knows where the listing starts', async () => {
    const { page } = listing(50_000);
    const w = new PhotoWindow(page);
    await w.open('p120', 120);
    expect(w.start).toBe(70);
    expect(await w.extendBefore(0)).toBe(true);
    // Only the 70 rows there are.
    expect(w.start).toBe(0);
    expect(w.items[0].id).toBe('p0');
    expect(w.atStart).toBe(true);
    expect(await w.extendBefore(0)).toBe(false);
  });

  it('a short listing is whole at once (left / right wrap)', async () => {
    const { page } = listing(12, false);
    const w = new PhotoWindow(page);
    expect(await w.open('p3', 3)).toBe(true);
    expect(w.items).toHaveLength(12);
    expect(w.complete).toBe(true);
  });

  it('says when the photo is not where the opener thought', async () => {
    const { page } = listing(500);
    expect(await new PhotoWindow(page).open('gone', 200)).toBe(false);
  });

  it('reads nothing more once stopped, and drops a late answer', async () => {
    let answer: () => void = () => {};
    const page = vi.fn<PhotoPager>(
      (offset, limit) =>
        new Promise((resolve) => {
          answer = () =>
            resolve({
              items: Array.from({ length: limit }, (_, i) => ({ id: `p${offset + i}`, posterPath: '' })),
              total: 10_000,
            });
        }),
    );
    const w = new PhotoWindow(page);
    const opened = w.open('p500', 500);
    w.stop();
    answer();
    expect(await opened).toBe(false);
    expect(w.items).toEqual([]);
    expect(await w.extendAfter(0)).toBe(false);
    expect(page).toHaveBeenCalledTimes(1);
  });
});

describe('offerPhotoSource / takePhotoSource', () => {
  it('hands the listing to the viewer of that photo, once', () => {
    const { page } = listing(10);
    offerPhotoSource({ id: 'p4', at: 4, page });
    expect(takePhotoSource('p4')?.at).toBe(4);
    expect(takePhotoSource('p4')).toBeNull();
    offerPhotoSource({ id: 'p4', at: 4, page });
    expect(takePhotoSource('p5')).toBeNull();
    expect(takePhotoSource('p4')).toBeNull();
  });
});
