// Reading direction, layout mode, and keyboard navigation for the
// CBZ/CBR/EPUB reader. Pulled out of +page.svelte so the state-transition
// rules (RTL flip, spread step, ttb→scroll override) can be unit-tested
// without mounting the page component or stubbing the item-detail API.
//
// Pure module — no DOM, no Svelte store dependencies.

export type ReadingDirection = 'ltr' | 'rtl' | 'ttb';
export type LayoutMode = 'single' | 'spread' | 'scroll';

// effectiveLayout collapses the user's preferred layout against the
// reading direction: ttb (vertical / webtoon) always renders as one
// long scroll regardless of what 'layout' was set to. Single and spread
// pass through.
export function effectiveLayout(direction: ReadingDirection, layout: LayoutMode): LayoutMode {
  return direction === 'ttb' ? 'scroll' : layout;
}

// pageStep returns how many pages a single advance/retreat moves. Spread
// mode shows two pages side-by-side, so going "forward" must skip both
// pages or the next click would re-show one of them.
export function pageStep(layout: LayoutMode): 1 | 2 {
  return layout === 'spread' ? 2 : 1;
}

// keyToPageDelta returns the signed page delta to apply for a keydown
// event. Null means "ignore this key" — either the key isn't bound, or
// the layout is scroll mode (browser scroll handles it).
//
// RTL flip: in right-to-left manga, ArrowRight advances to the NEXT page
// (a lower number visually but a higher one in archive order). ArrowLeft
// goes back. PageDown/PageUp/Space are reading-flow keys, not spatial,
// so they're never reversed.
export function keyToPageDelta(
  key: string,
  direction: ReadingDirection,
  layout: LayoutMode,
): number | null {
  if (effectiveLayout(direction, layout) === 'scroll') return null;
  const step = pageStep(effectiveLayout(direction, layout));
  switch (key) {
    case 'PageDown':
    case ' ':
      return step;
    case 'PageUp':
      return -step;
    case 'ArrowRight':
      return direction === 'rtl' ? -step : step;
    case 'ArrowLeft':
      return direction === 'rtl' ? step : -step;
    default:
      return null;
  }
}

// HOME and END are absolute jumps, not deltas. Returns the target page
// number (1-indexed) or null if the key isn't a jump key.
export function keyToAbsolutePage(
  key: string,
  pageCount: number,
  direction: ReadingDirection,
  layout: LayoutMode,
): number | null {
  if (effectiveLayout(direction, layout) === 'scroll') return null;
  if (key === 'Home') return 1;
  if (key === 'End') return pageCount;
  return null;
}
