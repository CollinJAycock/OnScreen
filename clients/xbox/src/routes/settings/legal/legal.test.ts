import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { render } from 'svelte/server';

// The page navigates (lib/nav) and its nav reads the route through
// SvelteKit, which isn't here.
vi.mock('$app/navigation', () => ({ goto: vi.fn() }));
vi.mock('$app/state', () => ({ page: { url: new URL('http://tv.local/') } }));

import LegalPage from './+page.svelte';
import { BSD_3_LICENCE, MIT_LICENCE, NOTICES, TERMS, apacheTerms } from '$lib/legal';

// The Licence & terms screen rendered on the server (no DOM: no focusable
// action, no onMount), signed out, every request answered 404.

// The project has no Node typings, so node:fs comes in untyped.
const fsModule = 'node:fs';
const { readFileSync } = (await import(/* @vite-ignore */ fsModule)) as {
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
  vi.stubGlobal('location', { hash: '#/settings/legal' });
  vi.stubGlobal(
    'fetch',
    vi.fn(async () => ({ status: 404, ok: false, json: async () => ({}) }) as unknown as Response),
  );
});

afterEach(() => {
  vi.unstubAllGlobals();
});

/** The page's text, tags and comments out, entities and spaces as read. */
function text(): string {
  return render(LegalPage)
    .body.replace(/<!--[\s\S]*?-->/g, '')
    .replace(/<[^>]+>/g, ' ')
    .replace(/&quot;/g, '"')
    .replace(/&#39;/g, "'")
    .replace(/&lt;/g, '<')
    .replace(/&gt;/g, '>')
    .replace(/&amp;/g, '&')
    .replace(/\s+/g, ' ');
}

/** The section with this id. */
function section(id: string): string {
  const body = render(LegalPage).body;
  const at = body.indexOf(`id="legal-${id}"`);
  expect(at, id).toBeGreaterThan(-1);
  return body.slice(at, body.indexOf('</section>', at));
}

describe('Licence & terms', () => {
  it('has the end-user terms', () => {
    const page = text();
    expect(page).toContain('Licence & terms');
    for (const para of TERMS) expect(page).toContain(para);
    expect(page).toContain('and not by Microsoft');
    expect(page).toContain('its developer is not liable for any loss or damage');
  });

  it('names the Apache-2.0 licence of the app', () => {
    const licence = section('licence');
    expect(licence).toContain('Copyright 2026 Collin Aycock');
    expect(licence).toContain('Apache License, Version 2.0 (Apache-2.0)');
  });

  it('lists the third-party notices, hls.js among them', () => {
    const notices = section('notices');
    expect(notices).toContain('hls.js');
    expect(notices).toContain('Copyright (c) 2017 Dailymotion (http://www.dailymotion.com)');
    for (const n of NOTICES) {
      expect(notices).toContain(n.name);
      for (const inc of n.includes ?? []) expect(notices).toContain(inc.name);
    }
  });

  it('carries the MIT, BSD 3-Clause and Apache License texts in full', () => {
    const page = text();
    for (const para of MIT_LICENCE) expect(page).toContain(para);
    for (const para of BSD_3_LICENCE) expect(page).toContain(para);
    for (const para of apacheTerms()) expect(page).toContain(para);
    expect(page).toContain(
      'MIT License (eventemitter3, structured-field-values, Svelte, SvelteKit, esm-env, Vite (module preload helper), QR Code generator library)',
    );
    expect(page).toContain('BSD 3-Clause License (dash.js (CEA-608 parser))');
    expect(page).toContain(
      'Apache License 2.0 (this app, hls.js, url-toolkit, videojs-contrib-hls, vtt.js, Common Media Library)',
    );
  });

  it('offers a chip per section', () => {
    const body = render(LegalPage).body;
    const chips = body.slice(body.indexOf('data-focus-row'), body.indexOf('id="legal-terms"'));
    for (const label of ['Terms of use', 'Licence', 'Third-party notices', 'Licence texts']) {
      expect(chips).toContain(`>${label}</button>`);
    }
  });

  // Not run on the server: every block takes focus (the page only scrolls as
  // focus moves), the first one when the page opens, and Back goes back.
  it('makes every text block focusable, and Back returns to Settings', () => {
    const source = readFileSync(new URL('./+page.svelte', import.meta.url), 'utf8');
    const blocks = source.match(/<(p|div)\b[^>]*class="block[^"]*"[^>]*>/g) ?? [];
    expect(blocks.length).toBeGreaterThan(0);
    for (const tag of blocks) expect(tag).toContain('use:focusable');
    expect(source).toContain('use:focusable={{ autofocus: i === 0 }}');
    expect(source).toContain("goBack('#/settings')");
  });

  // The app's page box keeps Settings' scroll (this page opens from the
  // bottom of Settings): back to the top on mount, before the first block's
  // autofocus (a microtask) scrolls it in from there.
  it('opens at its title, not at Settings\' scroll', () => {
    const source = readFileSync(new URL('./+page.svelte', import.meta.url), 'utf8');
    const mount = source.slice(source.indexOf('onMount(() => {'));
    expect(mount).toMatch(/^onMount\(\(\) => \{\s*scrollPageToTop\(\);/);
  });

  // webOS 6's Chromium 79 drops a whole rule over one selector it doesn't
  // know, and :focus-visible is Chrome 86.
  it('styles nothing with :focus-visible', () => {
    const source = readFileSync(new URL('./+page.svelte', import.meta.url), 'utf8');
    const style = source.slice(source.indexOf('<style>'));
    expect(style.replace(/\/\*[\s\S]*?\*\//g, '')).not.toContain(':focus-visible');
  });
});
