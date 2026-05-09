import { describe, it, expect, beforeEach, vi } from 'vitest';
import { get } from 'svelte/store';
import {
  sleepTimer,
  sleepTimerActive,
  startSleepTimer,
  cancelSleepTimer,
  formatRemaining,
} from './sleepTimer';

describe('sleepTimer', () => {
  beforeEach(() => {
    cancelSleepTimer();
    vi.useFakeTimers();
  });

  it('starts in off / 0ms state', () => {
    const s = get(sleepTimer);
    expect(s.mode).toBe('off');
    expect(s.remainingMs).toBe(0);
    expect(get(sleepTimerActive)).toBe(false);
  });

  it('arms a duration timer and ticks down', () => {
    startSleepTimer('15m', () => {});
    expect(get(sleepTimer).mode).toBe('15m');
    expect(get(sleepTimer).remainingMs).toBe(15 * 60 * 1000);
    expect(get(sleepTimerActive)).toBe(true);

    vi.advanceTimersByTime(60 * 1000);
    // After 1m the remaining should be ~14m, allowing for 1s tick precision.
    const r = get(sleepTimer).remainingMs;
    expect(r).toBeLessThanOrEqual(14 * 60 * 1000);
    expect(r).toBeGreaterThan(13 * 60 * 1000);
  });

  it('fires onFire and resets when duration elapses', () => {
    const onFire = vi.fn();
    startSleepTimer('15m', onFire);
    vi.advanceTimersByTime(15 * 60 * 1000 + 100);
    expect(onFire).toHaveBeenCalledOnce();
    // Reset back to off after firing.
    expect(get(sleepTimer).mode).toBe('off');
    expect(get(sleepTimer).remainingMs).toBe(0);
  });

  it('episode mode sets state without ticking', () => {
    const onFire = vi.fn();
    startSleepTimer('episode', onFire);
    expect(get(sleepTimer).mode).toBe('episode');
    expect(get(sleepTimer).remainingMs).toBe(0);
    expect(get(sleepTimerActive)).toBe(true);

    // Advance way past any duration — episode never fires the callback
    // because consumers gate auto-next on `mode === 'episode'` directly.
    vi.advanceTimersByTime(2 * 60 * 60 * 1000);
    expect(onFire).not.toHaveBeenCalled();
  });

  it('cancel resets state and clears any in-flight tick', () => {
    const onFire = vi.fn();
    startSleepTimer('15m', onFire);
    vi.advanceTimersByTime(60 * 1000);
    cancelSleepTimer();
    expect(get(sleepTimer).mode).toBe('off');
    // Fast-forward past the original duration — callback must NOT fire.
    vi.advanceTimersByTime(20 * 60 * 1000);
    expect(onFire).not.toHaveBeenCalled();
  });

  it('start replaces a prior timer (no double-fire)', () => {
    const firstFire = vi.fn();
    const secondFire = vi.fn();
    startSleepTimer('15m', firstFire);
    startSleepTimer('30m', secondFire);
    // The first 15m callback must not run; the second 30m one should.
    vi.advanceTimersByTime(30 * 60 * 1000 + 100);
    expect(firstFire).not.toHaveBeenCalled();
    expect(secondFire).toHaveBeenCalledOnce();
  });

  it('formatRemaining renders M:SS shape', () => {
    expect(formatRemaining(0)).toBe('0:00');
    expect(formatRemaining(45 * 1000)).toBe('0:45');
    expect(formatRemaining(60 * 1000)).toBe('1:00');
    expect(formatRemaining(12 * 60 * 1000 + 3 * 1000)).toBe('12:03');
    // Ceiling rounding: 999ms left should still read 1s, not 0s.
    expect(formatRemaining(999)).toBe('0:01');
  });
});
