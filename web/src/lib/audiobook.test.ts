import { describe, it, expect } from 'vitest';
import type { Bookmark, Chapter } from '$lib/api';
import {
  RATE_PRESETS,
  chapterAt,
  clampRate,
  compareBookmarks,
  formatPosition,
  formatRate,
  insertBookmark,
  listeningUnavailable,
  nextChapterBoundary,
  nextChapterStart,
  prevChapterStart,
  resumeStartMS,
} from './audiobook';

const ch = (title: string, start_ms: number, end_ms: number): Chapter => ({ title, start_ms, end_ms });
// Three back-to-back chapters: 0-60 s, 60-150 s, 150-300 s.
const chapters = [ch('One', 0, 60_000), ch('Two', 60_000, 150_000), ch('Three', 150_000, 300_000)];

describe('clampRate', () => {
  it('keeps presets as they are', () => {
    for (const r of RATE_PRESETS) expect(clampRate(r)).toBe(r);
  });
  it('clamps to 0.5-3', () => {
    expect(clampRate(0.1)).toBe(0.5);
    expect(clampRate(4)).toBe(3);
  });
  it('rounds to two decimals, like the server', () => {
    expect(clampRate(1.2500000476837158)).toBe(1.25);
    expect(clampRate(1.333)).toBe(1.33);
  });
  it.each([NaN, Infinity, -Infinity, '1.5', null, undefined])('treats %s as 1', (v) => {
    expect(clampRate(v)).toBe(1);
  });
});

describe('formatRate', () => {
  it('drops trailing zeros', () => {
    expect(formatRate(1)).toBe('1×');
    expect(formatRate(1.5)).toBe('1.5×');
    expect(formatRate(1.25)).toBe('1.25×');
    expect(formatRate(0.75)).toBe('0.75×');
  });
});

describe('formatPosition', () => {
  it('uses m:ss under an hour and h:mm:ss above', () => {
    expect(formatPosition(0)).toBe('0:00');
    expect(formatPosition(61_999)).toBe('1:01');
    expect(formatPosition(59 * 60_000 + 59_000)).toBe('59:59');
    expect(formatPosition(3_600_000)).toBe('1:00:00');
    expect(formatPosition(10 * 3_600_000 + 5 * 60_000 + 7_000)).toBe('10:05:07');
  });
  it('shows garbage as 0:00', () => {
    expect(formatPosition(-5)).toBe('0:00');
    expect(formatPosition(NaN)).toBe('0:00');
  });
});

describe('chapterAt', () => {
  it('finds the chapter a position is in', () => {
    expect(chapterAt(chapters, 0)?.title).toBe('One');
    expect(chapterAt(chapters, 59_999)?.title).toBe('One');
    expect(chapterAt(chapters, 60_000)?.title).toBe('Two');
    expect(chapterAt(chapters, 299_000)?.title).toBe('Three');
  });
  it('gives a gap to the chapter before it and works on unsorted lists', () => {
    const gappy = [ch('B', 100_000, 200_000), ch('A', 0, 50_000)];
    expect(chapterAt(gappy, 75_000)?.title).toBe('A');
    expect(chapterAt(gappy, 150_000)?.title).toBe('B');
  });
  it('is null before the first chapter or without chapters', () => {
    expect(chapterAt([ch('Late', 5_000, 9_000)], 1_000)).toBeNull();
    expect(chapterAt([], 1_000)).toBeNull();
    expect(chapterAt(undefined, 1_000)).toBeNull();
  });
});

describe('nextChapterBoundary', () => {
  it('is the end of the chapter the position is in', () => {
    expect(nextChapterBoundary(chapters, 10_000)).toBe(60_000);
    expect(nextChapterBoundary(chapters, 60_000)).toBe(150_000); // at a start: that chapter's end
    expect(nextChapterBoundary(chapters, 200_000)).toBe(300_000);
  });
  it('stops at the end of a chapter before a gap', () => {
    const gappy = [ch('A', 0, 50_000), ch('B', 100_000, 200_000)];
    expect(nextChapterBoundary(gappy, 10_000)).toBe(50_000);
    expect(nextChapterBoundary(gappy, 60_000)).toBe(100_000);
  });
  it('is null past the last boundary or without chapters', () => {
    expect(nextChapterBoundary(chapters, 300_000)).toBeNull();
    expect(nextChapterBoundary([], 0)).toBeNull();
    expect(nextChapterBoundary(undefined, 0)).toBeNull();
  });
  it('ignores an end that is not after its start', () => {
    expect(nextChapterBoundary([ch('Bad', 0, 0), ch('Next', 90_000, 90_000)], 10_000)).toBe(90_000);
  });
});

