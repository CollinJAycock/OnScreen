import { describe, expect, it } from 'vitest';
import {
  ENTER_HELD_MAX_MS,
  POINTER_ECHO_MS,
  PointerEcho,
  PointerTrack,
  WHEEL_STEP_GAP_MS,
  WheelSteps,
  scrollsNatively,
  type ScrollBox,
} from './pointer';

describe('PointerEcho (one OK in pointer mode = one activation)', () => {
  it('drops the Enter that follows a click', () => {
    const p = new PointerEcho();
    p.clicked(1000);
    expect(p.enterIsEcho(1000 + 50)).toBe(true);
    expect(p.enterIsEcho(1000 + POINTER_ECHO_MS - 1)).toBe(true);
    // A real press later on.
    expect(p.enterIsEcho(1000 + POINTER_ECHO_MS)).toBe(false);
  });

  it('drops the click that follows an Enter', () => {
    const p = new PointerEcho();
    p.entered(5000);
    p.released(5080);
    expect(p.clickIsEcho(5080 + 30)).toBe(true);
    expect(p.clickIsEcho(5080 + POINTER_ECHO_MS + 1)).toBe(false);
  });

  it("drops the click a held OK's release sends, a long press included", () => {
    const p = new PointerEcho();
    p.entered(5000);
    // Still down at 1.2 s (the long press opened the options at 0.6 s).
    expect(p.clickIsEcho(6200)).toBe(true);
    p.released(6250);
    expect(p.clickIsEcho(6260)).toBe(true);
    expect(p.clickIsEcho(6250 + POINTER_ECHO_MS)).toBe(false);
  });

  it('gives up on a keyup that never came', () => {
    const p = new PointerEcho();
    p.entered(5000);
    expect(p.clickIsEcho(5000 + ENTER_HELD_MAX_MS)).toBe(false);
  });

  it("keeps an echo's keyup from blocking the next click", () => {
    const p = new PointerEcho();
    p.clicked(1000);
    expect(p.enterIsEcho(1040)).toBe(true);
    // The echo's keyup: no press of its own acted.
    p.released(1100);
    expect(p.clickIsEcho(1250)).toBe(false);
  });

  it('drops nothing before anything happened, nor across the clocks', () => {
    const p = new PointerEcho();
    expect(p.enterIsEcho(0)).toBe(false);
    expect(p.clickIsEcho(0)).toBe(false);
    p.clicked(2000);
    // An event stamped before the click isn't its echo.
    expect(p.enterIsEcho(1990)).toBe(false);
  });
});

describe('PointerTrack', () => {
  it('counts a move only when the position changed', () => {
    const t = new PointerTrack();
    expect(t.moved(100, 200)).toBe(true);
    // Chromium's mousemove after a scroll under a still pointer.
    expect(t.moved(100, 200)).toBe(false);
    expect(t.moved(101, 200)).toBe(true);
  });
});

describe('WheelSteps', () => {
  it('turns a notch into ↓ / ↑ (the list moves the way the wheel turns)', () => {
    const w = new WheelSteps();
    expect(w.step(0, 120, 0, 1000)).toBe('down');
    expect(w.step(0, -120, 0, 2000)).toBe('up');
  });

  it('takes one step per WHEEL_STEP_GAP_MS from a burst', () => {
    const w = new WheelSteps();
    expect(w.step(0, 40, 0, 1000)).toBe('down');
    expect(w.step(0, 40, 0, 1016)).toBeNull();
    expect(w.step(0, 40, 0, 1000 + WHEEL_STEP_GAP_MS - 1)).toBeNull();
    expect(w.step(0, 40, 0, 1000 + WHEEL_STEP_GAP_MS)).toBe('down');
  });

  it('reads line and page deltas, a sideways scroll as ← / →, and ignores no movement', () => {
    const w = new WheelSteps();
    expect(w.step(0, 3, 1, 1000)).toBe('down');
    expect(w.step(0, -1, 2, 2000)).toBe('up');
    expect(w.step(-100, 10, 0, 3000)).toBe('left');
    expect(w.step(100, 0, 0, 4000)).toBe('right');
    expect(w.step(0, 0, 0, 5000)).toBeNull();
    // A no-move doesn't use up the next step.
    expect(w.step(0, 100, 0, 5001)).toBe('down');
  });
});

describe('scrollsNatively', () => {
  const box = (o: Partial<ScrollBox>): ScrollBox => ({
    overflowY: 'auto',
    scrollTop: 0,
    scrollHeight: 2000,
    clientHeight: 600,
    hasFocusables: false,
    ...o,
  });

  it('lets a text box with room scroll itself', () => {
    expect(scrollsNatively(box({}), 100)).toBe(true);
    expect(scrollsNatively(box({ scrollTop: 300 }), -100)).toBe(true);
    expect(scrollsNatively(box({ overflowY: 'scroll' }), 100)).toBe(true);
  });

  it('moves the focus instead for a box of focusables, a full box, or one at its end', () => {
    expect(scrollsNatively(box({ hasFocusables: true }), 100)).toBe(false);
    expect(scrollsNatively(box({ overflowY: 'hidden' }), 100)).toBe(false);
    expect(scrollsNatively(box({ scrollHeight: 600 }), 100)).toBe(false);
    expect(scrollsNatively(box({ scrollTop: 0 }), -100)).toBe(false);
    expect(scrollsNatively(box({ scrollTop: 1400 }), 100)).toBe(false);
    expect(scrollsNatively(box({}), 0)).toBe(false);
  });
});
