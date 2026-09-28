import {
  episodeCode,
  nextUpSubtitle,
  upNextLabel,
  markAllOptions,
  progressPct,
  cardWatchBadge,
  watchMenuActions,
  applyWatchedMark,
  WATCH_FILTER_OPTIONS,
  parseWatchFilter,
  urlWithWatchFilter,
  supportsWatchState,
  canMarkWatched,
  isNotFound,
  detailWatched,
  removeById,
  restoreAt,
} from './watchState';
import type { MediaItem, UpNext } from './api';

const ep = (season: number, episode: number, extra: Partial<NonNullable<UpNext['episode']>> = {}) => ({
  id: 'e1', title: 'Pilot', season_id: 's1', season_number: season, episode_number: episode, ...extra,
});

describe('episodeCode / nextUpSubtitle', () => {
  it('formats season and episode', () => {
    expect(episodeCode(2, 5)).toBe('S2 · E5');
    expect(episodeCode(0, 1)).toBe('S0 · E1');
  });
  it('drops missing halves', () => {
    expect(episodeCode(undefined, 5)).toBe('E5');
    expect(episodeCode(3, null)).toBe('S3');
    expect(episodeCode()).toBe('');
  });
  it('builds the Next Up tile subtitle', () => {
    expect(nextUpSubtitle({ title: 'The One', season_number: 2, episode_number: 5 })).toBe('S2 · E5 — The One');
    expect(nextUpSubtitle({ title: 'The One' })).toBe('The One');
  });
});

describe('upNextLabel', () => {
  it('labels each mode', () => {
    expect(upNextLabel({ mode: 'resume', episode: ep(3, 4) })).toBe('Resume S3 · E4');
    expect(upNextLabel({ mode: 'next', episode: ep(3, 5) })).toBe('Play S3 · E5');
    expect(upNextLabel({ mode: 'start', episode: ep(1, 1) })).toBe('Play S1 · E1');
    expect(upNextLabel({ mode: 'rewatch', episode: ep(1, 1) })).toBe('Watch again');
  });
  it('is null when there is nothing to play', () => {
    expect(upNextLabel({ mode: 'none' })).toBeNull();
    expect(upNextLabel({ mode: 'next' })).toBeNull();
    expect(upNextLabel(null)).toBeNull();
    expect(upNextLabel(undefined)).toBeNull();
  });
});

describe('markAllOptions', () => {
  it('offers only what can change', () => {
    expect(markAllOptions({ mode: 'start', episode: ep(1, 1) })).toEqual({ watched: true, unwatched: false });
    expect(markAllOptions({ mode: 'rewatch', episode: ep(1, 1) })).toEqual({ watched: false, unwatched: true });
    expect(markAllOptions({ mode: 'next', episode: ep(1, 2) })).toEqual({ watched: true, unwatched: true });
    expect(markAllOptions({ mode: 'resume', episode: ep(1, 2) })).toEqual({ watched: true, unwatched: true });
    expect(markAllOptions({ mode: 'none' })).toEqual({ watched: false, unwatched: false });
  });
  it('offers both when up-next is unknown', () => {
    expect(markAllOptions(null)).toEqual({ watched: true, unwatched: true });
  });
});

describe('progressPct', () => {
  it('clamps and guards', () => {
    expect(progressPct(30, 120)).toBe(25);
    expect(progressPct(200, 100)).toBe(100);
    expect(progressPct(0, 100)).toBe(0);
    expect(progressPct(10, 0)).toBe(0);
    expect(progressPct(undefined, 100)).toBe(0);
  });
});

