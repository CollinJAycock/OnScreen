import { describe, expect, it, vi } from 'vitest';
import type { ChildItem } from './api/types';
import {
  firstTrack,
  inPlayOrder,
  pickContainerStart,
  pickRandom,
  playAllAlbumOrder,
  resolvePlayAll,
  resolveShuffle,
  shuffled,
} from './playPick';

function child(id: string, extra: Partial<ChildItem> = {}): ChildItem {
  return { id, title: id, type: 'track', ...extra };
}

// mulberry32: a tiny deterministic RNG so the shuffle tests are repeatable.
function seeded(seed: number): () => number {
  let a = seed >>> 0;
  return () => {
    a = (a + 0x6d2b79f5) >>> 0;
    let t = a;
    t = Math.imul(t ^ (t >>> 15), t | 1);
    t ^= t + Math.imul(t ^ (t >>> 7), t | 61);
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

describe('inPlayOrder', () => {
  it('orders by disc (absent = 1), then number, keeping listing order for ties', () => {
    const list = [
      child('d2t1', { disc_number: 2, index: 1 }),
      child('t2', { index: 2 }),
      child('d1t1', { disc_number: 1, index: 1 }),
      child('none-a'),
      child('none-b'),
    ];
    expect(inPlayOrder(list).map((c) => c.id)).toEqual(['d1t1', 't2', 'none-a', 'none-b', 'd2t1']);
  });

  it('does not mutate its input', () => {
    const list = [child('b', { index: 2 }), child('a', { index: 1 })];
    inPlayOrder(list);
    expect(list.map((c) => c.id)).toEqual(['b', 'a']);
  });
});

describe('pickContainerStart', () => {
  it('prefers the part-played child, at its resume point', () => {
    const pick = pickContainerStart([
      child('1', { index: 1, watched: true }),
      child('2', { index: 2 }),
      child('3', { index: 3, view_offset_ms: 42_000 }),
    ]);
    expect(pick).toEqual({ child: expect.objectContaining({ id: '3' }), startMs: 42_000 });
  });

  it('ignores a resume point on a watched child', () => {
    const pick = pickContainerStart([
      child('1', { index: 1, watched: true, view_offset_ms: 10_000 }),
      child('2', { index: 2 }),
    ]);
    expect(pick?.child.id).toBe('2');
    expect(pick?.startMs).toBe(0);
  });

  it('falls back to the first unplayed child in (disc, track) order', () => {
    const pick = pickContainerStart([
      child('d2t1', { disc_number: 2, index: 1 }),
      child('d1t2', { disc_number: 1, index: 2 }),
      child('d1t1', { disc_number: 1, index: 1, watched: true }),
    ]);
    expect(pick?.child.id).toBe('d1t2');
  });

  it('starts over from the first child when everything is played', () => {
    const pick = pickContainerStart([
      child('2', { index: 2, watched: true }),
      child('1', { index: 1, watched: true }),
    ]);
    expect(pick).toEqual({ child: expect.objectContaining({ id: '1' }), startMs: 0 });
  });

  it('keeps the listing order of unnumbered audiobook chapters', () => {
    const pick = pickContainerStart([
      child('intro', { type: 'audiobook_chapter', watched: true }),
      child('part-1', { type: 'audiobook_chapter' }),
      child('part-2', { type: 'audiobook_chapter' }),
    ]);
    expect(pick?.child.id).toBe('part-1');
  });

  it('is null for an empty container', () => {
    expect(pickContainerStart([])).toBeNull();
  });
});

describe('playAllAlbumOrder', () => {
  it('orders albums by year (undated last), then index, and drops non-albums', () => {
    const order = playAllAlbumOrder([
      child('undated', { type: 'album' }),
      child('1999b', { type: 'album', year: 1999, index: 2 }),
      child('video', { type: 'music_video', year: 1960 }),
      child('1999a', { type: 'album', year: 1999, index: 1 }),
      child('1965', { type: 'album', year: 1965 }),
    ]);
    expect(order.map((a) => a.id)).toEqual(['1965', '1999a', '1999b', 'undated']);
  });
});

describe('firstTrack', () => {
  it('is the first numbered track by (disc, track)', () => {
    expect(
      firstTrack([
        child('bonus'),
        child('d2t1', { disc_number: 2, index: 1 }),
        child('d1t3', { disc_number: 1, index: 3 }),
        child('d1t2', { index: 2 }),
      ])?.id,
    ).toBe('d1t2');
  });

  it('is null when no track carries a number', () => {
    expect(firstTrack([child('a'), child('b', { type: 'music_video', index: 1 })])).toBeNull();
  });
});

describe('resolvePlayAll', () => {
  it('starts the first track of the chronologically first album', async () => {
    const albums: Record<string, ChildItem[]> = {
      revolver: [child('taxman', { index: 1 }), child('eleanor', { index: 2 })],
      please: [child('i-saw-her', { index: 1 }), child('misery', { index: 2 })],
    };
    const fetch = vi.fn(async (id: string) => albums[id] ?? []);
    const start = await resolvePlayAll(
      [
        child('revolver', { type: 'album', year: 1966 }),
        child('please', { type: 'album', year: 1963 }),
      ],
      fetch,
    );
    expect(start?.id).toBe('i-saw-her');
    // Only the album it started from was fetched.
    expect(fetch).toHaveBeenCalledTimes(1);
    expect(fetch).toHaveBeenCalledWith('please');
  });

  it('moves on past an album without numbered tracks', async () => {
    const fetch = vi.fn(async (id: string) =>
      id === 'early' ? [child('untagged')] : [child('first', { index: 1 })],
    );
    const start = await resolvePlayAll(
      [child('late', { type: 'album', year: 2000 }), child('early', { type: 'album', year: 1990 })],
      fetch,
    );
    expect(start?.id).toBe('first');
    expect(fetch.mock.calls.map((c) => c[0])).toEqual(['early', 'late']);
  });

  it('is null when the artist has no albums', async () => {
    const fetch = vi.fn(async () => [] as ChildItem[]);
    expect(await resolvePlayAll([child('mv', { type: 'music_video' })], fetch)).toBeNull();
    expect(fetch).not.toHaveBeenCalled();
  });
});

describe('shuffle', () => {
  it('shuffled is a permutation and deterministic for a seed', () => {
    const list = ['a', 'b', 'c', 'd', 'e', 'f'];
    const one = shuffled(list, seeded(7));
    expect([...one].sort()).toEqual(list);
    expect(shuffled(list, seeded(7))).toEqual(one);
    expect(list).toEqual(['a', 'b', 'c', 'd', 'e', 'f']);
  });

  it('pickRandom stays in range even for rng() at its upper edge', () => {
    expect(pickRandom(['a', 'b'], () => 0.9999999999)).toBe('b');
    expect(pickRandom(['a', 'b'], () => 0)).toBe('a');
    expect(pickRandom([], () => 0.5)).toBeNull();
  });

  it('resolveShuffle picks a seeded random track from a random album', async () => {
    const tracks: Record<string, ChildItem[]> = {
      a1: [child('a1-1', { index: 1 }), child('a1-2', { index: 2 })],
      a2: [child('a2-1', { index: 1 }), child('a2-2', { index: 2 }), child('a2-3', { index: 3 })],
    };
    const artist = [child('a1', { type: 'album' }), child('a2', { type: 'album' })];
    const fetch = vi.fn(async (id: string) => tracks[id] ?? []);
    const first = await resolveShuffle(artist, fetch, seeded(42));
    expect(first).not.toBeNull();
    expect([...tracks.a1, ...tracks.a2].map((t) => t.id)).toContain(first!.id);
    // Same seed, same pick; one album fetched.
    expect((await resolveShuffle(artist, vi.fn(async (id: string) => tracks[id] ?? []), seeded(42)))?.id).toBe(first!.id);
    expect(fetch).toHaveBeenCalledTimes(1);
  });

  it('resolveShuffle reaches every track over many seeds', async () => {
    const tracks: Record<string, ChildItem[]> = {
      a1: [child('x')],
      a2: [child('y'), child('z')],
    };
    const artist = [child('a1', { type: 'album' }), child('a2', { type: 'album' })];
    const seen = new Set<string>();
    for (let s = 1; s <= 60; s++) {
      const pick = await resolveShuffle(artist, async (id) => tracks[id] ?? [], seeded(s));
      if (pick) seen.add(pick.id);
    }
    expect([...seen].sort()).toEqual(['x', 'y', 'z']);
  });

  it('resolveShuffle skips an album with no tracks and ignores non-tracks', async () => {
    const tracks: Record<string, ChildItem[]> = {
      empty: [child('clip', { type: 'music_video' })],
      full: [child('only')],
    };
    // rng 0.99 leaves [empty, full] in order, so the empty album is tried first.
    const start = await resolveShuffle(
      [child('empty', { type: 'album' }), child('full', { type: 'album' })],
      async (id) => tracks[id] ?? [],
      () => 0.99,
    );
    expect(start?.id).toBe('only');
  });

  it('resolveShuffle is null when nothing is playable', async () => {
    expect(await resolveShuffle([child('mv', { type: 'music_video' })], async () => [], seeded(1))).toBeNull();
  });
});
