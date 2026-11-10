import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { render } from 'svelte/server';
import { createRawSnippet, type Component } from 'svelte';

// The browsing screens (stage 3 part 2), rendered on the server in Chinese:
// no English may be left in their text or their aria-labels, apart from
// names that are never translated. A server render shows a screen as it
// first paints (no onMount: nothing loads), so what only shows once data is
// in (the hub's rows, the library's chips, notices) is covered by the lib
// labels below and by the catalog check, which fails on a browse.json key
// left in English. Modelled on screens.test.ts.

vi.mock('$app/navigation', () => ({ goto: vi.fn() }));
vi.mock('$app/state', () => ({
  page: { url: new URL('http://tv.local/'), route: { id: '/' }, params: { id: 'lib1' } },
}));

import { setLocale, t, tn } from './index.svelte';
import enBrowse from './locales/en/browse.json';
import esBrowse from './locales/es/browse.json';
import zhBrowse from './locales/zh-Hans/browse.json';
import RootPage from '../../routes/+page.svelte';
import HubPage from '../../routes/hub/+page.svelte';
import LibrariesPage from '../../routes/libraries/+page.svelte';
import LibraryPage from '../../routes/library/[id]/+page.svelte';
import SearchPage from '../../routes/search/+page.svelte';
import WatchlistPage from '../../routes/watchlist/+page.svelte';
import FavoritesPage from '../../routes/favorites/+page.svelte';
import HistoryPage from '../../routes/history/+page.svelte';
import DiscoverPage from '../../routes/discover/+page.svelte';
import CollectionPage from '../../routes/collection/[id]/+page.svelte';
import HubRow from '../components/HubRow.svelte';
import LibrarySearch from '../components/LibrarySearch.svelte';
import PosterCard from '../components/PosterCard.svelte';
import QrCode from '../components/QrCode.svelte';
import Spinner from '../components/Spinner.svelte';
import { SORT_OPTIONS, sortLabel } from '../librarySort';
import { resultTypeLabel } from '../librarySearch';
import {
  STATUS_LABELS,
  WATCHLIST_EMPTY,
  WATCHLIST_MISSING,
  WatchlistRow,
  rowTitle,
  statusButtonLabel,
  statusChangedText,
  statusChoices,
  statusMenuMessage,
  visibleRows,
} from '../watchlist';

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
  vi.stubGlobal('sessionStorage', memoryStorage());
  vi.stubGlobal('location', { hash: '#/hub' });
  vi.stubGlobal(
    'fetch',
    vi.fn(async () => ({ status: 404, ok: false, json: async () => ({}) }) as unknown as Response),
  );
});

afterEach(async () => {
  vi.unstubAllGlobals();
  await setLocale('en');
});

/** The text a screen shows, its aria-labels included: its markup without
 *  tags, styles and comments. */
function text(html: string): string {
  const labels: string[] = [];
  const re = /aria-label="([^"]*)"/g;
  for (let m = re.exec(html); m; m = re.exec(html)) labels.push(m[1]);
  return (
    html
      .replace(/<style[\s\S]*?<\/style>/g, ' ')
      .replace(/<!--[\s\S]*?-->/g, '')
      .replace(/<[^>]+>/g, ' ')
      .replace(/&nbsp;|&#160;/g, ' ')
      .replace(/&amp;/g, '&') +
    ' ' +
    labels.join(' ')
  );
}

// Never translated (docs/i18n.md): the product, services and formats, and
// the on-screen keyboard's mode keys.
const NAMES = ['OnScreen', 'TMDB', 'DVR', 'ABC', 'abc', '#+='];

/** Words of three or more Latin letters left once the names are gone. */
function english(s: string): string[] {
  for (const n of NAMES) s = s.split(n).join(' ');
  return s.match(/[A-Za-z]{3,}/g) ?? [];
}

const screens: Record<string, Component> = {
  root: RootPage,
  hub: HubPage,
  libraries: LibrariesPage,
  library: LibraryPage,
  search: SearchPage,
  watchlist: WatchlistPage,
  favorites: FavoritesPage,
  history: HistoryPage,
  discover: DiscoverPage,
  collection: CollectionPage,
};

const snippet = createRawSnippet(() => ({ render: () => '<span></span>' }));

