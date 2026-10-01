import { describe, expect, it } from 'vitest';
import type { ChildItem } from '../api/types';
import { chaptersInOrder, findSiblings, nextInContainer, resolveNext } from './siblings';

function kid(id: string, type: string, index?: number, extra: Partial<ChildItem> = {}): ChildItem {
  return { id, title: id, type, index, ...extra };
}

/** A tiny fake library: children by parent id, parents by id. Records every
 *  read so the tests can tell which ones a lookup needed. */
function fakeLibrary(children: Record<string, ChildItem[]>, parents: Record<string, string | undefined>) {
  const reads: string[] = [];
  return {
    reads,
    fetchChildren: async (id: string) => {
      reads.push(`children:${id}`);
      const c = children[id];
      if (!c) throw new Error(`404 ${id}`);
      return c;
    },
    fetchItem: async (id: string) => {
      reads.push(`item:${id}`);
      return { id, parent_id: parents[id] };
    },
  };
}

describe('resolveNext — episodes', () => {
  const lib = () =>
    fakeLibrary(
      {
        s1: [kid('e1', 'episode', 1), kid('e2', 'episode', 2), kid('e3', 'episode', 3), kid('e5', 'episode', 5)],
        s2: [kid('s2e2', 'episode', 2), kid('s2e1', 'episode', 1)],
        show: [kid('s2', 'season', 2), kid('s0', 'season', 0), kid('s1', 'season', 1)],
      },
      { s1: 'show', s2: 'show', s0: 'show' },
    );

  it('steps over a gap in the numbering', async () => {
    const l = lib();
    const next = await resolveNext({ id: 'e3', type: 'episode', parent_id: 's1', index: 3 }, l.fetchChildren, l.fetchItem);
    expect(next?.id).toBe('e5');
  });

  it("rolls the season's last episode into the next season's first", async () => {
    const l = lib();
    const next = await resolveNext({ id: 'e5', type: 'episode', parent_id: 's1', index: 5 }, l.fetchChildren, l.fetchItem);
    expect(next?.id).toBe('s2e1');
  });

  it('has nothing after the last season', async () => {
    const l = lib();
    const next = await resolveNext({ id: 's2e2', type: 'episode', parent_id: 's2', index: 2 }, l.fetchChildren, l.fetchItem);
    expect(next).toBeNull();
  });

  it("uses the caller's listing of the season instead of reading it again", async () => {
    const l = lib();
    const kids = [kid('e1', 'episode', 1), kid('e2', 'episode', 2)];
    const next = await resolveNext({ id: 'e1', type: 'episode', parent_id: 's1', index: 1 }, l.fetchChildren, l.fetchItem, kids);
    expect(next?.id).toBe('e2');
    expect(l.reads).toEqual([]);
  });

  it('is null (not a throw) when a read fails', async () => {
    const l = fakeLibrary({ s1: [kid('e1', 'episode', 1)] }, { s1: 'gone' });
    const next = await resolveNext({ id: 'e1', type: 'episode', parent_id: 's1', index: 1 }, l.fetchChildren, l.fetchItem);
    expect(next).toBeNull();
  });
});

describe('resolveNext — music', () => {
  const lib = () =>
    fakeLibrary(
      {
        albumB: [
          kid('b2-1', 'track', 1, { disc_number: 2 }),
          kid('b1-2', 'track', 2, { disc_number: 1 }),
          kid('b1-1', 'track', 1, { disc_number: 1 }),
        ],
        albumA: [kid('a1', 'track', 1), kid('a2', 'track', 2)],
        albumC: [kid('c2', 'track', 2), kid('c1', 'track', 1), kid('cx', 'track')],
        artist: [
          kid('albumC', 'album', undefined, { year: 2010 }),
          kid('albumB', 'album', undefined, { year: 1999 }),
          kid('albumA', 'album', undefined, { year: 1995 }),
        ],
      },
      { albumA: 'artist', albumB: 'artist', albumC: 'artist' },
    );

  it('crosses from the last track of disc 1 to disc 2', async () => {
    const l = lib();
    const next = await resolveNext({ id: 'b1-2', type: 'track', parent_id: 'albumB', index: 2 }, l.fetchChildren, l.fetchItem);
    expect(next?.id).toBe('b2-1');
  });

  it("goes on to the artist's next album by year at the album's end", async () => {
    const l = lib();
    const next = await resolveNext({ id: 'b2-1', type: 'track', parent_id: 'albumB', index: 1 }, l.fetchChildren, l.fetchItem);
    expect(next?.id).toBe('c1');
    expect(l.reads).toEqual(['children:albumB', 'item:albumB', 'children:artist', 'children:albumC']);
  });

  it("stops after the artist's last album", async () => {
    const l = lib();
    const next = await resolveNext({ id: 'c2', type: 'track', parent_id: 'albumC', index: 2 }, l.fetchChildren, l.fetchItem);
    expect(next).toBeNull();
  });

  it('has no next for an unnumbered track', async () => {
    const l = lib();
    const next = await resolveNext({ id: 'cx', type: 'track', parent_id: 'albumC' }, l.fetchChildren, l.fetchItem);
    expect(next).toBeNull();
  });
});

