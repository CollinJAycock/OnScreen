import type { RemoteKey } from './keys';

type Direction = 'up' | 'down' | 'left' | 'right';

export interface Rect {
  top: number;
  left: number;
  right: number;
  bottom: number;
  cx: number;
  cy: number;
}

export function rectOf(el: Element): Rect {
  const r = el.getBoundingClientRect();
  return {
    top: r.top,
    left: r.left,
    right: r.right,
    bottom: r.bottom,
    cx: r.left + r.width / 2,
    cy: r.top + r.height / 2
  };
}

export function isDirection(k: RemoteKey): k is Direction {
  return k === 'up' || k === 'down' || k === 'left' || k === 'right';
}

// Above this many focusables a D-pad press measures only the ones near the
// current element in document order. A paged library grid can hold thousands
// of cards, and getBoundingClientRect on every one of them per press is
// visible key lag on a TV CPU. Screens below it (menus, detail pages, the
// first pages of a grid) keep the full search, unchanged.
export const CANDIDATE_LIMIT = 300;
// How far either side of the current element the windowed search looks:
// 150 cards is 25 rows of a six-wide grid, far more than the row above or
// below, and the start of a grid (where Up reaches the filter chips and the
// top nav) is inside the window of every card in its first rows.
export const CANDIDATE_RADIUS = 150;

/**
 * The candidates worth measuring for a move away from `current`: all of them
 * up to `limit`, else the `radius` either side of `current` in document
 * order (the card grids lay rows out in document order, so the neighbours in
 * every direction are close by). `current` missing from `all` (focus left a
 * scope) gets the full list.
 */
export function nearbyCandidates<T>(
  all: readonly T[],
  current: T,
  limit = CANDIDATE_LIMIT,
  radius = CANDIDATE_RADIUS
): readonly T[] {
  if (all.length <= limit) return all;
  const i = all.indexOf(current);
  if (i < 0) return all;
  return all.slice(Math.max(0, i - radius), i + radius + 1);
}

/**
 * pickNeighbor over the window nearbyCandidates gives, falling back to the
 * whole list only when nothing in the window lies that way: the edge of a
 * grid, or a control far away in document order. So a long grid measures a
 * few hundred cards per press instead of all of them, and a move the window
 * can't answer still finds what the full search would.
 */
export function pickNeighborNear(
  from: Element,
  all: Element[],
  dir: Direction,
  limit = CANDIDATE_LIMIT,
  radius = CANDIDATE_RADIUS
): Element | null {
  const near = nearbyCandidates(all, from, limit, radius);
  const next = pickNeighbor(from, near as Element[], dir);
  if (next || near.length === all.length) return next;
  return pickNeighbor(from, all, dir);
}

/**
 * Where an Up or Down press leaves a focus scope that lets the D-pad out of
 * its top and bottom edges only (the search page's on-screen keyboard):
 * the nearest element `outside` the scope that way, once nothing `inside` it
 * lies that way. Null leaves the press to the scope as usual: a Left or
 * Right (which stays in, so an overshoot at the end of a row can't land on
 * a filter chip or a result), a row still to move to, or nothing outside.
 */
export function verticalExit(
  from: Element,
  inside: Element[],
  outside: Element[],
  dir: Direction
): Element | null {
  if (dir !== 'up' && dir !== 'down') return null;
  if (pickNeighborNear(from, inside, dir)) return null;
  return pickNeighborNear(from, outside, dir);
}

/**
 * The focusable `dir` of `from`, chosen as Android's FocusFinder chooses
 * (findNextFocusInAbsoluteDirection): a candidate in the source's beam (the
 * band it covers across the move) beats one outside it, up to a point; the
 * rest goes by 13·major² + minor², major being the gap along the move and
 * minor the centre offset across it. Ties keep the earlier candidate.
 *
 * The squares are what let a short row be reached. The old score, the gap
 * plus twice the offset, let a few hundred pixels of offset outweigh a
 * whole row of height: Down from the right of the hub's "Demo Movies" row
 * skipped "Demo TV Shows" (two cards, far left) for the row under it, and
 * Up from an episode skipped the season chips for a hero button.
 */
