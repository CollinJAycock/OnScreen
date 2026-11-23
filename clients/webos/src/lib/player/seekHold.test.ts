import { describe, expect, it } from 'vitest';
import { HOLD_MAX_STEP_GAP_MS, HOLD_SPEEDS, SeekHold, holdDistance, holdRate } from './seekHold';

// The Samsung Q80B's remote, as logged on that TV (2026-11-19): a held → sends
// keydowns ~350 ms after the press and then every ~108 ms, none flagged as
// a repeat, and one keyup at the release. (webOS repeats faster; the pace
// test below covers both.)
function hold(h: SeekHold, key: string, tapMs: number, startAt: number, forMs: number) {
  const steps = [h.down(key, tapMs, false, startAt)];
  for (let at = startAt + 350; at <= startAt + forMs; at += 108) steps.push(h.down(key, tapMs, false, at));
  return steps;
}

describe('SeekHold', () => {
  it('moves a tap by its own step', () => {
    const h = new SeekHold();
    expect(h.down('right', 10_000, false, 1000)).toBe(10_000);
    expect(h.up('right')).toBe(false);
    expect(h.down('left', -10_000, false, 1300)).toBe(-10_000);
    expect(h.up('left')).toBe(false);
    expect(h.down('rewind', -30_000, false, 1600)).toBe(-30_000);
  });

  it('keeps taps separate once the remote has sent a keyup', () => {
    const h = new SeekHold();
    // 310 ms apart with a keyup between each (the owner's taps on the Q80B).
    for (let i = 0; i < 4; i++) {
      expect(h.down('right', 10_000, false, 1000 + i * 310)).toBe(10_000);
      expect(h.up('right')).toBe(false);
    }
  });

  it('scrubs on while held, faster the longer the hold', () => {
    const h = new SeekHold();
    const steps = hold(h, 'right', 10_000, 0, 6000);
    expect(steps[0]).toBe(10_000);
    // The first repeat counts for the ~350 ms since the press (~10 s), then
    // the first second is a gentle scrub: well under the old flat 10 s a
    // repeat.
    expect(steps[1]).toBe(Math.round(350 * HOLD_SPEEDS[0].rate));
    const firstSecond = steps.slice(2).filter((_, i) => 458 + i * 108 <= 1000);
    expect(firstSecond.length).toBeGreaterThan(3);
    for (const s of firstSecond) expect(s).toBeLessThan(5_000);
    // Later repeats move further than early ones.
    expect(steps[steps.length - 1]).toBeGreaterThan(steps[2] * 10);
    // ~6 s held covers ~23 min: 10 s tap + 45 s + 135 s + 360 s + 900 s.
    const total = steps.reduce((a, b) => a + b, 0);
    expect(total).toBeGreaterThan(20 * 60_000);
    expect(total).toBeLessThan(26 * 60_000);
  });

  it('scrubs at the same pace whatever the repeat rate', () => {
    const fast = new SeekHold();
    const slow = new SeekHold();
    let a = fast.down('right', 10_000, false, 0);
    let b = slow.down('right', 10_000, false, 0);
    // webOS-like: every 51 ms, flagged; Samsung-like: every 108 ms,
    // unflagged. Both end 3672 ms in, past two changes of speed.
    for (let t = 51; t <= 3672; t += 51) a += fast.down('right', 10_000, true, t);
    for (let t = 108; t <= 3672; t += 108) b += slow.down('right', 10_000, false, t);
    expect(Math.abs(a - b)).toBeLessThan(100);
    expect(a).toBe(10_000 + holdDistance(0, 3672));
  });

  it('scrubs backwards for ← and ◀◀', () => {
    const h = new SeekHold();
    const steps = hold(h, 'left', -10_000, 0, 2000);
    expect(steps.every((s) => s < 0)).toBe(true);
  });

  it('says the release of a hold lands the scrub, and only that', () => {
    const h = new SeekHold();
    hold(h, 'right', 10_000, 0, 1000);
    expect(h.up('left')).toBe(false);
    expect(h.up('right')).toBe(true);
    // The next press is a tap again.
    expect(h.down('right', 10_000, false, 1500)).toBe(10_000);
    expect(h.up('right')).toBe(false);
  });

  it('starts over when the direction changes mid-hold', () => {
    const h = new SeekHold();
    hold(h, 'right', 10_000, 0, 5000);
    expect(h.down('left', -10_000, false, 5050)).toBe(-10_000);
    // And the speed with it.
    expect(Math.abs(h.down('left', -10_000, false, 5400))).toBeLessThan(HOLD_MAX_STEP_GAP_MS * HOLD_SPEEDS[0].rate + 1);
  });

  it('caps what one late repeat counts for', () => {
    const h = new SeekHold();
    h.down('right', 10_000, false, 0);
    // A repeat flagged by the remote after a 900 ms stall (a busy page).
    expect(h.down('right', 10_000, true, 900)).toBe(HOLD_MAX_STEP_GAP_MS * holdRate(900));
  });

  it('counts a repeat across a change of speed at both speeds', () => {
    // 1400 → 1600 ms: 100 ms at the first speed, 100 at the second.
    expect(holdDistance(1400, 1600)).toBe(100 * HOLD_SPEEDS[0].rate + 100 * HOLD_SPEEDS[1].rate);
    expect(holdDistance(0, 1500)).toBe(1500 * HOLD_SPEEDS[0].rate);
  });

  it("takes a press long after the last one for a new press, even with no keyup (one was lost)", () => {
    const h = new SeekHold();
    h.down('right', 10_000, false, 0);
    h.up('right');
    h.down('right', 10_000, false, 1000);
    // No keyup came (the app was backgrounded mid-hold, say).
    expect(h.down('right', 10_000, false, 5000)).toBe(10_000);
  });

  it('forgets the key on reset', () => {
    const h = new SeekHold();
    hold(h, 'right', 10_000, 0, 1000);
    h.reset();
    expect(h.up('right')).toBe(false);
    expect(h.down('right', 10_000, false, 1100)).toBe(10_000);
  });
});

describe('holdRate', () => {
  it('climbs through the speeds in order', () => {
    expect(holdRate(0)).toBe(HOLD_SPEEDS[0].rate);
    for (let i = 1; i < HOLD_SPEEDS.length; i++) {
      expect(HOLD_SPEEDS[i].rate).toBeGreaterThan(HOLD_SPEEDS[i - 1].rate);
      expect(holdRate(HOLD_SPEEDS[i].fromMs)).toBe(HOLD_SPEEDS[i].rate);
      expect(holdRate(HOLD_SPEEDS[i].fromMs - 1)).toBe(HOLD_SPEEDS[i - 1].rate);
    }
  });
});
