import { describe, expect, it } from 'vitest';
import {
  actionRowKey,
  chapterAt,
  chapterRowLabel,
  formatClock,
  pickerWindow,
  playerActions,
} from './actions';

describe('playerActions', () => {
  it('offers Audio (≥ 1 track), Subtitles always, Chapters (≥ 2) for video', () => {
    expect(playerActions({ video: true, switchableAudioTracks: 1, chapters: 2 })).toEqual(['audio', 'subtitles', 'chapters']);
    expect(playerActions({ video: true, switchableAudioTracks: 0, chapters: 1 })).toEqual(['subtitles']);
    expect(playerActions({ video: true, switchableAudioTracks: 3, chapters: 0 })).toEqual(['audio', 'subtitles']);
  });

  it('offers nothing for music / books (their own view)', () => {
    expect(playerActions({ video: false, switchableAudioTracks: 2, chapters: 9 })).toEqual([]);
  });
});

describe('actionRowKey (arrows + OK only)', () => {
  it('Down enters the row; nothing else does', () => {
    expect(actionRowKey(-1, 3, 'down')).toEqual({ kind: 'focus', index: 0 });
    expect(actionRowKey(-1, 3, 'left')).toEqual({ kind: 'pass' });
    expect(actionRowKey(-1, 3, 'enter')).toEqual({ kind: 'pass' });
    expect(actionRowKey(-1, 0, 'down')).toEqual({ kind: 'pass' });
  });

  it('Down with the controls hidden only reveals them (leanback)', () => {
    expect(actionRowKey(-1, 3, 'down', false)).toEqual({ kind: 'reveal' });
    // Even with no buttons: the first press shows the bar.
    expect(actionRowKey(-1, 0, 'down', false)).toEqual({ kind: 'reveal' });
    expect(actionRowKey(-1, 3, 'enter', false)).toEqual({ kind: 'pass' });
    expect(actionRowKey(-1, 3, 'left', false)).toEqual({ kind: 'pass' });
    // Up passes: it may be the Up that returns to the Up Next card, which the
    // page asks after the row; the page's own Up shows the controls.
    expect(actionRowKey(-1, 3, 'up', false)).toEqual({ kind: 'pass' });
    // The second Down, with the controls up, focuses the row.
    expect(actionRowKey(-1, 3, 'down', true)).toEqual({ kind: 'focus', index: 0 });
  });

  it('←/→ move along it without wrapping, OK opens, ↑ / Back leave', () => {
    expect(actionRowKey(0, 3, 'right')).toEqual({ kind: 'focus', index: 1 });
    expect(actionRowKey(2, 3, 'right')).toEqual({ kind: 'focus', index: 2 });
    expect(actionRowKey(0, 3, 'left')).toEqual({ kind: 'focus', index: 0 });
    expect(actionRowKey(1, 3, 'enter')).toEqual({ kind: 'activate', index: 1 });
    expect(actionRowKey(1, 3, 'up')).toEqual({ kind: 'leave' });
    expect(actionRowKey(1, 3, 'back')).toEqual({ kind: 'leave' });
    expect(actionRowKey(1, 3, 'down')).toEqual({ kind: 'focus', index: 1 });
  });

  it('lets playback keys through while focused', () => {
    expect(actionRowKey(1, 3, 'playpause')).toEqual({ kind: 'pass' });
    expect(actionRowKey(1, 3, 'yellow')).toEqual({ kind: 'pass' });
    expect(actionRowKey(1, 3, 'forward')).toEqual({ kind: 'pass' });
    expect(actionRowKey(1, 3, 'channelUp')).toEqual({ kind: 'pass' });
    expect(actionRowKey(-1, 3, 'channelDown', false)).toEqual({ kind: 'pass' });
  });

  it('re-enters at the first button when the row shrank under the focus', () => {
    expect(actionRowKey(2, 1, 'down')).toEqual({ kind: 'focus', index: 0 });
  });
});

describe('chapter picker rows', () => {
  it('reads "N. <title or Chapter N> · m:ss"', () => {
    expect(chapterRowLabel({ title: 'Opening', start_ms: 0 }, 0)).toBe('1. Opening · 0:00');
    expect(chapterRowLabel({ title: '  ', start_ms: 754_000 }, 3)).toBe('4. Chapter 4 · 12:34');
    expect(chapterRowLabel({ title: 'Finale', start_ms: 3_723_000 }, 11)).toBe('12. Finale · 1:02:03');
  });

  it('formats clocks like Android', () => {
    expect(formatClock(59_999)).toBe('0:59');
    expect(formatClock(3_600_000)).toBe('1:00:00');
    expect(formatClock(-5)).toBe('0:00');
  });

  it('opens on the chapter playing', () => {
    const ch = [{ start_ms: 0 }, { start_ms: 60_000 }, { start_ms: 120_000 }];
    expect(chapterAt(ch, 90_000)).toBe(1);
    expect(chapterAt(ch, 120_000)).toBe(2);
    expect(chapterAt([{ start_ms: 5_000 }, { start_ms: 9_000 }], 1_000)).toBe(0);
    expect(chapterAt([], 1_000)).toBe(-1);
  });
});

describe('pickerWindow', () => {
  it('draws everything when it fits', () => {
    expect(pickerWindow(3, 5, 9)).toEqual({ start: 0, end: 5 });
    expect(pickerWindow(0, 0, 9)).toEqual({ start: 0, end: 0 });
  });

  it('keeps the cursor on screen in a long list, near the middle', () => {
    expect(pickerWindow(0, 40, 9)).toEqual({ start: 0, end: 9 });
    expect(pickerWindow(20, 40, 9)).toEqual({ start: 16, end: 25 });
    expect(pickerWindow(39, 40, 9)).toEqual({ start: 31, end: 40 });
    for (let c = 0; c < 40; c++) {
      const w = pickerWindow(c, 40, 9);
      expect(c >= w.start && c < w.end).toBe(true);
      expect(w.end - w.start).toBe(9);
    }
  });
});
