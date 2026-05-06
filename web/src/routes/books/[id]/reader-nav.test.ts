// Unit tests for the reader navigation helpers. These are pure
// functions, so they exercise without mounting the page component or
// stubbing the API — every test is a single (input → output) assertion.
//
// Coverage targets:
//   - effectiveLayout: ttb forces scroll, otherwise pass-through
//   - pageStep: 2 for spread, 1 elsewhere
//   - keyToPageDelta: arrow keys flip under rtl, scroll mode ignores all
//   - keyToAbsolutePage: Home/End jumps; scroll mode ignores

import { describe, it, expect } from 'vitest';
import {
  effectiveLayout,
  pageStep,
  keyToPageDelta,
  keyToAbsolutePage,
} from './reader-nav';

describe('effectiveLayout', () => {
  it('ttb always forces scroll regardless of layout choice', () => {
    expect(effectiveLayout('ttb', 'single')).toBe('scroll');
    expect(effectiveLayout('ttb', 'spread')).toBe('scroll');
    expect(effectiveLayout('ttb', 'scroll')).toBe('scroll');
  });

  it('ltr / rtl pass the layout through unchanged', () => {
    expect(effectiveLayout('ltr', 'single')).toBe('single');
    expect(effectiveLayout('ltr', 'spread')).toBe('spread');
    expect(effectiveLayout('rtl', 'single')).toBe('single');
    expect(effectiveLayout('rtl', 'spread')).toBe('spread');
  });
});

describe('pageStep', () => {
  it('spread advances by 2 (so the next click turns both pages)', () => {
    expect(pageStep('spread')).toBe(2);
  });

  it('single and scroll advance by 1', () => {
    expect(pageStep('single')).toBe(1);
    expect(pageStep('scroll')).toBe(1);
  });
});

describe('keyToPageDelta — ltr (Western)', () => {
  it('ArrowRight + PageDown + Space all advance one page', () => {
    expect(keyToPageDelta('ArrowRight', 'ltr', 'single')).toBe(1);
    expect(keyToPageDelta('PageDown', 'ltr', 'single')).toBe(1);
    expect(keyToPageDelta(' ', 'ltr', 'single')).toBe(1);
  });

  it('ArrowLeft + PageUp retreat one page', () => {
    expect(keyToPageDelta('ArrowLeft', 'ltr', 'single')).toBe(-1);
    expect(keyToPageDelta('PageUp', 'ltr', 'single')).toBe(-1);
  });

  it('spread mode doubles the step', () => {
    expect(keyToPageDelta('ArrowRight', 'ltr', 'spread')).toBe(2);
    expect(keyToPageDelta('ArrowLeft', 'ltr', 'spread')).toBe(-2);
    expect(keyToPageDelta('PageDown', 'ltr', 'spread')).toBe(2);
    expect(keyToPageDelta('PageUp', 'ltr', 'spread')).toBe(-2);
  });
});

describe('keyToPageDelta — rtl (manga)', () => {
  it('arrow keys are reversed: → goes BACK in archive order', () => {
    // Right arrow visually points "forward" in Western reading. In
    // RTL manga, reading flow runs right-to-left, so the next page is
    // to the left of the current one — pressing → must advance the
    // archive index.
    //
    // Wait — re-read the source: in RTL, ArrowRight returns -step.
    // That's because the archive is stored in reading order: page 1
    // is the cover, page 2 is the first interior page. In an RTL
    // book, the "next" page (in reading flow) is to the LEFT of the
    // current spread. So ArrowLeft must advance (+step), ArrowRight
    // must retreat (-step). Hand-verify against +page.svelte:281.
    expect(keyToPageDelta('ArrowRight', 'rtl', 'single')).toBe(-1);
    expect(keyToPageDelta('ArrowLeft', 'rtl', 'single')).toBe(1);
  });

  it('PageDown / PageUp / Space are reading-flow keys and stay un-flipped', () => {
    expect(keyToPageDelta('PageDown', 'rtl', 'single')).toBe(1);
    expect(keyToPageDelta(' ', 'rtl', 'single')).toBe(1);
    expect(keyToPageDelta('PageUp', 'rtl', 'single')).toBe(-1);
  });

  it('spread + rtl combines: arrow flip plus step doubling', () => {
    expect(keyToPageDelta('ArrowRight', 'rtl', 'spread')).toBe(-2);
    expect(keyToPageDelta('ArrowLeft', 'rtl', 'spread')).toBe(2);
  });
});

describe('keyToPageDelta — scroll / ttb', () => {
  it('returns null in scroll mode (browser owns scrolling)', () => {
    for (const k of ['ArrowRight', 'ArrowLeft', 'PageDown', 'PageUp', ' ', 'Home', 'End']) {
      expect(keyToPageDelta(k, 'ltr', 'scroll')).toBeNull();
    }
  });

  it('ttb direction forces scroll-mode handling regardless of layout', () => {
    expect(keyToPageDelta('ArrowRight', 'ttb', 'single')).toBeNull();
    expect(keyToPageDelta('PageDown', 'ttb', 'spread')).toBeNull();
  });

  it('returns null for unbound keys in any mode', () => {
    expect(keyToPageDelta('a', 'ltr', 'single')).toBeNull();
    expect(keyToPageDelta('Enter', 'rtl', 'spread')).toBeNull();
  });
});

describe('keyToAbsolutePage', () => {
  it('Home jumps to page 1, End to pageCount', () => {
    expect(keyToAbsolutePage('Home', 50, 'ltr', 'single')).toBe(1);
    expect(keyToAbsolutePage('End', 50, 'ltr', 'single')).toBe(50);
  });

  it('Home/End ignored in scroll / ttb (browser owns scroll position)', () => {
    expect(keyToAbsolutePage('Home', 50, 'ltr', 'scroll')).toBeNull();
    expect(keyToAbsolutePage('End', 50, 'ttb', 'single')).toBeNull();
  });

  it('returns null for non-jump keys', () => {
    expect(keyToAbsolutePage('ArrowRight', 50, 'ltr', 'single')).toBeNull();
    expect(keyToAbsolutePage('PageDown', 50, 'ltr', 'single')).toBeNull();
  });

  it('End respects RTL too — RTL just flips deltas, not absolute jumps', () => {
    // The "last page" semantically is the last entry in the archive,
    // regardless of direction. RTL only reverses arrow-key deltas.
    expect(keyToAbsolutePage('End', 100, 'rtl', 'single')).toBe(100);
  });
});
