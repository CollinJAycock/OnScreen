import { describe, it, expect, beforeEach, vi } from 'vitest';
import {
  decideStart,
  readLocalPosition,
  writeLocalPosition,
  pageFromLocator,
  pageProgression,
  describePosition,
  debounce,
  type LocalPosition,
} from './reading-sync';
import type { ReadingProgress } from '$lib/api';

const local = (locator: string, progression: number, updated_at: string): LocalPosition => ({
  locator, progression, updated_at,
});
const server = (locator: string, progression: number, updated_at: string, device_name = 'Phone'): ReadingProgress => ({
  locator, progression, updated_at, device_name,
});

describe('decideStart', () => {
  it('starts at the beginning with nothing saved', () => {
    expect(decideStart(null, null, false)).toEqual({ kind: 'none' });
  });

  it('opens at the server position when this browser has none', () => {
    const s = server('40', 0.4, '2026-10-14T10:00:00Z');
    expect(decideStart(null, s, false)).toEqual({ kind: 'open', position: s });
  });

  it('falls back to the local copy when the server has nothing', () => {
    const l = local('12', 0.12, '2026-10-14T10:00:00Z');
    expect(decideStart(l, null, false)).toEqual({ kind: 'open', position: l });
  });

  it('keeps the local position when it is the newer one', () => {
    const l = local('50', 0.5, '2026-10-14T11:00:00Z');
    const s = server('40', 0.4, '2026-10-14T10:00:00Z');
    expect(decideStart(l, s, false)).toEqual({ kind: 'open', position: l });
  });

  it('offers a newer server position that is meaningfully ahead', () => {
    const l = local('10', 0.1, '2026-10-14T09:00:00Z');
    const s = server('40', 0.4, '2026-10-14T10:00:00Z');
    expect(decideStart(l, s, false)).toEqual({ kind: 'offer', position: l, offer: s });
  });

  it('follows a newer server position that is behind or barely ahead', () => {
    const l = local('40', 0.4, '2026-10-14T09:00:00Z');
    const back = server('30', 0.3, '2026-10-14T10:00:00Z');
    expect(decideStart(l, back, false)).toEqual({ kind: 'open', position: back });
    const barely = server('epubcfi(/6/8)', 0.405, '2026-10-14T10:00:00Z');
    expect(decideStart(l, barely, false)).toEqual({ kind: 'open', position: barely });
  });

  it('respects an explicit ?p= and only offers a position ahead of it', () => {
    const s = server('40', 0.4, '2026-10-14T10:00:00Z');
    expect(decideStart(null, s, true)).toEqual({ kind: 'offer', position: null, offer: s });
    expect(decideStart(local('80', 0.8, '2026-10-14T09:00:00Z'), s, true)).toEqual({ kind: 'none' });
    expect(decideStart(null, null, true)).toEqual({ kind: 'none' });
  });
});

describe('local storage copy', () => {
  beforeEach(() => localStorage.clear());

  it('round-trips and rejects junk', () => {
    expect(readLocalPosition('b1')).toBeNull();
    writeLocalPosition('b1', local('7', 0.07, '2026-10-14T10:00:00Z'));
    expect(readLocalPosition('b1')).toEqual(local('7', 0.07, '2026-10-14T10:00:00Z'));
    localStorage.setItem('onscreen_reading_b2', '{not json');
    expect(readLocalPosition('b2')).toBeNull();
    localStorage.setItem('onscreen_reading_b3', '{"locator":7}');
    expect(readLocalPosition('b3')).toBeNull();
  });

  it('survives storage that throws', () => {
    const spy = vi.spyOn(Storage.prototype, 'setItem').mockImplementation(() => {
      throw new Error('quota');
    });
    expect(() => writeLocalPosition('b1', local('1', 0, 'x'))).not.toThrow();
    spy.mockRestore();
  });
});

describe('locators', () => {
  it('parses page locators and leaves CFIs alone', () => {
    expect(pageFromLocator('12')).toBe(12);
    expect(pageFromLocator('0')).toBeNull();
    expect(pageFromLocator('epubcfi(/6/4!/4/2)')).toBeNull();
  });

  it('computes page progression with the last page as finished', () => {
    expect(pageProgression(1, 100)).toBeCloseTo(0.01);
    expect(pageProgression(100, 100)).toBe(1);
    expect(pageProgression(5, 0)).toBe(0);
  });

  it('describes positions', () => {
    expect(describePosition({ locator: '40', progression: 0.4 })).toBe('page 40');
    expect(describePosition({ locator: 'epubcfi(/6/4)', progression: 0.423 })).toBe('42%');
  });
});

describe('debounce', () => {
  it('coalesces calls, flushes on demand and cancels', () => {
    vi.useFakeTimers();
    const fn = vi.fn();
    const d = debounce(fn, 1000);
    d.call(1);
    d.call(2);
    expect(d.pending).toBe(true);
    vi.advanceTimersByTime(999);
    expect(fn).not.toHaveBeenCalled();
    vi.advanceTimersByTime(1);
    expect(fn).toHaveBeenCalledTimes(1);
    expect(fn).toHaveBeenLastCalledWith(2);

    d.call(3);
    d.flush();
    expect(fn).toHaveBeenCalledTimes(2);
    expect(fn).toHaveBeenLastCalledWith(3);

    d.call(4);
    d.cancel();
    vi.advanceTimersByTime(5000);
    expect(fn).toHaveBeenCalledTimes(2);
    vi.useRealTimers();
  });
});
