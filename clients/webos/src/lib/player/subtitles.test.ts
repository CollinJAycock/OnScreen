import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import type { ItemFile } from '../api/types';
import {
  SubtitleFetchError,
  activeCues,
  buildSubtitleOptions,
  classifySubtitleFailure,
  cleanCueText,
  loadSubtitleCues,
  parseVttTimestamp,
  parseWebVtt,
  pickDownloadedSubtitle,
  ptsShiftOnFirstPlayMs,
  sameCues,
  subtitleClockMs,
  subtitleFetchError,
} from './subtitles';

type SubFile = Pick<ItemFile, 'id' | 'subtitle_streams' | 'external_subtitles'>;

const file = (extra: Partial<SubFile> = {}): SubFile => ({
  id: 'f1',
  subtitle_streams: [
    { index: 2, codec: 'subrip', language: 'eng', title: '', forced: false },
    { index: 3, codec: 'hdmv_pgs_subtitle', language: 'eng', title: 'PGS', forced: false },
    { index: 4, codec: 'ass', language: 'jpn', title: 'Signs', forced: true },
    { index: 5, codec: 'dvd_subtitle', language: 'fre', title: '', forced: false },
    { index: 6, codec: 'webvtt', language: 'spa', title: 'Spanish', forced: false, sdh: true },
  ],
  external_subtitles: [
    {
      id: 'x1',
      file_id: 'f1',
      language: 'pt-BR',
      title: 'Movie.2020.1080p',
      forced: false,
      sdh: false,
      source: 'opensubtitles',
      source_id: '9876',
      url: '/media/external-subtitles/x1',
    },
  ],
  ...extra,
});

describe('buildSubtitleOptions', () => {
  it('lists text tracks by ABSOLUTE stream index and leaves image tracks out', () => {
    const rows = buildSubtitleOptions(file());
    expect(rows.map((r) => r.key)).toEqual(['emb:2', 'emb:4', 'emb:6', 'ext:x1']);
    expect(rows[0].path).toBe('/media/subtitles/f1/2');
    expect(rows[1].path).toBe('/media/subtitles/f1/4');
    // No PGS / VobSub row at all.
    expect(rows.some((r) => r.key === 'emb:3' || r.key === 'emb:5')).toBe(false);
  });

  it('labels rows like Android and flags downloads', () => {
    const rows = buildSubtitleOptions(file());
    expect(rows.map((r) => r.label)).toEqual([
      'English',
      'Japanese · Signs · forced',
      'Spanish · SDH',
      'Portuguese (Brazil) · Movie.2020.1080p · downloaded',
    ]);
    expect(rows[3]).toMatchObject({
      downloaded: true,
      path: '/media/external-subtitles/x1',
      externalId: 'x1',
      sourceId: '9876',
      language: 'pt-BR',
    });
    expect(rows[0].downloaded).toBe(false);
  });

  it('keeps keys stable when the item is refreshed with another download', () => {
    const before = buildSubtitleOptions(file());
    const f = file();
    const after = buildSubtitleOptions({
      ...f,
      external_subtitles: [
        { ...f.external_subtitles![0], id: 'x0', source_id: '1', url: '/media/external-subtitles/x0', language: 'en' },
        ...f.external_subtitles!,
      ],
    });
    for (const r of before) expect(after.find((a) => a.key === r.key)?.path).toBe(r.path);
  });

  it('copes with a missing file, no external list, and rows without a url', () => {
    expect(buildSubtitleOptions(null)).toEqual([]);
    expect(buildSubtitleOptions({ id: 'f', subtitle_streams: [] })).toEqual([]);
    const f = file();
    const rows = buildSubtitleOptions({ ...f, external_subtitles: [{ ...f.external_subtitles![0], url: '' }] });
    expect(rows.some((r) => r.downloaded)).toBe(false);
  });
});

describe('pickDownloadedSubtitle', () => {
  const f = file();
  const before = buildSubtitleOptions(f);
  const after = buildSubtitleOptions({
    ...f,
    external_subtitles: [
      // The server orders by language, so the new row needn't be last.
      { ...f.external_subtitles![0], id: 'x2', language: 'en', source_id: '5555', url: '/media/external-subtitles/x2' },
      ...f.external_subtitles!,
    ],
  });

  it('selects the attached row the server returned', () => {
    expect(pickDownloadedSubtitle(before, after, 5555, 'x2')).toBe('ext:x2');
  });

  it('else the row whose source_id is the provider file id', () => {
    expect(pickDownloadedSubtitle(before, after, 5555)).toBe('ext:x2');
    expect(pickDownloadedSubtitle(before, after, 9876)).toBe('ext:x1');
  });

  it('else the external row that is new since the download', () => {
    expect(pickDownloadedSubtitle(before, after, 1)).toBe('ext:x2');
    expect(pickDownloadedSubtitle(after, after, 1)).toBeNull();
  });
});

