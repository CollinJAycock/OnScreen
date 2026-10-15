import { describe, it, expect } from 'vitest';
import {
  applicableCount,
  buildBulkRequest,
  emptyBulkForm,
  skipReasonText,
  summarizeBulkResult,
  validateBulkForm,
} from './bulkEdit';

describe('buildBulkRequest', () => {
  it('returns null when every field is left unchanged', () => {
    expect(buildBulkRequest(['a'], emptyBulkForm())).toBeNull();
    const f = { ...emptyBulkForm(), ratingMode: 'set' as const, rating: 'PG' };
    expect(buildBulkRequest([], f)).toBeNull();
  });

  it('sets and clears the content rating', () => {
    const set = buildBulkRequest(['a', 'b'], { ...emptyBulkForm(), ratingMode: 'set', rating: ' PG-13 ' });
    expect(set).toEqual({ item_ids: ['a', 'b'], set: { content_rating: 'PG-13' } });
    const clear = buildBulkRequest(['a'], { ...emptyBulkForm(), ratingMode: 'clear' });
    expect(clear?.set).toEqual({ content_rating: null });
  });

  it('maps the genre modes', () => {
    const base = { ...emptyBulkForm(), genres: 'Drama, drama , Heist' };
    expect(buildBulkRequest(['a'], { ...base, genreMode: 'add' })?.add_genres).toEqual(['Drama', 'Heist']);
    expect(buildBulkRequest(['a'], { ...base, genreMode: 'remove' })?.remove_genres).toEqual(['Drama', 'Heist']);
    expect(buildBulkRequest(['a'], { ...base, genreMode: 'replace' })?.set).toEqual({ genres: ['Drama', 'Heist'] });
    // Replace with nothing clears.
    expect(buildBulkRequest(['a'], { ...emptyBulkForm(), genreMode: 'replace' })?.set).toEqual({ genres: [] });
  });

  it('normalises labels and splits locks', () => {
    const req = buildBulkRequest(['a'], {
      ...emptyBulkForm(),
      addLabels: ['Late  Night', 'late night'],
      removeLabels: ['Kids'],
      locks: { title: 'lock', genres: 'unlock', year: 'keep' },
    });
    expect(req).toEqual({
      item_ids: ['a'],
      add_labels: ['late night'],
      remove_labels: ['kids'],
      lock: ['title'],
      unlock: ['genres'],
    });
  });
});

describe('validateBulkForm', () => {
  it('flags empty values for set / add / remove', () => {
    expect(validateBulkForm({ ...emptyBulkForm(), ratingMode: 'set' })).toMatch(/content rating/);
    expect(validateBulkForm({ ...emptyBulkForm(), genreMode: 'add', genres: ' , ' })).toMatch(/genre/);
    expect(validateBulkForm({ ...emptyBulkForm(), genreMode: 'replace' })).toBe('');
    expect(validateBulkForm(emptyBulkForm())).toBe('');
  });
});

describe('applicableCount', () => {
  it('counts the selected types a field applies to', () => {
    expect(applicableCount('content_rating', ['movie', 'album', 'photo'])).toBe(1);
    expect(applicableCount('genres', ['movie', 'album', 'photo'])).toBe(2);
  });
});

describe('summarizeBulkResult', () => {
  it('reports updates and skip reasons', () => {
    const line = summarizeBulkResult({
      matched: 3, updated: 1, labels_added: 2, labels_removed: 0,
      skipped: [
        { item_id: 'a', field: 'genres', reason: 'locked' },
        { item_id: 'b', field: 'content_rating', reason: 'not_applicable' },
        { item_id: 'c', reason: 'not_found' },
      ],
    }, 3);
    expect(line).toBe('Updated 1 of 3 items, labelled 2. Skipped: 1 locked, 1 not applicable, 1 no longer there.');
  });
  it('explains reasons', () => {
    expect(skipReasonText('not_applicable')).toMatch(/type/);
    expect(skipReasonText('weird')).toBe('weird');
  });
});
