// Reading-position sync for the book reader: the position is kept on the
// server (GET/PUT /items/{id}/reading-progress) so another device picks up
// where this one left off, with a localStorage copy as the fallback when the
// server can't be reached. Pure helpers, so they are unit-tested without
// mounting the reader.
import type { ReadingProgress } from '$lib/api';

/** A position as this browser saw it. */
export interface LocalPosition {
  locator: string;
  progression: number;
  updated_at: string;
}

const KEY_PREFIX = 'onscreen_reading_';

/** The locally stored position for a book, or null (absent, unreadable, or
 *  storage blocked). */
export function readLocalPosition(itemId: string): LocalPosition | null {
  try {
    const raw = localStorage.getItem(KEY_PREFIX + itemId);
    if (!raw) return null;
    const p = JSON.parse(raw) as LocalPosition;
    if (typeof p?.locator !== 'string' || typeof p.progression !== 'number' || typeof p.updated_at !== 'string') {
      return null;
    }
    return p;
  } catch {
    return null;
  }
}

export function writeLocalPosition(itemId: string, p: LocalPosition): void {
  try {
    localStorage.setItem(KEY_PREFIX + itemId, JSON.stringify(p));
  } catch {
    // Storage blocked or full: the server copy still syncs.
  }
}

/** How far ahead (as a share of the book) the server's position must be
 *  before it's worth interrupting the reader about. */
export const MEANINGFUL_AHEAD = 0.01;

export type StartDecision =
  /** Open at this position without asking. */
  | { kind: 'open'; position: LocalPosition | ReadingProgress }
  /** Open where the reader was here, and offer the newer one from another
   *  device. */
  | { kind: 'offer'; position: LocalPosition | null; offer: ReadingProgress }
  /** Nothing saved anywhere: start at the beginning (or ?p=). */
  | { kind: 'none' };

/** Decide where the reader opens.
 *
 *  - An explicit page in the URL (a shared / refreshed link) wins; a server
 *    position meaningfully ahead of it is offered.
 *  - With nothing saved locally, the server's position is used as is.
 *  - When both exist, the newer one is the reader's real position. If that's
 *    the server's and it is meaningfully ahead of this browser's, the reader
 *    opens where it was here and is offered the jump ("Continue from page 40
 *    on Phone") rather than being moved under their feet.
 */
export function decideStart(
  local: LocalPosition | null,
  server: ReadingProgress | null,
  explicitPage: boolean,
): StartDecision {
  if (explicitPage) {
    if (server && (!local || server.progression - local.progression >= MEANINGFUL_AHEAD)) {
      return { kind: 'offer', position: null, offer: server };
    }
    return { kind: 'none' };
  }
  if (!server) return local ? { kind: 'open', position: local } : { kind: 'none' };
  if (!local) return { kind: 'open', position: server };
  const serverNewer = Date.parse(server.updated_at) > Date.parse(local.updated_at);
  if (!serverNewer) return { kind: 'open', position: local };
  if (server.progression - local.progression >= MEANINGFUL_AHEAD) {
    return { kind: 'offer', position: local, offer: server };
  }
  // Newer but not meaningfully ahead (a re-read, a step back): follow it.
  return { kind: 'open', position: server };
}

/** Page-based formats store the 1-indexed page number as the locator. */
export function pageLocator(page: number): string {
  return String(page);
}

/** The page a locator names, or null when it isn't a page number (an EPUB
 *  CFI). */
export function pageFromLocator(locator: string): number | null {
  if (!/^\d+$/.test(locator)) return null;
  const n = parseInt(locator, 10);
  return n >= 1 ? n : null;
}

/** Progression for a page-based book: the last page reads as 1 (finished). */
export function pageProgression(page: number, pageCount: number): number {
  if (pageCount <= 0) return 0;
  return Math.min(1, Math.max(0, page / pageCount));
}

/** "page 40" for page books, "42%" for EPUB. */
export function describePosition(p: { locator: string; progression: number }): string {
  const page = pageFromLocator(p.locator);
  if (page !== null) return `page ${page}`;
  return `${Math.round(p.progression * 100)}%`;
}

/** Calls fn at most once per `ms`, trailing; flush() runs a pending call now
 *  (page hide / close), cancel() drops it. */
export function debounce<A extends unknown[]>(fn: (...args: A) => void, ms: number) {
  let timer: ReturnType<typeof setTimeout> | null = null;
  let pending: A | null = null;
  const run = () => {
    timer = null;
    if (pending) {
      const args = pending;
      pending = null;
      fn(...args);
    }
  };
  return {
    call(...args: A) {
      pending = args;
      if (timer) clearTimeout(timer);
      timer = setTimeout(run, ms);
    },
    flush() {
      if (timer) clearTimeout(timer);
      run();
    },
    cancel() {
      if (timer) clearTimeout(timer);
      timer = null;
      pending = null;
    },
    get pending() {
      return pending !== null;
    },
  };
}
