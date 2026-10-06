import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { holdScreenAwake, resetScreenSaverForTests } from './screenSaver';

const OFF = 0;
const ON = 1;

function installAppCommon(impl?: (state: number, ok?: () => void, err?: (e: unknown) => void) => void) {
  const setScreenSaver = vi.fn(impl ?? (() => {}));
  (globalThis as unknown as { webapis: unknown }).webapis = {
    appcommon: { AppCommonScreenSaverState: { SCREEN_SAVER_OFF: OFF, SCREEN_SAVER_ON: ON }, setScreenSaver },
  };
  return setScreenSaver;
}

describe('holdScreenAwake', () => {
  beforeEach(() => resetScreenSaverForTests());
  afterEach(() => {
    delete (globalThis as unknown as { webapis?: unknown }).webapis;
  });

  it('turns the screensaver off while anything holds it, on once nothing does', () => {
    const set = installAppCommon();
    holdScreenAwake('player', true);
    holdScreenAwake('slideshow', true);
    holdScreenAwake('player', false);
    expect(set.mock.calls.map((c) => c[0])).toEqual([OFF]);
    holdScreenAwake('slideshow', false);
    expect(set.mock.calls.map((c) => c[0])).toEqual([OFF, ON]);
  });

  it('sends nothing for a repeat of the same state', () => {
    const set = installAppCommon();
    holdScreenAwake('player', true);
    holdScreenAwake('player', true);
    holdScreenAwake('player', false);
    holdScreenAwake('player', false);
    expect(set.mock.calls.map((c) => c[0])).toEqual([OFF, ON]);
  });

  it('retries after the TV refuses', () => {
    const set = installAppCommon((_s, _ok, err) => err?.(new Error('refused')));
    holdScreenAwake('player', true);
    holdScreenAwake('player', true);
    expect(set).toHaveBeenCalledTimes(2);
  });

  it('survives a firmware whose call throws, and a browser without webapis', () => {
    installAppCommon(() => {
      throw new Error('not supported');
    });
    expect(() => holdScreenAwake('player', true)).not.toThrow();
    delete (globalThis as unknown as { webapis?: unknown }).webapis;
    expect(() => holdScreenAwake('player', false)).not.toThrow();
  });
});
