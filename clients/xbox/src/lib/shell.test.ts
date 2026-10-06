import { afterEach, describe, expect, it, vi } from 'vitest';
import { parseShellParams, postToShell, resolveShellParams, servedOrigin, shellCommand } from './shell';

function memory() {
  const m = new Map<string, string>();
  return { getItem: (k: string) => m.get(k) ?? null, setItem: (k: string, v: string) => void m.set(k, v) };
}

describe('shell parameters', () => {
  it('reads the shell, the console model and the display flags', () => {
    expect(parseShellParams('?shell=xbox&device=Xbox%20Series%20X&hdr=1&uhd=0')).toEqual({
      shell: 'xbox', device: 'Xbox Series X', hdr: true, uhd: false,
    });
  });

  it('is no shell without ?shell, and unknown flags stay unknown', () => {
    expect(parseShellParams('?device=x').shell).toBeNull();
    expect(parseShellParams('?shell=xbox&hdr=maybe').hdr).toBeNull();
    expect(parseShellParams('').shell).toBeNull();
  });

  it('keeps the parameters for a reload that lost the query string', () => {
    const store = memory();
    resolveShellParams('?shell=xbox&device=Xbox%20One', store);
    expect(resolveShellParams('', store)).toEqual({ shell: 'xbox', device: 'Xbox One', hdr: null, uhd: null });
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

describe('shellCommand', () => {
  it("reads the shell's back message and nothing else", () => {
    expect(shellCommand({ type: 'back' })).toBe('back');
    expect(shellCommand({ type: 'exit' })).toBeNull();
    expect(shellCommand('back')).toBeNull();
    expect(shellCommand(null)).toBeNull();
  });
});
