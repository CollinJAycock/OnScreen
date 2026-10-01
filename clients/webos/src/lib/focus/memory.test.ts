import { describe, expect, it } from 'vitest';
import {
  FocusMemory,
  RestoreGuard,
  episodeFocusKey,
  seasonOfEpisodeKey,
  type FocusMemo,
} from './memory';

// The page as the snapshot sees it.
function setup(initial: FocusMemo | null = { focusedId: 'card-1', loadedCount: 100 }) {
  let shown: FocusMemo | null = initial;
  const memory = new FocusMemory(() => shown);
  return {
    memory,
    show(memo: FocusMemo | null) {
      shown = memo;
    },
  };
}

describe('FocusMemory', () => {
  it('hands the note back when Back returns to the route', () => {
    const { memory } = setup({ focusedId: 'm42', loadedCount: 300 });
    memory.leave('#/library/a');
    memory.back('#/library/a');
    expect(memory.take('#/library/a')).toEqual({ focusedId: 'm42', loadedCount: 300 });
  });

  it('gives nothing to an arrival that is not Back (top nav, cold start)', () => {
    const { memory } = setup();
    memory.leave('#/favorites/');
    expect(memory.take('#/favorites/')).toBeNull();
    // Nor does a later Back find it consumed by that miss: it is still there.
    memory.back('#/favorites/');
    expect(memory.take('#/favorites/')).toEqual({ focusedId: 'card-1', loadedCount: 100 });
  });

  it('is consumed by the first take', () => {
    const { memory } = setup();
    memory.leave('#/hub');
    memory.back('#/hub');
    expect(memory.take('#/hub')).not.toBeNull();
    memory.back('#/hub');
    expect(memory.take('#/hub')).toBeNull();
  });

  it('forgets everything on clear (a sign-out: the next user starts fresh)', () => {
    const { memory } = setup();
    memory.leave('#/search/');
    memory.back('#/search/');
    memory.clear();
    expect(memory.take('#/search/')).toBeNull();
    memory.back('#/search/');
    expect(memory.take('#/search/')).toBeNull();
  });

  it('keeps one note per route, through a deeper trip', () => {
    const { memory, show } = setup({ focusedId: 'tile', loadedCount: 40 });
    // Hub → show page → episode's player, then Back twice.
    memory.leave('#/hub');
    show({ focusedId: 'season-2', loadedCount: 8 });
    memory.leave('#/item/show');
    memory.back('#/item/show');
    // The item page doesn't ask; Back goes on to the hub.
    memory.back('#/hub');
    expect(memory.take('#/hub')).toEqual({ focusedId: 'tile', loadedCount: 40 });
  });

  it('forgets a pending Back once the user navigates forward again', () => {
    const { memory } = setup();
    memory.leave('#/library/a');
    memory.back('#/library/a');
    // Something else was opened before the library asked (it never mounted).
    memory.leave('#/item/x');
    expect(memory.take('#/library/a')).toBeNull();
  });

  it('drops the old note when the page had nothing to note', () => {
    const { memory, show } = setup();
    memory.leave('#/history/');
    show(null);
    memory.leave('#/history/');
    memory.back('#/history/');
    expect(memory.take('#/history/')).toBeNull();
  });

  it('remembers a bounded number of routes, dropping the oldest', () => {
    const { memory } = setup();
    for (let i = 0; i < 25; i++) memory.leave(`#/item/${i}`);
    memory.back('#/item/0');
    expect(memory.take('#/item/0')).toBeNull();
    memory.back('#/item/24');
    expect(memory.take('#/item/24')).not.toBeNull();
  });
});

describe('RestoreGuard', () => {
  // Focus as the focus manager reports it: an element id, or null for none.
  function setup(initial: string | null = null) {
    let focused: string | null = initial;
    const guard = new RestoreGuard<string>(() => focused);
    return {
      guard,
      focus(el: string | null) {
        focused = el;
      },
    };
  }

  it('is active while nothing has focus yet', () => {
    const { guard } = setup();
    expect(guard.active).toBe(true);
    expect(guard.active).toBe(true);
  });

  it('stays active on the card the restore itself focused', () => {
    const { guard, focus } = setup();
    focus('card-42');
    guard.placedOn('card-42');
    expect(guard.active).toBe(true);
  });

  it('ends once the user moves focus somewhere else, for good', () => {
    const { guard, focus } = setup();
    guard.placedOn('card-42');
    focus('card-42');
    expect(guard.active).toBe(true);
    // The user pressed Up to the top nav while the grid was still refilling.
    focus('nav-favorites');
    expect(guard.active).toBe(false);
    // Coming back to the restored card doesn't revive it.
    focus('card-42');
    expect(guard.active).toBe(false);
  });

  it('ends when the user focuses something before the restore found its card', () => {
    const { guard, focus } = setup();
    focus('nav-home');
    expect(guard.active).toBe(false);
  });

  it('tolerates focus that was already there when it started', () => {
    const { guard } = setup('chip-sort');
    expect(guard.active).toBe(true);
  });

  it('ends when the page goes away, whatever has focus', () => {
    const { guard } = setup();
    guard.end();
    expect(guard.active).toBe(false);
  });

  it('treats focus lost to a detached element (null) as no move', () => {
    const { guard, focus } = setup();
    guard.placedOn('card-1');
    focus('card-1');
    focus(null);
    expect(guard.active).toBe(true);
  });
});

describe('episode focus keys', () => {
  it('carry the season the card was listed under', () => {
    const key = episodeFocusKey('season-2', 'ep-7');
    expect(key).toBe('episode:season-2:ep-7');
    expect(seasonOfEpisodeKey(key)).toBe('season-2');
  });

  it('name no season when the list had none', () => {
    expect(seasonOfEpisodeKey(episodeFocusKey(null, 'ep-7'))).toBeNull();
  });

  it('leave every other key alone', () => {
    expect(seasonOfEpisodeKey('action:play-start')).toBeNull();
    expect(seasonOfEpisodeKey('season:season-2')).toBeNull();
    expect(seasonOfEpisodeKey('3f2c-album-id')).toBeNull();
    expect(seasonOfEpisodeKey(null)).toBeNull();
  });
});
