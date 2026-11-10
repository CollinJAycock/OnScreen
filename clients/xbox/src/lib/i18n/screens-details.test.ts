import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { render } from 'svelte/server';
import type { Component } from 'svelte';

// The item details screen, the photo viewer, Live TV and Recordings (stage
// 3, part 2) in Chinese and Spanish. Three checks, since most of these
// screens show their text only after onMount has loaded something, which a
// server render never runs:
//
//   - the screens as the server renders them (their loading state; the
//     Report a problem dialog's first step in full), with no English left;
//   - their source: no English in the markup outside {…} expressions, in a
//     literal attribute, or in a string in the script (every label the item
//     page builds is one), apart from the names below;
//   - the labels their lib helpers build, in Chinese and Spanish, read when
//     shown so they follow a language change.
//
// A string added in English only, or a label built at import time, fails
// here (docs/i18n.md).

vi.mock('$app/navigation', () => ({ goto: vi.fn(), afterNavigate: vi.fn() }));
vi.mock('$app/state', () => ({
  page: { url: new URL('http://tv.local/'), route: { id: '/item/[id]' }, params: { id: 'item-1' } },
}));

import { setLocale, t } from './index.svelte';
import enDetails from './locales/en/details.json';
import enLive from './locales/en/live.json';
import esDetails from './locales/es/details.json';
import esLive from './locales/es/live.json';
import zhDetails from './locales/zh-Hans/details.json';
import zhLive from './locales/zh-Hans/live.json';
import { WATCH_FILTER_OPTIONS, upNextLabel, watchWriteError } from '../watchState';
import { formatRuntime, resumeLabel } from '../detailText';
import {
  ALREADY_REPORTED_TEXT, ISSUE_KIND_OPTIONS, REPORT_SENT_TEXT, issueKindLabel, reportErrorMessage,
} from '../reportProblem';
import { appearsOnAlbums } from '../music';
import { describeTuneFailure, type LiveFatal } from '../liveTune';
import type { UpNext } from '../api/types';
import ReportProblemDialog from '../components/ReportProblemDialog.svelte';
import ItemPage from '../../routes/item/[id]/+page.svelte';
import PhotoPage from '../../routes/photo/[id]/+page.svelte';
import LiveTvPage from '../../routes/livetv/+page.svelte';
import RecordingsPage from '../../routes/recordings/+page.svelte';

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
  vi.stubGlobal('location', { hash: '#/item/item-1' });
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

// Never translated (docs/i18n.md): the product, formats and protocols, the
// remote's keys (OK, CH: two letters, so never counted), the hls.js error
// name a detail line quotes, and the names of this test's sample data.
const NAMES = ['OnScreen', 'HLS', 'HTTP', 'App', 'fragLoadError'];

/** Words of three or more Latin letters left once the names are gone (as
 *  whole words: "App" doesn't hide "Apply"). */
function english(s: string): string[] {
  for (const n of NAMES) s = s.replace(new RegExp(`\\b${n}\\b`, 'g'), ' ');
  return s.match(/[A-Za-z]{3,}/g) ?? [];
}

describe('the screens in Chinese', () => {
  const screens: Record<string, Component> = {
    item: ItemPage,
    photo: PhotoPage,
    livetv: LiveTvPage,
    recordings: RecordingsPage,
  };

  it.each(Object.entries(screens))('%s has no English left', async (_name, screen) => {
    await setLocale('zh-Hans');
    const { body } = render(screen);
    expect(english(text(body))).toEqual([]);
  });

  it('Live TV, Recordings and the photo viewer say what they are', async () => {
    await setLocale('zh-Hans');
    expect(text(render(LiveTvPage).body)).toContain('电视直播');
    expect(text(render(RecordingsPage).body)).toContain('录制');
    expect(text(render(PhotoPage).body)).toContain('正在加载…');
  });

  it('the Report a problem dialog has no English left', async () => {
    await setLocale('zh-Hans');
    const { body } = render(ReportProblemDialog, {
      props: { itemId: 'i1', itemTitle: '火线', openKinds: new Set(['audio'] as const), onclose: () => {} },
    });
    expect(english(text(body))).toEqual([]);
    const shown = text(body).replace(/\s+/g, ' ');
    expect(shown).toContain('报告问题');
    expect(shown).toContain('出了什么问题？');
    expect(shown).toContain('音频问题 （已报告）');
    expect(shown).toContain('取消');
  });
});