describe('cardWatchBadge', () => {
  it('renders nothing when the server sent no watch fields', () => {
    expect(cardWatchBadge({})).toBeNull();
    expect(cardWatchBadge({ watch_state: 'unwatched' })).toBeNull();
  });
  it('checkmark for a watched movie', () => {
    const b = cardWatchBadge({ watch_state: 'watched' });
    expect(b?.watched).toBe(true);
    expect(b?.label).toBe('Watched');
  });
  it('progress bar for an in-progress movie', () => {
    const b = cardWatchBadge({ watch_state: 'in_progress', view_offset_ms: 30 * 60_000, duration_ms: 120 * 60_000 });
    expect(b?.progressPct).toBe(25);
    expect(b?.watched).toBe(false);
    expect(b?.label).toBe('In progress, 25% watched');
  });
  it('no bar when in progress but the duration is unknown', () => {
    expect(cardWatchBadge({ watch_state: 'in_progress', view_offset_ms: 5000 })).toBeNull();
  });
  it('unwatched count for a show with episodes left', () => {
    const b = cardWatchBadge({ leaf_count: 10, unwatched_count: 3 });
    expect(b?.unwatchedCount).toBe(3);
    expect(b?.label).toBe('3 unwatched episodes');
    expect(cardWatchBadge({ leaf_count: 10, unwatched_count: 1 })?.label).toBe('1 unwatched episode');
  });
  it('checkmark (no count) for a fully watched show', () => {
    const b = cardWatchBadge({ leaf_count: 10, unwatched_count: 0, watch_state: 'watched' });
    expect(b?.watched).toBe(true);
    expect(b?.unwatchedCount).toBeNull();
  });
  it('nothing for a show without episodes', () => {
    expect(cardWatchBadge({ leaf_count: 0, unwatched_count: 0 })).toBeNull();
  });
});

describe('watchMenuActions', () => {
  it('movies', () => {
    expect(watchMenuActions({})).toEqual(['watched']);
    expect(watchMenuActions({ watch_state: 'unwatched' })).toEqual(['watched']);
    expect(watchMenuActions({ watch_state: 'watched' })).toEqual(['unwatched']);
    expect(watchMenuActions({ watch_state: 'in_progress' })).toEqual(['watched', 'unwatched']);
  });
  it('shows', () => {
    expect(watchMenuActions({ leaf_count: 5, unwatched_count: 5 })).toEqual(['watched']);
    expect(watchMenuActions({ leaf_count: 5, unwatched_count: 0 })).toEqual(['unwatched']);
    expect(watchMenuActions({ leaf_count: 5, unwatched_count: 2 })).toEqual(['watched', 'unwatched']);
  });
});

describe('applyWatchedMark', () => {
  const movie = { id: 'm', title: 'M', type: 'movie', created_at: '', updated_at: '', watch_state: 'in_progress', view_offset_ms: 5000 } as MediaItem;
  const show = { id: 's', title: 'S', type: 'show', created_at: '', updated_at: '', leaf_count: 8, unwatched_count: 3 } as MediaItem;

  it('marks a movie watched / unwatched and clears the resume point', () => {
    expect(applyWatchedMark(movie, true)).toMatchObject({ watch_state: 'watched', view_offset_ms: 0 });
    expect(applyWatchedMark(movie, false)).toMatchObject({ watch_state: 'unwatched', view_offset_ms: 0 });
  });
  it('updates a show’s unwatched count', () => {
    expect(applyWatchedMark(show, true).unwatched_count).toBe(0);
    expect(applyWatchedMark(show, false).unwatched_count).toBe(8);
  });
  it('does not mutate the original (rollback needs it)', () => {
    applyWatchedMark(movie, true);
    expect(movie.watch_state).toBe('in_progress');
    expect(movie.view_offset_ms).toBe(5000);
  });
  it('the result drives the badge', () => {
    expect(cardWatchBadge(applyWatchedMark(show, true))?.watched).toBe(true);
    expect(cardWatchBadge(applyWatchedMark(movie, true))?.watched).toBe(true);
    expect(cardWatchBadge(applyWatchedMark(movie, false))).toBeNull();
  });
});

