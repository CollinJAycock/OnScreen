import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { render } from 'svelte/server';
import type { Component } from 'svelte';

// The screens translated so far, rendered on the server in Chinese: no
// English may be left in their text, apart from names that are never
// translated. A string added in English only (or a constant built at import
// time) shows up here. Screens join the list as their stage lands
// (docs/i18n.md).

vi.mock('$app/navigation', () => ({ goto: vi.fn() }));
vi.mock('$app/state', () => ({ page: { url: new URL('http://tv.local/'), route: { id: '/' } } }));

import { setLocale } from './index.svelte';
import SetupPage from '../../routes/setup/+page.svelte';
import LoginPage from '../../routes/login/+page.svelte';
import PairPage from '../../routes/pair/+page.svelte';
import SettingsPage from '../../routes/settings/+page.svelte';
import ScrobblePage from '../../routes/settings/scrobble/+page.svelte';
import ExitPopup from '../components/ExitPopup.svelte';
import TopNav from '../components/TopNav.svelte';
import { PRIVACY_POLICY_URL } from '../legal';

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

afterEach(async () => {
  vi.unstubAllGlobals();
  await setLocale('en');
});

/** The text a screen shows: its markup without tags, styles and comments. */
function text(html: string): string {
  return html
    .replace(/<style[\s\S]*?<\/style>/g, ' ')
    .replace(/<!--[\s\S]*?-->/g, '')
    .replace(/<[^>]+>/g, ' ')
    .replace(/&nbsp;|&#160;/g, ' ')
    .replace(/&amp;/g, '&');
}

// Never translated (docs/i18n.md): the product, services, formats, and the
// sample addresses and keyboard keys. "Scrobble" stays as it is in Chinese,
// as in the web app's catalog.
const NAMES = [
  PRIVACY_POLICY_URL, 'Scrobble', 'OnScreen', 'ListenBrainz', 'Last.fm', 'Trakt', 'https://media.example.com', 'https://', 'http://',
  'listenbrainz.org/settings', 'Settings › Applications', 'last.fm', 'API', 'PIN', 'URL', 'Apache',
  'ABC', 'abc', '#+=', 'Mbps', 'kbps', '------', '/pair', 'English', 'Español',
];

/** Words of three or more Latin letters left once the names are gone. */
function english(html: string): string[] {
  let s = text(html);
  for (const n of NAMES) s = s.split(n).join(' ');
  return s.match(/[A-Za-z]{3,}/g) ?? [];
}

const screens: Record<string, Component> = {
  setup: SetupPage,
  login: LoginPage,
  pair: PairPage,
  settings: SettingsPage,
  scrobble: ScrobblePage,
  topNav: TopNav,
};

describe('in Chinese', () => {
  it.each(Object.entries(screens))('%s has no English left', async (_name, screen) => {
    await setLocale('zh-Hans');
    const { body } = render(screen);
    expect(english(body)).toEqual([]);
  });

  it('the exit popup has no English left', async () => {
    await setLocale('zh-Hans');
    const { body } = render(ExitPopup, { props: { onexit: () => {}, oncancel: () => {} } });
    expect(english(body)).toEqual([]);
    expect(text(body)).toContain('退出 OnScreen？');
  });
});

describe('in Spanish', () => {
  it('styles the filled-in values without stray spaces', async () => {
    await setLocale('es');
    const { body } = render(SetupPage);
    const html = body.replace(/<!--[\s\S]*?-->/g, '');
    expect(html).toMatch(
      /p\. ej\. <code[^>]*>192\.168\.1\.10:7070<\/code> o <code[^>]*>https:\/\/media\.example\.com<\/code>\. Sin esquema/,
    );
  });

  it('Settings shows the language row in Spanish', async () => {
    await setLocale('es');
    const { body } = render(SettingsPage);
    const t = text(body).replace(/\s+/g, ' ');
    expect(t).toContain('Ajustes');
    expect(t).toContain('Idioma de la app');
    expect(t).toContain('Idioma del dispositivo (Español)');
    expect(t).toContain('Cerrar sesión');
  });
});
