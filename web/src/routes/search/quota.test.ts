import { describe, expect, it } from 'vitest';
import type { RequestQuota } from '$lib/api';
import { isRequestsDisabledError, quotaLines, windowPhrase, REQUESTS_DISABLED_MESSAGE } from './quota';

const q = (over: Partial<RequestQuota> = {}): RequestQuota => ({
  can_request: true,
  window_days: 7,
  movies: { limit: 0, used: 0, remaining: null },
  tv: { limit: 0, used: 0, remaining: null },
  ...over,
});

describe('windowPhrase', () => {
  it('names the common windows and falls back to the day count', () => {
    expect(windowPhrase(1)).toBe('today');
    expect(windowPhrase(7)).toBe('this week');
    expect(windowPhrase(30)).toBe('this month');
    expect(windowPhrase(14)).toBe('in this 14-day period');
  });
});

describe('quotaLines', () => {
  it('is empty for an unknown or unlimited allowance', () => {
    expect(quotaLines(null)).toEqual([]);
    expect(quotaLines(undefined)).toEqual([]);
    expect(quotaLines(q())).toEqual([]);
  });

  it('counts down per limited type with singular/plural', () => {
    expect(quotaLines(q({ movies: { limit: 5, used: 1, remaining: 4 } }))).toEqual(['4 movie requests left this week']);
    expect(quotaLines(q({ tv: { limit: 2, used: 1, remaining: 1 }, window_days: 1 }))).toEqual(['1 TV request left today']);
  });

  it('says a used-up type now needs approval', () => {
    expect(quotaLines(q({ movies: { limit: 2, used: 3, remaining: 0 }, window_days: 30 }))).toEqual([
      'No movie requests left this month — new ones need admin approval',
    ]);
  });

  it('explains a disabled account instead of counting', () => {
    expect(quotaLines(q({ can_request: false, movies: { limit: 5, used: 0, remaining: 5 } }))).toEqual([
      REQUESTS_DISABLED_MESSAGE,
    ]);
  });
});

describe('isRequestsDisabledError', () => {
  it('matches only the REQUESTS_DISABLED code', () => {
    expect(isRequestsDisabledError(Object.assign(new Error('x'), { code: 'REQUESTS_DISABLED' }))).toBe(true);
    expect(isRequestsDisabledError(Object.assign(new Error('x'), { code: 'FORBIDDEN' }))).toBe(false);
    expect(isRequestsDisabledError(new Error('REQUESTS_DISABLED'))).toBe(false);
    expect(isRequestsDisabledError(null)).toBe(false);
  });
});
