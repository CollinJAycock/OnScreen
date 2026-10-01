// Centralised routing for card clicks. Mirrors the Android Navigator —
// type-aware destination so callers don't have to special-case the
// types that don't drill into the standard /item detail page (photos
// go to a full-screen viewer; collections drill into their item grid).
//
// Also owns the in-memory back stack. TV webviews don't reliably round-
// trip browser history.back() through SvelteKit's hash router — popping
// past the SvelteKit-managed entries lands on the file:// origin's
// empty parent state, which the firmware reloads as the app entry.
// Tracking the previous-hash on every forward navigation here and
// having detail pages call goBack() instead of history.back() gives
// the user "back to where I came from" without depending on the
// webview's history.

import { goto } from '$app/navigation';
import { writable } from 'svelte/store';
import { focusMemory } from './focus/memory';

const backStack: string[] = [];

function currentHash(): string {
  // Default to hub if the hash is empty / root — protects pop() from
  // landing on the bare app shell that auto-redirects. (focus/memory's
  // currentRoute keys its notes by the same rule.)
  const h = typeof location !== 'undefined' ? location.hash : '';
  return !h || h === '#' || h === '#/' ? '#/hub' : h;
}

/** Navigate to a card target, pushing the current route so back works. */
export function openItem(id: string, type: string) {
  pushHere();
  switch (type) {
    case 'photo':
      goto(`#/photo/${id}`);
      return;
    case 'collection':
    case 'playlist':
      goto(`#/collection/${id}`);
      return;
    default:
      goto(`#/item/${id}`);
  }
}

/** Drill from one item detail page into a child (season → episode,
 *  show → season, book_author → book_series). */
export function openChild(id: string) {
  pushHere();
  goto(`#/item/${id}`);
}

/** Push an arbitrary destination, recording the current route. Used
 *  by pages that don't fit openItem's type-switch (discover → item,
 *  recordings → item). */
export function pushTo(hashRoute: string) {
  pushHere();
  goto(hashRoute);
}

// One-shot start positions for the next /watch of an item. The player
// normally resumes from the item's own view_offset_ms; "Watch again" and
// the show / season up-next button need to say "from the top" (or from the
// up-next episode's resume point) instead.
const startOverrides = new Map<string, number>();

/** Bumped to give the item already open in the player a fresh player: the
 *  watch layout keys its page on the item id AND this. Another item's route
 *  remounts by its id; the same item's is the same URL, where goto does
 *  nothing, so a transfer of the item playing here (at another position)
 *  would have been dropped. */
export const playerEpoch = writable(0);

/** Open the player for an item, optionally at a given position. Pushes the
 *  current route, so the player's Back (and the end of playback with nothing
 *  after it) returns to the screen that launched it: the album, the season,
 *  the hub. It used to go to the playing item's own detail page, so album →
 *  track → Back landed on a bare track page. `push: false` leaves the stack
 *  as it is (after resetStack). The item already open in the player starts
 *  over in a fresh player (see playerEpoch). */
export function playItem(id: string, startMs?: number, opts: { push?: boolean } = {}) {
  if (startMs !== undefined) startOverrides.set(id, Math.max(0, startMs));
  else startOverrides.delete(id);
  const route = `#/watch/${id}`;
  if (currentHash() === route) {
    playerEpoch.update((n) => n + 1);
    return;
  }
  if (opts.push !== false) pushHere();
  goto(route);
}

/** Start the back stack over: Back from the next screen lands on `root`,
 *  whatever led here. "Play on this TV" (a playback.transfer from another
 *  device) uses it, so the player it opens returns to the hub rather than
 *  into whatever the TV happened to show, with that screen's own history
 *  under it (Android pops its whole back stack for a transfer). */
export function resetStack(root = '#/hub') {
  backStack.length = 0;
  backStack.push(root);
}

/** Forget all navigation history: the back stack, pending start positions
 *  and the focus notes. On sign-out, so the next user's Back can't land on
 *  the previous user's screens (their search, their library on the old
 *  server after Change server). */
export function forgetHistory() {
  backStack.length = 0;
  startOverrides.clear();
  focusMemory.clear();
}

/** Move to `hashRoute` in place of the current route, pushing nothing: the
 *  player moving on to the next episode / track / chapter. Back from there
 *  still returns to the launching screen, never to the item just played
 *  (and an album of auto-advances doesn't grow the stack by a route a
 *  track). `start` gives the item opened there a start position, as
 *  playItem's does: the player starts the next item from the top (Android's
 *  newInstance(next.id, 0)), not at a resume point left by an earlier skip,
 *  which cut a song a minute in. */
export function replaceTo(hashRoute: string, start?: { id: string; ms: number }) {
  if (start) startOverrides.set(start.id, Math.max(0, start.ms));
  goto(hashRoute, { replaceState: true });
}

/** The position playItem asked for, consumed on read (undefined = resume). */
export function takeStartOverride(id: string): number | undefined {
  const v = startOverrides.get(id);
  startOverrides.delete(id);
  return v;
}

/** Detail-page back handler. Pops the stack; falls back to `fallback`
 *  (the hub unless the caller has a better parent) when empty, e.g. a
 *  cold-launch deep link. */
export function goBack(fallback = '#/hub') {
  const dest = backStack.pop() ?? fallback;
  // The page Back lands on may put focus back where it was (focus/memory).
  focusMemory.back(dest);
  goto(dest);
}

function pushHere() {
  const h = currentHash();
  // Note the focused card (and how many the page had loaded) for when
  // Back returns here; see focus/memory.
  focusMemory.leave(h);
  // Don't stack the same route twice — guards against re-clicking the
  // current tile from a slow-rendering page.
  if (backStack[backStack.length - 1] !== h) backStack.push(h);
}