describe('resolveNext — audiobook chapters', () => {
  const book = [
    kid('intro', 'audiobook_chapter'),
    kid('ch2', 'audiobook_chapter', 2),
    kid('ch1', 'audiobook_chapter', 1),
    kid('afterword', 'audiobook_chapter'),
  ];
  const lib = () => fakeLibrary({ book: book, author: [kid('book', 'audiobook'), kid('book2', 'audiobook')] }, { book: 'author' });

  it('plays numbered chapters first, then the unnumbered ones in listing order', () => {
    expect(chaptersInOrder(book).map((k) => k.id)).toEqual(['ch1', 'ch2', 'intro', 'afterword']);
  });

  it('moves from the last numbered chapter to the first unnumbered one', async () => {
    const l = lib();
    const next = await resolveNext({ id: 'ch2', type: 'audiobook_chapter', parent_id: 'book', index: 2 }, l.fetchChildren, l.fetchItem);
    expect(next?.id).toBe('intro');
  });

  it('chains an unnumbered chapter (no index needed)', async () => {
    const l = lib();
    const next = await resolveNext({ id: 'intro', type: 'audiobook_chapter', parent_id: 'book' }, l.fetchChildren, l.fetchItem);
    expect(next?.id).toBe('afterword');
  });

  it("ends at the book's last chapter, never running into the next book", async () => {
    const l = lib();
    const next = await resolveNext({ id: 'afterword', type: 'audiobook_chapter', parent_id: 'book' }, l.fetchChildren, l.fetchItem);
    expect(next).toBeNull();
    expect(l.reads).toEqual(['children:book']);
  });
});

describe('resolveNext — other types', () => {
  it('plays the next podcast episode in the feed', async () => {
    const l = fakeLibrary({ pod: [kid('p1', 'podcast_episode', 1), kid('p3', 'podcast_episode', 3), kid('p2', 'podcast_episode', 2)] }, {});
    const next = await resolveNext({ id: 'p1', type: 'podcast_episode', parent_id: 'pod', index: 1 }, l.fetchChildren, l.fetchItem);
    expect(next?.id).toBe('p2');
  });

  it('steps a podcast over a missing episode (41 → 43), as Android does, and stops at the feed end', async () => {
    const l = fakeLibrary(
      { pod: [kid('e44', 'podcast_episode', 44), kid('e41', 'podcast_episode', 41), kid('e43', 'podcast_episode', 43)] },
      {},
    );
    const after41 = await resolveNext({ id: 'e41', type: 'podcast_episode', parent_id: 'pod', index: 41 }, l.fetchChildren, l.fetchItem);
    expect(after41?.id).toBe('e43');
    const after44 = await resolveNext({ id: 'e44', type: 'podcast_episode', parent_id: 'pod', index: 44 }, l.fetchChildren, l.fetchItem);
    expect(after44).toBeNull();
    // Never into another feed: only the feed's own listing was read.
    expect(l.reads.every((r) => r === 'children:pod')).toBe(true);
  });

  it('has nothing after a movie or an item without a parent', async () => {
    const l = fakeLibrary({ lib: [kid('m1', 'movie', 1), kid('m2', 'movie', 2)] }, {});
    expect(await resolveNext({ id: 'm1', type: 'movie', parent_id: 'lib', index: 1 }, l.fetchChildren, l.fetchItem)).toBeNull();
    expect(await resolveNext({ id: 'm1', type: 'episode', index: 1 }, l.fetchChildren, l.fetchItem)).toBeNull();
  });
});

describe('nextInContainer / findSiblings', () => {
  it('takes the next greater index, not index + 1', () => {
    const kids = [kid('a', 'episode', 1), kid('c', 'episode', 4), kid('b', 'episode', 2)];
    expect(nextInContainer(kids, 'b', 'episode', 2)?.id).toBe('c');
    expect(nextInContainer(kids, 'c', 'episode', 4)).toBeNull();
  });

  it('still finds the previous track and the track count', () => {
    const kids = [kid('t2', 'track', 2), kid('t1', 'track', 1), kid('t3', 'track', 3)];
    const s = findSiblings(kids, { id: 't2', type: 'track', index: 2 });
    expect(s.prev?.id).toBe('t1');
    expect(s.next?.id).toBe('t3');
    expect([s.position, s.total]).toEqual([2, 3]);
  });
});
