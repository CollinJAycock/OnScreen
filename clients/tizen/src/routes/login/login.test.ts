import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { render } from 'svelte/server';

// The page navigates through SvelteKit, which isn't here.
vi.mock('$app/navigation', () => ({ goto: vi.fn() }));
vi.mock('$app/state', () => ({ page: { url: new URL('http://tv.local/') } }));

import LoginPage from './+page.svelte';
import PairPage from '../pair/+page.svelte';

// Sign in rendered on the server (no DOM: no focusable action, no onMount),
// signed out on a server that answers every request 404, and the sources of
// what doesn't run there.

// The project has no Node typings, so node:fs comes in untyped.
const fsModule = 'node:fs';
const { readFileSync } = (await import(/* @vite-ignore */ fsModule)) as {
  readFileSync(path: URL, encoding: 'utf8'): string;
};
const source = (path: string) => readFileSync(new URL(path, import.meta.url), 'utf8');

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
  vi.stubGlobal('location', { hash: '#/login' });
  vi.stubGlobal(
    'fetch',
    vi.fn(async () => ({ status: 404, ok: false, json: async () => ({}) }) as unknown as Response),
  );
});

afterEach(() => {
  vi.unstubAllGlobals();
});

/** The <button> whose text is `label`, with its attributes. */
function button(html: string, label: string): string | null {
  const m = new RegExp(`<button\\b[^>]*>\\s*(?:<!--[^>]*-->)?\\s*${label}\\s*(?:<!--[^>]*-->)?\\s*</button>`).exec(html);
  return m ? m[0] : null;
}

describe('Sign in', () => {
  // A wrong but reachable server took the user to Sign in with no way back:
  // Back there offered only to exit (Sign in is a first screen).
  it('offers Change server on the username step', () => {
    const html = render(LoginPage).body;
    expect(html).toContain('Username');
    expect(button(html, 'Change server')).not.toBeNull();
  });

  it('changes server as the hub does: forget it, then Setup', () => {
    const page = source('./+page.svelte');
    expect(page).toMatch(/await api\.forgetServer\(\);\s*goto\('#\/setup'\);/);
  });

  // Back on the first step: to Setup when Setup opened Sign in (it pushes
  // its route, lib/nav), the exit popup when Sign in is the launch screen
  // (the back stack is empty: the splash replaces itself).
  it('goes Back to Setup when Setup opened it, and leaves Back to the app otherwise', () => {
    const page = source('./+page.svelte');
    expect(page).toMatch(/if \(backTarget\(\) === '#\/setup'\) \{\s*goBack\('#\/setup'\);\s*return true;\s*\}\s*return false;/);
    const setup = source('../setup/+page.svelte');
    expect(setup).toContain("pushTo('#/login')");
    expect(setup).not.toContain("goto('#/login')");
  });
});

describe('Pair (sign in with another device)', () => {
  it('offers Change server too: it is reachable signed out', () => {
    const html = render(PairPage).body;
    expect(button(html, 'Change server')).not.toBeNull();
    expect(source('../pair/+page.svelte')).toMatch(/await api\.forgetServer\(\);\s*goto\('#\/setup'\);/);
  });
});
