import { describe, it, expect } from 'vitest';
import { stepDelay, formatOffset, offsetToSave, MAX_OFFSET_MS, OFFSET_STEPS_MS } from './subtitle-offset';

describe('stepDelay', () => {
  it('adds steps in whole milliseconds', () => {
    let d = 0;
    for (let i = 0; i < 3; i++) d = stepDelay(d, 100);
    expect(d).toBe(0.3);
    expect(stepDelay(d, -1000)).toBe(-0.7);
  });
  it('clamps to the server range', () => {
    expect(stepDelay(MAX_OFFSET_MS / 1000, 1000)).toBe(MAX_OFFSET_MS / 1000);
    expect(stepDelay(-MAX_OFFSET_MS / 1000, -100)).toBe(-MAX_OFFSET_MS / 1000);
  });
  it('offers 100 ms and 1 s steps both ways', () => {
    expect([...OFFSET_STEPS_MS]).toEqual([-1000, -100, 100, 1000]);
  });
});

describe('formatOffset', () => {
  it('signs and rounds to a tenth', () => {
    expect(formatOffset(0)).toBe('0.0 s');
    expect(formatOffset(1200)).toBe('+1.2 s');
    expect(formatOffset(-300)).toBe('−0.3 s');
    expect(formatOffset(40)).toBe('0.0 s');
    expect(formatOffset(-2500)).toBe('−2.5 s');
  });
});

describe('offsetToSave', () => {
  it('adds the local adjustment to the server offset', () => {
    expect(offsetToSave(500, 1.2)).toBe(1700);
    expect(offsetToSave(-1000, 0.3)).toBe(-700);
    expect(offsetToSave(0, -0.1)).toBe(-100);
  });
  it('clamps to ±10 minutes', () => {
    expect(offsetToSave(MAX_OFFSET_MS, 5)).toBe(MAX_OFFSET_MS);
    expect(offsetToSave(-MAX_OFFSET_MS, -5)).toBe(-MAX_OFFSET_MS);
  });
});