describe('in Chinese', () => {
  it.each(Object.entries(screens))('%s has no English left', async (_name, screen) => {
    await setLocale('zh-Hans');
    const { body } = render(screen);
    expect(english(text(body))).toEqual([]);
  });

  it('the search page shows its chips and prompt in Chinese', async () => {
    await setLocale('zh-Hans');
    const s = text(render(SearchPage).body);
    expect(s).toContain('✓ 电影');
    expect(s).toContain('单集');
    expect(s).toContain('请至少输入 2 个字符。');
  });

  it('a poster card names its badges in Chinese', async () => {
    await setLocale('zh-Hans');
    const count = render(PosterCard, { props: { title: '异形', unwatchedCount: 3 } }).body;
    expect(count).toContain('aria-label="3 集未看"');
    const done = render(PosterCard, { props: { title: '异形', watched: true } }).body;
    expect(done).toContain('aria-label="已看"');
    expect(english(text(count) + text(done))).toEqual([]);
  });

  it('a hub row, the spinner and the QR code have no English left', async () => {
    await setLocale('zh-Hans');
    const row = render(HubRow, { props: { title: '继续观看', hint: t('browse.holdOk'), children: snippet } }).body;
    expect(text(row)).toContain('按住 OK 键查看选项');
    const spinner = render(Spinner).body;
    expect(text(spinner)).toContain('正在加载…');
    const qr = render(QrCode, { props: { text: 'https://tv.local/' } }).body;
    expect(qr).toContain('aria-label="二维码"');
    expect(english(text(row) + text(spinner) + text(qr))).toEqual([]);
  });

  it('would see English left (the check is not empty)', () => {
    const words = english(text(render(SearchPage).body));
    expect(words).toContain('Movies');
    expect(words).toContain('characters');
  });

  it('Search this library has no English left', async () => {
    await setLocale('zh-Hans');
    const { body } = render(LibrarySearch, { props: { libraryId: 'lib1', libraryName: '电影', onclose: () => {} } });
    expect(text(body)).toContain('搜索 电影');
    expect(english(text(body))).toEqual([]);
  });
});

describe('labels built in lib', () => {
  it('reads the sort choices in the language shown now', async () => {
    expect(SORT_OPTIONS[0].label).toBe('Title (A–Z)');
    await setLocale('zh-Hans');
    expect(SORT_OPTIONS.map((o) => o.label)).toEqual([
      '标题（A–Z）', '标题（Z–A）', '最近添加', '年份（从新到旧）', '年份（从旧到新）', '评分最高',
    ]);
    expect(sortLabel({ sort: 'rating', dir: 'asc' })).toBe('排序');
    await setLocale('es');
    expect(sortLabel({ sort: 'rating', dir: 'desc' })).toBe('Mejor valorados');
  });

  it('names the watchlist statuses and rows in the language shown now', async () => {
    const plan = new WatchlistRow('plan_to_watch');
    plan.items = [{ id: 'p', type: 'movie', title: 'P', status: 'plan_to_watch' } as WatchlistRow['items'][number]];
    plan.total = 1234;
    // Drawn in English, then the language changes: the heading follows.
    const [row] = visibleRows([plan]);
    expect(row.title).toBe('Plan to Watch · 1,234');
    await setLocale('zh-Hans');
    expect(row.title).toBe('计划观看 · 1,234');
    expect(STATUS_LABELS.on_hold).toBe('暂时搁置');
    expect(rowTitle('dropped', null)).toBe('已弃坑');
    await setLocale('es');
    expect(row.title).toBe('Por ver · 1234');
    expect(STATUS_LABELS.completed).toBe('Completado');
  });

  it("words the title page's Status menu in the language shown now", async () => {
    await setLocale('zh-Hans');
    expect(statusButtonLabel(null)).toBe('加入待看清单');
    expect(statusButtonLabel('watching')).toBe('状态：正在观看');
    expect(statusButtonLabel(undefined)).toBe('待看清单');
    expect(statusChoices('on_hold').map((c) => c.label)).toEqual([
      '计划观看', '正在观看', '✓ 暂时搁置', '已看完', '已弃坑', '从待看清单中移除',
    ]);
    expect(statusMenuMessage('on_hold')).toBe('已在待看清单中：暂时搁置');
    expect(statusMenuMessage(null)).toBe('不在待看清单中');
    expect(statusChangedText(null)).toBe('已从待看清单中移除');
    // The empty Watchlist names the button by its own label.
    expect(t(WATCHLIST_EMPTY)).toContain(`“${statusButtonLabel(null)}”`);
    expect(english(t(WATCHLIST_MISSING))).toEqual([]);
    await setLocale('es');
    expect(statusButtonLabel('plan_to_watch')).toBe('Estado: Por ver');
    expect(t(WATCHLIST_EMPTY)).toContain(statusButtonLabel(null));
    expect(statusChangedText('completed')).toBe('Estado: Completado');
  });

  it('names result types, keeping one it has no label for', async () => {
    expect(resultTypeLabel('episode')).toBe('episode');
    await setLocale('zh-Hans');
    expect(resultTypeLabel('episode')).toBe('单集');
    expect(resultTypeLabel('music_video')).toBe('音乐视频');
    expect(resultTypeLabel('hologram')).toBe('hologram');
    expect(resultTypeLabel('constructor')).toBe('constructor');
    await setLocale('es');
    expect(resultTypeLabel('movie')).toBe('película');
  });

  it('uses the plural forms of each language', async () => {
    expect(tn('search.hidden', 1)).toBe('1 match hidden by the type filters above.');
    expect(tn('search.hidden', 3)).toBe('3 matches hidden by the type filters above.');
    await setLocale('es');
    expect(tn('search.hidden', 1)).toBe('1 coincidencia oculta por los filtros de tipo de arriba.');
    expect(tn('browse.typeMore', 2)).toBe('Escribe al menos 2 caracteres.');
    await setLocale('zh-Hans');
    expect(tn('browse.unwatched', 1)).toBe('1 集未看');
  });
});