describe('the dialog in English and Spanish', () => {
  const props = { itemId: 'i1', itemTitle: 'X', openKinds: new Set(['audio'] as const), onclose: () => {} };

  it('reads as it always has in English', () => {
    const html = render(ReportProblemDialog, { props }).body.replace(/<!--[\s\S]*?-->/g, '');
    expect(html).toContain('>Report a problem</div>');
    expect(html).toContain(">What's wrong?</div>");
    expect(html).toMatch(/Audio problem<span class="tag[^"]*">— already reported<\/span>/);
    expect(html).toContain("Video won't play or looks wrong");
  });

  it('is in Spanish', async () => {
    await setLocale('es');
    const shown = text(render(ReportProblemDialog, { props }).body).replace(/\s+/g, ' ');
    expect(shown).toContain('Informar de un problema');
    expect(shown).toContain('¿Qué pasa?');
    expect(shown).toContain('Problema de audio — ya informado');
    expect(shown).toContain('Cancelar');
  });
});

// ── The source ─────────────────────────────────────────────────────────────

const sources = import.meta.glob<string>(
  [
    '../../routes/item/**/*.svelte',
    '../../routes/photo/**/*.svelte',
    '../../routes/livetv/*.svelte',
    '../../routes/recordings/*.svelte',
    '../components/ReportProblemDialog.svelte',
  ],
  { query: '?raw', import: 'default', eager: true },
);

/** s without its {…} expressions (nested braces included). */
function withoutExpressions(s: string): string {
  let out = '';
  let depth = 0;
  for (const c of s) {
    if (c === '{') depth++;
    else if (c === '}') depth = Math.max(0, depth - 1);
    else if (depth === 0) out += c;
  }
  return out;
}

/** The text a template shows outside expressions, and its literal
 *  user-facing attributes (an OptionsDialog's title="…"). */
function markupText(src: string): string {
  const markup = src
    .replace(/<script[\s\S]*?<\/script>/g, ' ')
    .replace(/<style[\s\S]*?<\/style>/g, ' ')
    .replace(/<!--[\s\S]*?-->/g, ' ');
  const bare = withoutExpressions(markup);
  const attrs: string[] = [];
  const re = /\s(?:title|message|label|alt|placeholder|aria-label)="([^"]*)"/g;
  for (let m = re.exec(bare); m; m = re.exec(bare)) attrs.push(m[1]);
  return [bare.replace(/<[^>]*>/g, ' '), ...attrs].join(' ');
}

/** The strings in a component's script that could be shown: not import
 *  paths, message keys, selectors or codes. */
