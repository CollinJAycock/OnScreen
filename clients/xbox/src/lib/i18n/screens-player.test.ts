import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { render } from 'svelte/server';

// The player (stage 3, part 2) in Chinese and Spanish: the labels its lib
// builds, the page as the server renders it (the "Starting playback…" state:
// everything else needs onMount, a <video> and a stream), and its markup,
// which may hold no English outside {…} expressions. A string added in
// English only, or a label built at import time, fails here (docs/i18n.md).

vi.mock('$app/navigation', () => ({ goto: vi.fn() }));
vi.mock('$app/state', () => ({
  page: { url: new URL('http://tv.local/'), route: { id: '/watch/[id]' }, params: { id: 'item-1' } },
}));

import { setLocale } from './index.svelte';
import {
  ACTION_LABELS, chapterPickerRows, chapterRowLabel, findOnlineLabel, subtitlePickerRows, transportLabel,
} from '../player/actions';
import { queueLabel } from '../player/chrome';
import { tooManyStreamsMessage, tooManyStreamsTitle } from '../player/playDecision';
import { contentRevokedText } from '../player/progress-reporter';
import { buildSubtitleOptions } from '../player/subtitles';
import { burnInRefusal } from '../player/burnIn';
import { groupVersions } from '../player/versions';
import { adminStopText, stoppedTextFromServer } from '../playbackStop';
import type { ItemFile } from '../api/types';
import WatchPage from '../../routes/watch/[id]/+page.svelte';

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
  vi.stubGlobal('location', { hash: '#/watch/item-1' });
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

// Never translated (docs/i18n.md): remote keys (OK, CH), format and product
// names, and codec names in a version's label.
const NAMES = ['OnScreen', 'Dolby Vision', 'HLS', 'SDH', 'OK', 'CH', 'HEVC', 'H.264'];

/** Words of three or more Latin letters left once the names are gone. */
function english(s: string): string[] {
  for (const n of NAMES) s = s.split(n).join(' ');
  return s.match(/[A-Za-z]{3,}/g) ?? [];
}

function file(id: string, extra: Partial<ItemFile> = {}): ItemFile {
  return { id, ...extra } as ItemFile;
}

const subtitleFile = {
  id: 'f1',
  subtitle_streams: [
    { index: 3, codec: 'subrip', language: '', title: '', forced: false, default: false },
    { index: 4, codec: 'hdmv_pgs_subtitle', image_based: true, language: 'ger', title: '', forced: false, default: false },
  ],
  external_subtitles: [{ id: 'x1', url: '/subs/x1.vtt', language: '', title: '' }],
} as unknown as ItemFile;

function apiError(status: number, code: string, message = '') {
  return Object.assign(new Error(message), { status, code });
}