describe('watch filter <-> query', () => {
  it('has All / Unwatched / In progress / Watched', () => {
    expect(WATCH_FILTER_OPTIONS.map((o) => o.value)).toEqual(['', 'unwatched', 'in_progress', 'watched']);
  });
  it('parses only known values', () => {
    expect(parseWatchFilter('unwatched')).toBe('unwatched');
    expect(parseWatchFilter('in_progress')).toBe('in_progress');
    expect(parseWatchFilter('watched')).toBe('watched');
    expect(parseWatchFilter('bogus')).toBe('');
    expect(parseWatchFilter(null)).toBe('');
    expect(parseWatchFilter(undefined)).toBe('');
  });
  it('sets and clears ?watch= while keeping other params', () => {
    const base = new URL('http://x/libraries/1?genre=Drama&sort=year');
    const set = urlWithWatchFilter(base, 'in_progress');
    expect(set.searchParams.get('watch')).toBe('in_progress');
    expect(set.searchParams.get('genre')).toBe('Drama');
    expect(set.pathname).toBe('/libraries/1');
    const cleared = urlWithWatchFilter(set, '');
    expect(cleared.searchParams.has('watch')).toBe(false);
    expect(cleared.searchParams.get('sort')).toBe('year');
    // Input URL untouched.
    expect(base.searchParams.has('watch')).toBe(false);
  });
});

describe('type gates', () => {
  it('only video libraries get watch controls', () => {
    for (const t of ['movie', 'show', 'anime', 'cartoons', 'home_video', 'dvr']) expect(supportsWatchState(t)).toBe(true);
    for (const t of ['music', 'photo', 'audiobook', 'podcast', 'book', '', undefined, null]) expect(supportsWatchState(t)).toBe(false);
  });
  it('only playable video types get the mark menu', () => {
    expect(canMarkWatched('movie')).toBe(true);
    expect(canMarkWatched('show')).toBe(true);
    expect(canMarkWatched('episode')).toBe(true);
    expect(canMarkWatched('album')).toBe(false);
    expect(canMarkWatched('photo')).toBe(false);
  });
});

describe('isNotFound / detailWatched', () => {
  it('recognises a 404 API error', () => {
    expect(isNotFound({ status: 404 })).toBe(true);
    expect(isNotFound({ status: 500 })).toBe(false);
    expect(isNotFound(new Error('nope'))).toBe(false);
    expect(isNotFound(null)).toBe(false);
  });
  it('reads a watched flag only when present', () => {
    expect(detailWatched({ watch_state: 'watched' })).toBe(true);
    expect(detailWatched({ watched: true })).toBe(true);
    expect(detailWatched({ watch_state: 'in_progress' })).toBe(false);
    expect(detailWatched({})).toBe(false);
    expect(detailWatched(null)).toBe(false);
  });
});

describe('removeById / restoreAt', () => {
  const list = [{ id: 'a' }, { id: 'b' }, { id: 'c' }];
  it('removes and reports the position', () => {
    const r = removeById(list, 'b');
    expect(r.list.map((x) => x.id)).toEqual(['a', 'c']);
    expect(r.index).toBe(1);
    expect(r.item).toEqual({ id: 'b' });
    expect(list).toHaveLength(3);
  });
  it('is a no-op for an absent id', () => {
    const r = removeById(list, 'z');
    expect(r.list).toBe(list);
    expect(r.index).toBe(-1);
    expect(r.item).toBeNull();
  });
  it('restores at the old index, clamped, without duplicating', () => {
    expect(restoreAt([{ id: 'a' }, { id: 'c' }], { id: 'b' }, 1).map((x) => x.id)).toEqual(['a', 'b', 'c']);
    expect(restoreAt([{ id: 'a' }], { id: 'b' }, 9).map((x) => x.id)).toEqual(['a', 'b']);
    const already = [{ id: 'b' }];
    expect(restoreAt(already, { id: 'b' }, 0)).toBe(already);
  });
});
