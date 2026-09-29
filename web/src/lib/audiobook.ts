// Audiobook listening helpers shared by the audio player and the audiobook
// page: speed presets and clamping, where a single-file book's embedded
// chapters begin and end (sleep timer "end of chapter", chapter skips,
// resume), position labels and bookmark order. Pure, so they're
// unit-tested directly.

import type { Bookmark, Chapter } from '$lib/api';

/** Speeds the player offers. The server takes any rate from MIN_RATE to MAX_RATE. */
export const RATE_PRESETS: readonly number[] = [0.75, 1, 1.25, 1.5, 1.75, 2, 2.5, 3];
export const MIN_RATE = 0.5;
export const MAX_RATE = 3;

/** A playable speed: 1 for anything that isn't a finite number, else
 *  clamped to [MIN_RATE, MAX_RATE] and kept to two decimals, as the server
 *  stores it. */
export function clampRate(rate: unknown): number {
  if (typeof rate !== 'number' || !Number.isFinite(rate)) return 1;
  return Math.round(Math.min(MAX_RATE, Math.max(MIN_RATE, rate)) * 100) / 100;
}

/** "1×", "1.25×", "0.75×". */
export function formatRate(rate: number): string {
  return `${Number(rate.toFixed(2))}×`;
}

/** A position as h:mm:ss, or m:ss under an hour. */
export function formatPosition(ms: number): string {
  const total = Number.isFinite(ms) && ms > 0 ? Math.floor(ms / 1000) : 0;
  const h = Math.floor(total / 3600);
  const m = Math.floor((total % 3600) / 60);
  const s = String(total % 60).padStart(2, '0');
  return h > 0 ? `${h}:${String(m).padStart(2, '0')}:${s}` : `${m}:${s}`;
}

type Chapters = readonly Chapter[] | null | undefined;

/** Chapter start offsets, ascending and de-duplicated. */
function starts(chapters: Chapters): number[] {
  const out = (chapters ?? []).map((c) => c.start_ms).filter((v) => Number.isFinite(v) && v >= 0);
  return [...new Set(out)].sort((a, b) => a - b);
}

/** The chapter a position is in: the last one starting at or before it (a
 *  gap between chapters belongs to the one before). Null before the first
 *  chapter or when there are none. */
export function chapterAt(chapters: Chapters, positionMS: number): Chapter | null {
  let found: Chapter | null = null;
  for (const c of chapters ?? []) {
    if (!Number.isFinite(c.start_ms) || c.start_ms > positionMS) continue;
    if (!found || c.start_ms >= found.start_ms) found = c;
  }
  return found;
}

/** The next chapter boundary after a position — the next chapter start or
 *  chapter end, whichever comes first — or null when there is none (no
 *  chapters, or past the last one's end). */
export function nextChapterBoundary(chapters: Chapters, positionMS: number): number | null {
  let next: number | null = null;
  for (const c of chapters ?? []) {
    for (const b of [c.start_ms, c.end_ms > c.start_ms ? c.end_ms : NaN]) {
      if (Number.isFinite(b) && b > positionMS && (next === null || b < next)) next = b;
    }
  }
  return next;
}

/** Where Previous goes: the start of the current chapter once more than
 *  restartMS into it, else the previous chapter's start (the first
 *  chapter's own start at the top of the book). Null before the first
 *  chapter or when there are none. */
export function prevChapterStart(chapters: Chapters, positionMS: number, restartMS = 3000): number | null {
  const s = starts(chapters);
  let i = -1;
  while (i + 1 < s.length && s[i + 1] <= positionMS) i++;
  if (i < 0) return null;
  if (positionMS - s[i] > restartMS) return s[i];
  return s[Math.max(0, i - 1)];
}

/** Where Next goes: the next chapter's start, or null in the last chapter. */
export function nextChapterStart(chapters: Chapters, positionMS: number): number | null {
  return starts(chapters).find((v) => v > positionMS) ?? null;
}

/** Where a single-file book resumes: the start of the chapter the saved
 *  offset is in (rejoining narration mid-sentence is worse than hearing a
 *  few minutes again), the offset itself when there are no chapters, and
 *  the beginning when that's within 30 s of the end. */
export function resumeStartMS(chapters: Chapters, offsetMS: number, durationMS = 0): number {
  if (!Number.isFinite(offsetMS) || offsetMS <= 0) return 0;
  const at = (chapters ?? []).length > 0 ? (chapterAt(chapters, offsetMS)?.start_ms ?? 0) : offsetMS;
  if (durationMS > 0 && durationMS - at <= 30_000) return 0;
  return at;
}

/** The server's listening order: a single-file book's own bookmarks (and
 *  any chapter without an index) first, then by chapter index, then by
 *  position; ties by creation time, then id. */
export function compareBookmarks(a: Bookmark, b: Bookmark, bookId: string): number {
  const ia = a.item_id === bookId ? null : (a.item_index ?? null);
  const ib = b.item_id === bookId ? null : (b.item_index ?? null);
  if (ia !== ib) {
    if (ia === null) return -1;
    if (ib === null) return 1;
    return ia - ib;
  }
  if (a.position_ms !== b.position_ms) return a.position_ms - b.position_ms;
  if (a.created_at !== b.created_at) return a.created_at < b.created_at ? -1 : 1;
  return a.id < b.id ? -1 : a.id > b.id ? 1 : 0;
}

/** The list with `bookmark` added (or replacing one with its id), in
 *  listening order. */
export function insertBookmark(list: readonly Bookmark[], bookmark: Bookmark, bookId: string): Bookmark[] {
  return [...list.filter((b) => b.id !== bookmark.id), bookmark].sort((a, b) => compareBookmarks(a, b, bookId));
}

/** True when the listening routes answered "not here": 404 from a server
 *  without them (or a book the caller can't see), or 405 from a router that
 *  knows the path under another method. The UI hides bookmarks then. */
export function listeningUnavailable(e: unknown): boolean {
  const status = typeof e === 'object' && e !== null ? (e as { status?: unknown }).status : undefined;
  return status === 404 || status === 405;
}
