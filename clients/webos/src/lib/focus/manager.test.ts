import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { get } from 'svelte/store';
import { FocusManager } from './manager';
import { POINTER_ECHO_MS, WHEEL_STEP_GAP_MS, pointerShown } from './pointer';

// The focus manager against a small stand-in DOM (the tests run in Node, no
// DOM): elements with a tag, attributes, a parent, children and a box;
// selector lists of tag and attribute compounds (button, [a], [a="v"]),
// which is all the manager queries; events
// that run the capture listeners on the way down (the manager listens on
// the root in the capture phase) and then, unless one stopped them, the
// element's own click.

type Listener = (e: FakeEvent) => void;

interface FakeEvent {
  type: string;
  target: FakeEl;
  timeStamp: number;
  isTrusted: boolean;
  defaultPrevented: boolean;
  stopped: boolean;
  preventDefault(): void;
  stopPropagation(): void;
  [k: string]: unknown;
}

/** A selector list of `tag[attr][attr="v"]` compounds. */
function matches(el: FakeEl, selector: string): boolean {
  return selector.split(',').some((compound) => {
    const s = compound.trim();
    const tag = /^[a-z]+/.exec(s)?.[0];
    if (tag && tag !== el.tag) return false;
    const re = /\[([\w-]+)(?:="([^"]*)")?\]/g;
    let parts = tag ? 1 : 0;
    for (let m = re.exec(s); m; m = re.exec(s)) {
      parts++;
      const [, name, value] = m;
      if (value === undefined ? !el.hasAttribute(name) : el.getAttribute(name) !== value) return false;
    }
    return parts > 0;
  });
}

class FakeEl {
  readonly nodeType = 1;
  tag = 'div';
  parentElement: FakeEl | null = null;
  readonly children: FakeEl[] = [];
  private attrs = new Map<string, string>();
  private listeners = new Map<string, Listener[]>();
  /** Times the element's own click ran (a click nothing stopped). */
  clicks = 0;
  scrolls: unknown[] = [];

  constructor(
    readonly name: string,
    attrs: Record<string, string> = {},
    readonly box = { left: 0, top: 0, width: 100, height: 50 },
  ) {
    for (const [k, v] of Object.entries(attrs)) this.attrs.set(k, v);
  }

  append(...kids: FakeEl[]): this {
    for (const k of kids) {
      k.parentElement = this;
      this.children.push(k);
    }
    return this;
  }

  setAttribute(n: string, v: string) {
    this.attrs.set(n, String(v));
  }
  getAttribute(n: string) {
    return this.attrs.get(n) ?? null;
  }
  hasAttribute(n: string) {
    return this.attrs.has(n);
  }
  removeAttribute(n: string) {
    this.attrs.delete(n);
  }

  closest(sel: string): FakeEl | null {
    for (let el: FakeEl | null = this; el; el = el.parentElement) if (matches(el, sel)) return el;
    return null;
  }
  querySelectorAll(sel: string): FakeEl[] {
    const out: FakeEl[] = [];
    const walk = (el: FakeEl) => {
      for (const c of el.children) {
        if (matches(c, sel)) out.push(c);
        walk(c);
      }
    };
    walk(this);
    return out;
  }
  querySelector(sel: string): FakeEl | null {
    return this.querySelectorAll(sel)[0] ?? null;
  }
  contains(other: FakeEl | null): boolean {
    for (let el = other; el; el = el.parentElement) if (el === this) return true;
    return false;
  }
  getBoundingClientRect() {
    const { left, top, width, height } = this.box;
    return { left, top, width, height, right: left + width, bottom: top + height, x: left, y: top };
  }
  scrollIntoView(opts: unknown) {
    this.scrolls.push(opts);
  }
  addEventListener(type: string, fn: Listener) {
    const l = this.listeners.get(type) ?? [];
    l.push(fn);
    this.listeners.set(type, l);
  }
  removeEventListener(type: string, fn: Listener) {
    this.listeners.set(type, (this.listeners.get(type) ?? []).filter((f) => f !== fn));
  }
  listenersFor(type: string): Listener[] {
    return this.listeners.get(type) ?? [];
  }
  /** HTMLElement.click(): a scripted (untrusted) click. */
  click() {
    dispatch(this, 'click', { isTrusted: false });
  }
}

