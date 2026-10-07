import { collectionCoverPath } from './api/types';
import { describe, expect, it } from 'vitest';
import type { HubRowPref } from './api/types';
import { orderRows } from './hubLayout';

const DEFAULT = [
  'continue_tv',
  'next_up',
  'continue_movies',
  'continue_other',
  'plan_to_watch',
  'trending',
  'library:aaa',
  'library:bbb',
  'collections',
].map((key) => ({ key }));

const keys = (rows: { key: string }[]) => rows.map((r) => r.key);

describe('orderRows', () => {
  it('keeps the default order without a saved layout', () => {
    expect(keys(orderRows(DEFAULT, undefined))).toEqual(keys(DEFAULT));
    expect(keys(orderRows(DEFAULT, null))).toEqual(keys(DEFAULT));
    expect(keys(orderRows(DEFAULT, []))).toEqual(keys(DEFAULT));
  });

  it('puts saved rows first, in the saved order', () => {
    const layout: HubRowPref[] = [
      { key: 'library:bbb', enabled: true },
      { key: 'trending', enabled: true },
      { key: 'continue_tv', enabled: true },
    ];
    expect(keys(orderRows(DEFAULT, layout))).toEqual([
      'library:bbb',
      'trending',
      'continue_tv',
      // Not in the layout: appended in default order.
      'next_up',
      'continue_movies',
      'continue_other',
      'plan_to_watch',
      'library:aaa',
      'collections',
    ]);
  });

  it('hides rows saved as disabled', () => {
    const layout: HubRowPref[] = [
      { key: 'trending', enabled: false },
      { key: 'continue_tv', enabled: true },
      { key: 'library:aaa', enabled: false },
    ];
    const out = keys(orderRows(DEFAULT, layout));
    expect(out).not.toContain('trending');
    expect(out).not.toContain('library:aaa');
    expect(out[0]).toBe('continue_tv');
    expect(out).toHaveLength(DEFAULT.length - 2);
  });

  it('skips unknown keys (a deleted library, the web-only libraries grid)', () => {
    const layout: HubRowPref[] = [
      { key: 'libraries', enabled: true },
      { key: 'library:gone', enabled: true },
      { key: 'next_up', enabled: true },
    ];
    const out = keys(orderRows(DEFAULT, layout));
    expect(out[0]).toBe('next_up');
    expect(out).not.toContain('libraries');
    expect(out).not.toContain('library:gone');
    expect(out).toHaveLength(DEFAULT.length);
  });

  it('appends TV-only rows the web never saves in their default position', () => {
    const rows = ['continue_tv', 'trending', 'recently_added', 'collections'].map((key) => ({ key }));
    const layout: HubRowPref[] = [
      { key: 'trending', enabled: true },
      { key: 'continue_tv', enabled: true },
    ];
    expect(keys(orderRows(rows, layout))).toEqual(['trending', 'continue_tv', 'recently_added', 'collections']);
  });

  it('treats a missing enabled flag as shown and a repeated key as its first entry', () => {
    const layout = [
      { key: 'collections' },
      { key: 'next_up', enabled: false },
      { key: 'collections', enabled: false },
      { key: 'next_up', enabled: true },
    ] as HubRowPref[];
    const out = keys(orderRows(DEFAULT, layout));
    expect(out[0]).toBe('collections');
    expect(out.filter((k) => k === 'collections')).toHaveLength(1);
    expect(out).not.toContain('next_up');
  });

  it('ignores malformed entries', () => {
    const layout = [null, { enabled: true }, { key: 7 }, { key: 'trending', enabled: true }] as unknown as HubRowPref[];
    expect(keys(orderRows(DEFAULT, layout))[0]).toBe('trending');
    expect(orderRows(DEFAULT, 'nope' as unknown as HubRowPref[])).toHaveLength(DEFAULT.length);
  });

  it('returns the row objects themselves and leaves the input alone', () => {
    const rows = [{ key: 'a', n: 1 }, { key: 'b', n: 2 }];
    const out = orderRows(rows, [{ key: 'b', enabled: true }]);
    expect(out[0]).toBe(rows[1]);
    expect(keys(rows)).toEqual(['a', 'b']);
  });
});

describe('promoted collection rows', () => {
  it('orders and hides collection:<id> rows like any other', () => {
    const rows = [{ key: 'plan_to_watch' }, { key: 'collection:c-1' }, { key: 'collection:c-2' }, { key: 'trending' }];
    const out = orderRows(rows, [
      { key: 'trending', enabled: true },
      { key: 'collection:c-2', enabled: true },
      { key: 'collection:c-1', enabled: false },
    ]).map((r) => r.key);
    expect(out).toEqual(['trending', 'collection:c-2', 'plan_to_watch']);
  });

  it("builds an uploaded cover's path only when there is one", () => {
    expect(collectionCoverPath({ id: 'c-1', poster_version: 9 })).toBe('/api/v1/collections/c-1/poster?v=9');
    expect(collectionCoverPath({ id: 'c-1' })).toBeUndefined();
  });
});