function scriptStrings(src: string): string[] {
  const script = (src.match(/<script[^>]*>([\s\S]*?)<\/script>/) ?? ['', ''])[1]
    .replace(/\/\*[\s\S]*?\*\//g, ' ')
    .replace(/(^|[^:])\/\/[^\n]*/g, '$1')
    .replace(/^\s*import\s[\s\S]*?from\s*'[^']*';?/gm, ' ');
  const literals = script.match(/'(?:[^'\\\n]|\\.)*'|"(?:[^"\\\n]|\\.)*"|`(?:[^`\\]|\\.)*`/g) ?? [];
  return literals
    .map((l) => l.slice(1, -1).replace(/\$\{[^}]*\}/g, ''))
    .filter((s) => !/^[.#[]/.test(s) && !/^[A-Z_]+$/.test(s))
    .filter((s) => (/\s/.test(s.trim()) || /^[A-Z]/.test(s)) && english(s).length > 0);
}

describe('the source', () => {
  it('covers every file', () => {
    expect(Object.keys(sources).length).toBe(6);
  });

  it.each(Object.entries(sources))('%s has no English in its markup', (_path, src) => {
    expect(english(markupText(src))).toEqual([]);
  });

  it.each(Object.entries(sources))('%s has no English strings in its script', (_path, src) => {
    expect(scriptStrings(src)).toEqual([]);
  });
});

// ── The labels lib builds ──────────────────────────────────────────────────

const ep = { id: 'e1', title: 'Pilot', season_id: 's3', season_number: 3, episode_number: 4 };
const fatal = (extra: Partial<LiveFatal>): LiveFatal => ({ kind: 'network', details: 'fragLoadError', ...extra });

describe('lib labels in Chinese', () => {
  beforeEach(async () => {
    await setLocale('zh-Hans');
  });

  it('the show / season Play button', () => {
    const up = (mode: UpNext['mode']) => upNextLabel({ mode, episode: ep });
    expect(up('resume')).toBe('继续播放 S3 · E4');
    expect(up('next')).toBe('播放 S3 · E4');
    expect(up('rewatch')).toBe('重新观看');
    expect(upNextLabel({ mode: 'resume', episode: { ...ep, season_number: null as unknown as number, episode_number: 0 } })).toBe('继续播放');
  });

  it('the hero: resume point, runtime, the 429 line', () => {
    expect(resumeLabel(5_430_000)).toBe('从 1:30:30 继续播放');
    expect(formatRuntime(7_500_000)).toBe('2 小时 5 分钟');
    expect(formatRuntime(2_759_000)).toBe('45 分钟');
    expect(watchWriteError({ status: 429 }, 'x')).toBe('同时进行的更改过多。请一分钟后再试。');
    expect(watchWriteError({ status: 500 }, '无法更新状态。')).toBe('无法更新状态。');
  });

  it('the library watch filter follows the language', async () => {
    expect(WATCH_FILTER_OPTIONS.map((o) => o.label)).toEqual(['全部项目', '未观看', '观看中', '已观看']);
    expect(WATCH_FILTER_OPTIONS.map((o) => o.value)).toEqual(['', 'unwatched', 'in_progress', 'watched']);
    await setLocale('en');
    expect(WATCH_FILTER_OPTIONS.map((o) => o.label)).toEqual(['All items', 'Unwatched', 'In progress', 'Watched']);
  });

  it('Report a problem', () => {
    expect(ISSUE_KIND_OPTIONS.map((o) => o.label)).toEqual([
      '视频无法播放或显示异常', '音频问题', '字幕问题', '电影/剧集不对', '其他问题',
    ]);
    expect(issueKindLabel('nope')).toBe('其他问题');
    expect(t(REPORT_SENT_TEXT)).toBe('谢谢！已通知管理员，他们会查看。');
    expect(reportErrorMessage({ code: 'ALREADY_REPORTED' })).toBe(t(ALREADY_REPORTED_TEXT));
    expect(reportErrorMessage({ code: 'TOO_MANY_OPEN_ISSUES' })).toBe('你有多个报告正在等待管理员处理。请在处理后再试。');
    expect(reportErrorMessage({ status: 429 })).toBe('短时间内报告过多。请一分钟后再试。');
    expect(reportErrorMessage({})).toBe('无法发送报告。');
    // The server's message stays as it came.
    expect(reportErrorMessage({ message: 'disk full' })).toBe('无法发送报告：disk full');
  });

  it('an album without a title', () => {
    const [album] = appearsOnAlbums([
      { id: 't1', title: 'x', role: 'featured', album_id: 'a1', album_title: '', album_updated_at: 0 },
    ]);
    expect(album.title).toBe('未知专辑');
  });

  it("Live TV's tune errors", () => {
    expect(describeTuneFailure(fatal({ httpStatus: 404 }), false)).toEqual({
      title: '无法调谐到此频道。',
      detail: '服务器找不到此频道的流（HTTP 404）。',
    });
    expect(describeTuneFailure(fatal({ httpStatus: 403 }), true)).toEqual({
      title: '此频道已停止播放。',
      detail: '服务器拒绝了此流（HTTP 403）。',
    });
    expect(describeTuneFailure('timeout', false).detail).toBe('20 秒后仍没有画面。');
    expect(describeTuneFailure(fatal({}), true).detail).toBe('播放错误：fragLoadError');
  });

  it('no label is left in English', () => {
    const labels = [
      upNextLabel({ mode: 'next', episode: ep }) ?? '',
      resumeLabel(1000),
      formatRuntime(3_600_000),
      ...WATCH_FILTER_OPTIONS.map((o) => o.label),
      ...ISSUE_KIND_OPTIONS.map((o) => o.label),
      reportErrorMessage({ status: 404 }),
      ...[404, 503, 504, 401, 500].map((s) => describeTuneFailure(fatal({ httpStatus: s }), false).detail),
      describeTuneFailure(fatal({ kind: 'media', details: '' }), false).detail,
      describeTuneFailure('timeout', true).detail,
    ];
    expect(labels.flatMap(english)).toEqual([]);
  });
});

describe('lib labels in Spanish', () => {
  beforeEach(async () => {
    await setLocale('es');
  });

  it('reads the labels in Spanish', () => {
    expect(upNextLabel({ mode: 'resume', episode: ep })).toBe('Reanudar S3 · E4');
    expect(upNextLabel({ mode: 'start', episode: ep })).toBe('Reproducir S3 · E4');
    expect(resumeLabel(5_430_000)).toBe('Reanudar desde 1:30:30');
    expect(formatRuntime(7_500_000)).toBe('2 h 5 min');
    expect(WATCH_FILTER_OPTIONS[1].label).toBe('No vistos');
    expect(ISSUE_KIND_OPTIONS[3].label).toBe('Película o serie equivocada');
    expect(reportErrorMessage({ status: 404 })).toBe('Este título ya no está disponible.');
    expect(reportErrorMessage({ message: 'disk full' })).toBe('No se pudo enviar el informe: disk full');
    expect(describeTuneFailure('timeout', true).detail).toBe('Sin imagen durante 20 segundos.');
    expect(describeTuneFailure(fatal({ httpStatus: 500 }), false).detail).toBe('El servidor respondió HTTP 500.');
  });
});

describe('the catalogs of these screens', () => {
  const areas = [
    ['details', /^(item|report|photo|play|music)\./, { en: enDetails, es: esDetails, 'zh-Hans': zhDetails }],
    ['live', /^(livetv|recordings|live)\./, { en: enLive, es: esLive, 'zh-Hans': zhLive }],
  ] as const;

  it.each(areas)('%s.json holds only its own prefixes, translated in full', (_area, prefix, cats) => {
    const plural = /_(one|other)$/;
    const base = (k: string) => k.replace(plural, '');
    for (const cat of Object.values(cats)) {
      expect(Object.keys(cat).filter((k) => !prefix.test(k))).toEqual([]);
    }
    const enBases = [...new Set(Object.keys(cats.en).map(base))].sort();
    expect([...new Set(Object.keys(cats.es).map(base))].sort()).toEqual(enBases);
    expect([...new Set(Object.keys(cats['zh-Hans']).map(base))].sort()).toEqual(enBases);
    // Chinese has one plural form, Spanish two.
    expect(Object.keys(cats['zh-Hans']).filter((k) => k.endsWith('_one'))).toEqual([]);
    expect(Object.keys(cats.es).filter((k) => k.endsWith('_one')).length).toBe(
      Object.keys(cats.en).filter((k) => k.endsWith('_one')).length,
    );
  });

  it('Chinese drafts use full-width punctuation', () => {
    const halfWidth = Object.entries({ ...zhDetails, ...zhLive }).filter(([, v]) => /[一-鿿][,.:;?!()]|[,.:;?!(][一-鿿]/.test(v));
    expect(halfWidth.map(([k]) => k)).toEqual([]);
  });
});