describe('parseVttTimestamp', () => {
  it('reads hours-less, hours and lenient forms', () => {
    expect(parseVttTimestamp('00:01.500')).toBe(1_500);
    expect(parseVttTimestamp('01:02:03.456')).toBe(3_723_456);
    expect(parseVttTimestamp('123:00:00.000')).toBe(123 * 3_600_000);
    expect(parseVttTimestamp('00:00:01,250')).toBe(1_250);
    expect(parseVttTimestamp('0:01.5')).toBe(1_500);
    expect(parseVttTimestamp('00:61.000')).toBeNull();
    expect(parseVttTimestamp('abc')).toBeNull();
  });
});

describe('parseWebVtt', () => {
  it('handles a BOM, CRLF, the header, NOTE / STYLE / REGION, ids and settings', () => {
    const vtt =
      '﻿WEBVTT - a title\r\nX-TIMESTAMP-MAP=MPEGTS:900000,LOCAL:00:00:00.000\r\n\r\n' +
      'NOTE this is a comment\r\nwith --> an arrow in it\r\n\r\n' +
      'STYLE\r\n::cue { color: red }\r\n\r\n' +
      'REGION\r\nid:fred width:40%\r\n\r\n' +
      'cue-1\r\n00:01.000 --> 00:02.500 align:start line:10%\r\nHello\r\n\r\n' +
      '00:00:03.000 --> 00:00:04.000\r\nWorld\r\n';
    expect(parseWebVtt(vtt)).toEqual([
      { startMs: 1_000, endMs: 2_500, text: 'Hello' },
      { startMs: 3_000, endMs: 4_000, text: 'World' },
    ]);
  });

  it('keeps multi-line cues and strips tags except line breaks', () => {
    const vtt =
      'WEBVTT\n\n00:00:05.000 --> 00:00:07.000\n<v Bob><i>Previously</i> on</v>\n<c.yellow>the show</c><br>&amp; more&nbsp;\n\n' +
      '00:00:08.000 --> 00:00:09.000\n{\\an8}Signs <00:00:08.500>up top\n';
    expect(parseWebVtt(vtt)).toEqual([
      // The trailing &nbsp; is whitespace, trimmed with the line.
      { startMs: 5_000, endMs: 7_000, text: 'Previously on\nthe show\n& more' },
      { startMs: 8_000, endMs: 9_000, text: 'Signs up top' },
    ]);
  });

  it('skips malformed blocks instead of throwing, and sorts by start', () => {
    const vtt =
      'WEBVTT\n\n00:10.000 --> 00:11.000\nlater\n\n' +
      'garbage --> more garbage\ntext\n\n' +
      '00:05.000 --> 00:04.000\nends before it starts\n\n' +
      '00:06.000 --> 00:07.000\n<i></i>\n\n' +
      '00:01.000 --> 00:02.000\nearlier\n';
    expect(parseWebVtt(vtt).map((c) => c.text)).toEqual(['earlier', 'later']);
    expect(parseWebVtt('')).toEqual([]);
    expect(parseWebVtt('<html>Not found</html>')).toEqual([]);
  });

  it('reads a bare CR file', () => {
    expect(parseWebVtt('WEBVTT\r\r00:01.000 --> 00:02.000\rA\rB\r')).toEqual([
      { startMs: 1_000, endMs: 2_000, text: 'A\nB' },
    ]);
  });
});

describe('cleanCueText', () => {
  it('decodes entities after stripping, so escaped markup shows as text', () => {
    expect(cleanCueText('&lt;i&gt; is literal &#169; &#x263A;')).toBe('<i> is literal © ☺');
    expect(cleanCueText('a &bogus; b')).toBe('a &bogus; b');
  });
});

describe('activeCues and the content-time clock', () => {
  const cues = parseWebVtt(
    'WEBVTT\n\n45:00.000 --> 45:09.000\nbefore\n\n45:10.000 --> 45:12.000\nat 45:10\n\n' +
      '45:12.000 --> 45:14.000\nnext\n\n44:00.000 --> 46:00.000\nlong sign\n',
  );

  it('shows a cue at 45:10 ten seconds into a session that opens at 45:00', () => {
    const offsetMs = 45 * 60_000;
    const t = subtitleClockMs(10, offsetMs, 0);
    expect(t).toBe(2_710_000);
    expect(activeCues(cues, t).map((c) => c.text)).toEqual(['long sign', 'at 45:10']);
    // Stream time alone (the old textTracks clock) would look at 0:10.
    expect(activeCues(cues, 10_000)).toEqual([]);
  });

  it('is start-inclusive and end-exclusive, so back-to-back cues never overlap', () => {
    expect(activeCues(cues, 2_712_000).map((c) => c.text)).toEqual(['long sign', 'next']);
    expect(activeCues(cues, 2_709_500).map((c) => c.text)).toEqual(['long sign']);
  });

  it('takes a measured container shift off the clock', () => {
    // Started from the head, but the container's timestamps begin at 1.4 s.
    expect(subtitleClockMs(11.4, 0, 1_400)).toBe(10_000);
  });

  it('compares cue lists by identity for redraws', () => {
    const a = activeCues(cues, 2_710_000);
    expect(sameCues(a, activeCues(cues, 2_711_000))).toBe(true);
    expect(sameCues(a, activeCues(cues, 2_712_500))).toBe(false);
  });
});

