import { describe, it, expect } from 'vitest';
import type { WatchlistCounts, WatchlistItem } from './api';
import { applyStatusChange, cardSubtitle, parseStatus, tabCount, typeLabel, typeOptions } from './watchlist';

function item(over: Partial<WatchlistItem> = {}): WatchlistItem {
  return {
    id: 'i1',
    library_id: 'l1',
    type: 'movie',
    title: 'Alien',
    watch_state: 'unwatched',
    status: 'plan_to_watch',
    status_created_at: '2026-10-01T00:00:00Z',
    status_updated_at: '2026-10-01T00:00:00Z',
    ...over,
  };
}

function counts(): WatchlistCounts {
  return {
    total: 4,
    statuses: {
      plan_to_watch: { total: 3, types: { movie: 2, show: 1 } },
      watching: { total: 1, types: { show: 1 } },
      on_hold: { total: 0, types: {} },
      completed: { total: 0, types: {} },
      dropped: { total: 0, types: {} },
    },
  };
}

describe('watchlist helpers', () => {
  it('parses the status query, falling back to Plan to Watch', () => {
    expect(parseStatus('watching')).toBe('watching');
    expect(parseStatus('all')).toBe('plan_to_watch');
    expect(parseStatus(null)).toBe('plan_to_watch');
  });

  it('offers only the types a tab has, most common first', () => {
    expect(typeOptions(counts(), 'plan_to_watch')).toEqual([
      { type: 'movie', label: 'Movies', count: 2 },
      { type: 'show', label: 'Shows', count: 1 },
    ]);
    expect(typeOptions(counts(), 'dropped')).toEqual([]);
    expect(typeOptions(null, 'plan_to_watch')).toEqual([]);
    expect(typeLabel('podcast_episode')).toBe('Podcast episodes');
    expect(typeLabel('mystery_type')).toBe('mystery type');
  });

  it('reads tab badges, null when counts are unknown', () => {
    expect(tabCount(counts(), 'plan_to_watch')).toBe(3);
    expect(tabCount(counts(), 'on_hold')).toBe(0);
    expect(tabCount(null, 'watching')).toBeNull();
  });

  it('builds card subtitles per type', () => {
    expect(cardSubtitle(item({ year: 1979 }))).toBe('1979');
    expect(cardSubtitle(item({ type: 'episode', grandparent_title: 'Severance', parent_index: 2, index: 5 })))
      .toBe('Severance · S2 · E5');
    expect(cardSubtitle(item({ type: 'season', parent_title: 'Severance' }))).toBe('Severance');
    expect(cardSubtitle(item({ type: 'track', grandparent_title: 'Artist', parent_title: 'Album' }))).toBe('Artist · Album');
    expect(cardSubtitle(item({ type: 'show' }))).toBe('');
  });

  it('moves a card to another tab and shifts the badges', () => {
    const a = item({ id: 'a' });
    const b = item({ id: 'b', type: 'show' });
    const r = applyStatusChange([a, b], counts(), b, 'watching');
    expect(r.items.map((i) => i.id)).toEqual(['a']);
    expect(r.removed).toBe(true);
    expect(r.counts?.statuses.plan_to_watch).toEqual({ total: 2, types: { movie: 2 } });
    expect(r.counts?.statuses.watching).toEqual({ total: 2, types: { show: 2 } });
    expect(r.counts?.total).toBe(4);
  });

  it('removes a card from the list entirely', () => {
    const a = item({ id: 'a' });
    const r = applyStatusChange([a], counts(), a, null);
    expect(r.items).toEqual([]);
    expect(r.counts?.statuses.plan_to_watch.total).toBe(2);
    expect(r.counts?.total).toBe(3);
  });

  it('leaves everything alone when the status is unchanged', () => {
    const a = item({ id: 'a' });
    const c = counts();
    const r = applyStatusChange([a], c, a, 'plan_to_watch');
    expect(r.items).toEqual([a]);
    expect(r.counts).toBe(c);
    expect(r.removed).toBe(false);
  });
});