let clock = 10_000;

function dispatch(target: FakeEl, type: string, init: Record<string, unknown> = {}): FakeEvent {
  const e: FakeEvent = {
    type,
    target,
    timeStamp: clock,
    isTrusted: true,
    defaultPrevented: false,
    stopped: false,
    preventDefault() {
      this.defaultPrevented = true;
    },
    stopPropagation() {
      this.stopped = true;
    },
    ...init,
  };
  const path: FakeEl[] = [];
  for (let el: FakeEl | null = target; el; el = el.parentElement) path.unshift(el);
  for (const el of path) {
    for (const fn of el.listenersFor(type)) fn(e);
    if (e.stopped) return e;
  }
  if (type === 'click') target.clicks++;
  return e;
}

// ── The page ───────────────────────────────────────────────────────────

let body: FakeEl;
let docListeners: Map<string, Listener[]>;
let fm: FocusManager;

function focusable(name: string, left: number, top: number, attrs: Record<string, string> = {}): FakeEl {
  return new FakeEl(name, { 'data-focusable': 'true', 'data-focused': 'false', ...attrs }, { left, top, width: 200, height: 100 });
}

/** Two rows of three cards, the second row below the screen's first. */
function hubPage() {
  const cards: FakeEl[] = [];
  for (let r = 0; r < 2; r++) {
    for (let c = 0; c < 3; c++) cards.push(focusable(`card${r}${c}`, 100 + c * 260, 200 + r * 300));
  }
  // A child the pointer can land on inside a card (its poster image).
  const poster = new FakeEl('poster', {}, cards[4].box);
  cards[4].append(poster);
  const empty = new FakeEl('gap');
  body.append(...cards, empty);
  return { cards, poster, empty };
}

function key(keyCode: number, k = '', extra: Record<string, unknown> = {}) {
  return dispatch(body, 'keydown', { key: k, keyCode, repeat: false, ...extra });
}
function keyUp(keyCode: number, k = '') {
  return dispatch(body, 'keyup', { key: k, keyCode });
}
const BACK = () => key(461);
const enter = () => {
  const e = key(13, 'Enter');
  keyUp(13, 'Enter');
  return e;
};
const move = (el: FakeEl, x: number, y: number) => dispatch(el, 'mousemove', { clientX: x, clientY: y });
const pointerClick = (el: FakeEl) => dispatch(el, 'click', { isTrusted: true, detail: 1 });
const wheel = (el: FakeEl, deltaY: number) => dispatch(el, 'wheel', { deltaX: 0, deltaY, deltaMode: 0 });

beforeEach(() => {
  clock = 10_000;
  body = new FakeEl('body');
  docListeners = new Map();
  vi.stubGlobal('document', {
    body,
    querySelector: (s: string) => body.querySelector(s),
    querySelectorAll: (s: string) => body.querySelectorAll(s),
    // The innermost, last-drawn element whose box holds the point.
    elementFromPoint: (x: number, y: number) => {
      let hit: FakeEl | null = null;
      const walk = (el: FakeEl) => {
        for (const c of el.children) {
          const r = c.getBoundingClientRect();
          if (x >= r.left && x < r.right && y >= r.top && y < r.bottom) hit = c;
          walk(c);
        }
      };
      walk(body);
      return hit;
    },
    addEventListener: (type: string, fn: Listener) => docListeners.set(type, [...(docListeners.get(type) ?? []), fn]),
    removeEventListener: (type: string, fn: Listener) =>
      docListeners.set(type, (docListeners.get(type) ?? []).filter((f) => f !== fn)),
  });
  pointerShown.set(false);
  fm = new FocusManager();
});

afterEach(() => {
  fm.destroy(body as unknown as HTMLElement);
  vi.unstubAllGlobals();
});

const init = () => fm.init(body as unknown as HTMLElement);
const focused = () => (fm.currentElement() as unknown as FakeEl | null)?.name ?? null;

// ── Back ───────────────────────────────────────────────────────────────

