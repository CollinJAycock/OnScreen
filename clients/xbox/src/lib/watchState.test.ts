import { describe, expect, it } from 'vitest';
import type { HubItem } from './api/types';
import { episodeCode, hubTileText, nextUpSubtitle, upNextLabel } from './watchState';

function tile(extra: Partial<HubItem> = {}): HubItem {
  return {
    id: 'e1',
    title: 'Pilot',
    type: 'episode',
    updated_at: 0,
    show_title: 'The Wire',
    season_number: 1,
    episode_number: 1,
    ...extra,
  };
}

describe('episodeCode', () => {
  it('reads "S2 · E5", and a multi-episode file as its range', () => {
    expect(episodeCode(2, 5)).toBe('S2 · E5');
    expect(episodeCode(1, 1, 2)).toBe('S1 · E1–E2');
    // An end that isn't past the start is one episode.
    expect(episodeCode(1, 3, 3)).toBe('S1 · E3');
    expect(episodeCode(1, 0, 2)).toBe('S1');
  });
});

describe('Next Up / Continue Watching tiles (episode_number_end)', () => {
  it('labels a multi-episode file as a range under the show', () => {
    expect(hubTileText(tile({ episode_number_end: 2 }))).toEqual({
      title: 'The Wire',
      subtitle: 'S1 · E1–E2 — Pilot',
    });
    expect(nextUpSubtitle(tile({ episode_number_end: 2 }))).toBe('S1 · E1–E2 — Pilot');
  });

  it('reads as before without the field (one episode, an older server)', () => {
    expect(hubTileText(tile())).toEqual({ title: 'The Wire', subtitle: 'S1 · E1 — Pilot' });
  });
});

describe('upNextLabel (episode_number_end)', () => {
  const ep = { id: 'e1', title: 'Pilot', season_id: 's1', season_number: 1, episode_number: 1 };

  it('names the range on the show / season Play button', () => {
    expect(upNextLabel({ mode: 'next', episode: { ...ep, episode_number_end: 2 } })).toBe('Play S1 · E1–E2');
    expect(upNextLabel({ mode: 'resume', episode: { ...ep, episode_number_end: 2 } })).toBe('Resume S1 · E1–E2');
    expect(upNextLabel({ mode: 'start', episode: ep })).toBe('Play S1 · E1');
  });
});
