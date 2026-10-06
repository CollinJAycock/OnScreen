// The player's chrome around the picture: when the controls hide, what the
// top line and the now-playing view say, which art the now-playing view
// shows, and the Up Next card's buttons. Pure: the page keeps the state,
// these decide. Compared against Android's PlaybackFragment (leanback's
// controls fade, overlay_up_next, bindAudioBackdrop / NowPlaying.parentCover).

import type { RemoteKey } from '../focus/keys';
import { episodeCode } from '../watchState';

// ── Controls auto-hide ─────────────────────────────────────────────────

/** The controls fade this long after the last key or pointer move (Android's
 *  playbackControlsAutoHideTickleTimeout, themes.xml). */
export const CONTROLS_HIDE_MS = 5_000;
/** Longer while the action row has focus: the user is choosing a button. */
export const ACTION_ROW_HIDE_MS = 8_000;

/**
 * When the controls hide, in ms from now; null when they stay up. They never
 * fade while paused (leanback turns its auto-hide off for a paused player, so
 * the bar, the clock and the state stay readable), nor on the now-playing
 * view of music, books and podcast audio, which has no picture to uncover.
 * They used to fade after 3 s even when paused.
 */
export function controlsHideDelayMs(o: { alwaysOn: boolean; paused: boolean; actionRowFocused: boolean }): number | null {
  if (o.alwaysOn || o.paused) return null;
  return o.actionRowFocused ? ACTION_ROW_HIDE_MS : CONTROLS_HIDE_MS;
}

// ── Labels ─────────────────────────────────────────────────────────────

/** The line over an episode's title: "Show · S2 · E5". Whatever is known of
 *  it (just the show, just the code); '' when nothing is. */
export function episodeContextLine(show: string | null | undefined, season?: number | null, episode?: number | null): string {
  return [(show ?? '').trim(), episodeCode(season, episode)].filter((s) => s !== '').join(' · ');
}

/** "Track 3 of 12" on the now-playing view, named for what is queued: a
 *  book's chapters and a podcast's episodes aren't tracks. '' when the
 *  position isn't known. */
export function queueLabel(type: string | undefined, position: number, total: number): string {
  if (position <= 0 || total <= 0) return '';
  const noun = type === 'audiobook_chapter' ? 'Chapter' : type === 'podcast_episode' ? 'Episode' : 'Track';
  return `${noun} ${position} of ${total}`;
}

// ── CH ▲ / CH ▼ ────────────────────────────────────────────────────────

/** What one item of a queue is called, for the item types played as one (an
 *  album's tracks, a book's chapter files, a podcast's episodes); null for
 *  anything else (a film, an episode of a show, a single-file book). */
export function queueNoun(type: string | undefined): 'track' | 'chapter' | 'episode' | null {
  if (type === 'track') return 'track';
  if (type === 'audiobook_chapter') return 'chapter';
  if (type === 'podcast_episode') return 'episode';
  return null;
}

/** What CH ▲ / CH ▼ step through. */
export type ChannelStep =
  /** The queue's next / previous item (the path ◀◀ ▶▶ take for a track). */
  | 'item'
  /** The file's next / previous chapter (red / green). */
  | 'chapter'
  /** Nothing: a channel key that seeked would be a surprise. */
  | 'none';

/**
 * CH ▲ (next) / CH ▼ (previous) on the player. The Magic Remote has no
 * ◀◀ ▶▶ (the C1's MR21 doesn't) and no colour keys, so track skip and
 * chapter stepping had no key on it; every LG remote has the channel
 * rocker. A queue item steps the queue; anything else steps its chapters
 * when it has any (a film, a single-file book).
 */
export function channelStep(type: string | undefined, chapterCount: number): ChannelStep {
  if (queueNoun(type)) return 'item';
  return chapterCount > 0 ? 'chapter' : 'none';
}

// ── Now-playing art ────────────────────────────────────────────────────

/** Item types whose parent's cover stands in for their own: a track's
 *  album, a chapter's book (Android's NowPlaying.parentCover), and a podcast
 *  episode's podcast (this app gives podcast audio the now-playing view). */
export function usesParentCover(type: string | undefined): boolean {
  return type === 'track' || type === 'audiobook_chapter' || type === 'podcast_episode';
}

/** The art path the now-playing view shows: the item's poster, its fanart,
 *  then its parent's cover (Android's bindAudioBackdrop order); null for the
 *  ♪ placeholder. A track usually has no art of its own, so an album played
 *  through showed the placeholder on every track. */
export function nowPlayingArt(
  item: { poster_path?: string | null; fanart_path?: string | null },
  parentPoster?: string | null,
): string | null {
  for (const p of [item.poster_path, item.fanart_path, parentPoster]) {
    if (typeof p === 'string' && p.trim() !== '') return p;
  }
  return null;
}

// ── Up Next card ───────────────────────────────────────────────────────

export type UpNextButton = 'play' | 'cancel';
/** The card's buttons, left to right (Android's overlay_up_next). */
export const UP_NEXT_BUTTONS: readonly UpNextButton[] = ['play', 'cancel'];

export type UpNextStep =
  /** Focus this button (0 Play now, 1 Cancel). */
  | { kind: 'focus'; index: number }
  /** Activate the button: play the next item now, or decline the card. */
  | { kind: 'press'; button: UpNextButton }
  /** Down off the card: back to the player (←/→ seek, OK plays / pauses). */
  | { kind: 'release' }
  /** Not the card's key. */
  | { kind: 'pass' };

/**
 * What a key does to the Up Next card while it is up. `focus` is the focused
 * button, -1 when the card doesn't have focus. It takes focus when it shows
 * (Android focuses Play now), so OK plays the next item as before; ←/→ move
 * between Play now and Cancel; Down hands the keys back to the player, Up
 * returns to the card. Back declines it whether focused or not (Android's
 * overlayBackCallback), so the end of the episode leaves.
 */
export function upNextKey(focus: number, key: RemoteKey): UpNextStep {
  const last = UP_NEXT_BUTTONS.length - 1;
  if (key === 'back') return { kind: 'press', button: 'cancel' };
  if (focus < 0 || focus > last) return key === 'up' ? { kind: 'focus', index: 0 } : { kind: 'pass' };
  switch (key) {
    case 'left':
      return { kind: 'focus', index: Math.max(0, focus - 1) };
    case 'right':
      return { kind: 'focus', index: Math.min(last, focus + 1) };
    case 'up':
      return { kind: 'focus', index: focus };
    case 'down':
      return { kind: 'release' };
    case 'enter':
      return { kind: 'press', button: UP_NEXT_BUTTONS[focus] };
    default:
      return { kind: 'pass' };
  }
}