describe('Back (keyCode 461, the app owns it with disableBackHistoryAPI)', () => {
  it("goes to the root handler when no screen takes it, and nothing else sees the key", () => {
    hubPage();
    init();
    const root = vi.fn(() => true);
    fm.setRootBack(root);
    const e = BACK();
    expect(root).toHaveBeenCalledTimes(1);
    expect(e.defaultPrevented).toBe(true);
    expect(e.stopped).toBe(true);
  });

  it("leaves it to the screen's own handlers first (a dialog, then the page)", () => {
    hubPage();
    init();
    const root = vi.fn(() => true);
    fm.setRootBack(root);
    const page = vi.fn(() => true);
    const offPage = fm.pushBack(page);
    const dialog = vi.fn(() => true);
    const offDialog = fm.pushBack(dialog);
    BACK();
    expect(dialog).toHaveBeenCalledTimes(1);
    expect(page).not.toHaveBeenCalled();
    offDialog();
    BACK();
    expect(page).toHaveBeenCalledTimes(1);
    offPage();
    expect(root).not.toHaveBeenCalled();
    // A handler that passes (Sign in's first step) falls through.
    const pass = vi.fn(() => false);
    fm.pushBack(pass);
    BACK();
    expect(pass).toHaveBeenCalledTimes(1);
    expect(root).toHaveBeenCalledTimes(1);
  });

  it('lets a page key handler (the player) take Back before the back stack', () => {
    hubPage();
    init();
    const root = vi.fn(() => true);
    fm.setRootBack(root);
    const player = vi.fn((k: string) => k === 'back');
    fm.pushKeyHandler(player);
    BACK();
    expect(player).toHaveBeenCalledWith('back', expect.anything());
    expect(root).not.toHaveBeenCalled();
  });

  it("drops a held Back's repeats: one press, one step (no exit popup flickering open and shut)", () => {
    hubPage();
    init();
    const root = vi.fn(() => true);
    fm.setRootBack(root);
    const player = vi.fn(() => false);
    fm.pushKeyHandler(player);
    BACK();
    for (let i = 0; i < 3; i++) {
      clock += 50;
      const e = key(461, '', { repeat: true });
      expect(e.defaultPrevented).toBe(true);
      expect(e.stopped).toBe(true);
    }
    expect(root).toHaveBeenCalledTimes(1);
    expect(player).toHaveBeenCalledTimes(1);
    // Released and pressed again: a second step.
    clock += 300;
    BACK();
    expect(root).toHaveBeenCalledTimes(2);
  });

  it('unregisters the root handler', () => {
    hubPage();
    init();
    const root = vi.fn(() => true);
    const off = fm.setRootBack(root);
    off();
    BACK();
    expect(root).not.toHaveBeenCalled();
  });
});

// ── Pointer ────────────────────────────────────────────────────────────

