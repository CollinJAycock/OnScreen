import { describe, expect, it } from 'vitest';
import {
  episodeNumber,
  formatRuntime,
  formatTimecode,
  genreLine,
  numberedTitle,
  resumeLabel,
  studioLine,
} from './detailText';

describe('formatTimecode / resumeLabel', () => {
  it('uses h:mm:ss past an hour and m:ss below it', () => {
    expect(formatTimecode(3_723_000)).toBe('1:02:03');
    expect(formatTimecode(245_000)).toBe('4:05');
    expect(formatTimecode(59_999)).toBe('0:59');
    expect(formatTimecode(36_000_000)).toBe('10:00:00');
  });

  it('clamps nonsense to zero', () => {
    expect(formatTimecode(-5)).toBe('0:00');
    expect(formatTimecode(Number.NaN)).toBe('0:00');
  });

  it('builds the Android resume label', () => {
    expect(resumeLabel(5_430_000)).toBe('Resume from 1:30:30');
  });
});

describe('formatRuntime', () => {
  it('shows hours and minutes from an hour up', () => {
    expect(formatRuntime(7_500_000)).toBe('2h 5m');
    expect(formatRuntime(3_600_000)).toBe('1h 0m');
  });

  it('shows minutes below an hour, rounded down', () => {
    expect(formatRuntime(2_759_000)).toBe('45m');
    expect(formatRuntime(60_000)).toBe('1m');
  });

  it('is empty under a minute or without a duration', () => {
    expect(formatRuntime(59_000)).toBe('');
    expect(formatRuntime(0)).toBe('');
    expect(formatRuntime(undefined)).toBe('');
    expect(formatRuntime(null)).toBe('');
    expect(formatRuntime(Number.POSITIVE_INFINITY)).toBe('');
  });
});

describe('genreLine', () => {
  it('joins up to three genres', () => {
    expect(genreLine(['Drama', 'Crime', 'Thriller', 'Mystery'])).toBe('Drama, Crime, Thriller');
    expect(genreLine(['Comedy'])).toBe('Comedy');
  });

  it('drops blanks and repeats', () => {
    expect(genreLine([' Drama ', '', 'Drama', 'Crime'])).toBe('Drama, Crime');
  });

  it('is empty without genres', () => {
    expect(genreLine([])).toBe('');
    expect(genreLine(undefined)).toBe('');
    expect(genreLine(null)).toBe('');
  });
});

describe('studioLine', () => {
  it('names up to two studios or networks, in the order given', () => {
    expect(studioLine([{ name: 'A24' }])).toBe('A24');
    expect(studioLine([{ name: 'HBO' }, { name: 'BBC' }, { name: 'Sky' }])).toBe('HBO, BBC');
  });

  it('drops blank and repeated names, and is empty without studios', () => {
    expect(studioLine([{ name: ' ' }, { name: 'HBO' }, { name: 'HBO' }])).toBe('HBO');
    expect(studioLine([])).toBe('');
    expect(studioLine(undefined)).toBe('');
    expect(studioLine(null)).toBe('');
  });
});

describe('episodeNumber / numberedTitle', () => {
  it('numbers a single episode and labels a multi-episode file as a range', () => {
    expect(episodeNumber(4)).toBe('4');
    expect(episodeNumber(1, 2)).toBe('1–2');
    expect(numberedTitle({ title: 'Pilot', index: 1, index_end: 2 })).toBe('1–2. Pilot');
    expect(numberedTitle({ title: 'Pilot', index: 4 })).toBe('4. Pilot');
  });

  it('ignores an index_end that is not past the index, and no number at all', () => {
    expect(episodeNumber(3, 3)).toBe('3');
    expect(episodeNumber(3, 2)).toBe('3');
    expect(episodeNumber(0, 2)).toBe('');
    expect(episodeNumber(undefined, 2)).toBe('');
    expect(numberedTitle({ title: 'Extras' })).toBe('Extras');
  });
});
