import { KeyHold, REPEAT_GAP_MS, eventTime, repeatActivates } from './hold';
import { toRemoteKey, type RemoteKey } from './keys';
import { isDirection, pickNeighborNear } from './spatial';

const FOCUSABLE_ATTR = 'data-focusable';
const SCOPE_ATTR = 'data-focus-scope';
// A row whose Left / Right stay in it (the top nav, a hub row's scroller,
// the season chips, a page's filter chips): at its ends they do nothing
// rather than jump to whatever lies that way above or below it. Left on
// "Home" used to land on the hub's first card, the card's right edge being
// left of the pill; Right on a hub row's last card, on a card rows away;
// Right on the last season chip, on an episode. Up / Down aren't confined.
const ROW_ATTR = 'data-focus-row';
// An element that takes a held OK's repeats (focusable's repeatOk): see
// repeatActivates in ./hold.
const REPEAT_ATTR = 'data-repeat-ok';
// The current page's own top-nav pill (TopNav marks it), where focus goes
// when a page has nothing of its own to focus (an empty Recordings).
const CURRENT_PAGE_PILL = `[${FOCUSABLE_ATTR}][aria-current="page"]`;

type BackHandler = () => boolean;
type KeyHandler = (k: RemoteKey, e: KeyboardEvent) => boolean;

// Holding OK this long on an element with a long-press handler opens its
// options instead of activating it (Android's long-press timeout is ~500).
const LONG_PRESS_MS = 600;

class FocusManager {
  private current: HTMLElement | null = null;
  private backStack: BackHandler[] = [];
  private keyHandlers: KeyHandler[] = [];

  // A held OK is one press: its repeats are dropped before any handler sees
  // them (see ./hold). The long press below still measures the hold.
  private enterHold = new KeyHold();
  // The element the current press clicked (null when the press clicked
  // nothing: a page's handler took it, or it only brought the ring up).
  // Its repeats may click it again if it asked for them (data-repeat-ok).
  private enterPressed: HTMLElement | null = null;

  // Long press on OK. Elements opt in via the focusable action's
  // onLongPress; for them the click moves from keydown to keyup so a hold
  // can turn into the long-press instead. Every other element still
  // activates on keydown, exactly as before.
  private longPress = new WeakMap<HTMLElement, () => void>();
  private pressTarget: HTMLElement | null = null;
  // When the long press's keydown happened (eventTime), so the swallow
  // window below runs on the same clock as the keydowns it's compared with.
  private pressAt = 0;
  private pressTimer: ReturnType<typeof setTimeout> | null = null;
  // Set once a long press fired, until the key is released (or a fresh press
  // arrives): swallows the hold's remaining repeats + its keyup so they can't
  // activate whatever the long-press just focused (a dialog's first option).
  private swallowEnterUntil = 0;
  private swallowingEnter = false;

  init(root: HTMLElement = document.body) {
    root.addEventListener('keydown', this.onKey, true);
    root.addEventListener('keyup', this.onKeyUp, true);
    this.focusFirst();
  }

  destroy(root: HTMLElement = document.body) {
    root.removeEventListener('keydown', this.onKey, true);
    root.removeEventListener('keyup', this.onKeyUp, true);
    this.cancelPress();
  }

  /** Register (or clear, with undefined) an element's long-press handler. */
  setLongPress(el: HTMLElement, handler: (() => void) | undefined) {
    if (handler) this.longPress.set(el, handler);
    else this.longPress.delete(el);
  }

  /** The element that currently has the focus ring (null when none). */
  currentElement(): HTMLElement | null {
    return this.current && document.body.contains(this.current) ? this.current : null;
  }

  private cancelPress() {
    if (this.pressTimer) clearTimeout(this.pressTimer);
    this.pressTimer = null;
    this.pressTarget = null;
  }