describe('Magic Remote pointer', () => {
  it('scrolls a hovered card in a row (data-focus-row) fully into view, so the pointer reaches past the edge', () => {
    body.append(new FakeEl('top')); // something before the row
    const cards = [0, 1, 2].map((c) => focusable(`row${c}`, 100 + c * 700, 200));
    body.append(new FakeEl('scroller', { 'data-focus-row': 'true' }).append(...cards));
    init();
    move(cards[2], 1500, 220);
    expect(focused()).toBe('row2');
    expect(cards[2].scrolls).toEqual([{ block: 'nearest', inline: 'nearest', behavior: 'smooth' }]);
  });

  it('acts with OK on what the cursor is over, after a row scrolled under a still pointer', () => {
    const { cards } = hubPage();
    init();
    move(cards[1], 400, 220);
    expect(focused()).toBe('card01');
    // The row slides one card to the left; the pointer stays where it was,
    // over card02 now, and Chromium's mousemove for it is ignored.
    for (const c of cards.slice(0, 3)) c.box.left -= 260;
    move(cards[2], 400, 220);
    expect(focused()).toBe('card01');
    // OK: its Enter first, then the click on what the cursor is on.
    enter();
    expect(cards[1].clicks).toBe(0);
    clock += 30;
    pointerClick(cards[2]);
    expect(focused()).toBe('card02');
    expect(cards[2].clicks).toBe(1);
    expect(cards[1].clicks).toBe(0);
  });

  it('moves the ring to what the pointer is over, without scrolling the page', () => {
    const { cards, poster } = hubPage();
    init();
    expect(focused()).toBe('card00');
    move(cards[2], 700, 220);
    expect(focused()).toBe('card02');
    expect(cards[2].getAttribute('data-focused')).toBe('true');
    expect(cards[0].getAttribute('data-focused')).toBe('false');
    expect(cards[2].scrolls).toEqual([]);
    // Over a card's inner element: the card.
    move(poster, 400, 560);
    expect(focused()).toBe('card11');
    expect(get(pointerShown)).toBe(true);
  });

  it("ignores Chromium's mousemove for a page scrolled under a still pointer", () => {
    const { cards } = hubPage();
    init();
    move(cards[1], 400, 220);
    expect(focused()).toBe('card01');
    // The D-pad moves on; the page scrolls, and the card under the pointer
    // changes without the pointer moving.
    key(39, 'ArrowRight');
    expect(focused()).toBe('card02');
    move(cards[3], 400, 220);
    expect(focused()).toBe('card02');
  });

  it('keeps the ring when the pointer goes off every focusable', () => {
    const { cards, empty } = hubPage();
    init();
    move(cards[5], 700, 520);
    move(empty, 1800, 1000);
    expect(focused()).toBe('card12');
  });

  it('a click activates once when its Enter echo follows', () => {
    const { cards } = hubPage();
    init();
    move(cards[1], 400, 220);
    pointerClick(cards[1]);
    clock += 40;
    enter();
    expect(cards[1].clicks).toBe(1);
    // With the pointer gone (a D-pad press), OK is the key again.
    key(37, 'ArrowLeft');
    expect(focused()).toBe('card00');
    clock += POINTER_ECHO_MS;
    enter();
    expect(cards[0].clicks).toBe(1);
    expect(cards[1].clicks).toBe(1);
  });

  it('a click activates once when its Enter comes first', () => {
    vi.useFakeTimers();
    try {
      const { cards } = hubPage();
      init();
      move(cards[1], 400, 220);
      enter();
      expect(cards[1].clicks).toBe(0);
      clock += 30;
      expect(pointerClick(cards[1]).stopped).toBe(false);
      expect(cards[1].clicks).toBe(1);
      // Nothing else presses it later.
      vi.advanceTimersByTime(2 * POINTER_ECHO_MS);
      expect(cards[1].clicks).toBe(1);
    } finally {
      vi.useRealTimers();
    }
  });

  it('presses what the cursor is on when the Enter comes without a click', () => {
    vi.useFakeTimers();
    try {
      const { cards } = hubPage();
      init();
      move(cards[2], 700, 220);
      enter();
      vi.advanceTimersByTime(POINTER_ECHO_MS - 1);
      expect(cards[2].clicks).toBe(0);
      vi.advanceTimersByTime(1);
      expect(cards[2].clicks).toBe(1);
      expect(focused()).toBe('card02');
    } finally {
      vi.useRealTimers();
    }
  });

  it('leaves OK to the page over nothing clickable (the player: play / pause)', () => {
    const { cards, empty } = hubPage();
    init();
    const player = vi.fn((k: string) => k === 'enter');
    fm.pushKeyHandler(player);
    move(cards[1], 400, 220);
    move(empty, 1800, 1000);
    // A click on nothing, and its Enter: the Enter is the press.
    pointerClick(empty);
    clock += 30;
    enter();
    expect(player).toHaveBeenCalledWith('enter', expect.anything());
    expect(cards[1].clicks).toBe(0);
  });

  it('presses nothing with OK over nothing clickable that no page takes, in either order', () => {
    vi.useFakeTimers();
    try {
      const { cards, empty } = hubPage();
      init();
      // The ring stays on the card the pointer left.
      move(cards[1], 400, 220);
      move(empty, 1800, 1000);
      expect(focused()).toBe('card01');
      // The Enter, then its click on nothing.
      expect(enter().defaultPrevented).toBe(true);
      clock += 30;
      pointerClick(empty);
      vi.advanceTimersByTime(2 * POINTER_ECHO_MS);
      expect(cards[1].clicks).toBe(0);
      // The click first, then its Enter.
      clock += 1000;
      pointerClick(empty);
      clock += 30;
      enter();
      vi.advanceTimersByTime(2 * POINTER_ECHO_MS);
      expect(cards[1].clicks).toBe(0);
      // The D-pad hides the pointer: OK presses the ring again.
      clock += 1000;
      key(39, 'ArrowRight');
      expect(focused()).toBe('card02');
      clock += POINTER_ECHO_MS;
      enter();
      expect(cards[2].clicks).toBe(1);
      expect(cards[1].clicks).toBe(0);
    } finally {
      vi.useRealTimers();
    }
  });

  it("keeps OK over the player's own buttons from the page's keys: the click presses them", () => {
    vi.useFakeTimers();
    try {
      hubPage();
      const seekForward = Object.assign(new FakeEl('forward', {}, { left: 1200, top: 900, width: 100, height: 50 }), {
        tag: 'button',
      });
      const bar = new FakeEl('bar', { 'data-pointer-target': '' }, { left: 100, top: 980, width: 1700, height: 14 });
      body.append(seekForward, bar);
      init();
      const player = vi.fn(() => true);
      fm.pushKeyHandler(player);
      // The Enter first (held back), then the click.
      move(seekForward, 1210, 910);
      enter();
      expect(player).not.toHaveBeenCalled();
      pointerClick(seekForward);
      expect(seekForward.clicks).toBe(1);
      // The seek bar: its click needs the pointer's position, so an Enter
      // without one presses nothing in its place.
      clock += 1000;
      move(bar, 500, 985);
      enter();
      vi.advanceTimersByTime(2 * POINTER_ECHO_MS);
      expect(bar.clicks).toBe(0);
      expect(player).not.toHaveBeenCalled();
    } finally {
      vi.useRealTimers();
    }
  });

  // "Hold OK for options" on Continue Watching: with the cursor on the card
  // the hold used to open the item on release.
  it('opens a long press for the pointer too, and drops the release click', () => {
    vi.useFakeTimers();
    try {
      const { cards } = hubPage();
      init();
      const options = vi.fn();
      fm.setLongPress(cards[1] as unknown as HTMLElement, options);
      move(cards[1], 400, 220);
      key(13, 'Enter');
      vi.advanceTimersByTime(700);
      expect(options).toHaveBeenCalledTimes(1);
      // The hold's repeats, then the release: its click, then the keyup.
      clock += 600;
      expect(key(13, 'Enter', { repeat: true }).stopped).toBe(true);
      clock += 500;
      expect(pointerClick(cards[1]).stopped).toBe(true);
      keyUp(13, 'Enter');
      vi.advanceTimersByTime(1000);
      expect(cards[1].clicks).toBe(0);
      expect(options).toHaveBeenCalledTimes(1);
      // The next press is an ordinary click.
      clock += 1000;
      key(13, 'Enter');
      clock += 80;
      pointerClick(cards[1]);
      keyUp(13, 'Enter');
      vi.advanceTimersByTime(1000);
      expect(cards[1].clicks).toBe(1);
      expect(options).toHaveBeenCalledTimes(1);
    } finally {
      vi.useRealTimers();
    }
  });

  it('drops the release click after a long press that its keyup beats', () => {
    vi.useFakeTimers();
    try {
      const { cards } = hubPage();
      init();
      const options = vi.fn();
      fm.setLongPress(cards[1] as unknown as HTMLElement, options);
      move(cards[1], 400, 220);
      key(13, 'Enter');
      vi.advanceTimersByTime(700);
      clock += 5000;
      keyUp(13, 'Enter');
      clock += 50;
      expect(pointerClick(cards[1]).stopped).toBe(true);
      vi.advanceTimersByTime(1000);
      expect(cards[1].clicks).toBe(0);
    } finally {
      vi.useRealTimers();
    }
  });

  it('a short press on a card with a long press clicks it once, at the release', () => {
    vi.useFakeTimers();
    try {
      const { cards } = hubPage();
      init();
      const options = vi.fn();
      fm.setLongPress(cards[1] as unknown as HTMLElement, options);
      move(cards[1], 400, 220);
      key(13, 'Enter');
      vi.advanceTimersByTime(200);
      clock += 200;
      pointerClick(cards[1]);
      keyUp(13, 'Enter');
      vi.advanceTimersByTime(1000);
      expect(cards[1].clicks).toBe(1);
      expect(options).not.toHaveBeenCalled();
      // A remote that sends only the Enter: pressed in the click's place.
      clock += 1000;
      key(13, 'Enter');
      vi.advanceTimersByTime(200);
      clock += 200;
      keyUp(13, 'Enter');
      vi.advanceTimersByTime(1000);
      expect(cards[1].clicks).toBe(2);
      expect(options).not.toHaveBeenCalled();
    } finally {
      vi.useRealTimers();
    }
  });

  // The on-screen keyboard's delete key under the cursor: a held OK goes on
  // deleting, as it does with the D-pad (the press, then each repeat).
  it("lets a held OK's repeats click a repeatOk target, and drops the release click", () => {
    vi.useFakeTimers();
    try {
      hubPage();
      const del = focusable('delete', 1200, 800, { 'data-repeat-ok': 'true' });
      body.append(del);
      init();
      move(del, 1210, 810);
      key(13, 'Enter');
      for (let i = 0; i < 3; i++) {
        clock += i === 0 ? 500 : 50;
        key(13, 'Enter', { repeat: true });
      }
      // The press and its three repeats.
      expect(del.clicks).toBe(4);
      clock += 50;
      expect(pointerClick(del).stopped).toBe(true);
      keyUp(13, 'Enter');
      vi.advanceTimersByTime(1000);
      expect(del.clicks).toBe(4);
      // A short press still deletes once.
      clock += 1000;
      key(13, 'Enter');
      clock += 80;
      pointerClick(del);
      keyUp(13, 'Enter');
      vi.advanceTimersByTime(1000);
      expect(del.clicks).toBe(5);
    } finally {
      vi.useRealTimers();
    }
  });

  it("drops a click that echoes a D-pad OK, the long press's release included", () => {
    vi.useFakeTimers();
    try {
      const { cards } = hubPage();
      init();
      const options = vi.fn();
      fm.setLongPress(cards[0] as unknown as HTMLElement, options);
      // The pointer is hidden: OK is the key, held into a long press.
      key(13, 'Enter');
      vi.advanceTimersByTime(700);
      expect(options).toHaveBeenCalledTimes(1);
      clock += 1100;
      expect(pointerClick(cards[0]).stopped).toBe(true);
      keyUp(13, 'Enter');
      expect(cards[0].clicks).toBe(0);
    } finally {
      vi.useRealTimers();
    }
  });

  it('a click focuses what it hit, even without a move first', () => {
    const { cards } = hubPage();
    init();
    pointerClick(cards[4]);
    expect(focused()).toBe('card11');
    expect(cards[4].clicks).toBe(1);
  });

  it("drops the pointer's ring to the D-pad when webOS hides the cursor", () => {
    const { cards } = hubPage();
    init();
    const cursor = (visibility: unknown) =>
      (docListeners.get('cursorStateChange') ?? []).forEach((fn) =>
        fn({ detail: { visibility } } as unknown as FakeEvent),
      );
    cursor(true);
    expect(get(pointerShown)).toBe(true);
    move(cards[5], 700, 520);
    cursor(false);
    expect(get(pointerShown)).toBe(false);
    // Brought fully on screen for the D-pad to go on from.
    expect(cards[5].scrolls.length).toBe(1);
    key(37, 'ArrowLeft');
    expect(focused()).toBe('card11');
  });

  it('counts a D-pad press as leaving pointer mode', () => {
    const { cards } = hubPage();
    init();
    move(cards[1], 400, 220);
    expect(get(pointerShown)).toBe(true);
    key(40, 'ArrowDown');
    expect(get(pointerShown)).toBe(false);
  });
});

