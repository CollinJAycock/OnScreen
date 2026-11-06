// Display helpers for a title's IMDb / Rotten Tomatoes / Metacritic scores
// (ItemDetail.external_ratings).

import type { ExternalRatings } from './api';
import { t } from './i18n/index.svelte';

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
    out.push({
      key: 'imdb',
      label: `IMDb ${r.imdb_rating.toFixed(1)}`,
      title: r.imdb_votes != null
        ? t('player.ratings.imdbVotes', { rating: r.imdb_rating.toFixed(1), votes: r.imdb_votes })
        : t('player.ratings.imdb', { rating: r.imdb_rating.toFixed(1) }),
      href: /^tt\d+$/.test(r.imdb_id) ? `https://www.imdb.com/title/${r.imdb_id}/` : undefined
    });
  }
  if (r.rotten_tomatoes != null) {
    out.push({ key: 'rt', label: `RT ${r.rotten_tomatoes}%`, title: t('player.ratings.rottenTomatoes', { score: String(r.rotten_tomatoes) }) });
  }
  if (r.metacritic != null) {
    out.push({ key: 'metacritic', label: `Metacritic ${r.metacritic}`, title: t('player.ratings.metacritic', { score: String(r.metacritic) }) });
  }
  return out;
}