describe('ptsShiftOnFirstPlayMs (the web one-shot latch)', () => {
  it('measures the shift of a stream that started at its head', () => {
    expect(ptsShiftOnFirstPlayMs({ offsetMs: 0, startSec: -1, currentSec: 1.4 })).toBe(1_400);
    // A measured audio-gap start (0.4 s) is still the head.
    expect(ptsShiftOnFirstPlayMs({ offsetMs: 0, startSec: 0.4, currentSec: 2.4 })).toBe(2_000);
  });

  it('ignores a normal start, a session opened at an offset, and a resume inside a full-timeline stream', () => {
    expect(ptsShiftOnFirstPlayMs({ offsetMs: 0, startSec: -1, currentSec: 0.2 })).toBe(0);
    expect(ptsShiftOnFirstPlayMs({ offsetMs: 2_695_000, startSec: 5, currentSec: 5 })).toBe(0);
    expect(ptsShiftOnFirstPlayMs({ offsetMs: 0, startSec: 2_400, currentSec: 2_403 })).toBe(0);
    expect(ptsShiftOnFirstPlayMs({ offsetMs: 0, startSec: -1, currentSec: NaN })).toBe(0);
  });
});

describe('classifySubtitleFailure', () => {
  it('gives up on what a retry cannot change', () => {
    expect(classifySubtitleFailure(new SubtitleFetchError(415), 10)).toBe('final');
    expect(classifySubtitleFailure(new SubtitleFetchError(404), 10)).toBe('final');
  });

  it('reads a server or proxy timeout as "still preparing"', () => {
    for (const s of [502, 503, 504, 520, 524]) {
      expect(classifySubtitleFailure(new SubtitleFetchError(s), 10)).toBe('preparing');
    }
  });

  it("counts the server's own 504 SUBTITLE_PREPARING (the extraction failed) as an ordinary try", () => {
    expect(classifySubtitleFailure(new SubtitleFetchError(504, 'SUBTITLE_PREPARING'), 10)).toBe('retry');
    expect(classifySubtitleFailure(new SubtitleFetchError(504, 'SUBTITLE_PREPARING'), 70_000)).toBe('retry');
    // Another code on a gateway status is still a gateway's.
    expect(classifySubtitleFailure(new SubtitleFetchError(503, 'SERVICE_UNAVAILABLE'), 10)).toBe('preparing');
  });

  it("reads our own timeout and a connection dropped after a long wait the same way", () => {
    expect(classifySubtitleFailure(Object.assign(new Error('aborted'), { name: 'AbortError' }), 100_000)).toBe('preparing');
    expect(classifySubtitleFailure(new TypeError('Failed to fetch'), 61_000)).toBe('preparing');
  });

  it('counts a fast transport failure (offline, CORS) and other errors as ordinary tries', () => {
    expect(classifySubtitleFailure(new TypeError('Failed to fetch'), 40)).toBe('retry');
    expect(classifySubtitleFailure(new SubtitleFetchError(500), 10)).toBe('retry');
    expect(classifySubtitleFailure(new SubtitleFetchError(401), 10)).toBe('unauthorized');
  });
});

describe('subtitleFetchError', () => {
  it("reads the server's error code from its JSON body", async () => {
    const e = await subtitleFetchError({
      status: 504,
      json: async () => ({ error: { code: 'SUBTITLE_PREPARING', message: 'retry in a moment' } }),
    });
    expect(e).toBeInstanceOf(SubtitleFetchError);
    expect(e.status).toBe(504);
    expect(e.code).toBe('SUBTITLE_PREPARING');
  });

  it("has no code for a proxy's page or an odd body", async () => {
    const html = await subtitleFetchError({ status: 504, json: async () => { throw new SyntaxError('not json'); } });
    expect(html.code).toBe('');
    expect((await subtitleFetchError({ status: 502, json: async () => null })).code).toBe('');
    expect((await subtitleFetchError({ status: 502, json: async () => ({ error: { code: 7 } }) })).code).toBe('');
  });
});

