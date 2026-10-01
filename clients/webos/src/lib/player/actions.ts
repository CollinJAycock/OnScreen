// The player's on-screen actions (Audio, Subtitles, Chapters) and its pickers.
// Most Magic Remotes have no colour keys, and those were the only way to the
// audio / subtitle pickers (yellow / blue) and to chapters (red / green
// stepping), so on most LG TVs they were unreachable. Like Android's
// secondary actions: with the controls up, Down focuses a row of buttons,
// ←/→ move along it, OK opens the picker, ↑ or Back leaves it; the pointer
// clicks them too. The colour keys keep working. Pure: the page keeps the
// state, these decide.

import type { RemoteKey } from '../focus/keys';

export type PlayerAction = 'audio' | 'subtitles' | 'chapters';

export const ACTION_LABELS: Record<PlayerAction, string> = {
  audio: 'Audio',
  subtitles: 'Subtitles',
  chapters: 'Chapters',
};

/**
 * The buttons a video shows, in order (Android's refreshSecondaryActions):
 *  - Audio with any track the player can switch (≥ 1: "1 of 1" still says
 *    what is playing). Not for the file's direct stream, which has no
 *    session to re-issue.
 *  - Subtitles always for video: even a file with none offers Off and
 *    "Find more online…".
 *  - Chapters with ≥ 2 (a single chapter is the whole film).
 * None for music and audiobooks (their own now-playing view).
 */
export function playerActions(o: { video: boolean; switchableAudioTracks: number; chapters: number }): PlayerAction[] {
  if (!o.video) return [];
  const out: PlayerAction[] = [];
  if (o.switchableAudioTracks >= 1) out.push('audio');
  out.push('subtitles');
  if (o.chapters >= 2) out.push('chapters');
  return out;
}

export type ActionRowStep =
  /** Focus (or move to) this button. */
  | { kind: 'focus'; index: number }
  /** Off the row (↑ / Back), back to plain playback keys. */
  | { kind: 'leave' }
  /** OK on a button: open its picker. */
  | { kind: 'activate'; index: number }
  /** Down with the controls hidden: only bring them up. */
  | { kind: 'reveal' }
  /** Not the row's key: play / pause, colour keys, ◀◀ ▶▶ work as usual. */
  | { kind: 'pass' };

/** What a key does to the action row. `focus` is the focused button, -1
 *  when the row isn't focused (only Down enters it then). ←/→ stop at the
 *  ends rather than wrap or fall through to a seek. With the controls hidden
 *  (`visible` false) Down only shows them, as leanback's first D-pad press
 *  does: it used to focus Audio at once, so the OK meant to pause opened the
 *  Audio picker and ←/→ stopped seeking for the next 8 s. */
export function actionRowKey(focus: number, count: number, key: RemoteKey, visible = true): ActionRowStep {
  if (!visible) return key === 'down' ? { kind: 'reveal' } : { kind: 'pass' };
  if (count <= 0) return { kind: 'pass' };
  if (focus < 0 || focus >= count) return key === 'down' ? { kind: 'focus', index: 0 } : { kind: 'pass' };
  switch (key) {
    case 'left':
      return { kind: 'focus', index: Math.max(0, focus - 1) };
    case 'right':
      return { kind: 'focus', index: Math.min(count - 1, focus + 1) };
    case 'down':
      return { kind: 'focus', index: focus };
    case 'enter':
      return { kind: 'activate', index: focus };
    case 'up':
    case 'back':
      return { kind: 'leave' };
    default:
      return { kind: 'pass' };
  }
}

/** m:ss, or h:mm:ss from an hour (Android's fmtTimecode). */
export function formatClock(ms: number): string {
  const s = Math.max(0, Math.floor(ms / 1000));
  const h = Math.floor(s / 3600);
  const m = Math.floor((s % 3600) / 60);
  const sec = s % 60;
  return h > 0
    ? `${h}:${String(m).padStart(2, '0')}:${String(sec).padStart(2, '0')}`
    : `${m}:${String(sec).padStart(2, '0')}`;
}

/** A chapter picker row: "N. <title or Chapter N> · m:ss" (Android's). */
export function chapterRowLabel(ch: { title?: string | null; start_ms: number }, i: number): string {
  const title = (ch.title ?? '').trim() || `Chapter ${i + 1}`;
  return `${i + 1}. ${title} · ${formatClock(ch.start_ms)}`;
}

/** The chapter playing at `positionMs` (the last one started), 0 before the
 *  first; -1 with none. The picker opens on it. */
export function chapterAt(chapters: readonly { start_ms: number }[], positionMs: number): number {
  if (chapters.length === 0) return -1;
  let at = 0;
  for (let i = 0; i < chapters.length; i++) {
    if (chapters[i].start_ms <= positionMs) at = i;
  }
  return at;
}

/** Rows a picker shows at once; a longer list scrolls with the cursor. */
export const PICKER_VISIBLE_ROWS = 9;

/** The slice [start, end) of a `total`-row picker to draw so the cursor is
 *  on screen, kept near the middle once the list scrolls. A film with 40
 *  chapters or a disc with 30 subtitle tracks ran the picker off the panel. */
export function pickerWindow(cursor: number, total: number, size = PICKER_VISIBLE_ROWS): { start: number; end: number } {
  if (total <= size) return { start: 0, end: Math.max(0, total) };
  const c = Math.max(0, Math.min(total - 1, cursor));
  const start = Math.max(0, Math.min(total - size, c - Math.floor(size / 2)));
  return { start, end: start + size };
}
