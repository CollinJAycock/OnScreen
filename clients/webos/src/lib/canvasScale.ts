// The app is one fixed 1920x1080 canvas (main.tv-root, laid out in 1920-scale
// pixels throughout). On a smaller graphics plane it is scaled to fit.
//
// LG Full HD models give an app a 1280x720 graphics plane (LG's "App
// Resolution" spec; UHD models give 1920x1080), and webOS doesn't scale the
// page for it. Measured on the C1 with appinfo "resolution": "1280x720":
// innerWidth 1280, innerHeight 720, visualViewport.scale 1, the viewport
// meta's width=1920 ignored, so the 1920 px layout was clipped to its top-left
// two thirds. The fix is a transform on the root (the layout's `style:`).
// A transform rather than CSS `zoom`, because Chromium 79's zoom leaves
// getBoundingClientRect and pointer coordinates disagreeing; a transform keeps
// both visual, and elementFromPoint (the Magic Remote pointer) honours it.
//
// What a transform does change: getBoundingClientRect distances are visual
// (scaled), scrollTop / scrollLeft are layout pixels. Code that moves a
// scroll position by a measured distance converts with toLayoutPx.

export const CANVAS_WIDTH = 1920;
export const CANVAS_HEIGHT = 1080;

/** The scale that fits the canvas in a w x h window: below 1 only when it's smaller. */
export function fitScale(width: number, height: number): number {
  if (!(width > 0) || !(height > 0)) return 1;
  const s = Math.min(width / CANVAS_WIDTH, height / CANVAS_HEIGHT);
  return s < 1 ? s : 1;
}

let current = 1;

/** The scale the layout applied (1 on a 1920x1080 plane). */
export function canvasScale(): number {
  return current;
}

/** Set by the root layout when it (re)computes the fit. */
export function setCanvasScale(scale: number): void {
  current = scale > 0 ? scale : 1;
}

/** A distance measured with getBoundingClientRect, in layout pixels (for scrollTop / scrollLeft). */
export function toLayoutPx(visual: number): number {
  return visual / current;
}