describe('loadSubtitleCues', () => {
  beforeEach(() => vi.useFakeTimers());
  afterEach(() => vi.useRealTimers());

  const VTT = 'WEBVTT\n\n00:01.000 --> 00:02.000\nhi\n';

  it('retries a failing fetch, up to 3 tries in all', async () => {
    let calls = 0;
    const p = loadSubtitleCues(
      async () => {
        calls++;
        if (calls < 3) throw new SubtitleFetchError(500);
        return VTT;
      },
      () => true,
    );
    await vi.advanceTimersByTimeAsync(3_000);
    await expect(p).resolves.toEqual({ kind: 'ok', cues: [{ startMs: 1_000, endMs: 2_000, text: 'hi' }] });
    expect(calls).toBe(3);
  });

  it('gives up after 3 tries (the page shows "Subtitles unavailable")', async () => {
    let calls = 0;
    const p = loadSubtitleCues(async () => {
      calls++;
      throw new TypeError('Failed to fetch');
    }, () => true);
    await vi.advanceTimersByTimeAsync(10_000);
    await expect(p).resolves.toEqual({ kind: 'failed' });
    expect(calls).toBe(3);
  });

  it('does not retry what a retry cannot change (415 image track, 404)', async () => {
    let calls = 0;
    const p = loadSubtitleCues(async () => {
      calls++;
      throw new SubtitleFetchError(415);
    }, () => true);
    await vi.advanceTimersByTimeAsync(10_000);
    await expect(p).resolves.toEqual({ kind: 'failed' });
    expect(calls).toBe(1);
  });

  it('refreshes the token after a 401 before trying again', async () => {
    const steps: string[] = [];
    let calls = 0;
    const p = loadSubtitleCues(
      async () => {
        steps.push('fetch');
        if (++calls === 1) throw new SubtitleFetchError(401);
        return VTT;
      },
      () => true,
      { onUnauthorized: async () => steps.push('refresh') },
    );
    await vi.advanceTimersByTimeAsync(2_000);
    await expect(p).resolves.toMatchObject({ kind: 'ok' });
    expect(steps).toEqual(['fetch', 'refresh', 'fetch']);
  });

  it("keeps polling a track the server is still extracting (a proxy's 504 on the held request)", async () => {
    // A cold 4K remux: the first answers are a gateway's timeouts, the VTT
    // is cached after about 90 s. Three tries used to give up at about a
    // minute.
    let calls = 0;
    const t0 = Date.now();
    const p = loadSubtitleCues(async () => {
      calls++;
      if (Date.now() - t0 < 90_000) throw new SubtitleFetchError(504);
      return VTT;
    }, () => true);
    await vi.advanceTimersByTimeAsync(120_000);
    await expect(p).resolves.toMatchObject({ kind: 'ok' });
    expect(calls).toBeGreaterThan(3);
  });

  it("gives a failed extraction (the server's 504 SUBTITLE_PREPARING) its 3 tries, no more", async () => {
    // Each request re-runs the whole-file demux: polling it for 8 minutes
    // ran ffmpeg some 30 times for a track that can't be converted.
    let calls = 0;
    const p = loadSubtitleCues(async () => {
      calls++;
      throw new SubtitleFetchError(504, 'SUBTITLE_PREPARING');
    }, () => true);
    await vi.advanceTimersByTimeAsync(600_000);
    await expect(p).resolves.toEqual({ kind: 'failed' });
    expect(calls).toBe(3);
  });

  it('treats its own per-try timeout and a slow dropped connection as still preparing', async () => {
    const abort = Object.assign(new Error('The user aborted a request.'), { name: 'AbortError' });
    let calls = 0;
    const p = loadSubtitleCues(async () => {
      calls++;
      if (calls <= 4) throw abort;
      return VTT;
    }, () => true);
    await vi.advanceTimersByTimeAsync(60_000);
    await expect(p).resolves.toMatchObject({ kind: 'ok' });
    expect(calls).toBe(5);
  });

  it('gives up on a track that stays "preparing" past the budget', async () => {
    let calls = 0;
    const p = loadSubtitleCues(
      async () => {
        calls++;
        throw new SubtitleFetchError(504);
      },
      () => true,
      { preparingBudgetMs: 60_000 },
    );
    await vi.advanceTimersByTimeAsync(120_000);
    await expect(p).resolves.toEqual({ kind: 'failed' });
    // 0, 2, 6, 14, 29, 44, 59 s; the next poll (74 s) is past the budget.
    expect(calls).toBe(7);
  });

  it('drops a load that a newer pick replaced (the fetch generation)', async () => {
    let gen = 1;
    let release: (t: string) => void = () => {};
    const p = loadSubtitleCues(
      () => new Promise<string>((r) => (release = r)),
      ((mine) => () => mine === gen)(gen),
    );
    gen++; // the user picked another track meanwhile
    release(VTT);
    await expect(p).resolves.toEqual({ kind: 'stale' });
  });
});
