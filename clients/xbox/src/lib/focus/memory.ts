// Focus and scroll memory across a trip into an item and Back. Every route
// change remounts the page, so without this the hub, a library grid,
// Favorites and History came back on their first card, and a paged library
// back on its first page. Android keeps the grid's selected position on the
// fragment instance (GridScrollMemory); here nav.ts notes, when it leaves a
// route forward, which card had focus and how many cards the page had, and
// the page asks for that note when Back brings it up again.
//
// Cards opt in with a stable `data-focus-key` (PosterCard's focusKey).
// Other controls can too (a detail page's buttons, a library's filter
// chips: Back from Surprise me lands on Surprise me); they also carry
// `data-focus-control`, which keeps them out of loadedCount, the count of a
// paged grid's rows.

import { focusManager } from './manager';

export interface FocusMemo {
  /** The focused element's data-focus-key, or null when focus was on
   *  something without one (the top nav). */
  focusedId: string | null;
  /** How many keyed cards the page had (keyed controls not counted): a
   *  paged grid's loaded rows, which it fetches again before it focuses
   *  the card. */
  loadedCount: number;
}

/** Routes remembered at once. The back stack rarely holds more. */
const MAX_MEMOS = 20;

/** The bookkeeping, apart from the DOM (see `focusMemory` for the wiring). */
export class FocusMemory {
  private memos = new Map<string, FocusMemo>();
  private returning: string | null = null;

  /** @param snapshot what the page shows now: the focused card and count. */
  constructor(private readonly snapshot: () => FocusMemo | null) {}

  /** Leaving `route` forward (a card opened, the player started): note
   *  where focus is, for when Back returns here. */
  leave(route: string): void {
    this.returning = null;
    const memo = this.snapshot();
    this.memos.delete(route);
    if (!memo) return;
    this.memos.set(route, memo);
    // Oldest first in a Map: drop from the front.
    while (this.memos.size > MAX_MEMOS) {
      const oldest = this.memos.keys().next().value;
      if (oldest === undefined) break;
      this.memos.delete(oldest);
    }
  }

  /** Back is about to show `route`. */
  back(route: string): void {
    this.returning = route;
  }

  /** Forget every note (a sign-out: the next user's Back must not come
   *  back to the last user's cards). */
  clear(): void {
    this.memos.clear();
    this.returning = null;
  }

  /**
   * The note for `route` when Back is what brought it up; null for any
   * other arrival (the top nav, a cold start), which starts at the top as
   * Android does. Consumed: the page notes afresh the next time it's left.
   */
  take(route: string): FocusMemo | null {
    if (this.returning !== route) return null;
    this.returning = null;
    const memo = this.memos.get(route) ?? null;
    this.memos.delete(route);
    return memo;
  }
}

function domSnapshot(): FocusMemo | null {
  if (typeof document === 'undefined') return null;
  const keyed = focusManager.currentElement()?.closest<HTMLElement>('[data-focus-key]') ?? null;
  return {
    focusedId: keyed?.getAttribute('data-focus-key') ?? null,
    loadedCount: document.querySelectorAll('[data-focus-key]:not([data-focus-control])').length,
  };
}

export const focusMemory = new FocusMemory(domSnapshot);

/** The current route's key, as nav.ts records it. */
export function currentRoute(): string {
  const h = typeof location !== 'undefined' ? location.hash : '';
  return !h || h === '#' || h === '#/' ? '#/hub' : h;
}

/** This route's note, when it was reached by Back (consumed). */
export function takeFocusMemo(): FocusMemo | null {
  return focusMemory.take(currentRoute());
}

/**
 * Whether a restore that's still on its way may act. The page's list can be
 * slow to come (a slow link; a library grid refilling 30 pages one after
 * another), and by the time it is in the user may have left the page (the
 * cards a late restore found would then be the next page's: Favorites has
 * the same item ids as the library) or moved focus themselves, which a late
 * jump back to the old card would undo. So a restore asks `active` before
 * every page it fetches and every focus it moves, and once it's false it
 * stays false. Pure (the focused element is injected) for the tests.
 */
export class RestoreGuard<E> {
  private stopped = false;
  private readonly startedOn: E | null;
  private placed: E | null = null;

  /** @param focused the element that has focus now, null when none.
   *  @param inModal whether `el` is in a modal over the page (the exit
   *  popup): focus there, and its going when the popup closes, isn't the
   *  user moving off the restore, which carries on behind the popup (the
   *  focus manager keeps where it lands for the popup's Cancel). */
  constructor(
    private readonly focused: () => E | null,
    private readonly inModal: (el: E) => boolean = () => false,
  ) {
    // As a rule nothing: the page's first card holds its autofocus back for
    // the restore, and the last page's card is gone.
    this.startedOn = focused();
  }

