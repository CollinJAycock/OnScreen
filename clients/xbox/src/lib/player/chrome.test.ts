import { describe, expect, it } from 'vitest';
import {
  ACTION_ROW_HIDE_MS,
  CONTROLS_HIDE_MS,
  channelStep,
  controlsHideDelayMs,
  episodeContextLine,
  nowPlayingArt,
  queueLabel,
  queueNoun,
  upNextKey,
  usesParentCover,
} from './chrome';

describe('controlsHideDelayMs', () => {
  it('fades the controls 5 s after the last press while playing', () => {
    expect(CONTROLS_HIDE_MS).toBe(5_000);
    expect(controlsHideDelayMs({ alwaysOn: false, paused: false, actionRowFocused: false })).toBe(5_000);
  });

  it('holds them longer while the action row has focus', () => {
    expect(controlsHideDelayMs({ alwaysOn: false, paused: false, actionRowFocused: true })).toBe(ACTION_ROW_HIDE_MS);
  });

  it('never fades them while paused, nor on the now-playing view', () => {
    expect(controlsHideDelayMs({ alwaysOn: false, paused: true, actionRowFocused: false })).toBeNull();
    expect(controlsHideDelayMs({ alwaysOn: false, paused: true, actionRowFocused: true })).toBeNull();
    expect(controlsHideDelayMs({ alwaysOn: true, paused: false, actionRowFocused: false })).toBeNull();
  });
});

describe('episodeContextLine', () => {
  it('reads "Show · S2 · E5"', () => {
    expect(episodeContextLine('The Wire', 2, 5)).toBe('The Wire · S2 · E5');
  });

  it('keeps whatever is known', () => {
    expect(episodeContextLine('The Wire', null, 5)).toBe('The Wire · E5');
    expect(episodeContextLine('', 2, 5)).toBe('S2 · E5');
    expect(episodeContextLine('  The Wire ', undefined, 0)).toBe('The Wire');
    expect(episodeContextLine(null, null, null)).toBe('');
  });

  it('shows specials as season 0', () => {
    expect(episodeContextLine('Doctor Who', 0, 3)).toBe('Doctor Who · S0 · E3');
  });
});

describe('queueLabel', () => {
  it('names the queue by what it holds', () => {
    expect(queueLabel('track', 3, 12)).toBe('Track 3 of 12');
    expect(queueLabel('audiobook_chapter', 4, 30)).toBe('Chapter 4 of 30');
    expect(queueLabel('podcast_episode', 1, 200)).toBe('Episode 1 of 200');
  });

  it('is empty while the position is unknown', () => {
    expect(queueLabel('track', 0, 12)).toBe('');
    expect(queueLabel('track', 2, 0)).toBe('');
  });
});

describe('now-playing art', () => {
  it("falls back from the item's poster to its fanart to the parent's cover", () => {
    expect(nowPlayingArt({ poster_path: 'p.jpg', fanart_path: 'f.jpg' }, 'album.jpg')).toBe('p.jpg');
    expect(nowPlayingArt({ poster_path: '', fanart_path: 'f.jpg' }, 'album.jpg')).toBe('f.jpg');
    expect(nowPlayingArt({}, 'album.jpg')).toBe('album.jpg');
    expect(nowPlayingArt({ poster_path: '  ' }, null)).toBeNull();
  });

  it("borrows the parent's cover for tracks, chapters and podcast episodes only", () => {
    expect(usesParentCover('track')).toBe(true);
    expect(usesParentCover('audiobook_chapter')).toBe(true);
    expect(usesParentCover('podcast_episode')).toBe(true);
    // A book's parent is its author.
    expect(usesParentCover('audiobook')).toBe(false);
    expect(usesParentCover('episode')).toBe(false);
  });
});

describe('upNextKey (the Up Next card)', () => {
  it('OK on the focused button plays now or declines', () => {
    expect(upNextKey(0, 'enter')).toEqual({ kind: 'press', button: 'play' });
    expect(upNextKey(1, 'enter')).toEqual({ kind: 'press', button: 'cancel' });
  });

  it('←/→ move between Play now and Cancel without wrapping or seeking', () => {
    expect(upNextKey(0, 'right')).toEqual({ kind: 'focus', index: 1 });
    expect(upNextKey(1, 'right')).toEqual({ kind: 'focus', index: 1 });
    expect(upNextKey(1, 'left')).toEqual({ kind: 'focus', index: 0 });
    expect(upNextKey(0, 'left')).toEqual({ kind: 'focus', index: 0 });
  });

  it('Down hands the keys back to the player; Up returns to the card', () => {
    expect(upNextKey(1, 'down')).toEqual({ kind: 'release' });
    expect(upNextKey(-1, 'up')).toEqual({ kind: 'focus', index: 0 });
    expect(upNextKey(-1, 'enter')).toEqual({ kind: 'pass' });
    expect(upNextKey(-1, 'left')).toEqual({ kind: 'pass' });
  });

  it('Back declines the card, focused or not', () => {
    expect(upNextKey(0, 'back')).toEqual({ kind: 'press', button: 'cancel' });
    expect(upNextKey(-1, 'back')).toEqual({ kind: 'press', button: 'cancel' });
  });

  it('leaves the transport keys to the player', () => {
    expect(upNextKey(0, 'playpause')).toEqual({ kind: 'pass' });
    expect(upNextKey(0, 'forward')).toEqual({ kind: 'pass' });
    // CH ▲▼ step the playing item's queue or chapters, card or no card.
    expect(upNextKey(0, 'channelUp')).toEqual({ kind: 'pass' });
    expect(upNextKey(-1, 'channelDown')).toEqual({ kind: 'pass' });
  });
});

describe('CH ▲ / CH ▼ (channelStep)', () => {
  it('steps the queue for a track, a book chapter file and a podcast episode', () => {
    expect(channelStep('track', 0)).toBe('item');
    expect(channelStep('audiobook_chapter', 0)).toBe('item');
    // Even one with chapters of its own: red / green still step those.
    expect(channelStep('podcast_episode', 12)).toBe('item');
  });

  it('steps the chapters of anything else that has them', () => {
    expect(channelStep('movie', 18)).toBe('chapter');
    expect(channelStep('episode', 4)).toBe('chapter');
    expect(channelStep('audiobook', 30)).toBe('chapter');
  });

  it('does nothing without a queue or chapters', () => {
    expect(channelStep('movie', 0)).toBe('none');
    expect(channelStep(undefined, 0)).toBe('none');
  });

  it('names one item of the queue for the hints', () => {
    expect(queueNoun('track')).toBe('track');
    expect(queueNoun('audiobook_chapter')).toBe('chapter');
    expect(queueNoun('podcast_episode')).toBe('episode');
    expect(queueNoun('audiobook')).toBeNull();
    expect(queueNoun('movie')).toBeNull();
  });
});