describe('player labels in Chinese', () => {
  beforeEach(async () => {
    await setLocale('zh-Hans');
  });

  it('names the actions, the pickers and the transport buttons', () => {
    expect([ACTION_LABELS.audio, ACTION_LABELS.subtitles, ACTION_LABELS.chapters]).toEqual(['音轨', '字幕', '章节']);
    const rows = subtitlePickerRows([{ key: 'emb:3', label: '英语' }], null, true);
    expect(rows.map((r) => r.label)).toEqual(['关闭', '英语', findOnlineLabel()]);
    expect(findOnlineLabel()).toBe('在线查找更多…');
    expect(transportLabel('back', false)).toBe('-10 秒');
    expect(transportLabel('forward', false)).toBe('+10 秒');
    expect(chapterRowLabel({ title: '', start_ms: 754_000 }, 3)).toBe('4. 第 4 章 · 12:34');
    expect(chapterPickerRows([{ start_ms: 0 }, { title: 'Opening', start_ms: 60_000 }], 0).map((r) => r.label)).toEqual([
      '1. 第 1 章 · 0:00',
      '2. Opening · 1:00',
    ]);
  });

  it("counts a queue in the queued item's own words", () => {
    expect(queueLabel('track', 3, 12)).toBe('第 3 首，共 12 首');
    expect(queueLabel('audiobook_chapter', 4, 30)).toBe('第 4 章，共 30 章');
    expect(queueLabel('podcast_episode', 1, 1200)).toBe('第 1 集，共 1,200 集');
  });

  it('labels subtitle rows: numbered, burned in, external', () => {
    const rows = buildSubtitleOptions(subtitleFile);
    expect(rows.map((r) => r.label)).toEqual(['轨道 3', '德语（烧录）', '外部字幕 · 已下载']);
    for (const r of rows) expect(english(r.label)).toEqual([]);
  });

  it('names versions and their parts', () => {
    expect(groupVersions([file('x')])[0].label).toBe('版本');
    const stacked = groupVersions([file('a', { part_index: 1 }), file('b', { part_index: 2 })]);
    expect(stacked[0].label).toBe('版本 · 2 个部分');
    const twins = groupVersions([file('a', { resolution_h: 1080 }), file('b', { resolution_h: 1080 })]);
    expect(twins.map((v) => v.label)).toEqual(['1080p（1）', '1080p（2）']);
  });

  it("writes the player's own refusals, keeping the server's words as sent", () => {
    expect(tooManyStreamsTitle()).toBe('播放流过多');
    expect(english(tooManyStreamsMessage(apiError(429, 'TOO_MANY_SESSIONS')) ?? 'null')).toEqual([]);
    expect(tooManyStreamsMessage(apiError(429, 'TOO_MANY_SESSIONS', 'Stop one first.'))).toBe('Stop one first.');
    expect(contentRevokedText()).toBe('此标题在此个人资料中已不可用。');
    expect(burnInRefusal(apiError(400, 'SUBTITLE_NOT_IMAGE'))).toBe('这些字幕无法烧录。');
    expect(adminStopText()).toBe('服务器管理员已停止播放。');
    expect(adminStopText('Server restart')).toBe('服务器管理员已停止播放：Server restart');
    expect(stoppedTextFromServer('')).toBe('服务器管理员已停止播放。');
  });
});

describe('player labels in Spanish', () => {
  beforeEach(async () => {
    await setLocale('es');
  });

  it('builds the rows and counts in Spanish', () => {
    expect(ACTION_LABELS.subtitles).toBe('Subtítulos');
    expect(subtitlePickerRows([], null, true).map((r) => r.label)).toEqual(['Desactivados', 'Buscar más en línea…']);
    expect(chapterRowLabel({ start_ms: 0 }, 0)).toBe('1. Capítulo 1 · 0:00');
    expect(queueLabel('track', 3, 12)).toBe('Pista 3 de 12');
    expect(buildSubtitleOptions(subtitleFile).map((r) => r.label)).toEqual([
      'Pista 3',
      'Alemán (incrustados)',
      'Externo · descargado',
    ]);
    const stacked = groupVersions([file('a', { part_index: 1, resolution_h: 480, video_codec: 'h264' }), file('b', { part_index: 2 })]);
    expect(stacked[0].label).toBe('480p · H.264 · 2 partes');
    expect(tooManyStreamsTitle()).toBe('Demasiadas reproducciones');
    expect(adminStopText('Mantenimiento')).toBe('El administrador del servidor detuvo la reproducción: Mantenimiento');
  });
});

describe('the watch page', () => {
  it('has no English left in Chinese while it starts', async () => {
    await setLocale('zh-Hans');
    const { body } = render(WatchPage);
    expect(text(body)).toContain('正在开始播放…');
    expect(english(text(body))).toEqual([]);
  });

  it('starts in Spanish', async () => {
    await setLocale('es');
    const { body } = render(WatchPage);
    expect(text(body)).toContain('Iniciando la reproducción…');
  });

  // The rest of the page (controls, hints, pickers, the error overlay, Up
  // Next) only shows once a stream plays, which a server render can't reach:
  // its markup may hold text only through {$i18n(…)} or other expressions.
  // "hd" is the OpenSubtitles result's HD flag, a format name.
  it('writes no text into its markup outside expressions', () => {
    const src = readFileSync(new URL('../../routes/watch/[id]/+page.svelte', import.meta.url), 'utf8');
    let markup = src.slice(src.indexOf('</script>') + '</script>'.length, src.indexOf('<style>'));
    markup = markup.replace(/<!--[\s\S]*?-->/g, '');
    let outside = '';
    let depth = 0;
    for (const ch of markup) {
      if (ch === '{') depth++;
      else if (ch === '}') depth--;
      else if (depth === 0) outside += ch;
    }
    const words = outside.replace(/<[^>]*>/g, ' ').match(/[A-Za-z]{2,}/g) ?? [];
    expect(words.filter((w) => w !== 'hd')).toEqual([]);
  });
});