describe('browse.json', () => {
  const en = enBrowse as Record<string, string>;
  const es = esBrowse as Record<string, string>;
  const zh = zhBrowse as Record<string, string>;
  // Plural forms Chinese doesn't use (it has only _other).
  const base = (k: string) => k.replace(/_(one|few|many|other)$/, '');
  // The same in every language: a separator between two placeholders.
  const SAME = new Set(['watchlist.rowTitle']);

  it('has a Spanish and a Chinese draft for every key', () => {
    for (const k of Object.keys(en)) {
      expect(es[k], `es ${k}`).toBeTypeOf('string');
      const zhKey = k.endsWith('_one') ? `${base(k)}_other` : k;
      expect(zh[zhKey], `zh-Hans ${zhKey}`).toBeTypeOf('string');
    }
  });

  it('leaves no English in the Chinese', () => {
    for (const [k, v] of Object.entries(zh)) {
      const words = english(v.replace(/\{\w+\}/g, ' ')).filter((w) => w !== 'OK');
      expect(words, k).toEqual([]);
      if (!SAME.has(k)) expect(v, k).not.toBe(en[k]);
    }
  });
});

describe('the screens use the store in their markup', () => {
  // A bare t() in the markup of a legacy component renders once; these are
  // runes components, but the rule (docs/i18n.md) keeps them safe to convert.
  const files = [
    '../../routes/+page.svelte',
    '../../routes/+layout.svelte',
    '../../routes/hub/+page.svelte',
    '../../routes/libraries/+page.svelte',
    '../../routes/library/[id]/+page.svelte',
    '../../routes/search/+page.svelte',
    '../../routes/watchlist/+page.svelte',
    '../../routes/favorites/+page.svelte',
    '../../routes/history/+page.svelte',
    '../../routes/discover/+page.svelte',
    '../../routes/collection/[id]/+page.svelte',
    '../components/PosterCard.svelte',
    '../components/HubRow.svelte',
    '../components/LibrarySearch.svelte',
    '../components/Spinner.svelte',
    '../components/QrCode.svelte',
  ];
  it.each(files)('%s', (file) => {
    const markup = source(file)
      .replace(/<script[\s\S]*?<\/script>/g, '')
      .replace(/<style[\s\S]*?<\/style>/g, '');
    expect(markup).not.toMatch(/(?<![\w$.])tn?\(/);
  });
});