  /** The page went away (its onMount cleanup). */
  end(): void {
    this.stopped = true;
  }

  /** The restore itself put focus on `el`: that isn't the user moving it. */
  placedOn(el: E): void {
    this.placed = el;
  }

  /** True while the page is up and focus is where the restore left it. */
  get active(): boolean {
    if (this.stopped) return false;
    let now = this.focused();
    if (now !== null && this.inModal(now)) now = null;
    if (now !== null && now !== this.startedOn && now !== this.placed) this.stopped = true;
    return !this.stopped;
  }
}

/** A guard over the focus manager's focus, for a page about to restore. */
export function restoreGuard(): RestoreGuard<HTMLElement> {
  return new RestoreGuard(
    () => focusManager.currentElement(),
    (el) => !!el.closest('[data-focus-modal]'),
  );
}

/**
 * A show page's episode card key. It names the season whose list the card
 * is in: Back can open the page on another season (up-next moved on to the
 * next one after the last episode of a season played to the end), and the
 * page then selects that season again before it looks for the card.
 */
export function episodeFocusKey(seasonId: string | null, episodeId: string): string {
  return `episode:${seasonId ?? ''}:${episodeId}`;
}

/** The season an episodeFocusKey names; null for any other key (or none). */
export function seasonOfEpisodeKey(key: string | null): string | null {
  if (!key || !key.startsWith('episode:')) return null;
  const season = key.slice('episode:'.length, key.lastIndexOf(':'));
  return season || null;
}

/** The card with this focus key, if it is on the page. */
export function findKeyed(key: string, root: ParentNode = document): HTMLElement | null {
  for (const el of root.querySelectorAll<HTMLElement>('[data-focus-key]')) {
    if (el.getAttribute('data-focus-key') === key) return el;
  }
  return null;
}

/**
 * Focuses `el` as the page comes back. Scrolled into the middle of the
 * screen straight away rather than with the focus manager's smooth,
 * nearest-edge scroll: from the top of a long page that would play a visible
 * glide down to the card (and leave it on the bottom edge). Nothing happens
 * (false) once `guard` says the restore is over.
 */
export function restoreFocusTo(el: HTMLElement, guard?: RestoreGuard<HTMLElement> | null): boolean {
  if (guard && !guard.active) return false;
  el.scrollIntoView({ block: 'center', inline: 'nearest' });
  focusManager.focus(el);
  guard?.placedOn(el);
  return true;
}

/** Back on a page: focuses the remembered card when it's on the page. True
 *  when it was. */
export function restoreKeyed(memo: FocusMemo | null, guard?: RestoreGuard<HTMLElement> | null): boolean {
  const el = memo?.focusedId ? findKeyed(memo.focusedId) : null;
  if (!el) return false;
  return restoreFocusTo(el, guard);
}

/**
 * A restored row again, after content loaded in above it (Settings: Back
 * from Licence & terms lands on the last row, then the preferences and
 * scrobble rows arrive and push it off the screen): scrolled back to the
 * middle, while `guard` says the restore still holds. False when it didn't.
 */
export function restoreAgain(memo: FocusMemo | null, guard: RestoreGuard<HTMLElement> | null): boolean {
  if (!memo || !guard || !guard.active) return false;
  return restoreKeyed(memo, guard);
}

/** The app's page box (the root layout's main.tv-root) back to the top. It
 *  outlives the routes in it, scroll and all, so a page opened from far down
 *  another (Licence & terms, from the bottom of Settings) would start part
 *  way down itself. */
export function scrollPageToTop(): void {
  if (typeof document === 'undefined') return;
  const root = document.querySelector<HTMLElement>('main.tv-root');
  if (root) root.scrollTop = 0;
}

/** The first element matching `selector` takes focus (a page's first card,
 *  whose autofocus was held back for a restore that found nothing), else
 *  whatever the focus manager picks. Nothing once `guard` says the restore
 *  is over. */
export function focusFirstOf(selector: string, guard?: RestoreGuard<HTMLElement> | null): void {
  if (guard && !guard.active) return;
  const el = document.querySelector<HTMLElement>(selector);
  if (el) {
    focusManager.focus(el);
    guard?.placedOn(el);
  } else {
    focusManager.refocus();
  }
}
