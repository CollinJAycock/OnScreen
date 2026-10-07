import { describeRules, emptyRulesForm, formToRules, rulesToForm } from './collectionRules';
import { fitWithin } from './coverImage';

describe('collection rules form', () => {
  it('round-trips rules through the form', () => {
    const rules = {
      types: ['movie' as const, 'show' as const], genres: ['Horror'], year_min: 1980, year_max: 1989,
      rating_min: 7.5, library_ids: ['l-1'], limit: 50,
    };
    expect(formToRules(rulesToForm(rules))).toEqual(rules);
  });

  it('starts from movies with no other limits', () => {
    expect(formToRules(emptyRulesForm())).toEqual({ types: ['movie'] });
    expect(rulesToForm(null)).toEqual(emptyRulesForm());
  });

  it('refuses incomplete or out-of-range forms with a readable message', () => {
    const f = emptyRulesForm();
    expect(() => formToRules({ ...f, movies: false })).toThrow('Choose movies, shows or both');
    expect(() => formToRules({ ...f, yearMin: '1990', yearMax: '1980' })).toThrow('From year is after To year');
    expect(() => formToRules({ ...f, yearMin: 'soon' })).toThrow(/From year/);
    expect(() => formToRules({ ...f, ratingMin: '11' })).toThrow(/rating/);
    expect(() => formToRules({ ...f, limit: '501' })).toThrow(/Limit/);
    expect(() => formToRules({ ...f, limit: '2.5' })).toThrow(/Limit/);
  });

  it('describes rules in one line', () => {
    expect(describeRules({ types: ['movie'], genres: ['Horror'], year_min: 1980, year_max: 1989 }))
      .toBe('Horror movies from 1980–1989');
    expect(describeRules({ types: ['movie', 'show'], rating_min: 8, library_ids: ['a', 'b'] }))
      .toBe('Movies and shows rated 8+ in 2 libraries');
    expect(describeRules({ types: ['show'], year_min: 2000 })).toBe('Shows from 2000 on');
    expect(describeRules({ types: ['movie'], year_max: 1970 })).toBe('Movies up to 1970');
  });
});

describe('cover sizing', () => {
  it('fits large images in the cover box without enlarging small ones', () => {
    expect(fitWithin(2000, 3000)).toEqual({ w: 1000, h: 1500 });
    expect(fitWithin(4000, 1000)).toEqual({ w: 1000, h: 250 });
    expect(fitWithin(600, 900)).toEqual({ w: 600, h: 900 });
    expect(fitWithin(0, 10)).toEqual({ w: 0, h: 0 });
  });
});
