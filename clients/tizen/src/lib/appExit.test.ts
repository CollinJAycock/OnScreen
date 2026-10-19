import { describe, expect, it, vi } from 'vitest';
import { ENTRY_ROUTES, closeApp, isEntryRoute, rootBack, rootBackHandler } from './appExit';

describe('rootBack (Return that no screen took)', () => {
  it('knows the first screens: the splash, Home, Sign in and Setup', () => {
    expect(ENTRY_ROUTES).toEqual(['/', '/hub', '/login', '/setup']);
    for (const r of ENTRY_ROUTES) expect(isEntryRoute(r)).toBe(true);
    expect(isEntryRoute('/item/[id]')).toBe(false);
    expect(isEntryRoute(null)).toBe(false);
  });

  // Samsung's Return Key Policy (1.1.0 review, 2026-10): on the first
  // screen, ask with the app's own HTML popup before exiting.
  it('offers the exit popup on a first screen, and goes back a page everywhere else', () => {
    expect(rootBack('/hub')).toBe('exit-popup');
    expect(rootBack('/login')).toBe('exit-popup');
    expect(rootBack('/library/[id]')).toBe('previous');
    expect(rootBack('/watch/[id]')).toBe('previous');
  });
});

describe('rootBackHandler', () => {
  function setup(route: string) {
    const deps = { routeId: () => route, goBack: vi.fn(), openExitPopup: vi.fn() };
    return { deps, handler: rootBackHandler(deps) };
  }

  it('opens the exit popup on Home, never going back to an earlier page', () => {
    const { deps, handler } = setup('/hub');
    expect(handler()).toBe(true);
    expect(deps.openExitPopup).toHaveBeenCalledTimes(1);
    expect(deps.goBack).not.toHaveBeenCalled();
  });

  it('goes back a page off the first screens', () => {
    const { deps, handler } = setup('/settings');
    expect(handler()).toBe(true);
    expect(deps.goBack).toHaveBeenCalledTimes(1);
    expect(deps.openExitPopup).not.toHaveBeenCalled();
  });
});

describe('closeApp', () => {
  it("uses Tizen's application exit", () => {
    const exit = vi.fn();
    const close = vi.fn();
    closeApp({ tizen: { application: { getCurrentApplication: () => ({ exit }) } }, close });
    expect(exit).toHaveBeenCalledTimes(1);
    expect(close).not.toHaveBeenCalled();
  });

  it('falls back to window.close(), and survives a runtime that refuses', () => {
    const close = vi.fn();
    closeApp({ close });
    expect(close).toHaveBeenCalledTimes(1);
    const refusing = {
      tizen: { application: { getCurrentApplication: () => { throw new Error('denied'); } } },
      close: () => { throw new Error('no'); },
    };
    expect(() => closeApp(refusing)).not.toThrow();
  });
});
