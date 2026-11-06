// Interface translations (v3). Deliberately small and dependency-free so the
// same module can serve the web app and the TV web apps (Chromium 69/79):
//
//   - Catalogs are flat JSON, one file per language in ./locales. English is
//     bundled and is the source of truth; other languages load on demand.
//     Translation tools (Weblate, Crowdin) import this format as-is.
//   - t('key', { name }) looks the key up in the active language, falls back
//     to English, then to the key itself, and fills {placeholders}.
//   - tn('key', count) picks key_one / key_few / key_many / key_other with the
//     browser's own Intl.PluralRules, falling back to key_other.
//   - The active locale is $state, so every t() in a template re-renders when
//     the language changes, in runes and legacy components alike.
//
// See docs/i18n.md for conventions and how to add a string or a language.

import { writable, type Readable } from 'svelte/store';
import en from './locales/en.json';

/** Every key in the English catalog. t() accepts only these. */
export type MessageKey = keyof typeof en;

/** Keys usable with tn(): the base of every key that has an _other form. */
export type PluralKey = {
  [K in MessageKey]: K extends `${infer Base}_other` ? Base : never;
}[MessageKey];

/** The languages the apps ship, as BCP 47 tags. Mirrors the server's
 *  SupportedUILanguages (users.ui_language CHECK, migration 00063). */
export const LOCALES = ['en', 'es', 'zh-Hans'] as const;
export type Locale = (typeof LOCALES)[number];

/** Each language's name in itself, for the language picker. Never translated:
 *  a reader looks for their own language written their own way. */
export const LOCALE_NAMES: Record<Locale, string> = {
  en: 'English',
  es: 'Español',
  'zh-Hans': '简体中文',
};

type Catalog = Readonly<Record<string, string>>;
type Params = Readonly<Record<string, string | number>>;

const loaders: Record<Exclude<Locale, 'en'>, () => Promise<{ default: Catalog }>> = {
  es: () => import('./locales/es.json'),
  'zh-Hans': () => import('./locales/zh-Hans.json'),
};

const catalogs: Partial<Record<Locale, Catalog>> = { en };
const state = $state<{ locale: Locale }>({ locale: 'en' });

/** localStorage key for this device's choice (also used before sign-in). */
export const STORAGE_KEY = 'onscreen.locale';

export function isLocale(v: unknown): v is Locale {
  return typeof v === 'string' && (LOCALES as readonly string[]).includes(v);
}

/** The active language. Reading it in a template or $derived tracks it. */
export function locale(): Locale {
  return state.locale;
}

function lookup(loc: Locale, key: string): string | undefined {
  return catalogs[loc]?.[key] ?? (en as Catalog)[key];
}

const numberFormats = new Map<Locale, Intl.NumberFormat>();
/** A number in the active language's style ("1,234" / "1.234" / "1,234"). */
export function formatNumber(n: number): string {
  const loc = state.locale;
  let f = numberFormats.get(loc);
  if (!f) {
    f = new Intl.NumberFormat(loc);
    numberFormats.set(loc, f);
  }
  return f.format(n);
}

function interpolate(msg: string, params: Params): string {
  return msg.replace(/\{(\w+)\}/g, (whole, name: string) => {
    const v = params[name];
    if (v === undefined) return whole;
    return typeof v === 'number' ? formatNumber(v) : v;
  });
}

/** The translated string for key, with {placeholders} filled from params. */
export function t(key: MessageKey, params?: Params): string {
  const msg = lookup(state.locale, key) ?? key;
  return params ? interpolate(msg, params) : msg;
}

/** One piece of a tRich() message: literal text, or a filled-in placeholder
 *  (param names which one) that the template can style. */
export interface RichSegment {
  text: string;
  param?: string;
}

/** t() split around its placeholders, for a message whose filled-in values
 *  are styled (<strong>, <code>) by the template. No HTML is ever parsed:
 *  translators can move a placeholder, and values stay plain text.
 *
 *    {#each tRich('shell.viewingAs', { name }) as s}
 *      {#if s.param}<strong>{s.text}</strong>{:else}{s.text}{/if}
 *    {/each}
 */
export function tRich(key: MessageKey, params: Params): RichSegment[] {
  const msg = lookup(state.locale, key) ?? key;
  const out: RichSegment[] = [];
  let last = 0;
  // exec loop, not matchAll: the TV web apps run Chromium 69 (no matchAll).
  const re = /\{(\w+)\}/g;
  for (let m = re.exec(msg); m; m = re.exec(msg)) {
    const v = params[m[1]];
    if (v === undefined) continue;
    if (m.index > last) out.push({ text: msg.slice(last, m.index) });
    out.push({ text: typeof v === 'number' ? formatNumber(v) : v, param: m[1] });
    last = m.index + m[0].length;
  }
  if (last < msg.length) out.push({ text: msg.slice(last) });
  return out;
}

