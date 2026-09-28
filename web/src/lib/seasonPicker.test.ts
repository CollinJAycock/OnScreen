import type { SeasonInfo } from '$lib/api';
import {
  allState,
  defaultSelection,
  hasRequestableMissing,
  isSelectable,
  requestSeasons,
  seasonNote,
  seasonState,
  seasonTitle,
  submitLabel,
  toggleAll,
} from './seasonPicker';

function season(n: number, over: Partial<SeasonInfo> = {}): SeasonInfo {
  return {
    season_number: n,
    name: n === 0 ? 'Specials' : `Season ${n}`,
    episode_count: 10,
    aired_episodes: 10,
    air_date: '2020-01-01',
    owned_episodes: 0,
    requested: null,
    ...over,
  };
}

describe('seasonState', () => {
  it('classifies each season', () => {
    expect(seasonState(season(1, { owned_episodes: 10 }))).toBe('owned');
    expect(seasonState(season(1, { owned_episodes: 12 }))).toBe('owned');
    expect(seasonState(season(1, { owned_episodes: 4 }))).toBe('partial');
    expect(seasonState(season(1))).toBe('missing');
    expect(seasonState(season(1, { aired_episodes: 0, air_date: null }))).toBe('unaired');
    // A request wins over library state: the season is on its way.
    expect(seasonState(season(1, { owned_episodes: 4, requested: 'downloading' }))).toBe('requested');
  });

  it('only owned and requested seasons are locked', () => {
    expect(isSelectable(season(1, { owned_episodes: 10 }))).toBe(false);
    expect(isSelectable(season(1, { requested: 'pending' }))).toBe(false);
    expect(isSelectable(season(1, { owned_episodes: 3 }))).toBe(true);
    expect(isSelectable(season(1, { aired_episodes: 0 }))).toBe(true);
  });
});

describe('seasonNote / seasonTitle', () => {
  it('describes each state', () => {
    expect(seasonNote(season(1, { owned_episodes: 10 }))).toBe('In library');
    expect(seasonNote(season(1, { requested: 'approved' }))).toBe('Requested · Approved');
    expect(seasonNote(season(1, { owned_episodes: 3 }))).toBe('3 of 10 episodes in library');
    expect(seasonNote(season(1, { aired_episodes: 0, air_date: null }))).toBe('Not aired yet');
    expect(seasonNote(season(1, { aired_episodes: 0, air_date: '2027-03-04' }))).toMatch(/^Airs .*2027/);
    expect(seasonNote(season(1, { episode_count: 1 }))).toBe('1 episode');
  });

  it('titles specials and unnamed seasons', () => {
    expect(seasonTitle(season(0, { name: 'Extras' }))).toBe('Specials');
    expect(seasonTitle(season(3, { name: '' }))).toBe('Season 3');
    expect(seasonTitle(season(2, { name: 'Book Two: Earth' }))).toBe('Book Two: Earth');
  });
});

describe('selection', () => {
  const show = [
    season(1, { owned_episodes: 10 }),
    season(2, { owned_episodes: 3 }),
    season(3),
    season(4, { requested: 'pending' }),
    season(5, { aired_episodes: 0, air_date: null }),
    season(0, { episode_count: 2, aired_episodes: 2 }),
  ];

  it('starts with every pickable regular season, specials off', () => {
    expect([...defaultSelection(show)].sort()).toEqual([2, 3, 5]);
  });

  it('tracks and toggles "All seasons" over the pickable regular seasons', () => {
    const sel = defaultSelection(show);
    expect(allState(show, sel)).toBe('all');
    const none = toggleAll(show, sel);
    expect(allState(show, none)).toBe('none');
    expect([...toggleAll(show, none)].sort()).toEqual([2, 3, 5]);
    expect(allState(show, new Set([3]))).toBe('some');
    // Specials survive toggling the regular seasons off.
    expect([...toggleAll(show, new Set([2, 3, 5, 0]))]).toEqual([0]);
  });

  it('sends the picked seasons for a show partly owned or requested', () => {
    expect(requestSeasons(show, new Set([2, 3, 5]))).toEqual([2, 3, 5]);
    expect(requestSeasons(show, new Set([3, 0]))).toEqual([0, 3]);
    // Locked seasons never go out even if somehow selected.
    expect(requestSeasons(show, new Set([1, 4, 3]))).toEqual([3]);
    expect(requestSeasons(show, new Set())).toEqual([]);
  });

  it('omits seasons ("every season") for a fresh show with everything picked', () => {
    const fresh = [season(1), season(2), season(3, { aired_episodes: 0 }), season(0)];
    expect(requestSeasons(fresh, defaultSelection(fresh))).toBeUndefined();
    // Picking specials too, or leaving a season out, is explicit.
    expect(requestSeasons(fresh, new Set([0, 1, 2, 3]))).toEqual([0, 1, 2, 3]);
    expect(requestSeasons(fresh, new Set([1, 2]))).toEqual([1, 2]);
  });

  it('labels the submit button', () => {
    expect(submitLabel(undefined)).toBe('Request all seasons');
    expect(submitLabel([2])).toBe('Request season 2');
    expect(submitLabel([2, 3, 4, 7])).toBe('Request seasons 2–4, 7');
    expect(submitLabel([0, 1])).toBe('Request season 1 + specials');
    expect(submitLabel([0])).toBe('Request specials');
  });

  it('knows when there is anything missing to request', () => {
    expect(hasRequestableMissing(show)).toBe(true); // season 2 partial, 3 missing
    expect(hasRequestableMissing([season(1, { owned_episodes: 10 }), season(2, { requested: 'pending' })])).toBe(false);
    // Unaired or specials alone don't warrant the button.
    expect(hasRequestableMissing([season(1, { owned_episodes: 10 }), season(2, { aired_episodes: 0 }), season(0)])).toBe(false);
  });
});