  private onKeyUp = (e: KeyboardEvent) => {
    if (toRemoteKey(e) !== 'enter') return;
    this.enterHold.up();
    // The hold is over; don't keep the element (it may be unmounted next).
    this.enterPressed = null;
    if (this.swallowingEnter) {
      this.swallowingEnter = false;
      e.preventDefault();
      e.stopPropagation();
      return;
    }
    const target = this.pressTarget;
    if (!target) return;
    this.cancelPress();
    e.preventDefault();
    e.stopPropagation();
    // Released before the long-press threshold: an ordinary activation.
    if (document.body.contains(target)) target.click();
  };

  pushBack(handler: BackHandler) {
    this.backStack.push(handler);
    return () => {
      const i = this.backStack.indexOf(handler);
      if (i >= 0) this.backStack.splice(i, 1);
    };
  }

  pushKeyHandler(handler: KeyHandler) {
    this.keyHandlers.push(handler);
    return () => {
      const i = this.keyHandlers.indexOf(handler);
      if (i >= 0) this.keyHandlers.splice(i, 1);
    };
  }

  focus(el: HTMLElement | null) {
    if (!el || el === this.current) return;
    if (this.current) this.current.setAttribute('data-focused', 'false');
    this.current = el;
    el.setAttribute('data-focused', 'true');
    el.scrollIntoView({ block: 'nearest', inline: 'nearest', behavior: 'smooth' });
  }

  focusFirst() {
    // The page's own top-nav pill before the document's first focusable,
    // which on every page with the nav is "Home": a page arrived at with
    // nothing focused (Search, an empty Recordings) then wakes up on the
    // pill that opened it, not on the hub's. Pages don't autofocus the pill
    // themselves: that focus would end a Back restore (RestoreGuard).
    const first =
      document.querySelector<HTMLElement>(CURRENT_PAGE_PILL) ??
      document.querySelector<HTMLElement>(`[${FOCUSABLE_ATTR}]`);
    if (first) {
      this.focus(first);
    } else {
      // No focusable elements on the current page (photo viewer,
      // pure-player route, splash). Clear stale references so the
      // arrow/enter recovery in onKey doesn't preventDefault on a
      // detached node — would swallow keypresses meant for the
      // page's own document-level handler.
      this.current = null;
    }
  }

  refocus() {
    if (this.current && document.body.contains(this.current)) return;
    this.focusFirst();
  }

  private candidates(dir: RemoteKey): HTMLElement[] {
    if (!this.current) return [...document.querySelectorAll<HTMLElement>(`[${FOCUSABLE_ATTR}]`)];
    if (dir === 'left' || dir === 'right') {
      const row = this.current.closest(`[${ROW_ATTR}]`);
      if (row) return [...row.querySelectorAll<HTMLElement>(`[${FOCUSABLE_ATTR}]`)];
    }
    const scope = this.current.closest(`[${SCOPE_ATTR}]`) ?? document.body;
    return [...scope.querySelectorAll<HTMLElement>(`[${FOCUSABLE_ATTR}]`)];
  }