// ── Wheel ──────────────────────────────────────────────────────────────

describe('Magic Remote wheel', () => {
  it('moves the ring a row per notch, scrolling it into view', () => {
    const { cards } = hubPage();
    init();
    expect(focused()).toBe('card00');
    const e = wheel(cards[0], 120);
    expect(e.defaultPrevented).toBe(true);
    expect(focused()).toBe('card10');
    expect(cards[3].scrolls.length).toBe(1);
    clock += WHEEL_STEP_GAP_MS;
    wheel(cards[3], -120);
    expect(focused()).toBe('card00');
  });

  it('takes one step from a burst', () => {
    const { cards } = hubPage();
    init();
    wheel(cards[0], 120);
    clock += 10;
    wheel(cards[0], 120);
    expect(focused()).toBe('card10');
  });

  it("goes through the page's key handlers like the D-pad (a picker's cursor)", () => {
    hubPage();
    init();
    const handler = vi.fn(() => true);
    fm.pushKeyHandler(handler);
    wheel(body, 120);
    expect(handler).toHaveBeenCalledWith('down', expect.anything());
    expect(focused()).toBe('card00');
  });

  it('leaves a text box with nothing to focus to scroll itself', () => {
    const { cards } = hubPage();
    const text = Object.assign(new FakeEl('licence'), { scrollTop: 0, scrollHeight: 3000, clientHeight: 800 });
    body.append(text);
    vi.stubGlobal('getComputedStyle', (el: FakeEl) => ({ overflowY: el === text ? 'auto' : 'visible' }));
    init();
    const e = wheel(text, 120);
    expect(e.defaultPrevented).toBe(false);
    expect(focused()).toBe('card00');
    // A card is no text box: the ring moves.
    wheel(cards[0], 120);
    expect(focused()).toBe('card10');
  });
});

