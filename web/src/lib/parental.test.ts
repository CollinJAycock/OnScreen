import { describe, expect, it } from 'vitest';
import { isParentalLimitError, parentalBlockMessage } from './parental';

describe('parental watch limit text', () => {
  it('turns each reason code into a sentence', () => {
    expect(parentalBlockMessage('outside_allowed_hours')).toMatch(/allowed hours/);
    expect(parentalBlockMessage('daily_limit_reached')).toMatch(/limit/);
    // An unknown or empty reason still reads as a block, never the code.
    expect(parentalBlockMessage('something_new')).toMatch(/parental watch limit/);
    expect(parentalBlockMessage('')).toMatch(/parental watch limit/);
  });

  it('recognises the API error by its code', () => {
    expect(isParentalLimitError({ code: 'PARENTAL_LIMIT', message: 'daily_limit_reached' })).toBe(true);
    expect(isParentalLimitError({ code: 'PLAYBACK_STOPPED', message: 'x' })).toBe(false);
    expect(isParentalLimitError(new Error('PARENTAL_LIMIT'))).toBe(false);
    expect(isParentalLimitError(null)).toBe(false);
  });
});
