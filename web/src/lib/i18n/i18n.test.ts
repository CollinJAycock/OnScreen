import en from './locales/en.json';
import es from './locales/es.json';
import zhHans from './locales/zh-Hans.json';
import {
  LOCALES, STORAGE_KEY, applyAccountLocale, formatNumber, initLocale, locale, matchLocale,
  setLocale, storedLocale, t, tn, tRich, useDeviceLocale, type Locale, type MessageKey,
} from './index.svelte';

function setBrowserLanguages(langs: string[]) {
  Object.defineProperty(navigator, 'languages', { value: langs, configurable: true });
}

beforeEach(async () => {
  localStorage.clear();
  setBrowserLanguages(['en-US']);
  await setLocale('en');
});

describe('t', () => {
  it('returns the English string, fills placeholders and formats numbers', () => {
    expect(t('nav.home')).toBe('Home');
    expect(t('login.signInWith', { provider: 'Authentik' })).toBe('Sign in with Authentik');
    expect(t('nav.requestsPending', { count: 1234 })).toBe('1,234 pending');
  });

  it('leaves an unknown placeholder as written', () => {
    expect(t('login.signInWith')).toBe('Sign in with {provider}');
  });

  it('switches language and falls back to English for a missing key', async () => {
    await setLocale('es');
    expect(locale()).toBe('es');
    expect(t('nav.home')).toBe('Inicio');
    expect(document.documentElement.lang).toBe('es');
    // Every key falls back to English if a translation lacks it.
    expect(t('nonexistent.key' as MessageKey)).toBe('nonexistent.key');
  });
});

describe('tn', () => {
  it('picks the plural form for each language', async () => {
    expect(tn('nav.requestsWaiting', 1)).toBe('1 request waiting for approval');
    expect(tn('nav.requestsWaiting', 3)).toBe('3 requests waiting for approval');
    await setLocale('es');
    expect(tn('nav.requestsWaiting', 1)).toBe('1 solicitud pendiente de aprobación');
    expect(tn('nav.requestsWaiting', 2)).toBe('2 solicitudes pendientes de aprobación');
    // Spanish "many" (1,000,000) has no form of its own here: _other is used.
    expect(tn('nav.requestsWaiting', 1_000_000)).toBe('1.000.000 solicitudes pendientes de aprobación');
    await setLocale('zh-Hans');
    expect(tn('nav.requestsWaiting', 1)).toBe('1 个请求等待批准');
  });
});

describe('tRich', () => {
  it('splits around placeholders, wherever the translation puts them', async () => {
    expect(tRich('shell.viewingAs', { name: 'kid' })[0]).toEqual({ text: 'Viewing as ' });
    expect(tRich('shell.viewingAs', { name: 'kid' })[1]).toEqual({ text: 'kid', param: 'name' });
    await setLocale('zh-Hans');
    const segs = tRich('server.savedNote', { changeServer: 'A', server: 'B' });
    expect(segs.filter((s) => s.param).map((s) => s.text)).toEqual(['A', 'B']);
    expect(segs.map((s) => s.text).join('')).toContain('“A”');
  });

  it('never parses markup in values', () => {
    const segs = tRich('shell.viewingAs', { name: '<img src=x onerror=alert(1)>' });
    expect(segs.find((s) => s.param)!.text).toBe('<img src=x onerror=alert(1)>');
  });
});

describe('matchLocale', () => {
  it.each([
    [['es-MX', 'en'], 'es'],
    [['en-GB'], 'en'],
    [['zh-CN'], 'zh-Hans'],
    [['zh'], 'zh-Hans'],
    [['zh-Hans-SG'], 'zh-Hans'],
    [['zh-TW', 'es'], 'es'],
    [['zh-Hant-HK'], null],
    [['fr-FR', 'de'], null],
    [[], null],
  ])('%j -> %s', (tags, want) => {
    expect(matchLocale(tags as string[])).toBe(want);
  });
});

describe('startup and account', () => {
  it('uses the browser language when the device has no choice', async () => {
    setBrowserLanguages(['es-ES', 'en']);
    await initLocale();
    expect(locale()).toBe('es');
    expect(storedLocale()).toBeNull();
  });

  it("prefers this device's saved choice over the browser", async () => {
    setBrowserLanguages(['es-ES']);
    localStorage.setItem(STORAGE_KEY, 'zh-Hans');
    await initLocale();
    expect(locale()).toBe('zh-Hans');
  });

  it("ignores a stored value that isn't a shipped language", async () => {
    localStorage.setItem(STORAGE_KEY, 'fr');
    await initLocale();
    expect(locale()).toBe('en');
  });

  it("applies and remembers the account's choice; null keeps the device's", async () => {
    await applyAccountLocale('es');
    expect(locale()).toBe('es');
    expect(storedLocale()).toBe('es');
    await applyAccountLocale(null);
    expect(locale()).toBe('es');
    await applyAccountLocale('xx');
    expect(locale()).toBe('es');
  });

  it('"Device language" forgets the choice and follows the browser again', async () => {
    setBrowserLanguages(['zh-CN']);
    await setLocale('es', { persist: true });
    await useDeviceLocale();
    expect(storedLocale()).toBeNull();
    expect(locale()).toBe('zh-Hans');
  });

  it('formats numbers for the active language', async () => {
    await setLocale('es');
    expect(formatNumber(1234567)).toBe('1.234.567');
  });
});

// The catalogs themselves. Missing translations are allowed (they fall back
// to English) and only reported; a key English doesn't have, a placeholder
// mismatch or a plural form the language doesn't use is an error.
describe('catalogs', () => {
  const catalogs: Record<Exclude<Locale, 'en'>, Record<string, string>> = { es, 'zh-Hans': zhHans };
  const placeholders = (msg: string) => [...msg.matchAll(/\{(\w+)\}/g)].map((m) => m[1]).sort();
  const pluralForm = /_(zero|one|two|few|many|other)$/;

  it('covers every shipped language', () => {
    expect(Object.keys(catalogs).sort()).toEqual(LOCALES.filter((l) => l !== 'en').sort());
  });

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
          // An English plural form this language doesn't use isn't missing.
          const m = k.match(pluralForm);
          return !(m && !categories.includes(m[1] as Intl.LDMLPluralRule));
        });
        if (missing.length) console.info(`${loc}: ${missing.length} untranslated: ${missing.join(', ')}`);
        expect(Array.isArray(missing)).toBe(true);
      });
    });
  }
});
