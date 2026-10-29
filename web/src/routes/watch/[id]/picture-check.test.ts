import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { watchForPicture, NO_PICTURE_GRACE_MS, type PictureVerdict } from './picture-check';

// A <video> whose decode state the test drives: jsdom's media element never
// decodes anything, so videoWidth / paused / currentTime are stubbed.
function fakeVideo(init: { width?: number; paused?: boolean; ct?: number } = {}) {
  const el = document.createElement('video');
  const state = { width: init.width ?? 0, paused: init.paused ?? false, ct: init.ct ?? 0 };
  Object.defineProperty(el, 'videoWidth', { get: () => state.width });
  Object.defineProperty(el, 'paused', { get: () => state.paused });
  Object.defineProperty(el, 'currentTime', { get: () => state.ct, set: (v: number) => { state.ct = v; } });
  const fire = (type: string) => el.dispatchEvent(new Event(type));
  return { el, state, fire };
}

describe('watchForPicture', () => {
  let verdicts: PictureVerdict[];
  const record = (v: PictureVerdict) => verdicts.push(v);

  beforeEach(() => {
    verdicts = [];
    vi.useFakeTimers();
  });
  afterEach(() => {
    vi.useRealTimers();
  });

  it('Safari: width 0 at metadata, then a frame arrives → picture, never no-picture', () => {
    const v = fakeVideo();
    watchForPicture(v.el, record);
    v.fire('playing');
    vi.advanceTimersByTime(1500);
    v.state.width = 1920;
    v.fire('resize');
    vi.advanceTimersByTime(NO_PICTURE_GRACE_MS * 2);
    expect(verdicts).toEqual(['picture']);
  });

  it('audio plays but no picture → no-picture after ~2 s of playback', () => {
    const v = fakeVideo();
    watchForPicture(v.el, record);
    v.fire('playing');
    for (let t = 0.25; t <= 1.75; t += 0.25) {
      v.state.ct = t;
      v.fire('timeupdate');
    }
    expect(verdicts).toEqual([]);
    v.state.ct = 2.25;
    v.fire('timeupdate');
    expect(verdicts).toEqual(['no-picture']);
  });

  it('a resume seek is not playback', () => {
    const v = fakeVideo();
    watchForPicture(v.el, record);
    v.state.ct = 91.4; // jump to the resume point
    v.fire('timeupdate');
    v.state.ct = 91.6;
    v.fire('timeupdate');
    expect(verdicts).toEqual([]);
  });

  it('playing with neither picture nor progress → no-picture after the grace period', () => {
    const v = fakeVideo();
    watchForPicture(v.el, record);
    vi.advanceTimersByTime(NO_PICTURE_GRACE_MS - 1);
    expect(verdicts).toEqual([]);
    vi.advanceTimersByTime(1);
    expect(verdicts).toEqual(['no-picture']);
  });

  it('paused time does not count (blocked autoplay)', () => {
    const v = fakeVideo({ paused: true });
    watchForPicture(v.el, record);
    vi.advanceTimersByTime(NO_PICTURE_GRACE_MS * 3);
    expect(verdicts).toEqual([]);
    // The viewer presses play; the grace period starts now.
    v.state.paused = false;
    v.fire('playing');
    vi.advanceTimersByTime(NO_PICTURE_GRACE_MS);
    expect(verdicts).toEqual(['no-picture']);
  });

  it('pausing mid-grace stops the clock', () => {
    const v = fakeVideo();
    watchForPicture(v.el, record);
    vi.advanceTimersByTime(NO_PICTURE_GRACE_MS / 2);
    v.state.paused = true;
    v.fire('pause');
    vi.advanceTimersByTime(NO_PICTURE_GRACE_MS * 2);
    expect(verdicts).toEqual([]);
  });

  it('already decoding → picture', async () => {
    const v = fakeVideo({ width: 1280 });
    watchForPicture(v.el, record);
    await Promise.resolve();
    expect(verdicts).toEqual(['picture']);
  });

  it('cancel suppresses the verdict and detaches', async () => {
    const v = fakeVideo();
    const cancel = watchForPicture(v.el, record);
    cancel();
    vi.advanceTimersByTime(NO_PICTURE_GRACE_MS * 2);
    v.state.width = 1920;
    v.fire('resize');
    await Promise.resolve();
    expect(verdicts).toEqual([]);
  });

  it('answers only once', () => {
    const v = fakeVideo();
    watchForPicture(v.el, record);
    vi.advanceTimersByTime(NO_PICTURE_GRACE_MS);
    v.state.width = 1920;
    v.fire('resize');
    v.fire('playing');
    expect(verdicts).toEqual(['no-picture']);
  });
});
