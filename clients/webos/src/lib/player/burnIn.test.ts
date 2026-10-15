import { describe, expect, it } from 'vitest';
import type { ItemFile } from '../api/types';
import {
  BURN_IN_ERROR_CODES,
  burnIndexOfKey,
  burnInRefusal,
  burnKey,
  keyAfterBurnRefused,
  planSubtitlePick,
  streamVideoFields,
} from './burnIn';
import { buildSubtitleOptions } from './subtitles';

type SubFile = Pick<ItemFile, 'id' | 'subtitle_streams' | 'external_subtitles'>;

// A file as a burn-in server lists it: a text track, a PGS track OCR'd to
// text, a PGS track that isn't, and the OCR'd entry among the externals.
const file = (): SubFile => ({
  id: 'f1',
  subtitle_streams: [
    { index: 2, codec: 'subrip', language: 'eng', title: '', forced: false, image_based: false },
    { index: 3, codec: 'hdmv_pgs_subtitle', language: 'fre', title: '', forced: false, image_based: true, text_version_id: 'ocr3' },
    { index: 4, codec: 'hdmv_pgs_subtitle', language: 'ger', title: '', forced: true, image_based: true },
  ],
  external_subtitles: [
    {
      id: 'ocr3',
      file_id: 'f1',
      language: 'fre',
      forced: false,
      sdh: false,
      source: 'ocr',
      source_id: 'stream_3',
      url: '/media/external-subtitles/ocr3',
    },
    {
      id: 'x1',
      file_id: 'f1',
      language: 'spa',
      forced: false,
      sdh: false,
      source: 'opensubtitles',
      source_id: '42',
      url: '/media/external-subtitles/x1',
    },
  ],
});

describe('buildSubtitleOptions with image tracks', () => {
  it('lists an image track with a text version as that text track, in the stream’s place', () => {
    const rows = buildSubtitleOptions(file());
    expect(rows.map((r) => r.key)).toEqual(['emb:2', 'ext:ocr3', 'burn:4', 'ext:x1']);
    expect(rows[1]).toMatchObject({
      label: 'French',
      path: '/media/external-subtitles/ocr3',
      externalId: 'ocr3',
      downloaded: false,
    });
    expect(rows[1].burnIndex).toBeUndefined();
    // The OCR'd entry isn't listed a second time among the externals.
    expect(rows.filter((r) => r.externalId === 'ocr3')).toHaveLength(1);
  });

  it('lists one without as a "(burned in)" row carrying its ABSOLUTE index', () => {
    const burn = buildSubtitleOptions(file())[2];
    expect(burn).toMatchObject({ key: 'burn:4', label: 'German · forced (burned in)', burnIndex: 4, path: '', forced: true });
  });

  it('burns a track whose text version is not there (yet)', () => {
    const f = file();
    const rows = buildSubtitleOptions({ ...f, external_subtitles: f.external_subtitles!.slice(1) });
    expect(rows.map((r) => r.key)).toEqual(['emb:2', 'burn:3', 'burn:4', 'ext:x1']);
  });

  it('offers no image track on a server without burn-in (no image_based)', () => {
    const rows = buildSubtitleOptions({
      id: 'f1',
      subtitle_streams: [
        { index: 2, codec: 'subrip', language: 'eng', title: '', forced: false },
        { index: 3, codec: 'hdmv_pgs_subtitle', language: 'fre', title: '', forced: false },
      ],
    });
    expect(rows.map((r) => r.key)).toEqual(['emb:2']);
  });

  it('trusts the server’s image_based over the codec', () => {
    const rows = buildSubtitleOptions({
      id: 'f1',
      subtitle_streams: [{ index: 5, codec: 'some_bitmap', language: 'eng', title: '', forced: false, image_based: true }],
    });
    expect(rows.map((r) => r.key)).toEqual(['burn:5']);
  });
});

describe('burn keys', () => {
  it('round-trip the stream index; nothing else burns', () => {
    expect(burnKey(7)).toBe('burn:7');
    expect(burnIndexOfKey('burn:7')).toBe(7);
    expect(burnIndexOfKey('burn:0')).toBe(0);
    expect(burnIndexOfKey('emb:7')).toBeNull();
    expect(burnIndexOfKey('ext:burn:7')).toBeNull();
    expect(burnIndexOfKey('burn:')).toBeNull();
    expect(burnIndexOfKey(null)).toBeNull();
  });
});

