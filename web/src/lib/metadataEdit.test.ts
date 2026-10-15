import type { ItemDetail } from './api';
import {
  buildMetadataEdit,
  changedFields,
  editErrorMessage,
  formFromItem,
  lockState,
  lockedFields,
  parseGenres,
  validateForm,
  type LockableField,
} from './metadataEdit';

function movie(over: Partial<ItemDetail> = {}): ItemDetail {
  return {
    id: 'm1',
    library_id: 'l1',
    title: 'Heat',
    type: 'movie',
    year: 1995,
    summary: 'A heist.',
    rating: 8.3,
    content_rating: 'R',
    genres: ['Crime', 'Drama'],
    taken_at: '1995-12-15T00:00:00Z',
    sort_title: 'Heat',
    view_offset_ms: 0,
    updated_at: 0,
    is_favorite: false,
    files: [],
    ...over,
  };
}

describe('formFromItem', () => {
  it('fills every field and leaves a sort title that matches the title blank', () => {
    const f = formFromItem(movie({ tagline: 'A Los Angeles crime saga', original_title: 'Heat' }));
    expect(f).toEqual({
      title: 'Heat',
      sort_title: '',
      original_title: 'Heat',
      year: '1995',
      originally_available_at: '1995-12-15',
      summary: 'A heist.',
      tagline: 'A Los Angeles crime saga',
      rating: '8.3',
      content_rating: 'R',
      genres: 'Crime, Drama',
    });
    expect(formFromItem(movie({ title: 'The Thing', sort_title: 'Thing' })).sort_title).toBe('Thing');
  });

  it('copes with a sparse item', () => {
    const f = formFromItem(movie({ year: undefined, rating: undefined, genres: [], taken_at: undefined, summary: undefined }));
    expect([f.year, f.rating, f.genres, f.originally_available_at, f.summary]).toEqual(['', '', '', '', '']);
  });
});

describe('parseGenres', () => {
  it('trims, drops blanks and case-insensitive repeats, keeps order', () => {
    expect(parseGenres(' Sci-Fi, ,noir, sci-fi ,Noir,Drama')).toEqual(['Sci-Fi', 'noir', 'Drama']);
    expect(parseGenres('')).toEqual([]);
  });
});

describe('changedFields / buildMetadataEdit', () => {
  it('sends nothing when nothing changed', () => {
    const f = formFromItem(movie());
    expect(changedFields(f, { ...f })).toEqual([]);
    expect(buildMetadataEdit(f, { ...f }, new Set())).toEqual({});
  });

  it('sends only changed fields, typed for the server', () => {
    const initial = formFromItem(movie());
    const form = {
      ...initial,
      title: ' Heat (Director’s Cut) ',
      year: '',
      rating: '9',
      genres: 'Crime, Thriller',
      originally_available_at: '',
      content_rating: 'NC-17',
    };
    expect(buildMetadataEdit(initial, form, new Set())).toEqual({
      title: 'Heat (Director’s Cut)',
      year: null,
      taken_at: '',
      content_rating: 'NC-17',
      rating: 9,
      genres: ['Crime', 'Thriller'],
    });
  });

  it('treats the same genres with different spacing as unchanged', () => {
    const initial = formFromItem(movie());
    expect(changedFields(initial, { ...initial, genres: 'Crime,Drama ' })).toEqual([]);
  });

  it('adds unlocks, but an edited field is edited rather than unlocked', () => {
    const initial = formFromItem(movie());
    const unlock = new Set<LockableField>(['summary', 'genres']);
    const body = buildMetadataEdit(initial, { ...initial, summary: 'New.' }, unlock);
    expect(body).toEqual({ summary: 'New.', unlock_fields: ['genres'] });
  });

  it('sends a cleared sort title so the server resets it to the title', () => {
    const initial = formFromItem(movie({ title: 'The Thing', sort_title: 'Thing' }));
    expect(buildMetadataEdit(initial, { ...initial, sort_title: '' }, new Set())).toEqual({ sort_title: '' });
  });
});

describe('validateForm', () => {
  it('needs a title, a four-digit year and a 0-10 rating', () => {
    const f = formFromItem(movie());
    expect(validateForm(f)).toBe('');
    expect(validateForm({ ...f, title: '  ' })).toMatch(/Title/);
    expect(validateForm({ ...f, year: '95' })).toMatch(/four-digit/);
    expect(validateForm({ ...f, rating: '11' })).toMatch(/0 and 10/);
    expect(validateForm({ ...f, rating: 'abc' })).toMatch(/0 and 10/);
    expect(validateForm({ ...f, year: '', rating: '' })).toBe('');
  });
});

describe('lockState / lockedFields', () => {
  it('reports locked, unlocking, locking and open', () => {
    const locked = lockedFields({ locked_fields: ['title', 'genres', 'tags'] });
    expect([...locked]).toEqual(['title', 'genres']); // unknown names ignored
    const unlock = new Set<LockableField>(['genres']);
    expect(lockState('title', locked, unlock, [])).toBe('locked');
    expect(lockState('genres', locked, unlock, [])).toBe('unlocking');
    expect(lockState('summary', locked, unlock, ['summary'])).toBe('locking');
    expect(lockState('year', locked, unlock, [])).toBe('open');
  });

  it('treats a missing locked_fields (older server) as no locks', () => {
    expect(lockedFields({}).size).toBe(0);
  });
});

describe('editErrorMessage', () => {
  it('explains a title + year clash', () => {
    expect(editErrorMessage({ status: 409, message: 'x' })).toMatch(/already has this title and year/);
    expect(editErrorMessage({ status: 500, message: 'boom' })).toBe("Couldn't save: boom");
  });
});
