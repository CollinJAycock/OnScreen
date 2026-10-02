import { readdirSync, readFileSync } from 'node:fs';
import { join, relative } from 'node:path';

const mockInvoke = vi.hoisted(() => vi.fn());
vi.mock('@tauri-apps/api/core', () => ({ invoke: mockInvoke }));

import { confirmAction } from './native';

afterEach(() => {
  delete window.__TAURI_INTERNALS__;
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
  mockInvoke.mockReset();
});

describe('confirmAction in the browser', () => {
  it('returns the answer from window.confirm', async () => {
    const confirmSpy = vi.fn().mockReturnValueOnce(true).mockReturnValueOnce(false);
    vi.stubGlobal('confirm', confirmSpy);

    expect(await confirmAction('Delete it?')).toBe(true);
    expect(await confirmAction('Delete it?')).toBe(false);
    expect(confirmSpy).toHaveBeenCalledWith('Delete it?');
    expect(mockInvoke).not.toHaveBeenCalled();
  });
});

describe('confirmAction in the desktop app', () => {
  // What tauri-plugin-dialog's init script installs: an async function,
  // so its Promise result is always truthy.
  const pluginConfirm = vi.fn(async () => true);

  beforeEach(() => {
    window.__TAURI_INTERNALS__ = {};
    vi.stubGlobal('confirm', pluginConfirm);
    pluginConfirm.mockClear();
  });

  it('asks the shell and returns OK as true', async () => {
    mockInvoke.mockResolvedValue(true);
    expect(await confirmAction('Delete it?')).toBe(true);
    expect(mockInvoke).toHaveBeenCalledWith('confirm_dialog', { message: 'Delete it?' });
  });

  it('returns Cancel as false and never trusts the replaced window.confirm', async () => {
    mockInvoke.mockResolvedValue(false);
    expect(await confirmAction('Delete it?')).toBe(false);
    expect(pluginConfirm).not.toHaveBeenCalled();
  });

  it('fails closed when the dialog cannot be shown', async () => {
    vi.spyOn(console, 'error').mockImplementation(() => {});
    mockInvoke.mockRejectedValue(new Error('Command confirm_dialog not found'));
    expect(await confirmAction('Delete it?')).toBe(false);
  });

  it('treats anything but a boolean true as Cancel', async () => {
    mockInvoke.mockResolvedValue('true');
    expect(await confirmAction('Delete it?')).toBe(false);
  });
});

// tauri-plugin-dialog replaces window.confirm and window.alert in the
// desktop webview with calls the webview isn't allowed to make: confirm
// returns a truthy Promise (so a guard on it never stops anything) and
// alert shows nothing. Keep guards on confirmAction and error reports on
// toasts.
describe('browser dialogs', () => {
  const src = join(import.meta.dirname, '..');
  const stripComments = (code: string) =>
    code
      .replace(/<!--[\s\S]*?-->/g, '')
      .replace(/\/\*[\s\S]*?\*\//g, '')
      .replace(/(^|[^:])\/\/.*$/gm, '$1');
  const sources = (readdirSync(src, { recursive: true }) as string[])
    .filter((f) => /\.(svelte|ts)$/.test(f) && !/\.test\.ts$/.test(f))
    .map((f) => ({ file: f, code: stripComments(readFileSync(join(src, f), 'utf8')) }));
  const callersOf = (name: string) => {
    const call = new RegExp(`(?<![\\w$.])${name}\\s*\\(|\\b(?:window|globalThis|self)\\.${name}\\s*\\(`);
    return sources.filter((s) => call.test(s.code)).map((s) => s.file);
  };

  it('guards go through confirmAction, not window.confirm', () => {
    expect(callersOf('confirm').filter((f) => f !== join('lib', 'native.ts'))).toEqual([]);
  });

  it('errors are reported with toasts, not window.alert', () => {
    expect(callersOf('alert')).toEqual([]);
  });
});
