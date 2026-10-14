import { describe, it, expect, vi, beforeEach } from 'vitest';

const { get, put, post } = vi.hoisted(() => ({ get: vi.fn(), put: vi.fn(), post: vi.fn() }));
vi.mock('./api', () => ({
  api: { get, put, post },
  assetUrl: (p: string) => p,
}));

import {
  userStatsApi, digestApi, fmtDuration, fmtHours, heatGrid, pctChange, foldTypes,
  reviewYears, parseYearParam, artworkSrc, monthLabel,
} from './userStats';

describe('userStatsApi', () => {
  beforeEach(() => { get.mockReset(); put.mockReset(); post.mockReset(); });

  it('sends the range and the browser time zone', () => {
    userStatsApi.get('year:2025');
    const url = get.mock.calls[0][0] as string;
    expect(url.startsWith('/users/me/stats?')).toBe(true);
    const q = new URLSearchParams(url.split('?')[1]);
    expect(q.get('range')).toBe('year:2025');
    expect(q.get('tz')).toBeTruthy();
  });

  it('asks for a year in review by year', () => {
    userStatsApi.yearInReview(2024);
    expect(get.mock.calls[0][0]).toMatch(/^\/users\/me\/year-in-review\?year=2024&tz=/);
  });

  it('digest calls hit the user and admin endpoints', () => {
    digestApi.setMine('weekly');
    expect(put).toHaveBeenCalledWith('/users/me/digest', { frequency: 'weekly' });
    digestApi.sendTest();
    expect(post).toHaveBeenCalledWith('/settings/digest/test', {});
  });
});

describe('formatting helpers', () => {
  it('formats durations', () => {
    expect(fmtDuration(0)).toBe('0 min');
    expect(fmtDuration(45 * 60_000)).toBe('45 min');
    expect(fmtDuration(90 * 60_000)).toBe('1.5 h');
    expect(fmtDuration(250 * 3_600_000)).toBe('250 h');
    expect(fmtHours(12.46)).toBe('12.5');
    expect(fmtHours(1204.4)).toBe((1204).toLocaleString());
  });

  it('labels months', () => {
    expect(monthLabel('2026-03')).toMatch(/Mar/);
    expect(monthLabel('2026-03', true)).toMatch(/2026/);
    expect(monthLabel('bad')).toBe('bad');
  });

  it('expands the sparse heatmap into 7×24', () => {
    const { grid, max } = heatGrid([
      { weekday: 1, hour: 20, plays: 3 },
      { weekday: 7, hour: 0, plays: 5 },
      { weekday: 9, hour: 1, plays: 99 }, // out of range: ignored
    ]);
    expect(grid).toHaveLength(7);
    expect(grid[0]).toHaveLength(24);
    expect(grid[0][20]).toBe(3);
    expect(grid[6][0]).toBe(5);
    expect(max).toBe(5);
  });

  it('computes year-over-year change only with a previous year', () => {
    expect(pctChange(120, 100)).toBe(20);
    expect(pctChange(50, 100)).toBe(-50);
    expect(pctChange(10, 0)).toBeNull();
  });

  it('folds audiobook chapters into audiobooks and sorts by time', () => {
    const out = foldTypes([
      { type: 'track', plays: 10, ms: 100 },
      { type: 'audiobook', plays: 1, ms: 300 },
      { type: 'audiobook_chapter', plays: 2, ms: 200 },
    ]);
    expect(out[0]).toEqual({ label: 'Audiobooks', plays: 3, ms: 500 });
    expect(out[1].label).toBe('Music');
  });

  it('lists review years and validates the route param', () => {
    const now = new Date(2026, 5, 1);
    expect(reviewYears(now, 3)).toEqual([2026, 2025, 2024]);
    expect(parseYearParam('2025', now)).toBe(2025);
    expect(parseYearParam('2027', now)).toBeNull();
    expect(parseYearParam('1999', now)).toBeNull();
    expect(parseYearParam('20x5', now)).toBeNull();
  });

  it('encodes artwork path segments but keeps the slashes', () => {
    expect(artworkSrc('Movies/Heat #2 (1995)/poster.jpg', 150))
      .toBe('/artwork/Movies/Heat%20%232%20(1995)/poster.jpg?w=150');
  });
});
