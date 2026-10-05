import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { SLIDESHOW_INTERVAL_MS, Slideshow, photoCommand } from './slideshow';

describe('photoCommand', () => {
  it('maps the remote like Android: OK and Play/Pause toggle, Play starts, Pause/Stop stop', () => {
    expect(photoCommand('enter')).toBe('toggle');
    expect(photoCommand('playpause')).toBe('toggle');
    expect(photoCommand('play')).toBe('start');
    expect(photoCommand('pause')).toBe('stop');
    expect(photoCommand('stop')).toBe('stop');
  });

  it('steps with ←/→ and the track / seek keys', () => {
    expect(photoCommand('right')).toBe('next');
    expect(photoCommand('forward')).toBe('next');
    expect(photoCommand('left')).toBe('prev');
    expect(photoCommand('rewind')).toBe('prev');
  });

  it('leaves Back and the rest to the app', () => {
    expect(photoCommand('back')).toBeNull();
    expect(photoCommand('up')).toBeNull();
    expect(photoCommand(null)).toBeNull();
  });
});

describe('Slideshow', () => {
  beforeEach(() => {
    vi.useFakeTimers();
  });
  afterEach(() => {
    vi.useRealTimers();
  });

  function setup() {
    const steps: number[] = [];
    const changes: boolean[] = [];
    const show = new Slideshow(
      () => steps.push(Date.now()),
      (on) => changes.push(on),
    );
    return { show, steps, changes };
  }

  it('moves on every interval while running', () => {
    const { show, steps, changes } = setup();
    expect(show.start(5)).toBe(true);
    expect(show.running).toBe(true);
    expect(changes).toEqual([true]);
    vi.advanceTimersByTime(SLIDESHOW_INTERVAL_MS - 1);
    expect(steps).toHaveLength(0);
    vi.advanceTimersByTime(1);
    expect(steps).toHaveLength(1);
    vi.advanceTimersByTime(SLIDESHOW_INTERVAL_MS * 2);
    expect(steps).toHaveLength(3);
  });

  it('needs two photos or more', () => {
    const { show, steps, changes } = setup();
    expect(show.start(1)).toBe(false);
    show.toggle(0);
    vi.advanceTimersByTime(SLIDESHOW_INTERVAL_MS * 3);
    expect(show.running).toBe(false);
    expect(steps).toHaveLength(0);
    expect(changes).toEqual([]);
  });

  it('stops, and toggles', () => {
    const { show, steps, changes } = setup();
    show.toggle(3);
    vi.advanceTimersByTime(SLIDESHOW_INTERVAL_MS);
    show.toggle(3);
    vi.advanceTimersByTime(SLIDESHOW_INTERVAL_MS * 3);
    expect(steps).toHaveLength(1);
    expect(changes).toEqual([true, false]);
    // Stop when already stopped: nothing to report.
    show.stop();
    expect(changes).toEqual([true, false]);
  });

  it('restarts the wait on a manual step', () => {
    const { show, steps } = setup();
    show.start(3);
    vi.advanceTimersByTime(3000);
    // The user pressed → at 3 s: the next step is 4 s after that, not at 4 s.
    show.stepped();
    vi.advanceTimersByTime(1500);
    expect(steps).toHaveLength(0);
    vi.advanceTimersByTime(2500);
    expect(steps).toHaveLength(1);
  });

  it('ignores manual steps while stopped', () => {
    const { show, steps } = setup();
    show.stepped();
    vi.advanceTimersByTime(SLIDESHOW_INTERVAL_MS * 2);
    expect(steps).toHaveLength(0);
    expect(show.running).toBe(false);
  });

  it('restarts the wait on Play while running, without a second start report', () => {
    const { show, steps, changes } = setup();
    show.start(3);
    vi.advanceTimersByTime(3000);
    expect(show.start(3)).toBe(true);
    vi.advanceTimersByTime(3000);
    expect(steps).toHaveLength(0);
    vi.advanceTimersByTime(1000);
    expect(steps).toHaveLength(1);
    expect(changes).toEqual([true]);
  });
});