  private onKey = (e: KeyboardEvent) => {
    const k = toRemoteKey(e);
    if (!k) return;
    // When the key went down: the event's own stamp, not Date.now(). A page
    // rendering between two of a hold's repeats delays their handling, and a
    // gap measured then reads as a release and a second press (eventTime).
    const now = eventTime(e);

    // Any other key abandons a pending long press (no click, no options).
    if (k !== 'enter' && this.pressTarget) this.cancelPress();
    if (k === 'enter') {
      const fresh = this.enterHold.down(e.repeat, now);
      if (this.swallowingEnter) {
        if (!fresh || now < this.swallowEnterUntil) {
          // Still the hold that fired the long press (the window also
          // catches a remote that starts repeating late and flags nothing).
          this.swallowEnterUntil = now + REPEAT_GAP_MS;
          e.preventDefault();
          e.stopPropagation();
          return;
        }
        this.swallowingEnter = false; // a fresh press
      }
      if (!fresh) {
        // The few elements that want a held OK to go on acting (the
        // on-screen keyboard's delete key) get the repeat as a click, and
        // nothing else sees it.
        const el = this.currentElement();
        if (
          el &&
          repeatActivates({
            repeatOk: el.hasAttribute(REPEAT_ATTR),
            pressed: el === this.enterPressed,
            longPress: this.longPress.has(el),
          })
        ) {
          e.preventDefault();
          e.stopPropagation();
          el.click();
          return;
        }
        // A repeat of the OK being held. Nothing sees it: not the element
        // the press activated (a pill's page is up by now, with its own
        // control autofocused), not a page's handler (the player would
        // toggle pause on every repeat), not a pending long press (its
        // timer measures the hold; the keyup ends it).
        e.preventDefault();
        e.stopPropagation();
        return;
      }
      // A new press: what it clicks (below) is what its repeats may click.
      this.enterPressed = null;
    }

    for (let i = this.keyHandlers.length - 1; i >= 0; i--) {
      if (this.keyHandlers[i](k, e)) {
        e.preventDefault();
        e.stopPropagation();
        return;
      }
    }

    if (k === 'back') {
      // Walk the back stack top-down; the first handler that consumes
      // the event wins (in-app back navigation).
      for (let i = this.backStack.length - 1; i >= 0; i--) {
        if (this.backStack[i]()) {
          e.preventDefault();
          e.stopPropagation();
          return;
        }
      }
      // Nothing in-app handled Back — hand it to the platform so webOS
      // performs its native back / app-exit instead of swallowing the
      // key (which would otherwise leave the user stuck at the root).
      if (typeof window !== 'undefined' && window.webOS?.platformBack) {
        e.preventDefault();
        e.stopPropagation();
        window.webOS.platformBack();
      }
      return;
    }

    if (k === 'enter') {
      if (!this.current || !document.body.contains(this.current)) {
        // Nothing focused: like an arrow below, OK only brings the ring up.
        // Clicking what it lands on, unseen, would act on a control the
        // user never chose (the nav's "Home" pill, before focusFirst
        // preferred the page's own).
        this.focusFirst();
        if (this.current) e.preventDefault();
        return;
      }
      const el = this.current;
      e.preventDefault();
      const onLong = this.longPress.get(el);
      if (!onLong) {
        this.enterPressed = el;
        el.click();
        return;
      }
      // Long-press capable: keyup before the threshold clicks (onKeyUp),
      // holding past it opens the options instead. The hold's repeats were
      // dropped above; this guards a keyup that never came.
      if (this.pressTarget) return;
      this.pressTarget = el;
      this.pressAt = now;
      this.pressTimer = setTimeout(() => {
        const target = this.pressTarget;
        const at = this.pressAt;
        this.cancelPress();
        if (!target || !document.body.contains(target)) return;
        this.swallowingEnter = true;
        // Generous first window, a second from now: some remotes start
        // repeating late. On the keydowns' clock (the press's stamp plus
        // the hold so far), not Date.now(), which they aren't stamped on.
        this.swallowEnterUntil = at + LONG_PRESS_MS + 1000;
        (this.longPress.get(target) ?? onLong)();
      }, LONG_PRESS_MS);
      return;
    }

    if (isDirection(k)) {
      // Recover from "no current focus" — e.g. a page mounted no
      // autofocus target (setup, book hierarchy), or the previously-
      // focused element was unmounted on route change. Without this
      // the remote stays dead until the user clicks something.
      if (!this.current || !document.body.contains(this.current)) {
        this.focusFirst();
        if (this.current) {
          e.preventDefault();
          return;
        }
      }
      if (this.current) {
        // Windowed: a paged grid of thousands of cards measures only those
        // near the focused one (see pickNeighborNear). Left / Right inside
        // a [data-focus-row] (ROW_ATTR) consider that row only.
        const next = pickNeighborNear(this.current, this.candidates(k), k);
        if (next) {
          e.preventDefault();
          this.focus(next as HTMLElement);
        }
      }
    }
  };
}

export const focusManager = new FocusManager();
