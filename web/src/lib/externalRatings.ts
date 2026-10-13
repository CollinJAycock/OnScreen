// Display helpers for a title's IMDb / Rotten Tomatoes / Metacritic scores
// (ItemDetail.external_ratings).

import type { ExternalRatings } from './api';

export interface RatingChip {
  key: 'imdb' | 'rt' | 'metacritic';
  label: string;
  title: string;
  href?: string;
}

/** The chips to show, in IMDb, Rotten Tomatoes, Metacritic order; only the
 *  scores the title has. */
export function externalRatingChips(r: ExternalRatings | undefined): RatingChip[] {
  if (!r) return [];
  const out: RatingChip[] = [];
  if (r.imdb_rating != null) {
    const votes = r.imdb_votes != null ? ` from ${r.imdb_votes.toLocaleString()} votes` : '';
    out.push({
      key: 'imdb',
      label: `IMDb ${r.imdb_rating.toFixed(1)}`,
      title: `IMDb rating: ${r.imdb_rating.toFixed(1)}/10${votes}`,
      href: /^tt\d+$/.test(r.imdb_id) ? `https://www.imdb.com/title/${r.imdb_id}/` : undefined
    });
  }
  if (r.rotten_tomatoes != null) {
    out.push({ key: 'rt', label: `RT ${r.rotten_tomatoes}%`, title: `Rotten Tomatoes critics: ${r.rotten_tomatoes}%` });
  }
  if (r.metacritic != null) {
    out.push({ key: 'metacritic', label: `Metacritic ${r.metacritic}`, title: `Metacritic: ${r.metacritic}/100` });
  }
  return out;
}
