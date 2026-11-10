import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import en from './locales/en';
import es from './locales/es';
import zhHans from './locales/zh-Hans';
import {
  LOCALES, STORAGE_KEY, applyAccountLocale, initLocale, locale, matchLocale, setLocale, storedLocale,
  t, tn, tRich, useDeviceLocale, type Locale, type MessageKey,
} from './index.svelte';
import { accountLocaleReader, chooseLocale } from './account';
import { audioTrackLabel, languageName, subtitleLabel } from '../langName';
import { LANGUAGE_OPTIONS, QUALITY_ROWS, SUBTITLE_LANGUAGE_OPTIONS } from '../settingsPrefs';
import { scrobbleSummary } from '../scrobbleLink';

// The TV apps' copy of the web app's i18n module (web/src/lib/i18n, kept
// identical by scripts/tv-i18n.mjs) with the TV catalogs, and the TV's own
// glue: the account's language (lib/i18n/account). The tests run under
// node: localStorage, navigator and document are the test's.

function memoryStorage() {
  const m = new Map<string, string>();
  return {
    getItem: (k: string) => (m.has(k) ? (m.get(k) as string) : null),
    setItem: (k: string, v: string) => void m.set(k, String(v)),
    removeItem: (k: string) => void m.delete(k),
    clear: () => m.clear(),
  };
}

function setTvLanguages(langs: string[]) {
  vi.stubGlobal('navigator', { languages: langs, language: langs[0] ?? '' });
}

const html: { lang?: string } = {};

beforeEach(async () => {
  vi.stubGlobal('localStorage', memoryStorage());
  vi.stubGlobal('document', { documentElement: html });
  setTvLanguages(['en-US']);
  await setLocale('en');
});

afterEach(() => {
  vi.unstubAllGlobals();
});

describe('t', () => {
  it('returns English, fills placeholders and switches language', async () => {
    expect(t('nav.home')).toBe('Home');
    expect(t('scrobble.connect', { service: 'Trakt' })).toBe('Connect Trakt');
    await setLocale('es');
    expect(locale()).toBe('es');
    expect(t('nav.home')).toBe('Inicio');
    expect(html.lang).toBe('es');
    await setLocale('zh-Hans');
    expect(t('nav.settings')).toBe('设置');
    // Every key falls back to English, then to itself.
    expect(t('nonexistent.key' as MessageKey)).toBe('nonexistent.key');
  });

  it('picks plural forms', async () => {
    expect(tn('scrobble.countLinked', 2)).toBe('2 linked');
    await setLocale('es');
    expect(tn('scrobble.countLinked', 1)).toBe('1 vinculado');
    expect(tn('scrobble.countLinked', 3)).toBe('3 vinculados');
  });

  it('splits a rich message around its placeholders in any order', async () => {
    await setLocale('zh-Hans');
    const segs = tRich('login.passwordFor', { username: 'ana' });
    expect(segs[0]).toEqual({ text: 'ana', param: 'username' });
    expect(segs.map((s) => s.text).join('')).toBe('ana 的密码');
  });
});

describe('the TV language', () => {
  it.each([
    [['es-US', 'en'], 'es'],
    [['zh-CN'], 'zh-Hans'],
    [['zh-TW', 'en-US'], 'en'],
    [['fr-FR'], null],
  ])('%j -> %s', (tags, want) => {
    expect(matchLocale(tags as string[])).toBe(want);
  });

  it("starts in the TV's language, unless this TV has a choice saved", async () => {
    setTvLanguages(['es-MX']);
    await initLocale();
    expect(locale()).toBe('es');
    localStorage.setItem(STORAGE_KEY, 'zh-Hans');
    await initLocale();
    expect(locale()).toBe('zh-Hans');
  });

  it('starts in English without storage or a language it ships', async () => {
    vi.stubGlobal('localStorage', undefined);
    setTvLanguages(['de-DE']);
    await initLocale();
    expect(locale()).toBe('en');
  });
});

describe('the account language (lib/i18n/account)', () => {
  it("applies the account's choice from a preferences read and keeps it on the TV", async () => {
    await accountLocaleReader()('es');
    expect(locale()).toBe('es');
    expect(storedLocale()).toBe('es');
    // No choice on the account: the TV keeps its own.
    await accountLocaleReader()(null);
    expect(locale()).toBe('es');
    await accountLocaleReader()('xx');
    expect(locale()).toBe('es');
  });

  it('ignores a read that was already out when the language was changed here', async () => {
    const stale = accountLocaleReader();
    await chooseLocale('zh-Hans', async () => {});
    await stale('es');
    expect(locale()).toBe('zh-Hans');
    // A read started after the change applies.
    await accountLocaleReader()('es');
    expect(locale()).toBe('es');
  });

  it('saves the choice to the account, and keeps it on the TV when that fails', async () => {
    const saved: (Locale | null)[] = [];
    expect(await chooseLocale('es', async (l) => void saved.push(l))).toBe(true);
    expect(saved).toEqual(['es']);
    expect(await chooseLocale('zh-Hans', async () => Promise.reject(new Error('404')))).toBe(false);
    expect(locale()).toBe('zh-Hans');
    expect(storedLocale()).toBe('zh-Hans');
  });

  it('"Device language" forgets the choice and follows the TV again', async () => {
    setTvLanguages(['es-ES']);
    await chooseLocale('zh-Hans', async () => {});
    const saved: (Locale | null)[] = [];
    await chooseLocale(null, async (l) => void saved.push(l));
    expect(saved).toEqual([null]);
    expect(storedLocale()).toBeNull();
    expect(locale()).toBe('es');
  });

  it('applyAccountLocale is the web one', async () => {
    await applyAccountLocale('zh-Hans');
    expect(locale()).toBe('zh-Hans');
    await useDeviceLocale();
    expect(locale()).toBe('en');
  });
});

