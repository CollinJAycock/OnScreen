import { describe, expect, it } from 'vitest';
import { COLD_HOLD_GAP_MS, HOLD_STALE_MS, KeyHold, eventTime, repeatActivates } from './hold';

describe('KeyHold', () => {
  it('takes a press, then a press after its release', () => {
    const hold = new KeyHold();
    expect(hold.down(false, 0)).toBe(true);
    hold.up();
    expect(hold.down(false, 120)).toBe(true);
  });

  it("drops a held OK's flagged repeats until the key is released", () => {
    // The TV test: keydown, one repeat 500 ms later, keyup 100 ms after.
    const hold = new KeyHold();
    expect(hold.down(false, 0)).toBe(true);
    expect(hold.down(true, 500)).toBe(false);
    expect(hold.down(true, 550)).toBe(false);
    hold.up();
    expect(hold.down(false, 2000)).toBe(true);
  });

  it('drops a flagged repeat whatever the gap', () => {
    const hold = new KeyHold();
    expect(hold.down(false, 0)).toBe(true);
    expect(hold.down(true, 5000)).toBe(false);
    // Cold or not.
    hold.up();
    expect(hold.down(false, 6000)).toBe(true);
    expect(hold.down(true, 6000 + HOLD_STALE_MS + 500)).toBe(false);
  });

  it("drops a cold session's unflagged hold: the first repeat after the initial delay, and a jank gap", () => {
    // The TV's failing run: the session's first OK, held, on a remote that
    // doesn't flag repeats, before any keyup. Its keydowns came 507 ms
    // after the press (webOS's initial repeat delay), then ~52 ms apart,
    // with a 578 ms gap while the library grid rendered. It used to click
    // the Libraries pill, then 4K Movies, then 12 Angry Men.
    const hold = new KeyHold();
    const gaps = [507, 53, 54, 52, 51, 52, 578, 92, 51, 52];
    let at = 0;
    expect(hold.down(false, at)).toBe(true);
    for (const gap of gaps) {
      at += gap;
      expect(hold.down(false, at)).toBe(false);
    }
    // Released: a press again.
    hold.up();
    expect(hold.down(false, at + 300)).toBe(true);
  });

  it("ends the cold window with the first keyup: a quick second press is a press", () => {
    const hold = new KeyHold();
    expect(hold.down(false, 0)).toBe(true);
    hold.up();
    // A deliberate second OK well inside COLD_HOLD_GAP_MS of the first.
    expect(hold.down(false, 250)).toBe(true);
    // … and its own unflagged repeats are still one hold.
    expect(hold.down(false, 250 + 505)).toBe(false);
  });

  it('drops unflagged repeats while the key is down, once keyups are known to come', () => {
    const hold = new KeyHold();
    hold.down(false, 0);
    hold.up();
    // A hold on firmware that doesn't flag repeats: the first one after the
    // initial repeat delay, then a quick run.
    expect(hold.down(false, 1000)).toBe(true);
    expect(hold.down(false, 1000 + 500)).toBe(false);
    expect(hold.down(false, 1000 + 550)).toBe(false);
    expect(hold.down(false, 1000 + 600)).toBe(false);
    hold.up();
    expect(hold.down(false, 1000 + 700)).toBe(true);
  });

  it('takes a keydown long after a lost keyup as a fresh press', () => {
    const hold = new KeyHold();
    hold.down(false, 0);
    hold.up();
    expect(hold.down(false, 1000)).toBe(true);
    // No keyup for this one (the app went to the background mid-hold).
    expect(hold.down(false, 1000 + HOLD_STALE_MS + 1)).toBe(true);
  });

  it('tells repeats from presses by the gap on firmware that never sends keyup', () => {
    const hold = new KeyHold();
    expect(hold.down(false, 0)).toBe(true);
    expect(hold.down(false, 80)).toBe(false);
    expect(hold.down(false, 160)).toBe(false);
    // The gap counts from the last repeat, not from the press.
    expect(hold.down(false, 160 + COLD_HOLD_GAP_MS - 1)).toBe(false);
    // Released and pressed again a while later: a second press.
    const last = 160 + COLD_HOLD_GAP_MS - 1;
    expect(hold.down(false, last + COLD_HOLD_GAP_MS)).toBe(true);
  });

  it("clears webOS's initial repeat delay with room to spare", () => {
    // 500 ms on webOS (measured 503-507 ms); under HOLD_STALE_MS so a lost
    // keyup still ends a hold sooner once keyups are known.
    expect(COLD_HOLD_GAP_MS).toBeGreaterThanOrEqual(507 + 150);
    expect(COLD_HOLD_GAP_MS).toBeLessThan(HOLD_STALE_MS);
  });
});

describe('eventTime', () => {
  it("uses the event's own stamp, not the time it's handled", () => {
    // A repeat stamped 51 ms after the last, handled 578 ms later because a
    // page was rendering: the stamp is what the gap is measured on.
    expect(eventTime({ timeStamp: 1051.5 }, () => 1578)).toBe(1051.5);
  });

  it('falls back to the clock for an event with no stamp', () => {
    expect(eventTime({ timeStamp: 0 }, () => 4242)).toBe(4242);
  });
});

describe('repeatActivates', () => {
  const keyboardDelete = { repeatOk: true, pressed: true, longPress: false };

  it("repeats an element that asked for it (the keyboard's delete key)", () => {
    expect(repeatActivates(keyboardDelete)).toBe(true);
  });

  it("drops repeats for everything else: pills, cards, dialogs' options", () => {
    expect(repeatActivates({ repeatOk: false, pressed: true, longPress: false })).toBe(false);
    expect(repeatActivates(null)).toBe(false);
  });

  it("doesn't hand a hold to an element the press didn't activate", () => {
    // The press moved focus (or a page's handler took it), and the repeat
    // would land on whatever has focus now.
    expect(repeatActivates({ ...keyboardDelete, pressed: false })).toBe(false);
  });

  it('never repeats an element with a long press: the hold is the long press', () => {
    expect(repeatActivates({ ...keyboardDelete, longPress: true })).toBe(false);
  });
});