describe('prevChapterStart / nextChapterStart', () => {
  it('restarts the current chapter once past 3 s into it', () => {
    expect(prevChapterStart(chapters, 70_000)).toBe(60_000);
  });
  it('goes to the previous chapter near its start', () => {
    expect(prevChapterStart(chapters, 61_000)).toBe(0);
    expect(prevChapterStart(chapters, 150_000)).toBe(60_000);
  });
  it('stays on the first chapter at the top of the book', () => {
    expect(prevChapterStart(chapters, 1_000)).toBe(0);
  });
  it('is null without chapters or before the first one', () => {
    expect(prevChapterStart([], 70_000)).toBeNull();
    expect(prevChapterStart([ch('Late', 5_000, 9_000)], 1_000)).toBeNull();
  });
  it('next goes to the following chapter, null in the last', () => {
    expect(nextChapterStart(chapters, 0)).toBe(60_000);
    expect(nextChapterStart(chapters, 60_000)).toBe(150_000);
    expect(nextChapterStart(chapters, 200_000)).toBeNull();
    expect(nextChapterStart(undefined, 0)).toBeNull();
  });
});

describe('resumeStartMS', () => {
  it('snaps back to the start of the chapter', () => {
    expect(resumeStartMS(chapters, 100_000, 300_000)).toBe(60_000);
  });
  it('resumes at the offset itself without chapters', () => {
    expect(resumeStartMS([], 100_000, 300_000)).toBe(100_000);
  });
  it('starts over within 30 s of the end', () => {
    expect(resumeStartMS([], 280_000, 300_000)).toBe(0);
  });
  it('starts at 0 with nothing saved', () => {
    expect(resumeStartMS(chapters, 0, 300_000)).toBe(0);
    expect(resumeStartMS(chapters, NaN, 300_000)).toBe(0);
  });
});

describe('bookmark order', () => {
  const bm = (id: string, item_id: string, position_ms: number, item_index?: number, created_at = '2026-09-01T00:00:00Z'): Bookmark => ({
    id, item_id, item_title: item_id, item_index, position_ms, note: '', created_at,
  });

  it('orders a multi-file book by chapter index, then position', () => {
    const list = [bm('a', 'c2', 5_000, 2), bm('b', 'c1', 90_000, 1), bm('c', 'c1', 10_000, 1)];
    expect([...list].sort((x, y) => compareBookmarks(x, y, 'book')).map((b) => b.id)).toEqual(['c', 'b', 'a']);
  });

  it('puts the book’s own bookmarks and index-less chapters first, ties by creation time then id', () => {
    const list = [
      bm('z', 'c1', 1_000, 1),
      bm('y', 'book', 1_000, undefined, '2026-09-02T00:00:00Z'),
      bm('x', 'book', 1_000, undefined, '2026-09-01T00:00:00Z'),
      bm('w', 'cx', 500),
    ];
    expect([...list].sort((a, b) => compareBookmarks(a, b, 'book')).map((b) => b.id)).toEqual(['w', 'x', 'y', 'z']);
  });

  it('inserts in place and replaces a bookmark with the same id', () => {
    const list = [bm('a', 'book', 1_000), bm('c', 'book', 3_000)];
    expect(insertBookmark(list, bm('b', 'book', 2_000), 'book').map((b) => b.id)).toEqual(['a', 'b', 'c']);
    const moved = insertBookmark(list, { ...bm('a', 'book', 4_000), note: 'later' }, 'book');
    expect(moved.map((b) => b.id)).toEqual(['c', 'a']);
    expect(moved[1].note).toBe('later');
    expect(list).toHaveLength(2); // not mutated
  });
});

describe('listeningUnavailable', () => {
  it('is true for 404 and 405 only', () => {
    expect(listeningUnavailable({ status: 404 })).toBe(true);
    expect(listeningUnavailable({ status: 405 })).toBe(true);
    expect(listeningUnavailable({ status: 409 })).toBe(false);
    expect(listeningUnavailable(new Error('network'))).toBe(false);
    expect(listeningUnavailable(null)).toBe(false);
  });
});
