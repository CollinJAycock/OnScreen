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

const backStack: string[] = [];

function currentHash(): string {
  // Default to hub if the hash is empty / root — protects pop() from
  // landing on the bare app shell that auto-redirects.
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

/** Detail-page back handler. Pops the stack; falls back to hub when
 *  empty (e.g. cold-launch deep link). */
export function goBack() {
  const dest = backStack.pop() ?? '#/hub';
  goto(dest);
}

function pushHere() {
  const h = currentHash();
  // Don't stack the same route twice — guards against re-clicking the
  // current tile from a slow-rendering page.
  if (backStack[backStack.length - 1] !== h) backStack.push(h);
}
