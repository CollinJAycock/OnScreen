import { describe, expect, it } from 'vitest';
import { SORT_OPTIONS, defaultSortFor, parseSort, sameSort, sortKey, sortLabel } from './librarySort';

describe('librarySort', () => {
  it("offers Android's six options in Android's order", () => {
    expect(SORT_OPTIONS.map((o) => `${o.sort}:${o.dir}`)).toEqual([
      'title:asc',
      'title:desc',
      'created_at:desc',
      'year:desc',
      'year:asc',
      'rating:desc',
    ]);
  });

  it('opens date-driven libraries newest first and the rest A–Z', () => {
    for (const t of ['home_video', 'photo', 'dvr']) {
      expect(defaultSortFor(t)).toEqual({ sort: 'created_at', dir: 'desc' });
    }
    for (const t of ['movie', 'show', 'anime', 'music', 'audiobook', '', null, undefined]) {
      expect(defaultSortFor(t)).toEqual({ sort: 'title', dir: 'asc' });
    }
  });

  it('round-trips through its stored form', () => {
    for (const o of SORT_OPTIONS) {
      expect(parseSort(sortKey(o))).toEqual({ sort: o.sort, dir: o.dir });
    }
  });

  it('rejects stored values that are not one of the options', () => {
    for (const v of [null, undefined, '', 'title', 'title:up', 'updated_at:desc', 'rating:asc']) {
      expect(parseSort(v)).toBeNull();
    }
  });

  it('labels a sort and compares sorts', () => {
    expect(sortLabel({ sort: 'rating', dir: 'desc' })).toBe('Highest rated');
    expect(sortLabel({ sort: 'rating', dir: 'asc' })).toBe('Sort');
    expect(sameSort({ sort: 'year', dir: 'asc' }, { sort: 'year', dir: 'asc' })).toBe(true);
    expect(sameSort({ sort: 'year', dir: 'asc' }, { sort: 'year', dir: 'desc' })).toBe(false);
  });
});
