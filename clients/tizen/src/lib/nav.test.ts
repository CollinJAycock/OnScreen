import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { get } from 'svelte/store';

// nav.ts navigates through SvelteKit; the router isn't here, so goto is a spy
// (hoisted: vi.mock's factory runs before the imports).
const { goto } = vi.hoisted(() => ({ goto: vi.fn() }));
vi.mock('$app/navigation', () => ({ goto }));

import {
  backTarget,
  forgetHistory,
  goBack,
  playItem,
  playerEpoch,
  pushTo,
  replaceTo,
  resetStack,
  takeStartOverride,
} from './nav';

const ITEM = 'a3c1';
const NEXT = 'b7d2';

function at(hash: string) {
  vi.stubGlobal('location', { hash });
}

beforeEach(() => {
  goto.mockClear();
  // Drain whatever an earlier test left on the stack.
  resetStack('#/hub');
  at('#/hub');
  goBack();
  goto.mockClear();
});

afterEach(() => {
  vi.unstubAllGlobals();
  takeStartOverride(ITEM);
  takeStartOverride(NEXT);
});

describe('replaceTo (the player moving on to the next item)', () => {
  it('replaces the route and starts the next item where it was told, once', () => {
    at(`#/watch/${ITEM}`);
    replaceTo(`#/watch/${NEXT}`, { id: NEXT, ms: 0 });
    expect(goto).toHaveBeenCalledWith(`#/watch/${NEXT}`, { replaceState: true });
    // From the top, not at a resume point an earlier skip left.
    expect(takeStartOverride(NEXT)).toBe(0);
    // Consumed: a later open of the item resumes as usual.
    expect(takeStartOverride(NEXT)).toBeUndefined();
  });

  it('without a start leaves the resume point alone', () => {
    replaceTo(`#/watch/${NEXT}`);
    expect(takeStartOverride(NEXT)).toBeUndefined();
  });

  it('pushes nothing: Back from the next item returns to the launching screen', () => {
    at('#/item/season-1');
    playItem(ITEM);
    at(`#/watch/${ITEM}`);
    replaceTo(`#/watch/${NEXT}`, { id: NEXT, ms: 0 });
    at(`#/watch/${NEXT}`);
    goto.mockClear();
    goBack();
    expect(goto).toHaveBeenCalledWith('#/item/season-1');
  });

  it('clamps a negative start to the top', () => {
    replaceTo(`#/watch/${NEXT}`, { id: NEXT, ms: -40 });
    expect(takeStartOverride(NEXT)).toBe(0);
  });
});

describe('playItem', () => {
  it('opens the player at the asked position and pushes the launching route', () => {
    at('#/item/album-9');
    playItem(ITEM, 65_000);
    expect(goto).toHaveBeenCalledWith(`#/watch/${ITEM}`);
    expect(takeStartOverride(ITEM)).toBe(65_000);
    at(`#/watch/${ITEM}`);
    goto.mockClear();
    goBack();
    expect(goto).toHaveBeenCalledWith('#/item/album-9');
  });

  it('after resetStack, without a push: Back lands on the hub, not the screen the TV showed', () => {
    at('#/item/movie-3');
    resetStack('#/hub');
    playItem(ITEM, 1_000, { push: false });
    expect(goto).toHaveBeenCalledWith(`#/watch/${ITEM}`);
    at(`#/watch/${ITEM}`);
    goto.mockClear();
    goBack();
    expect(goto).toHaveBeenCalledWith('#/hub');
  });

  it('gives the item already open in the player a fresh player, without navigating', () => {
    at(`#/watch/${ITEM}`);
    const before = get(playerEpoch);
    playItem(ITEM, 754_000, { push: false });
    expect(goto).not.toHaveBeenCalled();
    expect(get(playerEpoch)).toBe(before + 1);
    expect(takeStartOverride(ITEM)).toBe(754_000);
  });
});

// Sign in's Back on its first step (routes/login): to Setup when Setup
// opened it this session, else the app's (the exit popup).
describe('backTarget', () => {
  it('names where Back would go, without going', () => {
    expect(backTarget()).toBeNull();
    at('#/setup');
    pushTo('#/login');
    expect(goto).toHaveBeenCalledTimes(1);
    expect(backTarget()).toBe('#/setup');
    expect(backTarget()).toBe('#/setup');
  });

  it('is empty again after a sign-out forgets the history', () => {
    at('#/setup');
    pushTo('#/login');
    forgetHistory();
    expect(backTarget()).toBeNull();
  });
});
