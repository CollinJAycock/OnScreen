// Trickplay scrub-preview helpers.
//
// OnScreen's trickplay shape (matches Plex / Emby / Jellyfin
// conventions): a WebVTT index + N sprite-sheet JPEGs. Each VTT
// cue carries a `sprite_N.jpg#xywh=x,y,w,h` URL fragment naming
// the rectangular region inside that sprite that should be shown
// for the cue's time range.
//
// Render side: the watch screen wraps a `<div>` sized to (w, h)
// with `background-image: url(spriteUrl)` and
// `background-position: -x,-y` so only the cue's region is
// visible. No client-side image manipulation, no canvas — the
// browser's native sprite rendering does it.

export interface TrickplayCue {
  startMs: number;
  endMs: number;
  spritePath: string;
  x: number;
  y: number;
  w: number;
  h: number;
}

export function parseVtt(text: string): TrickplayCue[] {
  if (!text) return [];
  const lines = text.replace(/\r/g, '').split('\n');
  const cues: TrickplayCue[] = [];

  let state: 'timing' | 'payload' = 'timing';
  let pendingStart = 0;
  let pendingEnd = 0;

  for (const raw of lines) {
    const line = raw.trim();
    if (line === '') {
      state = 'timing';
      continue;
    }
    if (state === 'timing') {
      const arrowAt = line.indexOf('-->');
      if (arrowAt < 0) continue;
      const lhs = line.slice(0, arrowAt).trim();
      const rhs = line.slice(arrowAt + 3).trim();
      const start = parseTimestampMs(lhs);
      const end = parseTimestampMs(rhs);
      if (start >= 0 && end > start) {
        pendingStart = start;
        pendingEnd = end;
        state = 'payload';
      }
    } else {
      const cue = parseCueLine(line, pendingStart, pendingEnd);
      if (cue) cues.push(cue);
      state = 'timing';
    }
  }

  return cues;
}

export function findCue(cues: TrickplayCue[], posMs: number): TrickplayCue | null {
  for (const cue of cues) {
    if (posMs >= cue.startMs && posMs < cue.endMs) return cue;
  }
  return null;
}

function parseTimestampMs(s: string): number {
  if (!s) return -1;
  const dotAt = s.indexOf('.');
  let head = s;
  let millis = 0;
  if (dotAt >= 0) {
    head = s.slice(0, dotAt);
    let tail = s.slice(dotAt + 1);
    if (tail.length > 0) {
      if (tail.length === 1) tail = tail + '00';
      else if (tail.length === 2) tail = tail + '0';
      else if (tail.length > 3) tail = tail.slice(0, 3);
      millis = parseInt(tail, 10);
      if (isNaN(millis)) millis = 0;
    }
  }
  const parts = head.split(':');
  let h = 0,
    mn = 0,
    sec = 0;
  if (parts.length === 3) {
    h = parseInt(parts[0], 10);
    mn = parseInt(parts[1], 10);
    sec = parseInt(parts[2], 10);
  } else if (parts.length === 2) {
    mn = parseInt(parts[0], 10);
    sec = parseInt(parts[1], 10);
  } else {
    return -1;
  }
  if ([h, mn, sec].some(isNaN)) return -1;
  return (h * 3600 + mn * 60 + sec) * 1000 + millis;
}

function parseCueLine(line: string, startMs: number, endMs: number): TrickplayCue | null {
  const hashAt = line.indexOf('#xywh=');
  if (hashAt < 0) return null;
  const spritePath = line.slice(0, hashAt).trim();
  const coords = line.slice(hashAt + '#xywh='.length).split(',');
  if (coords.length !== 4) return null;
  const [x, y, w, h] = coords.map((c) => parseInt(c, 10));
  if ([x, y, w, h].some((n) => isNaN(n))) return null;
  return { startMs, endMs, spritePath, x, y, w, h };
}
