import { describe, expect, it, vi } from 'vitest';
import { createTempoMedia } from './tempoMedia';

function ranges(...pairs: [number, number][]): TimeRanges {
  return { length: pairs.length, start: (i: number) => pairs[i][0], end: (i: number) => pairs[i][1] } as unknown as TimeRanges;
}

function fakeElement() {
  const el = {
    currentTime: 0,
    duration: 100,
    seekable: ranges([0, 40]),
    buffered: ranges([0, 10]),
    playbackRate: 1,
    defaultPlaybackRate: 1,
    src: '',
    paused: true,
    play: vi.fn(function (this: unknown) {
      return this;
    }),
    addEventListener: vi.fn(),
  };
  return el;
}

describe('createTempoMedia', () => {
  it('is the element, unchanged, with no server rate', () => {
    const el = fakeElement();
    const t = createTempoMedia(el as unknown as HTMLVideoElement);
    el.currentTime = 12;
    expect(t.media.currentTime).toBe(12);
    t.media.currentTime = 30;
    expect(el.currentTime).toBe(30);
    t.media.playbackRate = 1.5;
    expect(el.playbackRate).toBe(1.5);
    t.media.src = 'x';
    expect(el.src).toBe('x');
    expect(t.media.duration).toBe(100);
  });

  it('reads a sped stream in content seconds, and seeks in them', () => {
    const el = fakeElement();
    const t = createTempoMedia(el as unknown as HTMLVideoElement);
    el.playbackRate = 1.5; // what the page set before it knew the TV ignores it
    t.setServerRate(1.5);
    expect(el.playbackRate).toBe(1); // the stream is already sped
    el.currentTime = 10; // 10 s of stream = 15 s of book
    expect(t.media.currentTime).toBe(15);
    expect(t.media.duration).toBe(150);
    expect(t.media.seekable.end(0)).toBe(60);
    expect(t.media.buffered.end(0)).toBe(15);
    t.media.currentTime = 30; // 30 s of book
    expect(el.currentTime).toBe(20);
    expect(t.media.playbackRate).toBe(1.5);
    t.media.playbackRate = 2; // the page's own bookkeeping; the element stays at 1x
    expect(t.media.playbackRate).toBe(2);
    expect(el.playbackRate).toBe(1);
  });

  it('keeps Infinity a growing stream reports, and binds methods to the element', () => {
    const el = fakeElement();
    el.duration = Infinity;
    const t = createTempoMedia(el as unknown as HTMLVideoElement);
    t.setServerRate(2);
    expect(t.media.duration).toBe(Infinity);
    expect(t.media.play()).toBe(el);
  });

  it('goes back to the plain element at 1x or with no rate', () => {
    const el = fakeElement();
    const t = createTempoMedia(el as unknown as HTMLVideoElement);
    t.setServerRate(1.5);
    t.setServerRate(1);
    expect(t.serverRate).toBeNull();
    el.currentTime = 10;
    expect(t.media.currentTime).toBe(10);
    t.setServerRate(null);
    expect(t.media.currentTime).toBe(10);
  });
});
