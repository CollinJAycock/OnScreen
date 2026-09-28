import { describe, expect, it } from 'vitest';
import {
  limitsSummary, parseDefaultQuota, parseUserQuota, parseWindowDays,
  quotaDefaultsFrom, quotaInputValue, quotaPlaceholder, windowHint,
} from './requestLimits';

describe('parseUserQuota', () => {
  it('maps blank to the server default and accepts 0..1000', () => {
    expect(parseUserQuota('', 'Movies')).toEqual({ ok: true, value: null });
    expect(parseUserQuota('  ', 'Movies')).toEqual({ ok: true, value: null });
    expect(parseUserQuota('0', 'Movies')).toEqual({ ok: true, value: 0 });
    expect(parseUserQuota(' 12 ', 'Movies')).toEqual({ ok: true, value: 12 });
    expect(parseUserQuota('1000', 'Movies')).toEqual({ ok: true, value: 1000 });
  });

  it('rejects negatives, fractions, junk and too-large values', () => {
    for (const bad of ['-1', '1.5', 'abc', '1001', '1e3']) {
      const r = parseUserQuota(bad, 'TV');
      expect(r.ok).toBe(false);
      if (!r.ok) expect(r.error).toMatch(/^TV: /);
    }
  });
});

describe('parseDefaultQuota / parseWindowDays', () => {
  it('treats a blank default as unlimited', () => {
    expect(parseDefaultQuota('', 'Movies')).toEqual({ ok: true, value: 0 });
    expect(parseDefaultQuota('4', 'Movies')).toEqual({ ok: true, value: 4 });
    expect(parseDefaultQuota('-4', 'Movies').ok).toBe(false);
  });

  it('bounds the window to 1..90 days', () => {
    expect(parseWindowDays('1')).toEqual({ ok: true, value: 1 });
    expect(parseWindowDays('90')).toEqual({ ok: true, value: 90 });
    for (const bad of ['0', '91', '', 'week']) expect(parseWindowDays(bad).ok).toBe(false);
  });
});

describe('display helpers', () => {
  it('normalises settings from older servers', () => {
    expect(quotaDefaultsFrom(undefined)).toEqual({ quota_movies: 0, quota_tv: 0, quota_window_days: 7 });
    expect(quotaDefaultsFrom({ quota_movies: 2, quota_tv: 1, quota_window_days: 14 }))
      .toEqual({ quota_movies: 2, quota_tv: 1, quota_window_days: 14 });
  });

  it('summarises a row', () => {
    expect(limitsSummary({ can_request: false, request_quota_movies: 3 })).toBe('Requests off');
    expect(limitsSummary({})).toBe('Default limits');
    expect(limitsSummary({ can_request: true, request_quota_movies: null, request_quota_tv: null })).toBe('Default limits');
    expect(limitsSummary({ request_quota_movies: 0, request_quota_tv: 2 })).toBe('Movies no limit · TV 2');
  });

  it('spells out blank/default and the window', () => {
    expect(quotaPlaceholder(0)).toBe('Default (no limit)');
    expect(quotaPlaceholder(5)).toBe('Default (5)');
    expect(quotaPlaceholder(undefined)).toBe('Default (no limit)');
    expect(quotaInputValue(null)).toBe('');
    expect(quotaInputValue(0)).toBe('0');
    expect(windowHint(7)).toMatch(/^Requests per week\./);
    expect(windowHint(1)).toMatch(/^Requests per day\./);
    expect(windowHint(30)).toMatch(/^Requests per 30 days\./);
  });
});
