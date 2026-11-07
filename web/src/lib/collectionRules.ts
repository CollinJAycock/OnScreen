// The smart-collection rules form: what the editor binds to, and the
// conversion to and from the CollectionRules the server stores.
//
// Messages come from the i18n catalog; tx defaults to the current
// translator (a template passes $i18n so the text follows a language change).
import { get } from 'svelte/store';
import type { CollectionRules } from './api';
import { i18n, type Translator } from '$lib/i18n/index.svelte';

export interface RulesForm {
  movies: boolean;
  shows: boolean;
  genre: string;
  yearMin: string;
  yearMax: string;
  ratingMin: string;
  libraryIds: string[];
  limit: string;
}

export function emptyRulesForm(): RulesForm {
  return { movies: true, shows: false, genre: '', yearMin: '', yearMax: '', ratingMin: '', libraryIds: [], limit: '' };
}

export function rulesToForm(r: CollectionRules | null | undefined): RulesForm {
  if (!r) return emptyRulesForm();
  return {
    movies: r.types.includes('movie'),
    shows: r.types.includes('show'),
    genre: r.genres?.[0] ?? '',
    yearMin: r.year_min != null ? String(r.year_min) : '',
    yearMax: r.year_max != null ? String(r.year_max) : '',
    ratingMin: r.rating_min != null ? String(r.rating_min) : '',
    libraryIds: [...(r.library_ids ?? [])],
    limit: r.limit != null ? String(r.limit) : '',
  };
}

function whole(s: string, field: string, min: number, max: number, tx: Translator): number | undefined {
  const t = s.trim();
  if (!t) return undefined;
  const n = Number(t);
  if (!Number.isInteger(n) || n < min || n > max) {
    // Bounds are years and counts: written as-is, never "1,800".
    throw new Error(tx('lib.collectionRules.wholeNumber', { field, min: String(min), max: String(max) }));
  }
  return n;
}

/** The rules a form describes; throws an Error with a readable message
 *  when the form is incomplete or out of range. */
export function formToRules(f: RulesForm, tx: Translator = get(i18n)): CollectionRules {
  const types: ('movie' | 'show')[] = [];
  if (f.movies) types.push('movie');
  if (f.shows) types.push('show');
  if (types.length === 0) throw new Error(tx('lib.collectionRules.chooseTypes'));
  const rules: CollectionRules = { types };
  const genre = f.genre.trim();
  if (genre) rules.genres = [genre];
  const yearMin = whole(f.yearMin, tx('ui.rules.fromYear'), 1800, 2200, tx);
  const yearMax = whole(f.yearMax, tx('ui.rules.toYear'), 1800, 2200, tx);
  if (yearMin != null && yearMax != null && yearMin > yearMax) throw new Error(tx('lib.collectionRules.yearOrder'));
  if (yearMin != null) rules.year_min = yearMin;
  if (yearMax != null) rules.year_max = yearMax;
  const rt = f.ratingMin.trim();
  if (rt) {
    const n = Number(rt);
    if (!Number.isFinite(n) || n < 0 || n > 10) throw new Error(tx('lib.collectionRules.ratingRange'));
    rules.rating_min = n;
  }
  if (f.libraryIds.length) rules.library_ids = [...f.libraryIds];
  const limit = whole(f.limit, tx('ui.rules.limit'), 1, 500, tx);
  if (limit != null) rules.limit = limit;
  return rules;
}

/** One line describing rules, for the collection header: "Horror movies
 *  from 1980–1989 rated 7+ in 2 libraries". Each condition is its own
 *  message, joined by the language's separator (a space in English). */
export function describeRules(r: CollectionRules, tx: Translator = get(i18n)): string {
  const both = r.types.length === 2;
  const shows = !both && r.types[0] === 'show';
  const genre = r.genres?.[0];
  const parts = [
    genre
      ? tx(both ? 'lib.collectionRules.genreMoviesAndShows' : shows ? 'lib.collectionRules.genreShows' : 'lib.collectionRules.genreMovies', { genre })
      : tx(both ? 'lib.collectionRules.moviesAndShows' : shows ? 'ui.rules.shows' : 'ui.rules.movies'),
  ];
  // Years are written as-is, never "1,980".
  if (r.year_min != null && r.year_max != null) {
    parts.push(tx('lib.collectionRules.yearRange', { min: String(r.year_min), max: String(r.year_max) }));
  } else if (r.year_min != null) parts.push(tx('lib.collectionRules.yearFrom', { min: String(r.year_min) }));
  else if (r.year_max != null) parts.push(tx('lib.collectionRules.yearTo', { max: String(r.year_max) }));
  if (r.rating_min != null) parts.push(tx('lib.collectionRules.ratedAtLeast', { rating: r.rating_min }));
  if (r.library_ids?.length) parts.push(tx.n('lib.collectionRules.inLibraries', r.library_ids.length));
  return parts.join(tx('lib.collectionRules.separator'));
}
