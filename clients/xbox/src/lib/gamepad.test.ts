import { describe, expect, it } from 'vitest';
import { DEDUPE_MS, PadKeys, REPEAT_DELAY_MS, REPEAT_EVERY_MS, padState } from './gamepad';

const buttons = (...down: number[]) => Array.from({ length: 17 }, (_, i) => ({ pressed: down.includes(i) }));
const set = (...keys: string[]) => new Set(keys);

describe('padState', () => {
  it('names the standard buttons that are down', () => {
    expect([...padState(buttons(0, 12), [0, 0])].sort()).toEqual(['GamepadA', 'GamepadDPadUp']);
  });

  it('reads the left stick as the D-pad past the threshold only', () => {
    expect([...padState(buttons(), [0.9, -0.1])]).toEqual(['GamepadLeftThumbstickRight']);
    expect([...padState(buttons(), [0.3, 0.3])]).toEqual([]);
    expect([...padState(buttons(), [0, -0.8])]).toEqual(['GamepadLeftThumbstickUp']);
  });
});

describe('PadKeys', () => {
  it('holds a press back DEDUPE_MS, then sends keydown, repeats, and keyup on release', () => {
    const k = new PadKeys();
    expect(k.update(set('GamepadA'), 0)).toEqual([]);
    expect(k.update(set('GamepadA'), DEDUPE_MS)).toEqual([{ type: 'keydown', key: 'GamepadA', repeat: false }]);
    expect(k.update(set('GamepadA'), DEDUPE_MS + REPEAT_DELAY_MS - 1)).toEqual([]);
    expect(k.update(set('GamepadA'), DEDUPE_MS + REPEAT_DELAY_MS)).toEqual([{ type: 'keydown', key: 'GamepadA', repeat: true }]);
    expect(k.update(set('GamepadA'), DEDUPE_MS + REPEAT_DELAY_MS + REPEAT_EVERY_MS)).toEqual([
      { type: 'keydown', key: 'GamepadA', repeat: true },
    ]);
    expect(k.update(set(), DEDUPE_MS + REPEAT_DELAY_MS + REPEAT_EVERY_MS + 5)).toEqual([
      { type: 'keyup', key: 'GamepadA', repeat: false },
    ]);
  });

  it('sends nothing for a tap shorter than the hold-back (and no keyup for it)', () => {
    const k = new PadKeys();
    k.update(set('GamepadB'), 0);
    expect(k.update(set(), 20)).toEqual([]);
  });

  it('ignores buttons that map to nothing', () => {
    const k = new PadKeys();
    expect(k.update(set('GamepadMenu'), 0)).toEqual([]);
    expect(k.update(set('GamepadMenu'), 1000)).toEqual([]);
  });

  it('goes quiet for good once the platform delivers gamepad keys itself', () => {
    const k = new PadKeys();
    k.update(set('GamepadDPadDown'), 0);
    k.stop();
    expect(k.update(set('GamepadDPadDown'), DEDUPE_MS)).toEqual([]);
    expect(k.update(set(), 1000)).toEqual([]);
    expect(k.isStopped).toBe(true);
  });
});
