import { toRemoteKey, type RemoteKey } from './keys';
import { isDirection, pickNeighbor } from './spatial';

const FOCUSABLE_ATTR = 'data-focusable';
const SCOPE_ATTR = 'data-focus-scope';

type BackHandler = () => boolean;
type KeyHandler = (k: RemoteKey, e: KeyboardEvent) => boolean;

// Holding OK this long on an element with a long-press handler opens its
// options instead of activating it (Android's long-press timeout is ~500).
const LONG_PRESS_MS = 600;
// After a long press fires, Enter key-repeats keep arriving until the key
// is released. A gap longer than this between them means a fresh press
// (covers firmware that never sends keyup or doesn't flag repeats).
const REPEAT_GAP_MS = 350;

class FocusManager {
  private current: HTMLElement | null = null;
  private backStack: BackHandler[] = [];
  private keyHandlers: KeyHandler[] = [];

  // Long press on OK. Elements opt in via the focusable action's
  // onLongPress; for them the click moves from keydown to keyup so a hold
  // can turn into the long-press instead. Every other element still
  // activates on keydown, exactly as before.
  private longPress = new WeakMap<HTMLElement, () => void>();
  private pressTarget: HTMLElement | null = null;
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
    const first = document.querySelector<HTMLElement>(`[${FOCUSABLE_ATTR}]`);
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

  private candidates(): HTMLElement[] {
    if (!this.current) return [...document.querySelectorAll<HTMLElement>(`[${FOCUSABLE_ATTR}]`)];
    const scope = this.current.closest(`[${SCOPE_ATTR}]`) ?? document.body;
    return [...scope.querySelectorAll<HTMLElement>(`[${FOCUSABLE_ATTR}]`)];
  }

  private onKey = (e: KeyboardEvent) => {
    const k = toRemoteKey(e);
    if (!k) return;

    // Any other key abandons a pending long press (no click, no options).
    if (k !== 'enter' && this.pressTarget) this.cancelPress();
    if (k === 'enter' && this.swallowingEnter) {
      const now = Date.now();
      if (e.repeat || now < this.swallowEnterUntil) {
        // Still the hold that fired the long press.
        this.swallowEnterUntil = now + REPEAT_GAP_MS;
        e.preventDefault();
        e.stopPropagation();
        return;
      }
      this.swallowingEnter = false; // a fresh press
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
        this.focusFirst();
      }
      const el = this.current;
      if (!el) return;
      e.preventDefault();
      const onLong = this.longPress.get(el);
      if (!onLong) {
        el.click();
        return;
      }
      // Long-press capable: keyup before the threshold clicks (onKeyUp),
      // holding past it opens the options instead. Repeats of the same
      // hold are ignored.
      if (this.pressTarget) return;
      this.pressTarget = el;
      this.pressTimer = setTimeout(() => {
        const target = this.pressTarget;
        this.cancelPress();
        if (!target || !document.body.contains(target)) return;
        this.swallowingEnter = true;
        // Generous first window: some remotes start repeating late.
        this.swallowEnterUntil = Date.now() + 1000;
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
        const next = pickNeighbor(this.current, this.candidates(), k);
        if (next) {
          e.preventDefault();
          this.focus(next as HTMLElement);
        }
      }
    }
  };
}

export const focusManager = new FocusManager();