describe('planSubtitlePick', () => {
  it('a text version is picked like a text track: no restart', () => {
    const rows = buildSubtitleOptions(file());
    expect(planSubtitlePick(rows[1].key, null)).toEqual({ key: 'ext:ocr3', reissue: false });
    expect(planSubtitlePick(rows[1].key, 'emb:2')).toEqual({ key: 'ext:ocr3', reissue: false });
  });

  it('an image track without one re-opens the stream with the burn', () => {
    expect(planSubtitlePick('burn:4', null)).toEqual({ key: 'burn:4', reissue: true });
    expect(planSubtitlePick('burn:4', 'emb:2')).toEqual({ key: 'burn:4', reissue: true });
    expect(planSubtitlePick('burn:4', 'burn:3')).toEqual({ key: 'burn:4', reissue: true });
    expect(streamVideoFields(true, 'burn:4')).toEqual({ videoCopy: false, burnSubtitleStreamIndex: 4 });
  });

  it('Off (or a text track) takes the burn off: re-opened without it', () => {
    expect(planSubtitlePick(null, 'burn:4')).toEqual({ key: null, reissue: true });
    expect(streamVideoFields(true, null)).toEqual({ videoCopy: true });
    expect(planSubtitlePick('emb:2', 'burn:4')).toEqual({ key: 'emb:2', reissue: true });
    expect(streamVideoFields(true, 'emb:2')).toEqual({ videoCopy: true });
  });

  it('Off with nothing burned changes no stream', () => {
    expect(planSubtitlePick(null, 'emb:2')).toEqual({ key: null, reissue: false });
    expect(planSubtitlePick(null, null)).toEqual({ key: null, reissue: false });
  });
});

describe('streamVideoFields', () => {
  it('keeps the burn on every start while it is chosen (an audio switch, a seek)', () => {
    // An audio switch re-issues with the verdict's mode (a remux here); the
    // burn still chosen makes it the burned re-encode again.
    const audioSwitch = streamVideoFields(true, 'burn:4');
    expect(audioSwitch).toEqual({ videoCopy: false, burnSubtitleStreamIndex: 4 });
    expect(streamVideoFields(false, 'burn:4')).toEqual({ videoCopy: false, burnSubtitleStreamIndex: 4 });
  });

  it('leaves the request alone with a text track or Off', () => {
    expect(streamVideoFields(false, 'ext:x1')).toEqual({ videoCopy: false });
    expect(streamVideoFields(true, 'ext:ocr3')).toEqual({ videoCopy: true });
  });
});

describe('burnInRefusal', () => {
  const apiError = (status: number, code: string, message: string) => Object.assign(new Error(message), { status, code });

  it('gives the server’s message for its burn-in 400s', () => {
    for (const code of BURN_IN_ERROR_CODES) {
      expect(burnInRefusal(apiError(400, code, 'cannot burn this one'))).toBe('cannot burn this one');
    }
    expect(burnInRefusal(apiError(400, 'BURN_IN_UNSUPPORTED', ''))).toBe('These subtitles can’t be burned in.');
  });

  it('also refuses a 503 when no worker can burn subtitles in', () => {
    expect(burnInRefusal(apiError(503, 'BURN_IN_UNAVAILABLE', 'no worker can burn'))).toBe('no worker can burn');
    expect(burnInRefusal(apiError(503, 'SERVICE_UNAVAILABLE', 'x'))).toBeNull();
  });

  it('is null for anything else', () => {
    expect(burnInRefusal(apiError(400, 'BAD_REQUEST', 'x'))).toBeNull();
    expect(burnInRefusal(apiError(500, 'BURN_IN_UNSUPPORTED', 'x'))).toBeNull();
    expect(burnInRefusal(new Error('network'))).toBeNull();
    expect(burnInRefusal(null)).toBeNull();
  });
});

describe('keyAfterBurnRefused', () => {
  it('marks the burn the live session still has', () => {
    expect(keyAfterBurnRefused(3, 'burn:3')).toBe('burn:3');
    expect(keyAfterBurnRefused(3, 'emb:2')).toBe('burn:3');
  });

  it('else goes back to the text track (or Off) chosen before the pick', () => {
    expect(keyAfterBurnRefused(null, 'emb:2')).toBe('emb:2');
    expect(keyAfterBurnRefused(null, null)).toBeNull();
    // A burn that never reached the stream isn't put back.
    expect(keyAfterBurnRefused(null, 'burn:3')).toBeNull();
  });
});
