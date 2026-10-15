import { afterEach, describe, expect, it } from 'vitest';
import { clearQueue, queueNeighbours, setQueue } from './queue';

const t = (id: string) => ({ id, title: `Song ${id}`, type: 'track' });

afterEach(() => clearQueue());

describe('play queue', () => {
  it('steps through the queue in its own order, Track N of M', () => {
    setQueue([t('c'), t('a'), t('b')]);
    expect(queueNeighbours('c')).toEqual({ prev: null, next: t('a'), position: 1, total: 3 });
    const mid = queueNeighbours('a');
    expect(mid?.prev?.id).toBe('c');
    expect(mid?.next?.id).toBe('b');
    expect([mid?.position, mid?.total]).toEqual([2, 3]);
  });

  it('ends at its last track (no running on into the album)', () => {
    setQueue([t('a'), t('b')]);
    expect(queueNeighbours('b')?.next).toBeNull();
  });

  it("is null for a track that isn't queued, and after it is cleared", () => {
    setQueue([t('a')]);
    expect(queueNeighbours('z')).toBeNull();
    clearQueue();
    expect(queueNeighbours('a')).toBeNull();
  });

  it('keeps a repeated track at its first place', () => {
    setQueue([t('a'), t('b'), t('a'), t('c')]);
    expect(queueNeighbours('b')?.next?.id).toBe('c');
    expect(queueNeighbours('c')?.total).toBe(3);
  });
});
