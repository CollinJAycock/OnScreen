import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { parseShellParams, postToShell, resolveShellParams, servedOrigin, shellCommand, shellKey } from './shell';

function memory() {
  const m = new Map<string, string>();
  return { getItem: (k: string) => m.get(k) ?? null, setItem: (k: string, v: string) => void m.set(k, v) };
}

describe('shell parameters', () => {
  it('reads the shell, the console model and the display flags', () => {
    expect(parseShellParams('?shell=xbox&device=Xbox%20Series%20X&native=1&hdr=1&uhd=0')).toEqual({
      shell: 'xbox', device: 'Xbox Series X', hdr: true, uhd: false, native: true,
    });
    // A shell without the native player (before 0.1.6).
    expect(parseShellParams('?shell=xbox').native).toBe(false);
  });

  it('is no shell without ?shell, and unknown flags stay unknown', () => {
    expect(parseShellParams('?device=x').shell).toBeNull();
    expect(parseShellParams('?shell=xbox&hdr=maybe').hdr).toBeNull();
    expect(parseShellParams('').shell).toBeNull();
  });

  it('keeps the parameters for a reload that lost the query string', () => {
    const store = memory();
    resolveShellParams('?shell=xbox&device=Xbox%20One&native=1', store);
    expect(resolveShellParams('', store)).toEqual({ shell: 'xbox', device: 'Xbox One', hdr: null, uhd: null, native: true });
  });
});

describe('servedOrigin', () => {
  it('is the page origin when a server serves the page', () => {
    expect(servedOrigin({ protocol: 'http:', origin: 'http://192.168.1.10:7070' }, false)).toBe('http://192.168.1.10:7070');
    expect(servedOrigin({ protocol: 'https:', origin: 'https://media.example.com' }, false)).toBe('https://media.example.com');
  });

  it('is null in vite dev and off http(s)', () => {
    expect(servedOrigin({ protocol: 'http:', origin: 'http://localhost:5174' }, true)).toBeNull();
    expect(servedOrigin({ protocol: 'file:', origin: 'null' }, false)).toBeNull();
    expect(servedOrigin(null, false)).toBeNull();
  });
});

describe('postToShell', () => {
  afterEach(() => vi.unstubAllGlobals());

  it('posts through WebView2 when there is a shell, and says so when there is not', () => {
    expect(postToShell('exit')).toBe(false);
    const postMessage = vi.fn();
    vi.stubGlobal('chrome', { webview: { postMessage } });
    expect(postToShell('changeServer')).toBe(true);
    expect(postMessage).toHaveBeenCalledWith({ type: 'changeServer' });
  });
});

describe("the desktop app's TV mode (Steam Deck)", () => {
  // shellParams() is read once per module load.
  beforeEach(() => vi.resetModules());
  afterEach(() => {
    vi.unstubAllGlobals();
    vi.resetModules();
  });

  it('asks for exit and a server change by navigating to onscreen-shell: URLs', async () => {
    const assign = vi.fn();
    vi.stubGlobal('location', { search: '?shell=desktop&device=Steam%20Deck', assign });
    vi.stubGlobal('sessionStorage', memory());
    const shell = await import('./shell');
    expect(shell.shellParams()).toEqual({ shell: 'desktop', device: 'Steam Deck', hdr: null, uhd: null, native: false });
    expect(shell.inShell()).toBe(true);
    expect(shell.postToShell('exit')).toBe(true);
    expect(assign).toHaveBeenLastCalledWith('onscreen-shell://exit');
    // Forget server: the desktop app's own server screen takes over.
    expect(shell.afterForgetServer(false)).toBeNull();
    expect(assign).toHaveBeenLastCalledWith('onscreen-shell://changeServer');
    // The Xbox shell's key and player messages have no desktop meaning.
    assign.mockClear();
    expect(shell.postToShell('nativeKeys')).toBe(false);
    expect(shell.postToShell('webKeys')).toBe(false);
    expect(assign).not.toHaveBeenCalled();
  });

  it('asks by navigation even where WebView2 gives the page chrome.webview (Windows)', async () => {
    const assign = vi.fn();
    const postMessage = vi.fn();
    vi.stubGlobal('location', { search: '?shell=desktop', assign });
    vi.stubGlobal('sessionStorage', memory());
    // On Windows that channel is Tauri's IPC, which ignores these messages.
    vi.stubGlobal('chrome', { webview: { postMessage } });
    const shell = await import('./shell');
    expect(shell.postToShell('exit')).toBe(true);
    expect(assign).toHaveBeenLastCalledWith('onscreen-shell://exit');
    expect(postMessage).not.toHaveBeenCalled();
    expect(shell.postNative({ cmd: 'play' })).toBe(false);
  });

  it('is no shell in a plain browser', async () => {
    const assign = vi.fn();
    vi.stubGlobal('location', { search: '', assign });
    vi.stubGlobal('sessionStorage', memory());
    const shell = await import('./shell');
    expect(shell.inShell()).toBe(false);
    expect(shell.postToShell('exit')).toBe(false);
    expect(shell.afterForgetServer(false)).toBe('#/login');
    expect(assign).not.toHaveBeenCalled();
  });

  it('only has URLs for exit and changeServer', async () => {
    const { desktopShellUrl } = await import('./shell');
    expect(desktopShellUrl('exit')).toBe('onscreen-shell://exit');
    expect(desktopShellUrl('changeServer')).toBe('onscreen-shell://changeServer');
    expect(desktopShellUrl('native')).toBeNull();
  });
});

describe('shellCommand', () => {
  it("reads the shell's back message and nothing else", () => {
    expect(shellCommand({ type: 'back' })).toBe('back');
    expect(shellCommand({ type: 'exit' })).toBeNull();
    expect(shellCommand('back')).toBeNull();
    expect(shellCommand(null)).toBeNull();
  });
});

describe('shellKey', () => {
  it("reads the shell's controller keys by their gamepad name", () => {
    expect(shellKey({ type: 'key', event: 'keydown', code: 196, repeat: false })).toEqual({
      type: 'keydown',
      key: 'GamepadB',
      code: 196,
      repeat: false,
    });
    expect(shellKey({ type: 'key', event: 'keydown', code: 206, repeat: true })?.repeat).toBe(true);
    expect(shellKey({ type: 'key', event: 'keyup', code: 195, repeat: true })).toEqual({
      type: 'keyup',
      key: 'GamepadA',
      code: 195,
      repeat: false,
    });
  });

  it('ignores anything that is not a gamepad key', () => {
    expect(shellKey({ type: 'key', event: 'keydown', code: 13 })).toBeNull();
    expect(shellKey({ type: 'key', event: 'press', code: 195 })).toBeNull();
    expect(shellKey({ type: 'back' })).toBeNull();
    expect(shellKey(null)).toBeNull();
  });
});
