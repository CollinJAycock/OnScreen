import { describe, it, expect, vi } from 'vitest';
import {
  pollForItemChange,
  updatedSince,
  matchApplied,
  matchAppliedMsg,
  POLL_ATTEMPTS,
  POLL_INTERVAL_MS,
} from './itemRefresh';

type Row = { id: string; updated_at: string; tmdb_id?: number; title?: string };

const T0 = '2026-10-19T10:00:00Z';
const T1 = '2026-10-19T10:00:07.250Z';

function sequence(rows: Row[]) {
  let i = 0;
  return vi.fn(async () => rows[Math.min(i++, rows.length - 1)]);
}
const noSleep = vi.fn(async () => {});

describe('pollForItemChange', () => {
  it('returns the fresh row as soon as it changes', async () => {
    const fetch = sequence([
      { id: 'a', updated_at: T0 },
      { id: 'a', updated_at: T1 },
      { id: 'a', updated_at: T1 },
    ]);
    const got = await pollForItemChange(fetch, updatedSince(T0), { sleep: noSleep });
    expect(got?.updated_at).toBe(T1);
    expect(fetch).toHaveBeenCalledTimes(2);
  });

  it('returns null when the 24 s window passes with no change', async () => {
    const fetch = sequence([{ id: 'a', updated_at: T0 }]);
    const sleep = vi.fn(async () => {});
    const got = await pollForItemChange(fetch, updatedSince(T0), { sleep });
    expect(got).toBeNull();
    expect(fetch).toHaveBeenCalledTimes(POLL_ATTEMPTS);
    expect(sleep).toHaveBeenCalledWith(POLL_INTERVAL_MS);
    expect(POLL_ATTEMPTS * POLL_INTERVAL_MS).toBe(24_000);
  });

  it('waits on the real clock by default', async () => {
    vi.useFakeTimers();
    try {
      const fetch = sequence([{ id: 'a', updated_at: T1 }]);
      const p = pollForItemChange(fetch, updatedSince(T0));
      await vi.advanceTimersByTimeAsync(1999);
      expect(fetch).not.toHaveBeenCalled();
      await vi.advanceTimersByTimeAsync(1);
      expect((await p)?.updated_at).toBe(T1);
    } finally {
      vi.useRealTimers();
    }
  });

  it('stops when cancelled (user navigated away)', async () => {
    const fetch = sequence([{ id: 'a', updated_at: T0 }]);
    let n = 0;
    const got = await pollForItemChange(fetch, updatedSince(T0), {
      sleep: noSleep,
      cancelled: () => ++n > 3,
    });
    expect(got).toBeNull();
    expect(fetch.mock.calls.length).toBeLessThan(POLL_ATTEMPTS);
  });

  it('propagates a fetch error', async () => {
    const fetch = vi.fn(async () => {
      throw new Error('boom');
    });
    await expect(pollForItemChange(fetch, () => true, { sleep: noSleep })).rejects.toThrow('boom');
  });
});

describe('updatedSince', () => {
  it('compares times, not strings', () => {
    // String comparison would say "…:00.5Z" < "…:00Z" is false/true wrongly.
    expect(updatedSince('2026-10-19T10:00:00Z')({ updated_at: '2026-10-19T10:00:00.500Z' })).toBe(true);
    expect(updatedSince('2026-10-19T10:00:00.500Z')({ updated_at: '2026-10-19T10:00:00Z' })).toBe(false);
    expect(updatedSince(T0)({ updated_at: T0 })).toBe(false);
  });

  it('takes Unix ms, as GET /items/{id} returns', () => {
    expect(updatedSince(1_000)({ updated_at: 1_001 })).toBe(true);
    expect(updatedSince(1_000)({ updated_at: 1_000 })).toBe(false);
    expect(matchApplied(7, 1_000)({ tmdb_id: 7, updated_at: 2_000 })).toBe(true);
  });

  it('treats a different unparseable stamp as a change', () => {
    expect(updatedSince(undefined)({ updated_at: T0 })).toBe(true);
    expect(updatedSince('x')({ updated_at: 'x' })).toBe(false);
    expect(updatedSince('x')({ updated_at: undefined })).toBe(false);
  });
});

describe('matchApplied', () => {
  it('needs the chosen tmdb id and a rewrite', () => {
    const done = matchApplied(603, T0);
    expect(done({ tmdb_id: 603, updated_at: T1 })).toBe(true);
    expect(done({ tmdb_id: 603, updated_at: T0 })).toBe(false);
    expect(done({ tmdb_id: 604, updated_at: T1 })).toBe(false);
    expect(done({ updated_at: T1 })).toBe(false);
  });

  it('drives a Fix Match poll to the new row', async () => {
    const fetch = sequence([
      { id: 'a', updated_at: T0, tmdb_id: 1 },
      { id: 'a', updated_at: T1, tmdb_id: 603, title: 'The Matrix' },
    ]);
    const got = await pollForItemChange(fetch, matchApplied(603, T0), { sleep: noSleep });
    expect(got?.title).toBe('The Matrix');
    expect(matchAppliedMsg(got?.title)).toBe('Matched to “The Matrix”');
    expect(matchAppliedMsg(undefined)).toBe('Match applied');
  });
});
