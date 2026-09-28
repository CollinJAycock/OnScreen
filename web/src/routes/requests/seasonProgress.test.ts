import type { MediaRequest } from '$lib/api';
import { isPartiallyAvailable, seasonsProgressLabel } from './seasonProgress';

type R = Pick<MediaRequest, 'type' | 'status' | 'seasons' | 'seasons_available'>;
const show = (over: Partial<R> = {}): R => ({ type: 'show', status: 'downloading', ...over });

describe('seasonsProgressLabel', () => {
  it('adds how many requested seasons are in once one has landed', () => {
    expect(seasonsProgressLabel(show({ seasons: [1, 2, 3], seasons_available: [1, 2] }))).toBe(
      'Seasons 1–3 (2 of 3 available)',
    );
    expect(seasonsProgressLabel(show({ seasons: [2], seasons_available: [] }))).toBe('Season 2');
    expect(seasonsProgressLabel(show({ seasons_available: [1, 2] }))).toBe('All seasons (2 available)');
  });

  it('is the plain label before anything lands, once available, and for old servers', () => {
    expect(seasonsProgressLabel(show({ seasons: [1, 2] }))).toBe('Seasons 1, 2');
    expect(seasonsProgressLabel(show({ status: 'available', seasons: [1, 2], seasons_available: [1, 2] }))).toBe(
      'Seasons 1, 2',
    );
    expect(seasonsProgressLabel(show({ status: 'pending', seasons: [1] }))).toBe('Season 1');
  });

  it('ignores recorded seasons the request did not ask for', () => {
    expect(seasonsProgressLabel(show({ seasons: [2, 3], seasons_available: [1, 3] }))).toBe(
      'Seasons 2, 3 (1 of 2 available)',
    );
  });
});

describe('isPartiallyAvailable', () => {
  it('is true for an in-flight TV request with a season in', () => {
    expect(isPartiallyAvailable(show({ seasons: [1, 2], seasons_available: [1] }))).toBe(true);
    expect(isPartiallyAvailable(show({ status: 'failed', seasons_available: [1] }))).toBe(true);
  });

  it('is false otherwise', () => {
    expect(isPartiallyAvailable(show({ seasons: [1, 2], seasons_available: [] }))).toBe(false);
    expect(isPartiallyAvailable(show({ status: 'available', seasons_available: [1] }))).toBe(false);
    expect(isPartiallyAvailable({ type: 'movie', status: 'downloading', seasons_available: [1] })).toBe(false);
    expect(isPartiallyAvailable(show())).toBe(false);
  });
});
