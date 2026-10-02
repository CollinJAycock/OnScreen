import { describe, expect, it, vi } from 'vitest';
import {
  chromeMajor,
  closeApp,
  isEntryRoute,
  platformBackFn,
  rootBack,
  rootBackHandler,
  webosFromChrome,
  webosFromDeviceInfo,
  webosVersion,
  type RootBackDeps,
} from './appExit';

// User agents as the TVs send them (webOS writes "Web0S", with a zero).
const ua = (chrome: string) =>
  `Mozilla/5.0 (Web0S; Linux/SmartTV) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/${chrome} Safari/537.36 WebAppManager`;
const WEBOS_6 = ua('79.0.3945.79'); // the C1
const WEBOS_22 = ua('87.0.4280.88');
const WEBOS_23 = ua('94.0.4606.128');
const WEBOS_24 = ua('108.0.5359.211');
const WEBOS_25 = ua('120.0.6099.270');
const DESKTOP = 'Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/140.0.0.0 Safari/537.36';

describe('webOS version', () => {
  it('reads the Chromium major off a user agent', () => {
    expect(chromeMajor(WEBOS_6)).toBe(79);
    expect(chromeMajor(WEBOS_25)).toBe(120);
    expect(chromeMajor('Mozilla/5.0 (Web0S; Linux/SmartTV)')).toBeNull();
  });

  it("maps the engine to the release by LG's table, a newer engine to the latest", () => {
    expect(webosFromChrome(38)).toBe(3);
    expect(webosFromChrome(53)).toBe(4);
    expect(webosFromChrome(68)).toBe(5);
    expect(webosFromChrome(79)).toBe(6);
    expect(webosFromChrome(87)).toBe(22);
    expect(webosFromChrome(94)).toBe(23);
    expect(webosFromChrome(108)).toBe(24);
    expect(webosFromChrome(120)).toBe(25);
    expect(webosFromChrome(132)).toBe(26);
    expect(webosFromChrome(150)).toBe(26);
    expect(webosFromChrome(30)).toBeNull();
    expect(webosFromChrome(null)).toBeNull();
  });

  it('reads deviceInfo platformVersion as the SDK version (7.x = 22, 8.x = 23)', () => {
    expect(webosFromDeviceInfo(JSON.stringify({ platformVersion: '6.3.1' }))).toBe(6);
    expect(webosFromDeviceInfo(JSON.stringify({ platformVersion: '7.2.0' }))).toBe(22);
    expect(webosFromDeviceInfo({ platformVersion: '8.0.0' })).toBe(23);
    expect(webosFromDeviceInfo({ platformVersionMajor: '9', platformVersion: '9.1.0' })).toBe(24);
    expect(webosFromDeviceInfo({ platformVersionMajor: 10 })).toBe(25);
    expect(webosFromDeviceInfo('not json')).toBeNull();
    expect(webosFromDeviceInfo({ modelName: 'OLED55C1' })).toBeNull();
    expect(webosFromDeviceInfo(undefined)).toBeNull();
  });

  it('prefers the engine, falls back to deviceInfo, and is null off webOS', () => {
    expect(webosVersion({ userAgent: WEBOS_6 })).toBe(6);
    expect(webosVersion({ userAgent: WEBOS_22 })).toBe(22);
    expect(webosVersion({ userAgent: WEBOS_23, deviceInfo: '{"platformVersion":"8.0.0"}' })).toBe(23);
    expect(webosVersion({ userAgent: WEBOS_24 })).toBe(24);
    expect(webosVersion({ userAgent: 'Mozilla/5.0 (Web0S; Linux/SmartTV)', deviceInfo: '{"platformVersion":"7.1.0"}' })).toBe(22);
    // A desktop browser (the dev server) is not a TV, whatever its Chrome.
    expect(webosVersion({ userAgent: DESKTOP })).toBeNull();
  });
});

