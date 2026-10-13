import { describe, it, expect } from 'vitest';
import { externalRatingChips } from './externalRatings';

describe('externalRatingChips', () => {
  it('shows the scores a title has, in order', () => {
    const chips = externalRatingChips({ imdb_id: 'tt0111161', imdb_rating: 9.3, imdb_votes: 3012345, rotten_tomatoes: 89, metacritic: 82 });
    expect(chips.map((c) => c.label)).toEqual(['IMDb 9.3', 'RT 89%', 'Metacritic 82']);
    expect(chips[0].href).toBe('https://www.imdb.com/title/tt0111161/');
    expect(chips[0].title).toContain((3012345).toLocaleString());
  });

  it('leaves out what is missing', () => {
    expect(externalRatingChips(undefined)).toEqual([]);
    expect(externalRatingChips({ imdb_id: 'tt1', rotten_tomatoes: 0 }).map((c) => c.label)).toEqual(['RT 0%']);
    const imdbOnly = externalRatingChips({ imdb_id: 'tt1', imdb_rating: 7 });
    expect(imdbOnly.map((c) => c.label)).toEqual(['IMDb 7.0']);
    expect(imdbOnly[0].title).toBe('IMDb rating: 7.0/10');
  });

  it('links only real IMDb IDs', () => {
    expect(externalRatingChips({ imdb_id: 'javascript:alert(1)', imdb_rating: 5 })[0].href).toBeUndefined();
  });
});
