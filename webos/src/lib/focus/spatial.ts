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

export function pickNeighbor(
  from: Element,
  candidates: Element[],
  dir: Direction
): Element | null {
  const src = rectOf(from);
  let best: Element | null = null;
  let bestScore = Infinity;

  for (const c of candidates) {
    if (c === from) continue;
    const r = rectOf(c);
    if (!inDirection(src, r, dir)) continue;

    const score = distance(src, r, dir);
    if (score < bestScore) {
      bestScore = score;
      best = c;
    }
  }
  return best;
}

function inDirection(src: Rect, tgt: Rect, dir: Direction): boolean {
  const eps = 2;
  switch (dir) {
    case 'up':
      return tgt.bottom <= src.top + eps;
    case 'down':
      return tgt.top >= src.bottom - eps;
    case 'left':
      return tgt.right <= src.left + eps;
    case 'right':
      return tgt.left >= src.right - eps;
  }
}

// Score = primary-axis distance + lateral misalignment penalty.
// Weighting lateral misalignment heavily keeps "down" from jumping sideways
// across rows when there's a closer neighbor directly below.
function distance(src: Rect, tgt: Rect, dir: Direction): number {
  const axis = dir === 'up' || dir === 'down' ? 'y' : 'x';
  if (axis === 'y') {
    const dy = dir === 'up' ? src.top - tgt.bottom : tgt.top - src.bottom;
    const dx = Math.abs(src.cx - tgt.cx);
    return dy + dx * 2;
  } else {
    const dx = dir === 'left' ? src.left - tgt.right : tgt.left - src.right;
    const dy = Math.abs(src.cy - tgt.cy);
    return dx + dy * 2;
  }
}
