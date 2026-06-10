import { toRemoteKey, type RemoteKey } from './keys';
import { isDirection, pickNeighbor } from './spatial';

const FOCUSABLE_ATTR = 'data-focusable';
const SCOPE_ATTR = 'data-focus-scope';

type BackHandler = () => boolean;
type KeyHandler = (k: RemoteKey, e: KeyboardEvent) => boolean;

class FocusManager {
  private current: HTMLElement | null = null;
  private backStack: BackHandler[] = [];
  private keyHandlers: KeyHandler[] = [];

  init(root: HTMLElement = document.body) {
    root.addEventListener('keydown', this.onKey, true);
    this.focusFirst();
  }

  destroy(root: HTMLElement = document.body) {
    root.removeEventListener('keydown', this.onKey, true);
  }

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

  /** Called by the focusable action's destroy so an unmounting node
   *  doesn't linger as `current` — refocus()/onKey recovery would
   *  otherwise keep deferring to a detached element. */
  clearIfCurrent(el: HTMLElement) {
    if (this.current === el) this.current = null;
  }

  private candidates(): HTMLElement[] {
    if (!this.current) return [...document.querySelectorAll<HTMLElement>(`[${FOCUSABLE_ATTR}]`)];
    const scope = this.current.closest(`[${SCOPE_ATTR}]`) ?? document.body;
    return [...scope.querySelectorAll<HTMLElement>(`[${FOCUSABLE_ATTR}]`)];
  }

  private onKey = (e: KeyboardEvent) => {
    const k = toRemoteKey(e);
    if (!k) return;

    for (let i = this.keyHandlers.length - 1; i >= 0; i--) {
      if (this.keyHandlers[i](k, e)) {
        e.preventDefault();
        e.stopPropagation();
        return;
      }
    }

    if (k === 'back') {
      const handler = this.backStack[this.backStack.length - 1];
      if (handler && handler()) {
        e.preventDefault();
        e.stopPropagation();
      }
      return;
    }

    if (k === 'enter') {
      if (!this.current || !document.body.contains(this.current)) {
        this.focusFirst();
      }
      if (this.current) {
        e.preventDefault();
        this.current.click();
      }
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
