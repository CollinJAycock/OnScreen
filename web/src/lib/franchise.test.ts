import type { CollectionItem, FranchisePart } from '$lib/api';
import {
  applyRequestResult,
  collectionCover,
  collectionRows,
  ownedPosterPath,
  ownedSummary,
  partItemIds,
  partOfHeading,
  partState,
  requestStatusLabel,
  shelfParts,
  type FranchisePartCopies,
} from './franchise';

const part = (tmdb_id: number, title: string, item_id: string | null = null, request_status: FranchisePart['request_status'] = null): FranchisePart => ({
  tmdb_id,
  title,
  item_id,
  request_status,
});

describe('partState', () => {
  it('distinguishes owned, requested and missing', () => {
    expect(partState(part(1, 'A', 'item-1'))).toBe('owned');
    expect(partState(part(2, 'B', null, 'pending'))).toBe('requested');
    expect(partState(part(3, 'C'))).toBe('missing');
    // Owned wins even if a request echo is present.
    expect(partState(part(4, 'D', 'item-4', 'approved'))).toBe('owned');
  });
});

describe('requestStatusLabel', () => {
  it('labels each open status', () => {
    expect(requestStatusLabel('pending')).toBe('Requested');
    expect(requestStatusLabel('approved')).toBe('Approved');
    expect(requestStatusLabel('downloading')).toBe('Downloading');
    expect(requestStatusLabel(null)).toBe('');
    expect(requestStatusLabel('declined')).toBe('');
  });
});

describe('applyRequestResult', () => {
  const parts = [part(348, 'Alien', 'i-1'), part(679, 'Aliens')];

  it('flips the missing part to its open request status', () => {
    const next = applyRequestResult(parts, 679, 'pending');
    expect(next[1].request_status).toBe('pending');
    expect(next[0]).toBe(parts[0]);
  });

  it('ignores non-open statuses and owned parts', () => {
    expect(applyRequestResult(parts, 679, 'declined')).toBe(parts);
    expect(applyRequestResult(parts, 348, 'pending')[0].request_status).toBeNull();
  });
});

describe('collectionRows', () => {
  const items: CollectionItem[] = [
    { id: 'i-1', title: 'Alien', type: 'movie', year: 1979, poster_path: 'movies/alien/poster.jpg' },
    { id: 'i-9', title: 'Alien (Director’s Cut)', type: 'movie', year: 1979 },
  ];

  it('keeps parts in order and appends owned items the snapshot does not list', () => {
    const rows = collectionRows([part(348, 'Alien', 'i-1'), part(679, 'Aliens')], items);
    expect(rows.map((r) => r.title)).toEqual(['Alien', 'Aliens', 'Alien (Director’s Cut)']);
    expect(rows[2].item_id).toBe('i-9');
  });

  it('falls back to the owned items when there is no snapshot', () => {
    const rows = collectionRows(undefined, items);
    expect(rows).toHaveLength(2);
    expect(rows.every((r) => !!r.item_id)).toBe(true);
  });

  it('finds local artwork for owned parts', () => {
    expect(ownedPosterPath(part(348, 'Alien', 'i-1'), items)).toBe('movies/alien/poster.jpg');
    expect(ownedPosterPath(part(679, 'Aliens'), items)).toBeUndefined();
  });

  // The same film in a 4K and a 1080p library: the server links one copy
  // from the part and lists both in item_ids, while the items listing
  // returns each copy. The second copy is the same film, not an extra row.
  const copies: CollectionItem[] = [
    { id: 'i-1080', title: 'Alien', type: 'movie', year: 1979 },
    { id: 'i-4k', title: 'Alien', type: 'movie', year: 1979, poster_path: 'movies-4k/alien/poster.jpg' },
    { id: 'i-9', title: 'Alien (Director’s Cut)', type: 'movie', year: 1979 },
  ];
  const alienBoth: FranchisePartCopies = { ...part(348, 'Alien', 'i-1080'), item_ids: ['i-1080', 'i-4k'] };

  it('folds another copy of a listed film into its part', () => {
    const rows = collectionRows([alienBoth, part(679, 'Aliens')], copies);
    expect(rows.map((r) => r.title)).toEqual(['Alien', 'Aliens', 'Alien (Director’s Cut)']);
    expect(rows.filter((r) => r.tmdb_id === 0).map((r) => r.item_id)).toEqual(['i-9']);
  });

  it('takes artwork from any copy of the part', () => {
    expect(ownedPosterPath(alienBoth, copies)).toBe('movies-4k/alien/poster.jpg');
  });

  it('falls back to item_id alone for servers without item_ids', () => {
    expect(partItemIds(part(348, 'Alien', 'i-1'))).toEqual(['i-1']);
    expect(partItemIds(part(679, 'Aliens'))).toEqual([]);
    expect(partItemIds(alienBoth)).toEqual(['i-1080', 'i-4k']);
  });
});

describe('shelfParts', () => {
  it('drops the current movie and puts owned films first, each group in release order', () => {
    const parts = [
      part(348, 'Alien', 'cur'),
      part(679, 'Aliens'),
      part(8077, 'Alien³', 'i-3'),
      part(8078, 'Alien Resurrection'),
      part(126889, 'Alien: Covenant', 'i-5'),
    ];
    expect(shelfParts(parts, 'cur').map((p) => p.title)).toEqual([
      'Alien³',
      'Alien: Covenant',
      'Aliens',
      'Alien Resurrection',
    ]);
  });

  it('drops the current film when the page is a copy the part does not link', () => {
    const parts: FranchisePartCopies[] = [
      { ...part(348, 'Alien', 'i-1080'), item_ids: ['i-1080', 'i-4k'] },
      part(679, 'Aliens', 'i-2'),
    ];
    // Watching the 4K copy: the part links the 1080p one, but it is still
    // this film and must not appear in its own shelf.
    expect(shelfParts(parts, 'i-4k').map((p) => p.title)).toEqual(['Aliens']);
    expect(shelfParts(parts, 'i-1080').map((p) => p.title)).toEqual(['Aliens']);
  });
});

describe('labels and covers', () => {
  it('builds the heading and summary', () => {
    expect(partOfHeading('Alien Collection')).toBe('Part of the Alien Collection');
    expect(ownedSummary(2, 4)).toBe('2 of 4 films');
    expect(ownedSummary(3, 3)).toBe('3 films');
    expect(ownedSummary(1, 1)).toBe('1 film');
  });

  it('prefers the TMDB poster, then the member artwork', () => {
    expect(collectionCover({ poster_url: 'https://x/p.jpg', poster_path: 'a.jpg' })).toEqual({ url: 'https://x/p.jpg' });
    expect(collectionCover({ poster_path: 'a.jpg' })).toEqual({ artwork: 'a.jpg' });
    // An admin-uploaded cover wins over both.
    expect(collectionCover({ id: 'c-1', poster_version: 7, poster_url: 'https://x/p.jpg', poster_path: 'a.jpg' }).url)
      .toBe('/api/v1/collections/c-1/poster?v=7');
    expect(collectionCover({})).toEqual({});
  });
});
