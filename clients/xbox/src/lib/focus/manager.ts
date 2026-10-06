import { KeyHold, REPEAT_GAP_MS, eventTime, repeatActivates } from './hold';
import { toRemoteKey, type RemoteKey } from './keys';
import { POINTER_ECHO_MS, PointerEcho, PointerTrack, WheelSteps, pointerShown, scrollsNatively } from './pointer';
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
// A popup over every page (focusable's focusModal: the exit popup) keeps
// the ring while it's up: a page behind it that finishes loading can't
// autofocus a card there, and the pointer can't hover one.
const MODAL_ATTR = 'data-focus-modal';

type BackHandler = () => boolean;
/** What the key path reads of its event: a keydown, or the wheel event a
 *  notch's D-pad step comes from (see onWheel). */
export interface KeyEventLike {
  repeat?: boolean;
  timeStamp: number;
  preventDefault(): void;
  stopPropagation(): void;
}
type KeyHandler = (k: RemoteKey, e: KeyEventLike) => boolean;

/** The use:focusable element an event happened in, if any. */
function focusableOf(target: EventTarget | null): HTMLElement | null {
  const el = target as Element | null;
  if (!el || typeof el.closest !== 'function') return null;
  return el.closest<HTMLElement>(`[${FOCUSABLE_ATTR}]`);
}

// What a pointer click acts on: the focusables, the player's own buttons
// and anything marked for the pointer (POINTER_TARGET_ATTR: the player's
// seek bar).
const POINTER_TARGET_ATTR = 'data-pointer-target';
const CLICKABLE = `[${FOCUSABLE_ATTR}], button, a[href], [role="button"], [${POINTER_TARGET_ATTR}]`;

function clickableOf(target: EventTarget | null): Element | null {
  const el = target as Element | null;
  if (!el || typeof el.closest !== 'function') return null;
  return el.closest(CLICKABLE);
}

// Holding OK this long on an element with a long-press handler opens its
// options instead of activating it (Android's long-press timeout is ~500).
const LONG_PRESS_MS = 600;

export class FocusManager {
  private current: HTMLElement | null = null;
  private backStack: BackHandler[] = [];
  private keyHandlers: KeyHandler[] = [];
  // Back that nothing on the back stack took: the root layout's (the
  // previous page, or the exit popup on a first screen; lib/appExit).
  private rootBack: BackHandler | null = null;
  private root: HTMLElement | null = null;
  // What a page asked the ring onto while a modal kept it (a Back restore
  // or an autofocus finishing behind the exit popup): where the popup's
  // Cancel puts it when nothing had the ring before the popup opened.
  private behindModal: HTMLElement | null = null;