export function pickNeighbor(
  from: Element,
  candidates: Element[],
  dir: Direction
): Element | null {
  const src = rectOf(from);
  let best: Element | null = null;
  let bestRect: Rect | null = null;

  for (const c of candidates) {
    if (c === from) continue;
    const r = rectOf(c);
    if (!inDirection(src, r, dir)) continue;
    if (!bestRect || isBetter(src, r, bestRect, dir)) {
      best = c;
      bestRect = r;
    }
  }
  return best;
}

// Rects this close count as touching: a focused element is scaled up 6%,
// which can push its edge a pixel or two past a neighbour's.
const EPS = 2;

function inDirection(src: Rect, tgt: Rect, dir: Direction): boolean {
  switch (dir) {
    case 'up':
      return tgt.bottom <= src.top + EPS;
    case 'down':
      return tgt.top >= src.bottom - EPS;
    case 'left':
      return tgt.right <= src.left + EPS;
    case 'right':
      return tgt.left >= src.right - EPS;
  }
}

/** FocusFinder.isBetterCandidate, for two candidates that both lie `dir`. */
function isBetter(src: Rect, a: Rect, b: Rect, dir: Direction): boolean {
  if (beamBeats(src, a, b, dir)) return true;
  if (beamBeats(src, b, a, dir)) return false;
  return weightedDistance(src, a, dir) < weightedDistance(src, b, dir);
}

/** Whether `r` overlaps the band `src` covers across a move `dir`. */
function inBeam(src: Rect, r: Rect, dir: Direction): boolean {
  return dir === 'left' || dir === 'right'
    ? r.bottom > src.top && r.top < src.bottom
    : r.right > src.left && r.left < src.right;
}

/**
 * `a` wins by beam: it's in the source's beam and `b` isn't. For Left /
 * Right that settles it (the next card in the source's own row, before one
 * in the row below). For Up / Down only while `b` doesn't lie wholly
 * nearer than `a`: a short row off to one side, between the source and the
 * row in line with it, is then weighed by distance like the rest
 * (FocusFinder.beamBeats).
 */
function beamBeats(src: Rect, a: Rect, b: Rect, dir: Direction): boolean {
  if (!inBeam(src, a, dir) || inBeam(src, b, dir)) return false;
  if (dir === 'left' || dir === 'right') return true;
  return majorDistance(src, a, dir) < majorDistanceToFarEdge(src, b, dir);
}

/** The gap between the source's leading edge and the target's near edge. */
function majorDistance(src: Rect, r: Rect, dir: Direction): number {
  switch (dir) {
    case 'up':
      return Math.max(0, src.top - r.bottom);
    case 'down':
      return Math.max(0, r.top - src.bottom);
    case 'left':
      return Math.max(0, src.left - r.right);
    case 'right':
      return Math.max(0, r.left - src.right);
  }
}

/** The same edge to the target's far edge. */
function majorDistanceToFarEdge(src: Rect, r: Rect, dir: Direction): number {
  switch (dir) {
    case 'up':
      return Math.max(1, src.top - r.top);
    case 'down':
      return Math.max(1, r.bottom - src.bottom);
    case 'left':
      return Math.max(1, src.left - r.left);
    case 'right':
      return Math.max(1, r.right - src.right);
  }
}

/** FocusFinder.getWeightedDistanceFor: the gap along the move weighs 13×
 *  (squared) what the centre offset across it does. */
function weightedDistance(src: Rect, r: Rect, dir: Direction): number {
  const major = majorDistance(src, r, dir);
  const minor = dir === 'left' || dir === 'right' ? Math.abs(src.cy - r.cy) : Math.abs(src.cx - r.cx);
  return 13 * major * major + minor * minor;
}
