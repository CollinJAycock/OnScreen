import { describe, expect, it } from 'vitest';
import {
  FIND_ONLINE_LABEL,
  actionRowKey,
  barSeekMs,
  chapterAt,
  chapterRowLabel,
  formatClock,
  pickerWindow,
  playerActions,
  subtitlePickerRows,
  transportButtons,
  transportSteps,
  transportLabel,
} from './actions';

describe('playerActions', () => {
  const video = { video: true, subtitleTracks: 2, onlineSubtitles: false };

  it('offers Audio (≥ 1 track), Subtitles, Chapters (≥ 2) for video', () => {
    expect(playerActions({ ...video, switchableAudioTracks: 1, chapters: 2 })).toEqual(['audio', 'subtitles', 'chapters']);
    expect(playerActions({ ...video, switchableAudioTracks: 0, chapters: 1 })).toEqual(['subtitles']);
    expect(playerActions({ ...video, switchableAudioTracks: 3, chapters: 0 })).toEqual(['audio', 'subtitles']);
  });

  it('offers Subtitles for a file without tracks only with the online search', () => {
    const none = { video: true, switchableAudioTracks: 1, chapters: 0, subtitleTracks: 0 };
    expect(playerActions({ ...none, onlineSubtitles: false })).toEqual(['audio']);
    expect(playerActions({ ...none, onlineSubtitles: true })).toEqual(['audio', 'subtitles']);
    // A track is enough without it.
    expect(playerActions({ ...none, subtitleTracks: 1, onlineSubtitles: false })).toEqual(['audio', 'subtitles']);
  });

  it('offers nothing for music / books (their own view)', () => {
    expect(playerActions({ video: false, switchableAudioTracks: 2, subtitleTracks: 3, onlineSubtitles: true, chapters: 9 })).toEqual([]);
  });
});

describe('subtitlePickerRows', () => {
  const tracks = [
    { key: 'emb:2', label: 'English' },
    { key: 'ext:7', label: 'French' },
  ];

  it('lists Off and the tracks, with no "Find more online…" on a server without the search', () => {
    const rows = subtitlePickerRows(tracks, 'ext:7', false);
    expect(rows.map((r) => r.label)).toEqual(['Off', 'English', 'French']);
    expect(rows.some((r) => r.action)).toBe(false);
    expect(rows.map((r) => r.current)).toEqual([false, false, true]);
  });

  it('ends with "Find more online…" (an action row) when the server has it', () => {
    const rows = subtitlePickerRows(tracks, null, true);
    expect(rows.map((r) => r.label)).toEqual(['Off', 'English', 'French', FIND_ONLINE_LABEL]);
    expect(rows[3]).toEqual({ label: FIND_ONLINE_LABEL, current: false, action: true });
    expect(rows[0].current).toBe(true);
    // A file without tracks: Off and the search.
    expect(subtitlePickerRows([], null, true).map((r) => r.label)).toEqual(['Off', FIND_ONLINE_LABEL]);
  });

  it("adds a track's status to its label", () => {
    const rows = subtitlePickerRows(tracks, 'emb:2', false, (k) => (k === 'emb:2' ? ' · loading…' : ''));
    expect(rows[1].label).toBe('English · loading…');
    expect(rows[2].label).toBe('French');
  });
});

describe("the pointer's playback controls", () => {
  it('offers previous / next only where CH ▲▼ step somewhere', () => {
    expect(transportButtons({ prev: true, next: true })).toEqual(['prev', 'back', 'playpause', 'forward', 'next']);
    expect(transportButtons({ prev: false, next: false })).toEqual(['back', 'playpause', 'forward']);
    // The first track of an album: no previous; the last: no next.
    expect(transportButtons({ prev: false, next: true })).toEqual(['back', 'playpause', 'forward', 'next']);
    expect(transportButtons({ prev: true, next: false })).toEqual(['prev', 'back', 'playpause', 'forward']);
  });

  describe('transportSteps', () => {
    const base = { prevSibling: false, nextSibling: false, chapterStarts: [] as number[], positionMs: 0 };

    it("a queue item steps to its siblings, and only where there's one", () => {
      expect(transportSteps({ ...base, step: 'item' })).toEqual({ prev: false, next: false });
      expect(transportSteps({ ...base, step: 'item', nextSibling: true })).toEqual({ prev: false, next: true });
      expect(transportSteps({ ...base, step: 'item', prevSibling: true })).toEqual({ prev: true, next: false });
    });

    // jumpToChapter: back always lands somewhere (the chapter's start, or
    // the one before); next has nowhere to go from the last chapter.
    it('chapters step back always, and on only before the last one', () => {
      const chapterStarts = [0, 60_000, 120_000];
      expect(transportSteps({ ...base, step: 'chapter', chapterStarts, positionMs: 30_000 })).toEqual({
        prev: true,
        next: true,
      });
      expect(transportSteps({ ...base, step: 'chapter', chapterStarts, positionMs: 130_000 })).toEqual({
        prev: true,
        next: false,
      });
      // Within jumpToChapter's 2 s of the last start: that's on it already.
      expect(transportSteps({ ...base, step: 'chapter', chapterStarts, positionMs: 119_000 })).toEqual({
        prev: true,
        next: false,
      });
    });

    it('nothing steps where CH ▲▼ do nothing', () => {
      expect(transportSteps({ ...base, step: 'none', prevSibling: true, nextSibling: true })).toEqual({
        prev: false,
        next: false,
      });
    });
  });

  it('labels play / pause by the state', () => {
    expect(transportLabel('playpause', true)).toBe('▶');
    expect(transportLabel('playpause', false)).toBe('❚❚');
    expect(transportLabel('back', false)).toBe('-10s');
    expect(transportLabel('forward', false)).toBe('+10s');
  });

  it('seeks to where the bar was clicked, clamped to the item', () => {
    // A 1000 px bar from x 200 on a 100 s item.
    expect(barSeekMs(200, 200, 1000, 100_000)).toBe(0);
    expect(barSeekMs(450, 200, 1000, 100_000)).toBe(25_000);
    expect(barSeekMs(1200, 200, 1000, 100_000)).toBe(100_000);
    expect(barSeekMs(100, 200, 1000, 100_000)).toBe(0);
    expect(barSeekMs(1500, 200, 1000, 100_000)).toBe(100_000);
    // Nothing to seek in yet.
    expect(barSeekMs(450, 200, 1000, 0)).toBeNull();
    expect(barSeekMs(450, 200, 0, 100_000)).toBeNull();
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