describe('rootBack (Back that no screen took)', () => {
  it('knows the first screens: the splash, Home, Sign in and Setup', () => {
    for (const r of ['/', '/hub', '/login', '/setup']) expect(isEntryRoute(r)).toBe(true);
    for (const r of ['/libraries', '/item/[id]', '/watch/[id]', '/pair', '/settings', null, undefined]) {
      expect(isEntryRoute(r)).toBe(false);
    }
  });

  it('shows the exit popup on a first screen on webOS TV 6 to 22, and when the version is unknown', () => {
    for (const route of ['/hub', '/login', '/setup', '/']) {
      expect(rootBack(route, 6, true)).toBe('exit-popup');
      expect(rootBack(route, 22, true)).toBe('exit-popup');
      expect(rootBack(route, null, true)).toBe('exit-popup');
    }
  });

  it("goes to the TV's Home through platformBack on webOS TV 23 and later", () => {
    for (const v of [23, 24, 25, 26]) expect(rootBack('/hub', v, true)).toBe('platform-back');
    // No platformBack in this runtime: the popup still lets the user out.
    expect(rootBack('/hub', 24, false)).toBe('exit-popup');
  });

  it('goes to the previous page everywhere else, whatever the version', () => {
    for (const v of [6, 22, 23, null]) {
      expect(rootBack('/item/[id]', v, true)).toBe('previous');
      expect(rootBack(null, v, true)).toBe('previous');
    }
  });
});

describe('rootBackHandler', () => {
  function deps(route: string, version: number | null, withPlatformBack = true) {
    const platformBack = vi.fn();
    const d = {
      routeId: () => route,
      version: () => version,
      platformBack: () => (withPlatformBack ? platformBack : null),
      goBack: vi.fn(),
      openExitPopup: vi.fn(),
    } satisfies RootBackDeps;
    return { d, platformBack };
  }

  it('opens the popup on Home (webOS TV 6), never going back to an earlier page', () => {
    const { d, platformBack } = deps('/hub', 6);
    expect(rootBackHandler(d)()).toBe(true);
    expect(d.openExitPopup).toHaveBeenCalledTimes(1);
    expect(d.goBack).not.toHaveBeenCalled();
    expect(platformBack).not.toHaveBeenCalled();
  });

  it('calls platformBack on Sign in on webOS TV 23+', () => {
    const { d, platformBack } = deps('/login', 23);
    expect(rootBackHandler(d)()).toBe(true);
    expect(platformBack).toHaveBeenCalledTimes(1);
    expect(d.openExitPopup).not.toHaveBeenCalled();
    expect(d.goBack).not.toHaveBeenCalled();
  });

  it('goes back a page off the first screens', () => {
    const { d, platformBack } = deps('/libraries', 24);
    expect(rootBackHandler(d)()).toBe(true);
    expect(d.goBack).toHaveBeenCalledTimes(1);
    expect(d.openExitPopup).not.toHaveBeenCalled();
    expect(platformBack).not.toHaveBeenCalled();
  });
});

describe('runtime hooks', () => {
  it('finds platformBack on webOSSystem, then PalmSystem, then webOSTV.js, bound to its object', () => {
    const sys = { calls: 0, platformBack(this: { calls: number }) { this.calls++; } };
    platformBackFn({ webOSSystem: sys, PalmSystem: { platformBack: () => { throw new Error('not this one'); } } })?.();
    expect(sys.calls).toBe(1);

    const palm = vi.fn();
    platformBackFn({ PalmSystem: { platformBack: palm } })?.();
    expect(palm).toHaveBeenCalledTimes(1);

    const lib = vi.fn();
    platformBackFn({ webOS: { platformBack: lib } })?.();
    expect(lib).toHaveBeenCalledTimes(1);

    expect(platformBackFn({})).toBeNull();
    expect(platformBackFn({ PalmSystem: { deviceInfo: '{}' } })).toBeNull();
  });

  it('closes the app with window.close(), and survives a runtime that refuses', () => {
    const close = vi.fn();
    closeApp({ close });
    expect(close).toHaveBeenCalledTimes(1);
    expect(() =>
      closeApp({
        close: () => {
          throw new Error('Scripts may close only the windows that were opened by them.');
        },
      }),
    ).not.toThrow();
  });
});
