import { describe, expect, it } from 'vitest';
import {
  episodeHeading,
  episodeNumberLabel,
  episodeShortCode,
  lastEpisode,
  nextEpisode,
  previousEpisode
} from './episodeRange';

describe('episode range labels', () => {
  it('shows a single episode as before', () => {
    expect(episodeNumberLabel(3)).toBe('3');
    expect(episodeHeading(3)).toBe('Episode 3');
    expect(episodeShortCode(3)).toBe('E3');
    expect(lastEpisode(3)).toBe(3);
  });

  it('shows a multi-episode file as a range', () => {
    expect(episodeNumberLabel(1, 2)).toBe('1–2');
    expect(episodeHeading(1, 2)).toBe('Episodes 1–2');
    expect(episodeShortCode(1, 3)).toBe('E1–E3');
    expect(lastEpisode(1, 2)).toBe(2);
  });

  it('ignores an end that is not past the start', () => {
    expect(episodeNumberLabel(4, 4)).toBe('4');
    expect(episodeHeading(4, 2)).toBe('Episode 4');
  });

  it('is empty without a number', () => {
    expect(episodeNumberLabel(undefined)).toBe('');
    expect(episodeHeading(null, 2)).toBe('');
    expect(episodeShortCode(undefined)).toBe('');
    expect(lastEpisode(undefined, 3)).toBeNull();
  });
});

describe('stepping between episodes', () => {
  const e12 = { id: 'a', index: 1, index_end: 2 };
  const e3 = { id: 'b', index: 3 };
  const e4 = { id: 'c', index: 4 };
  const e6 = { id: 'd', index: 6 };
  const season = [e12, e3, e4, e6];

  it('plays E03 after an E01–E02 file', () => {
    expect(nextEpisode(season, e12)).toBe(e3);
    expect(nextEpisode(season, e3)).toBe(e4);
  });

  it('stops at a gap and at the end, as for single episodes', () => {
    expect(nextEpisode(season, e4)).toBeNull();
    expect(nextEpisode(season, e6)).toBeNull();
  });

  it('goes back from E03 to the E01–E02 file', () => {
    expect(previousEpisode(season, e3)).toBe(e12);
    expect(previousEpisode(season, e4)).toBe(e3);
    expect(previousEpisode(season, e12)).toBeNull();
    expect(previousEpisode(season, e6)).toBeNull();
  });

  it('works on a server that sends no index_end', () => {
    const old = [{ index: 1 }, { index: 2 }];
    expect(nextEpisode(old, old[0])).toBe(old[1]);
    expect(previousEpisode(old, old[1])).toBe(old[0]);
  });
});
