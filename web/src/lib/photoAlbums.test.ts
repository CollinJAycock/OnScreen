import type { PhotoAlbum } from './api';
import {
  bulkMessage,
  normalizeAlbumName,
  photoCountLabel,
  runBulk,
  toggleSelected,
  withCreated,
  withRenamed,
  withoutAlbum,
} from './photoAlbums';

const album = (id: string, name: string, item_count = 0): PhotoAlbum => ({
  id, name, item_count, created_at: '2024-01-01T00:00:00Z', updated_at: '2024-01-01T00:00:00Z',
});

describe('normalizeAlbumName', () => {
  it('trims, and rejects a blank name', () => {
    expect(normalizeAlbumName('  Trip  ')).toBe('Trip');
    expect(normalizeAlbumName('   ')).toBeNull();
    expect(normalizeAlbumName('')).toBeNull();
  });
});

describe('photoCountLabel', () => {
  it('pluralizes', () => {
    expect(photoCountLabel(0)).toBe('0 photos');
    expect(photoCountLabel(1)).toBe('1 photo');
    expect(photoCountLabel(2)).toBe('2 photos');
  });
});

describe('runBulk', () => {
  it('runs each id once, in order, one at a time', async () => {
    const seen: string[] = [];
    let inFlight = 0;
    let maxInFlight = 0;
    const r = await runBulk(['a', 'b', 'a', 'c'], async (id) => {
      inFlight++;
      maxInFlight = Math.max(maxInFlight, inFlight);
      await Promise.resolve();
      seen.push(id);
      inFlight--;
    });
    expect(seen).toEqual(['a', 'b', 'c']);
    expect(maxInFlight).toBe(1);
    expect(r).toEqual({ done: 3, failed: 0, firstError: '' });
  });

  it('keeps going past a failure and reports the first error', async () => {
    const r = await runBulk(['a', 'b', 'c'], async (id) => {
      if (id === 'b') throw new Error('HTTP 500');
      if (id === 'c') throw new Error('later');
    });
    expect(r).toEqual({ done: 1, failed: 2, firstError: 'HTTP 500' });
  });

  it('reports progress after each id', async () => {
    const progress: string[] = [];
    await runBulk(['a', 'b'], async () => {}, (n, total) => progress.push(`${n}/${total}`));
    expect(progress).toEqual(['1/2', '2/2']);
  });
});

describe('bulkMessage', () => {
  it('reports a full success', () => {
    expect(bulkMessage('add', { done: 1, failed: 0, firstError: '' }, 'Trip')).toEqual({ ok: true, message: 'Added to "Trip"' });
    expect(bulkMessage('add', { done: 3, failed: 0, firstError: '' }, 'Trip')).toEqual({ ok: true, message: 'Added 3 photos to "Trip"' });
    expect(bulkMessage('remove', { done: 2, failed: 0, firstError: '' }, 'Trip')).toEqual({ ok: true, message: 'Removed 2 photos from "Trip"' });
  });

  it('reports a partial failure as an error with the reason', () => {
    const m = bulkMessage('add', { done: 2, failed: 1, firstError: 'HTTP 500' }, 'Trip');
    expect(m.ok).toBe(false);
    expect(m.message).toBe('Added 2 of 3 photos to "Trip"; 1 failed: HTTP 500');
  });

  it('reports a total failure', () => {
    expect(bulkMessage('add', { done: 0, failed: 1, firstError: 'Not found' }, 'Trip')).toEqual({
      ok: false, message: 'Couldn\'t add the photo to "Trip": Not found',
    });
    expect(bulkMessage('remove', { done: 0, failed: 2, firstError: '' }, 'Trip').message)
      .toBe('Couldn\'t remove the photos from "Trip"');
  });
});

describe('album list updates', () => {
  const list = [album('a', 'Alps', 4), album('b', 'Beach', 9)];

  it('puts a new album first with a zero count', () => {
    const created = { ...album('c', 'City'), item_count: undefined as unknown as number };
    const next = withCreated(list, created);
    expect(next.map((a) => a.id)).toEqual(['c', 'a', 'b']);
    expect(next[0].item_count).toBe(0);
  });

  it('renames in place and keeps the count and cover', () => {
    const withCover = [{ ...list[0], cover_path: 'p/1.jpg' }, list[1]];
    const next = withRenamed(withCover, { id: 'a', name: 'Alps 2024', updated_at: '2024-02-02T00:00:00Z' });
    expect(next[0]).toMatchObject({ id: 'a', name: 'Alps 2024', item_count: 4, cover_path: 'p/1.jpg', updated_at: '2024-02-02T00:00:00Z' });
    expect(next[1]).toBe(withCover[1]);
  });

  it('removes a deleted album', () => {
    expect(withoutAlbum(list, 'a').map((a) => a.id)).toEqual(['b']);
  });
});

describe('toggleSelected', () => {
  it('adds and removes, returning a new set', () => {
    const empty = new Set<string>();
    const one = toggleSelected(empty, 'a');
    expect(one).not.toBe(empty);
    expect([...one]).toEqual(['a']);
    expect(empty.size).toBe(0);
    expect([...toggleSelected(one, 'a')]).toEqual([]);
  });
});
