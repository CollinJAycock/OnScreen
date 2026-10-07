// The smart-collection rules form: what the editor binds to, and the
// conversion to and from the CollectionRules the server stores.
import type { CollectionRules } from './api';

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

function whole(s: string, field: string, min: number, max: number): number | undefined {
  const t = s.trim();
  if (!t) return undefined;
  const n = Number(t);
  if (!Number.isInteger(n) || n < min || n > max) throw new Error(`${field} must be a whole number from ${min} to ${max}`);
  return n;
}

/** The rules a form describes; throws an Error with a readable message
 *  when the form is incomplete or out of range. */
export function formToRules(f: RulesForm): CollectionRules {
  const types: ('movie' | 'show')[] = [];
  if (f.movies) types.push('movie');
  if (f.shows) types.push('show');
  if (types.length === 0) throw new Error('Choose movies, shows or both');
  const rules: CollectionRules = { types };
  const genre = f.genre.trim();
  if (genre) rules.genres = [genre];
  const yearMin = whole(f.yearMin, 'From year', 1800, 2200);
  const yearMax = whole(f.yearMax, 'To year', 1800, 2200);
  if (yearMin != null && yearMax != null && yearMin > yearMax) throw new Error('From year is after To year');
  if (yearMin != null) rules.year_min = yearMin;
  if (yearMax != null) rules.year_max = yearMax;
  const rt = f.ratingMin.trim();
  if (rt) {
    const n = Number(rt);
    if (!Number.isFinite(n) || n < 0 || n > 10) throw new Error('Minimum rating must be from 0 to 10');
    rules.rating_min = n;
  }
  if (f.libraryIds.length) rules.library_ids = [...f.libraryIds];
  const limit = whole(f.limit, 'Limit', 1, 500);
  if (limit != null) rules.limit = limit;
  return rules;
}

/** One line describing rules, for the collection header. */
export function describeRules(r: CollectionRules): string {
  const kinds = r.types.length === 2 ? 'Movies and shows' : r.types[0] === 'show' ? 'Shows' : 'Movies';
  const parts = [r.genres?.[0] ? `${r.genres[0]} ${kinds.toLowerCase()}` : kinds];
  if (r.year_min != null && r.year_max != null) parts.push(`from ${r.year_min}–${r.year_max}`);
  else if (r.year_min != null) parts.push(`from ${r.year_min} on`);
  else if (r.year_max != null) parts.push(`up to ${r.year_max}`);
  if (r.rating_min != null) parts.push(`rated ${r.rating_min}+`);
  if (r.library_ids?.length) parts.push(`in ${r.library_ids.length} ${r.library_ids.length === 1 ? 'library' : 'libraries'}`);
  return parts.join(' ');
}