describe('labels built in lib', () => {
  it('names languages, regions and scripts in the app language', async () => {
    expect(languageName('spa')).toBe('Spanish');
    expect(languageName('pt-BR')).toBe('Portuguese (Brazil)');
    await setLocale('es');
    expect(languageName('eng')).toBe('Inglés');
    expect(languageName('pt-BR')).toBe('Portugués (Brasil)');
    await setLocale('zh-Hans');
    expect(languageName('zh-Hant')).toBe('中文（繁体）');
    // Unknown codes still show as themselves.
    expect(languageName('xyz')).toBe('xyz');
  });

  it('builds track rows in the app language', async () => {
    await setLocale('es');
    expect(subtitleLabel({ language: 'fre', forced: true, sdh: false, downloaded: true, fallback: 'x' })).toBe(
      'Francés · forzados · descargado',
    );
    expect(audioTrackLabel({ language: '', channels: 6 }, 2)).toBe('3. Pista 3 · 6 canales');
  });

  it("reads Settings' choices in the language shown now", async () => {
    expect(LANGUAGE_OPTIONS[0].label).toBe('Auto / file default');
    expect(SUBTITLE_LANGUAGE_OPTIONS[0].label).toBe('Off');
    expect(QUALITY_ROWS[0].title).toBe('Maximum resolution');
    await setLocale('es');
    expect(LANGUAGE_OPTIONS[1].label).toBe('Inglés');
    expect(SUBTITLE_LANGUAGE_OPTIONS[0].label).toBe('Desactivados');
    expect(QUALITY_ROWS[0].title).toBe('Resolución máxima');
    expect(QUALITY_ROWS[0].options[0].label).toBe('Original');
    expect(scrobbleSummary(null).label).toBe('Desactivado');
  });
});

// The catalogs. Missing translations are allowed (they fall back to English)
// and only reported; a key English doesn't have, a placeholder mismatch or a
// plural form the language doesn't use is an error.
describe('catalog files', () => {
  const files = import.meta.glob<Record<string, string>>('./locales/*/*.json', { eager: true, import: 'default' });
  const langs = [...new Set(Object.keys(files).map((p) => p.split('/')[2]))];

  it('has the same area files for every language', () => {
    const areas = (lang: string) => Object.keys(files).filter((p) => p.split('/')[2] === lang).map((p) => p.split('/')[3]).sort();
    expect(langs.sort()).toEqual([...LOCALES].sort());
    for (const lang of langs) expect(areas(lang)).toEqual(areas('en'));
  });

  it.each(langs)('%s: no key is in two area files', (lang) => {
    const seen = new Map<string, string>();
    const dupes: string[] = [];
    for (const [p, cat] of Object.entries(files)) {
      if (p.split('/')[2] !== lang) continue;
      for (const k of Object.keys(cat)) {
        if (seen.has(k)) dupes.push(`${k} (${seen.get(k)}, ${p})`);
        else seen.set(k, p);
      }
    }
    expect(dupes).toEqual([]);
  });
});

describe('catalogs', () => {
  const catalogs: Record<Exclude<Locale, 'en'>, Record<string, string>> = { es, 'zh-Hans': zhHans };
  const placeholders = (msg: string) => (msg.match(/\{(\w+)\}/g) ?? []).sort();
  const pluralForm = /_(zero|one|two|few|many|other)$/;

  for (const [loc, cat] of Object.entries(catalogs)) {
    describe(loc, () => {
      const categories = new Intl.PluralRules(loc).resolvedOptions().pluralCategories;

      it('has only keys English has (plural forms by their base)', () => {
        const stale = Object.keys(cat).filter((k) => {
          if (k in en) return false;
          const m = k.match(pluralForm);
          return !(m && `${k.slice(0, -m[0].length)}_other` in en);
        });
        expect(stale).toEqual([]);
      });

      it("uses only the language's own plural forms", () => {
        const wrong = Object.keys(cat).filter((k) => {
          const m = k.match(pluralForm);
          return m !== null && !categories.includes(m[1] as Intl.LDMLPluralRule);
        });
        expect(wrong).toEqual([]);
      });

      it("keeps English's placeholders", () => {
        const mismatched = Object.entries(cat).filter(([k, msg]) => {
          const m = k.match(pluralForm);
          const enKey = k in en ? k : m ? `${k.slice(0, -m[0].length)}_other` : k;
          const source = (en as Record<string, string>)[enKey];
          return source !== undefined && placeholders(source).join() !== placeholders(msg).join();
        });
        expect(mismatched.map(([k]) => k)).toEqual([]);
      });

      it('reports untranslated strings', () => {
        const missing = Object.keys(en).filter((k) => {
          if (k in cat) return false;
          const m = k.match(pluralForm);
          return !(m && !categories.includes(m[1] as Intl.LDMLPluralRule));
        });
        if (missing.length) console.info(`${loc}: ${missing.length} untranslated: ${missing.join(', ')}`);
        expect(Array.isArray(missing)).toBe(true);
      });
    });
  }
});
