import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { render } from 'svelte/server';

// The page navigates and its nav reads the route through SvelteKit, which
// isn't here.
vi.mock('$app/navigation', () => ({ goto: vi.fn() }));
vi.mock('$app/state', () => ({ page: { url: new URL('http://tv.local/') } }));

import SettingsPage from './+page.svelte';
import { encodeQr, qrPath } from '$lib/qr';
import { PRIVACY_POLICY_URL } from '$lib/legal';

// Settings > About rendered on the server (no DOM: no focusable action, no
// onMount), signed out, every request answered 404.

// The project has no Node typings, so node:fs comes in untyped.
const fsModule = 'node:fs';
const { existsSync, readFileSync } = (await import(/* @vite-ignore */ fsModule)) as {
  existsSync(path: URL): boolean;
  readFileSync(path: URL, encoding: 'utf8'): string;
};

function memoryStorage() {
  const m = new Map<string, string>();
  return {
    getItem: (k: string) => (m.has(k) ? (m.get(k) as string) : null),
    setItem: (k: string, v: string) => void m.set(k, String(v)),
    removeItem: (k: string) => void m.delete(k),
    clear: () => m.clear(),
  };
}

beforeEach(() => {
  vi.stubGlobal('localStorage', memoryStorage());
  vi.stubGlobal('location', { hash: '#/settings' });
  vi.stubGlobal(
    'fetch',
    vi.fn(async () => ({ status: 404, ok: false, json: async () => ({}) }) as unknown as Response),
  );
});

afterEach(() => {
  vi.unstubAllGlobals();
});

/** The About section's markup. */
function about(): string {
  const { body } = render(SettingsPage);
  const at = body.indexOf('>About<');
  expect(at).toBeGreaterThan(-1);
  return body.slice(at);
}

/** The element whose opening tag carries `attr`, up to the next row. */
function row(html: string, attr: string): string {
  const at = html.indexOf(attr);
  expect(at, attr).toBeGreaterThan(-1);
  const start = html.lastIndexOf('<', at);
  const next = html.indexOf('data-focus-key=', at + attr.length);
  return html.slice(start, next < 0 ? undefined : next);
}

describe('Settings > About', () => {
  it('shows the privacy policy address as text', () => {
    const privacy = row(about(), 'data-focus-key="settings:privacy"');
    expect(privacy).toContain('Privacy policy');
    expect(privacy).toContain(`>${PRIVACY_POLICY_URL}<`);
  });

  it('shows the privacy policy as a QR code that encodes that address', () => {
    const privacy = row(about(), 'data-focus-key="settings:privacy"');
    expect(privacy).toContain('aria-label="Privacy policy address"');
    const qr = encodeQr(PRIVACY_POLICY_URL, 'M');
    expect(qr).not.toBeNull();
    expect(privacy).toContain(`d="${qrPath(qr!, 4)}"`);
  });

  it('has no LG terms screen (that one is the webOS app\'s)', () => {
    expect(about()).not.toContain('Licence &amp; terms');
    expect(existsSync(new URL('./legal/+page.svelte', import.meta.url))).toBe(false);
  });

  // Back to Settings puts the ring on the row that was left; the preferences
  // and scrobble rows then load in above it and can push it off the screen. Each load puts it back in view while the
  // restore still holds (lib/focus/memory restoreAgain).
  it('brings the restored row back into view after each late load', () => {
    const source = readFileSync(new URL('./+page.svelte', import.meta.url), 'utf8');
    expect(source).toContain('const guard = backMemo?.focusedId ? restoreGuard() : null;');
    expect(source).toContain('restoreKeyed(backMemo, guard)');
    const loads = source.split('await tick();\n      restoreAgain(backMemo, guard);').length - 1;
    expect(loads).toBe(2);
    expect(source).toContain('guard?.end()');
  });

  it('still shows the version', () => {
    expect(about()).toContain('Version');
  });
});