  // The Magic Remote (./pointer): a hover moves the ring, OK in pointer
  // mode acts once (as its click, whichever of click and Enter comes
  // first), a wheel notch is a D-pad step.
  private echo = new PointerEcho();
  private track = new PointerTrack();
  private wheel = new WheelSteps();
  // Whether the pointer is on screen (pointerShown, kept here too).
  private pointerOn = false;
  // An OK pressed with the cursor on something clickable: its click acts,
  // so the Enter is held back (with what the cursor was on) in case it
  // doesn't come.
  private pointerPress: Element | null = null;
  private pointerPressTimer: ReturnType<typeof setTimeout> | null = null;
  // The long press of an OK held back for its click: a hold past
  // LONG_PRESS_MS opens the options (Continue Watching's "Hold OK for
  // options") before the click, which comes with the release, can open the
  // item.
  private pointerLongTimer: ReturnType<typeof setTimeout> | null = null;
  // That press already acted while held (its long press, or a repeatOk
  // target's repeats): the click its release sends is the same press, so
  // it's dropped while the key is down and POINTER_ECHO_MS after the keyup
  // (dropClickUntil, on the events' clock).
  private pointerHoldActed = false;
  private dropClickUntil = -Infinity;

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
    this.root = root;
    root.addEventListener('keydown', this.onKey, true);
    root.addEventListener('keyup', this.onKeyUp, true);
    root.addEventListener('mousemove', this.onPointerMove, true);
    root.addEventListener('click', this.onClick, true);
    // Not passive: a wheel that moves the focus mustn't also scroll a box.
    root.addEventListener('wheel', this.onWheel, { capture: true, passive: false });
    document.addEventListener('cursorStateChange', this.onCursorState);
    this.focusFirst();
  }

  destroy(root: HTMLElement = document.body) {
    root.removeEventListener('keydown', this.onKey, true);
    root.removeEventListener('keyup', this.onKeyUp, true);
    root.removeEventListener('mousemove', this.onPointerMove, true);
    root.removeEventListener('click', this.onClick, true);
    root.removeEventListener('wheel', this.onWheel, true);
    document.removeEventListener('cursorStateChange', this.onCursorState);
    if (this.root === root) this.root = null;
    this.cancelPress();
    this.cancelPointerPress();
  }

  /** What Back does when no screen took it (the root layout's; one at a
   *  time). Returns the unregister. */
  setRootBack(handler: BackHandler) {
    this.rootBack = handler;
    return () => {
      if (this.rootBack === handler) this.rootBack = null;
    };
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
    // The press is over: a pointer click on its heels is its release.
    this.echo.released(eventTime(e));
    // Released before its long press: an ordinary press, left to its click.
    if (this.pointerLongTimer) {
      clearTimeout(this.pointerLongTimer);
      this.pointerLongTimer = null;
    }
    if (this.pointerHoldActed) {
      this.pointerHoldActed = false;
      this.dropClickUntil = eventTime(e) + POINTER_ECHO_MS;
    }
    // An OK held back for its click: the click comes with the release, or
    // it never will (pressPointerTarget).
    if (this.pointerPress && !this.pointerPressTimer) {
      this.pointerPressTimer = setTimeout(this.pressPointerTarget, POINTER_ECHO_MS);
    }
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

  /** Put the ring on `el`, scrolled into view unless `scroll` is false (the
   *  pointer's hover outside a row: the page stays put under the cursor).
   *  Behind a modal it doesn't move, and `el` is kept for takeBehindModal. */
  focus(el: HTMLElement | null, opts: { scroll?: boolean } = {}) {
    if (el && this.blockedByModal(el)) {
      this.behindModal = el;
      return;
    }
    this.place(el, opts);
  }

  /** Once the modal is gone: what a page last asked the ring onto behind it
   *  (focus), if it is still on the page. Taken once. */
  takeBehindModal(): HTMLElement | null {
    const el = this.behindModal;
    if (document.querySelector(`[${MODAL_ATTR}]`)) return null;
    this.behindModal = null;
    return el && document.body.contains(el) ? el : null;
  }

  private blockedByModal(el: HTMLElement): boolean {
    const modal = document.querySelector(`[${MODAL_ATTR}]`);
    return !!modal && !modal.contains(el);
  }

  // The ring's own moves (the D-pad, the pointer): behind a modal they go
  // nowhere and aren't kept.
  private place(el: HTMLElement | null, opts: { scroll?: boolean } = {}) {
    if (!el || el === this.current) return;
    if (this.blockedByModal(el)) return;
    if (this.current) this.current.setAttribute('data-focused', 'false');
    this.current = el;
    el.setAttribute('data-focused', 'true');
    if (opts.scroll !== false) el.scrollIntoView({ block: 'nearest', inline: 'nearest', behavior: 'smooth' });
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
      this.place(first);
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
    // A D-pad press: the remote is in 5-way mode (webOS hides the pointer,
    // and says so with cursorStateChange too).
    if (isDirection(k)) this.setPointer(false);
    this.handle(k, e);
  };

  // A key, or a wheel notch's D-pad step (onWheel), through the page's key
  // handlers, the back stack and the spatial focus moves.
  private handle(k: RemoteKey, e: KeyEventLike) {
    // When the key went down: the event's own stamp, not Date.now(). A page
    // rendering between two of a hold's repeats delays their handling, and a
    // gap measured then reads as a release and a second press (eventTime).
    const now = eventTime(e);

    // A held Back is one press too. Its repeats would walk back a page
    // each, and on a first screen open and cancel the exit popup in turn.
    if (k === 'back' && e.repeat) {
      e.preventDefault();
      e.stopPropagation();
      return;
    }

    // Any other key abandons a pending long press (no click, no options),
    // and an OK held back for its click.
    if (k !== 'enter' && this.pressTarget) this.cancelPress();
    if (k !== 'enter') this.cancelPointerPress();
    if (k === 'enter') {
      const fresh = this.enterHold.down(!!e.repeat, now);
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
          // An OK aimed with the pointer, still held back for its click:
          // the hold is that press, which acts now (as the D-pad's did on
          // its keydown), and the release's click is dropped.
          if (this.pointerPress && focusableOf(this.pointerPress) === el) {
            this.cancelPointerPress();
            this.pointerHoldActed = true;
            el.click();
          }
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
      // The Enter a pointer click sent after it: the click acted.
      if (this.echo.enterIsEcho(now)) {
        e.preventDefault();
        e.stopPropagation();
        return;
      }
      // In pointer mode OK is a click on what the cursor is on, and the
      // click event carries it: an Enter sent along (before the click)
      // would act a second time, on the ring, which a row scrolled under a
      // still pointer may have left on another card. So it's held back for
      // the click, and pressed in its place should none come (onKeyUp).
      // Over nothing clickable OK stays a key (the player's play / pause,
      // the photo viewer's slideshow): there the click does nothing, and
      // so does the key if no page takes it (below).
      const aimed = this.clickableUnderPointer();
      if (aimed) {
        this.cancelPointerPress();
        this.pointerHoldActed = false;
        this.pointerPress = aimed;
        // A held OK is still a hold under the pointer: its repeats may click
        // a repeatOk target (the keyboard's delete key), and an element
        // with a long press opens it at LONG_PRESS_MS, unless the click
        // came first (a short press: cancelPointerPress) or the key went up
        // (onKeyUp).
        const el = focusableOf(aimed);
        this.enterPressed = el;
        const onLong = el ? this.longPress.get(el) : undefined;
        if (el && onLong) {
          this.pointerLongTimer = setTimeout(() => {
            this.pointerLongTimer = null;
            if (this.pointerPress !== aimed) return;
            this.cancelPointerPress();
            if (!document.body.contains(el)) return;
            this.pointerHoldActed = true;
            // The hold's remaining repeats and its keyup, as after the
            // D-pad's long press.
            this.swallowingEnter = true;
            this.swallowEnterUntil = now + LONG_PRESS_MS + 1000;
            (this.longPress.get(el) ?? onLong)();
          }, LONG_PRESS_MS);
        }
        e.preventDefault();
        e.stopPropagation();
        return;
      }
      this.echo.entered(now);
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
      // Nothing on the screen took Back. The platform does nothing with it
      // either (disableBackHistoryAPI, appinfo.json): the root handler goes
      // to the previous page, or on a first screen offers to leave the app
      // (lib/appExit).
      e.preventDefault();
      e.stopPropagation();
      this.rootBack?.();
      return;
    }

    if (k === 'enter') {
      // The pointer is on screen over nothing clickable, and no page took
      // OK: nothing to press. The ring stays on the last thing hovered,
      // which the cursor has left (a card, the exit popup's Exit with the
      // cursor on its backdrop); the D-pad's OK presses it.
      if (this.pointerOn && this.track.at()) {
        e.preventDefault();
        return;
      }
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
          this.place(next as HTMLElement);
        }
      }
    }
  }

  // ── The Magic Remote's pointer and wheel (./pointer) ─────────────────

  private setPointer(on: boolean) {
    this.pointerOn = on;
    pointerShown.set(on);
  }

  // The pointer over a focusable takes the ring there, the same selection
  // effect the D-pad gives (OK is then the click on it). The ring stays on
  // the last one when the pointer moves off, for the D-pad to go on from.
  // A card or pill in a row (ROW_ATTR) is scrolled fully into view, so
  // the pointer works its way along a hub row past the screen's edge;
  // anything else stays put under the cursor (the wheel scrolls pages).
  private onPointerMove = (e: MouseEvent) => {
    if (!this.track.moved(e.clientX, e.clientY)) return;
    this.setPointer(true);
    const el = focusableOf(e.target);
    if (el) this.place(el, { scroll: !!el.closest(`[${ROW_ATTR}]`) });
  };

  // What a click would act on under the pointer, while it's on screen.
  private clickableUnderPointer(): Element | null {
    const at = this.pointerOn ? this.track.at() : null;
    if (!at || typeof document.elementFromPoint !== 'function') return null;
    return clickableOf(document.elementFromPoint(at.x, at.y));
  }

  // An OK held back for its click (pointerPress) that never came: a remote
  // that sent only the Enter. POINTER_ECHO_MS after the key's release it
  // presses what the cursor was on, as the click would have; not the seek
  // bar, whose click needs the pointer's position.
  private pressPointerTarget = () => {
    const target = this.pointerPress;
    this.cancelPointerPress();
    if (!target || !document.body.contains(target) || target.hasAttribute(POINTER_TARGET_ATTR)) return;
    const el = focusableOf(target);
    if (el) this.place(el, { scroll: false });
    (target as HTMLElement).click();
  };

  private cancelPointerPress() {
    if (this.pointerPressTimer) clearTimeout(this.pointerPressTimer);
    this.pointerPressTimer = null;
    if (this.pointerLongTimer) clearTimeout(this.pointerLongTimer);
    this.pointerLongTimer = null;
    this.pointerPress = null;
  }

  // A pointer click acts once (PointerEcho): dropped when it echoes an
  // Enter that already acted (while that OK is down, a long press included,
  // or just after), else, on something clickable, noted so the Enter after
  // it is dropped. A click on nothing leaves its Enter to act as the key.
  // Only real clicks: el.click() above (and any scripted click) isn't one.
  // In the capture phase, so a dropped click never reaches the element.
  private onClick = (e: MouseEvent) => {
    if (!e.isTrusted) return;
    const at = eventTime(e);
    // The release of an OK that already acted while held under the pointer.
    if (this.pointerHoldActed || (at < this.dropClickUntil && at >= this.dropClickUntil - POINTER_ECHO_MS)) {
      e.preventDefault();
      e.stopPropagation();
      return;
    }
    if (this.echo.clickIsEcho(at)) {
      e.preventDefault();
      e.stopPropagation();
      return;
    }
    this.setPointer(true);
    // The click an OK held back for (or any click since): it acts.
    this.cancelPointerPress();
    if (!clickableOf(e.target)) return;
    this.echo.clicked(at);
    const el = focusableOf(e.target);
    if (el) this.place(el, { scroll: false });
  };

  // A wheel notch is the D-pad's ↑ / ↓ (WheelSteps), through the same path
  // as the key: the ring moves and scrolls the row, grid or page into view,
  // a grid pages in more, a picker's cursor moves. A text box with nothing
  // to focus scrolls natively instead.
  private onWheel = (e: WheelEvent) => {
    if (this.wheelScrollsBox(e)) return;
    e.preventDefault();
    const k = this.wheel.step(e.deltaX, e.deltaY, e.deltaMode, eventTime(e));
    if (k) this.handle(k, e);
  };

  private wheelScrollsBox(e: WheelEvent): boolean {
    if (typeof getComputedStyle !== 'function') return false;
    for (let el = e.target as HTMLElement | null; el && el !== this.root; el = el.parentElement) {
      if (el.nodeType !== 1) continue;
      let overflowY: string;
      try {
        overflowY = getComputedStyle(el).overflowY;
      } catch {
        return false;
      }
      if (overflowY !== 'auto' && overflowY !== 'scroll') continue;
      return scrollsNatively(
        {
          overflowY,
          scrollTop: el.scrollTop,
          scrollHeight: el.scrollHeight,
          clientHeight: el.clientHeight,
          hasFocusables: !!el.querySelector(`[${FOCUSABLE_ATTR}]`),
        },
        e.deltaY,
      );
    }
    return false;
  }

  // webOS says when the pointer comes and goes (LG's System UI Visibility
  // guide). When it goes, the D-pad takes over from the ring where the
  // pointer left it, brought fully on screen; with no ring (its element
  // gone), from the page's first focusable.
  private onCursorState = (e: Event) => {
    const v = (e as CustomEvent<{ visibility?: unknown } | null>).detail?.visibility;
    const visible = v === true || v === 'true' ? true : v === false || v === 'false' ? false : null;
    if (visible === null) return;
    this.setPointer(visible);
    if (visible) return;
    const el = this.currentElement();
    if (el) el.scrollIntoView({ block: 'nearest', inline: 'nearest', behavior: 'smooth' });
    else this.focusFirst();
  };
}

export const focusManager = new FocusManager();
