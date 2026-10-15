import { describe, expect, it } from 'vitest';
import type { ExternalSubtitle, ItemFile, SubtitleStream } from '$lib/api';
import {
  BURN_ERROR_CODES,
  applyBurn,
  imageSubtitleOptions,
  isImageSubtitle,
  planSubtitlePick,
  textVersionOf,
} from './subtitleBurn';

function stream(over: Partial<SubtitleStream> = {}): SubtitleStream {
  return { index: 3, codec: 'hdmv_pgs_subtitle', language: 'eng', title: '', forced: false, ...over };
}

function ext(over: Partial<ExternalSubtitle> = {}): ExternalSubtitle {
  return {
    id: 'x1', file_id: 'f1', language: 'eng', forced: false, sdh: false,
    source: 'ocr', source_id: 'stream_3', url: '/media/external-subtitles/x1', ...over,
  };
}

function file(over: Partial<ItemFile> = {}): ItemFile {
  return {
    id: 'f1', resolution_w: 1920, resolution_h: 1080,
    subtitle_streams: [], external_subtitles: [], ...over,
  } as unknown as ItemFile;
}

describe('isImageSubtitle', () => {
  it('trusts the server flag', () => {
    expect(isImageSubtitle(stream({ codec: 'subrip', image_based: true }))).toBe(true);
    expect(isImageSubtitle(stream({ image_based: false }))).toBe(false);
  });

  it('falls back to the codec on older servers', () => {
    expect(isImageSubtitle(stream({ codec: 'dvd_subtitle' }))).toBe(true);
    expect(isImageSubtitle(stream({ codec: 'subrip' }))).toBe(false);
  });
});

describe('textVersionOf', () => {
  it('follows text_version_id', () => {
    const tv = ext({ id: 'x9', source_id: 'stream_99' });
    const f = file({ external_subtitles: [ext(), tv] });
    expect(textVersionOf(f, stream({ text_version_id: 'x9' }))).toBe(tv);
  });

  it('falls back to the OCR row tagged with the stream', () => {
    const tv = ext();
    expect(textVersionOf(file({ external_subtitles: [tv] }), stream())).toBe(tv);
    expect(textVersionOf(file({ external_subtitles: [ext({ source: 'opensubtitles' })] }), stream())).toBeUndefined();
  });
});

describe('planSubtitlePick', () => {
  it('plays an image track with a text version as that text subtitle, no restart', () => {
    const tv = ext({ id: 'x7' });
    const [option] = imageSubtitleOptions(file({
      subtitle_streams: [stream({ image_based: true, text_version_id: 'x7' })],
      external_subtitles: [tv],
    }));
    expect(planSubtitlePick({ kind: 'image', option }, null))
      .toEqual({ action: 'select', text: { external: tv }, unburn: false });
  });

  it('burns an image track without a text version', () => {
    const [option] = imageSubtitleOptions(file({ subtitle_streams: [stream({ index: 5, image_based: true })] }));
    expect(option.textVersion).toBeUndefined();
    expect(planSubtitlePick({ kind: 'image', option }, null)).toEqual({ action: 'burn', streamIndex: 5 });
  });

  it('does nothing when that track is already burned in', () => {
    const [option] = imageSubtitleOptions(file({ subtitle_streams: [stream({ index: 5 })] }));
    expect(planSubtitlePick({ kind: 'image', option }, 5)).toEqual({ action: 'none' });
  });

  it('Off drops the burn', () => {
    expect(planSubtitlePick({ kind: 'off' }, 5)).toEqual({ action: 'select', text: null, unburn: true });
    expect(planSubtitlePick({ kind: 'off' }, null)).toEqual({ action: 'select', text: null, unburn: false });
  });

  it('a text subtitle while burned drops the burn', () => {
    const sub = { key: 'e:2' };
    expect(planSubtitlePick({ kind: 'text', sub }, 5)).toEqual({ action: 'select', text: { sub }, unburn: true });
  });
});

describe('applyBurn', () => {
  it('leaves a request alone with no burn', () => {
    expect(applyBurn({ height: 0, videoCopy: true }, null, file()))
      .toEqual({ height: 0, videoCopy: true });
  });

  it('keeps the burn on an audio switch, turning the remux into a full transcode', () => {
    // selectAudioTrack on a remuxable file asks for (0, videoCopy).
    expect(applyBurn({ height: 0, videoCopy: true }, 5, file()))
      .toEqual({ height: 1080, videoCopy: false, burnSubtitleStreamIndex: 5 });
  });

  it('keeps an explicit quality and picks the source tier for 4K', () => {
    expect(applyBurn({ height: 720, videoCopy: false }, 5, file()))
      .toEqual({ height: 720, videoCopy: false, burnSubtitleStreamIndex: 5 });
    expect(applyBurn({ height: 0, videoCopy: true }, 5, file({ resolution_w: 3840, resolution_h: 1604 })))
      .toEqual({ height: 2160, videoCopy: false, burnSubtitleStreamIndex: 5 });
  });
});

describe('BURN_ERROR_CODES', () => {
  it('covers the server burn refusals', () => {
    for (const c of ['INVALID_SUBTITLE_STREAM', 'SUBTITLE_NOT_IMAGE', 'BURN_IN_UNSUPPORTED']) {
      expect(BURN_ERROR_CODES.has(c)).toBe(true);
    }
    expect(BURN_ERROR_CODES.has('TOO_MANY_SESSIONS')).toBe(false);
  });
});