// ── Modal ──────────────────────────────────────────────────────────────

describe('a modal popup (the exit popup)', () => {
  it('keeps the ring: nothing behind it autofocuses or is hovered', () => {
    const { cards } = hubPage();
    init();
    const cancel = focusable('cancel', 900, 500);
    const popup = new FakeEl('popup', { 'data-focus-modal': 'true', 'data-focus-scope': 'true' }).append(cancel);
    body.append(popup);
    fm.focus(cancel as unknown as HTMLElement);
    expect(focused()).toBe('cancel');
    fm.focus(cards[4] as unknown as HTMLElement);
    move(cards[5], 700, 520);
    expect(focused()).toBe('cancel');
  });

  // The hub restoring after a Back when the popup opened: the card the
  // restore finds behind the popup is where Cancel puts the ring.
  it('keeps what a page focused behind it, for when it closes', () => {
    const { cards } = hubPage();
    init();
    const cancel = focusable('cancel', 900, 500);
    const popup = new FakeEl('popup', { 'data-focus-modal': 'true', 'data-focus-scope': 'true' }).append(cancel);
    body.append(popup);
    fm.focus(cancel as unknown as HTMLElement);
    fm.focus(cards[4] as unknown as HTMLElement);
    // The pointer's hover behind it is no page asking for the ring.
    move(cards[5], 700, 520);
    expect(focused()).toBe('cancel');
    // Still up: nothing to take yet.
    expect(fm.takeBehindModal()).toBeNull();
    body.children.splice(body.children.indexOf(popup), 1);
    popup.parentElement = null;
    expect((fm.takeBehindModal() as unknown as FakeEl | null)?.name).toBe('card11');
    // Taken once.
    expect(fm.takeBehindModal()).toBeNull();
  });

  it('forgets what was focused behind it once it is gone (removed, or unmounted)', () => {
    const { cards } = hubPage();
    init();
    const cancel = focusable('cancel', 900, 500);
    const popup = new FakeEl('popup', { 'data-focus-modal': 'true', 'data-focus-scope': 'true' }).append(cancel);
    body.append(popup);
    fm.focus(cancel as unknown as HTMLElement);
    fm.focus(cards[4] as unknown as HTMLElement);
    body.children.splice(body.children.indexOf(popup), 1);
    popup.parentElement = null;
    body.children.splice(body.children.indexOf(cards[4]), 1);
    cards[4].parentElement = null;
    expect(fm.takeBehindModal()).toBeNull();
  });

  it("doesn't press Exit with OK once the pointer has left it for the backdrop", () => {
    hubPage();
    init();
    const exit = focusable('exit', 900, 400);
    const cancel = focusable('cancel', 900, 520);
    // The backdrop covers the screen, over the page.
    const popup = new FakeEl('popup', { 'data-focus-modal': 'true', 'data-focus-scope': 'true' }, {
      left: 0,
      top: 0,
      width: 1920,
      height: 1080,
    }).append(exit, cancel);
    body.append(popup);
    fm.focus(cancel as unknown as HTMLElement);
    move(exit, 950, 420);
    expect(focused()).toBe('exit');
    move(popup, 1600, 900);
    enter();
    expect(exit.clicks).toBe(0);
  });
});