/** The translator as a value: tr('key'), tr.n('key', count), tr.rich(...). */
export interface Translator {
  (key: MessageKey, params?: Params): string;
  n: (key: PluralKey, count: number, params?: Params) => string;
  rich: (key: MessageKey, params: Params) => RichSegment[];
}

function makeTranslator(): Translator {
  const f = ((key: MessageKey, params?: Params) => t(key, params)) as Translator;
  f.n = (key, count, params) => tn(key, count, params);
  f.rich = (key, params) => tRich(key, params);
  return f;
}

const translator = writable<Translator>(makeTranslator());

/** Use this in templates: {$tr('nav.home')}, {$tr.n('x', n)},
 *  {#each $tr.rich(...) as s}. A new translator is published whenever the
 *  language changes, so the text re-renders in every component, including
 *  legacy (non-runes) ones, where the compiler treats a bare {t('…')} as
 *  static and renders it once. Plain t() is for script code that runs at a
 *  moment in time (an error message being set). */
export const tr: Readable<Translator> = { subscribe: translator.subscribe };

const pluralRules = new Map<Locale, Intl.PluralRules>();
/** The plural form of key for count: key_one, key_few, key_many or key_other,
 *  as the language's plural rules pick. {count} is filled in. */
export function tn(key: PluralKey, count: number, params?: Params): string {
  const loc = state.locale;
  let rules = pluralRules.get(loc);
  if (!rules) {
    rules = new Intl.PluralRules(loc);
    pluralRules.set(loc, rules);
  }
  const form = rules.select(count);
  const msg =
    catalogs[loc]?.[`${key}_${form}`] ??
    catalogs[loc]?.[`${key}_other`] ??
    (en as Catalog)[`${key}_${form}`] ??
    (en as Catalog)[`${key}_other`] ??
    key;
  return interpolate(msg, { ...params, count });
}

/** The best shipped language for a list of BCP 47 tags in preference order
 *  (navigator.languages), or null. Traditional Chinese (zh-TW, zh-HK,
 *  zh-Hant) isn't shipped, so it doesn't match zh-Hans: the next tag decides. */
export function matchLocale(tags: readonly string[]): Locale | null {
  for (const raw of tags) {
    const tag = raw.toLowerCase();
    const [lang, second] = tag.split('-');
    if (lang === 'en') return 'en';
    if (lang === 'es') return 'es';
    if (lang === 'zh') {
      if (second === 'hans' || second === undefined || second === 'cn' || second === 'sg' || second === 'my') {
        return 'zh-Hans';
      }
    }
  }
  return null;
}

function readStored(): Locale | null {
  try {
    const v = localStorage.getItem(STORAGE_KEY);
    return isLocale(v) ? v : null;
  } catch {
    return null;
  }
}

/** The language explicitly chosen on this device (or synced from the
 *  account), or null when it follows the browser. */
export function storedLocale(): Locale | null {
  return readStored();
}

function writeStored(loc: Locale | null): void {
  try {
    if (loc) localStorage.setItem(STORAGE_KEY, loc);
    else localStorage.removeItem(STORAGE_KEY);
  } catch {
    /* private mode / blocked storage: the choice lasts this session only */
  }
}

/** Switch the interface language, loading its catalog first. persist keeps
 *  the choice on this device (for the sign-in screen and the next visit). */
export async function setLocale(loc: Locale, opts: { persist?: boolean } = {}): Promise<void> {
  if (!catalogs[loc] && loc !== 'en') {
    try {
      catalogs[loc] = (await loaders[loc]()).default;
    } catch (e) {
      console.warn(`loading the ${loc} translations failed`, e);
      return;
    }
  }
  state.locale = loc;
  translator.set(makeTranslator());
  if (typeof document !== 'undefined') document.documentElement.lang = loc;
  if (opts.persist) writeStored(loc);
}

/** Startup: this device's stored choice, else the browser's languages, else
 *  English. Never throws. */
export async function initLocale(): Promise<void> {
  const browser = typeof navigator !== 'undefined' ? navigator.languages ?? [navigator.language] : [];
  await setLocale(readStored() ?? matchLocale(browser) ?? 'en');
}

/** After sign-in: the account's choice (GET /users/me/preferences
 *  ui_language) wins over the device's and is remembered on the device.
 *  null = the account has no choice: keep whatever the device uses. */
export async function applyAccountLocale(uiLanguage: string | null | undefined): Promise<void> {
  if (isLocale(uiLanguage) && uiLanguage !== state.locale) {
    await setLocale(uiLanguage, { persist: true });
  } else if (isLocale(uiLanguage)) {
    writeStored(uiLanguage);
  }
}

/** Picker "Use device language": forget this device's choice and follow the
 *  browser again. */
export async function useDeviceLocale(): Promise<void> {
  writeStored(null);
  await initLocale();
}
