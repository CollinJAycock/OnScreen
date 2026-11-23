import { afterEach, describe, expect, it } from 'vitest';
import { canvasScale, fitScale, setCanvasScale, toLayoutPx } from './canvasScale';

describe('fitScale', () => {
  it('is 1 on the 1920x1080 plane a UHD model gives', () => {
    expect(fitScale(1920, 1080)).toBe(1);
  });

  it('is two thirds on the 1280x720 plane a Full HD model gives', () => {
    expect(fitScale(1280, 720)).toBeCloseTo(2 / 3, 10);
  });

  it('fits by the tighter side', () => {
    expect(fitScale(1280, 1080)).toBeCloseTo(1280 / 1920, 10);
    expect(fitScale(1920, 540)).toBeCloseTo(0.5, 10);
  });

  it('never scales up, and ignores a window with no size yet', () => {
    expect(fitScale(3840, 2160)).toBe(1);
    expect(fitScale(0, 0)).toBe(1);
    expect(fitScale(Number.NaN, 720)).toBe(1);
  });
});

describe('toLayoutPx', () => {
  afterEach(() => setCanvasScale(1));

  it('is the identity unscaled', () => {
    expect(toLayoutPx(120)).toBe(120);
  });

  it('turns a scaled distance back into layout pixels', () => {
    setCanvasScale(fitScale(1280, 720));
    expect(canvasScale()).toBeCloseTo(2 / 3, 10);
    // A card that moved 80 visual px moved 120 layout px.
    expect(toLayoutPx(80)).toBeCloseTo(120, 10);
  });

  it('ignores a nonsense scale', () => {
    setCanvasScale(0);
    expect(canvasScale()).toBe(1);
  });
});
