import { afterEach, describe, expect, it, vi } from 'vitest';
import { closeApp, isEntryRoute, rootBack, rootBackHandler } from './appExit';

describe('rootBack', () => {
  it('offers to exit on the first screens and goes back elsewhere', () => {
    for (const r of ['/', '/hub', '/login', '/setup']) expect(rootBack(r)).toBe('exit-popup');
    for (const r of ['/settings', '/item/[id]', '/watch/[id]', null, undefined]) expect(rootBack(r)).toBe('previous');
    expect(isEntryRoute('/hub')).toBe(true);
  });

  it('always takes the key', () => {
    const goBack = vi.fn();
    const openExitPopup = vi.fn();
    let route = '/hub';
    const handler = rootBackHandler({ routeId: () => route, goBack, openExitPopup });
    expect(handler()).toBe(true);
    expect(openExitPopup).toHaveBeenCalledTimes(1);
    route = '/settings';
    expect(handler()).toBe(true);
    expect(goBack).toHaveBeenCalledTimes(1);
  });
});

describe('closeApp', () => {
  afterEach(() => vi.unstubAllGlobals());

  it('asks the shell to exit, and does not also close the window', () => {
    const postMessage = vi.fn();
    vi.stubGlobal('chrome', { webview: { postMessage } });
    const close = vi.fn();
    closeApp({ close });
    expect(postMessage).toHaveBeenCalledWith({ type: 'exit' });
    expect(close).not.toHaveBeenCalled();
  });

  it('falls back to window.close without a shell', () => {
    const close = vi.fn();
    closeApp({ close });
    expect(close).toHaveBeenCalledTimes(1);
  });
});
