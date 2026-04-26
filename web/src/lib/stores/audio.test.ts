import { describe, it, expect, beforeEach } from 'vitest';
import { get } from 'svelte/store';
import { audio, nextTrack, type AudioTrack } from './audio';

const t = (id: string): AudioTrack => ({ id, fileId: `f-${id}`, title: id });

beforeEach(() => audio.clear());

describe('peekNext / nextTrack — gapless preload candidate', () => {
  it('returns the next item in linear order', () => {
    audio.play([t('a'), t('b'), t('c')], 0);
    expect(audio.peekNext()?.id).toBe('b');
    expect(get(nextTrack)?.id).toBe('b');
  });

  it('returns null at end of queue when repeat=off', () => {
    audio.play([t('a'), t('b')], 1);
    expect(audio.peekNext()).toBeNull();
    expect(get(nextTrack)).toBeNull();
  });

  it('wraps to the first track when repeat=all', () => {
    audio.play([t('a'), t('b')], 1);
    audio.cycleRepeat(); // off → all
    expect(audio.peekNext()?.id).toBe('a');
    expect(get(nextTrack)?.id).toBe('a');
  });

  it('returns the same track when repeat=one', () => {
    audio.play([t('a'), t('b')], 0);
    audio.cycleRepeat(); // → all
    audio.cycleRepeat(); // → one
    expect(audio.peekNext()?.id).toBe('a');
    expect(get(nextTrack)?.id).toBe('a');
  });

  it('honors the shuffled order, not raw queue order', () => {
    audio.play([t('a'), t('b'), t('c'), t('d'), t('e')], 0);
    audio.toggleShuffle();
    // Shuffle pins the active track to position 0; peekNext returns
    // the queue item at shuffleOrder[1], whatever that ended up.
    const want = audio.peekNext();
    expect(want).not.toBeNull();
    // Whatever it is, it should NOT be the active track.
    expect(want?.id).not.toBe('a');
  });

  it('returns null when the queue is empty', () => {
    expect(audio.peekNext()).toBeNull();
    expect(get(nextTrack)).toBeNull();
  });
});
