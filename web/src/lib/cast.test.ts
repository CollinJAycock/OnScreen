// Unit tests for the Cast helper. Pure module — no SDK, no DOM.
// Coverage:
//   - castContentType: container + codec → Cast MIME
//   - castMetadataType: item.type → Cast type integer
//   - isCastable: gates on status + stream_token + supported MIME
//   - buildCastMediaInfo: produces a valid LOAD payload, builds an
//     absolute URL with the stream token, includes poster when present

import { describe, it, expect } from 'vitest';
import {
  CAST_METADATA_TYPE,
  buildCastMediaInfo,
  castContentType,
  castMetadataType,
  isCastable,
  type CastableFile,
  type CastableItem,
} from './cast';

const goodFile: CastableFile = {
  id: 'file-uuid',
  status: 'active',
  container: 'mp4',
  video_codec: 'h264',
  audio_codec: 'aac',
  duration_seconds: 5400,
  stream_token: 'tok-abc',
};

const goodItem: CastableItem = {
  id: 'item-uuid',
  type: 'movie',
  title: 'The Movie',
  poster_path: '/artwork/poster.jpg',
  parent_title: null,
};

describe('castContentType', () => {
  it('returns video/mp4 for H.264 in mp4', () => {
    expect(castContentType(goodFile)).toBe('video/mp4');
  });

  it('returns video/mp4 for HEVC in mp4 (Cast Ultra path)', () => {
    expect(castContentType({ ...goodFile, video_codec: 'hevc' })).toBe('video/mp4');
    expect(castContentType({ ...goodFile, video_codec: 'h265' })).toBe('video/mp4');
  });

  it('returns video/mp4 for m4v / mov containers', () => {
    expect(castContentType({ ...goodFile, container: 'm4v' })).toBe('video/mp4');
    expect(castContentType({ ...goodFile, container: 'mov' })).toBe('video/mp4');
  });

  it('returns video/webm only for webm container', () => {
    expect(castContentType({ ...goodFile, container: 'webm' })).toBe('video/webm');
  });

  it('returns "" (unsupported) for AV1 / VP9 video', () => {
    expect(castContentType({ ...goodFile, video_codec: 'av1' })).toBe('');
    expect(castContentType({ ...goodFile, video_codec: 'vp9' })).toBe('');
  });

  it('returns "" for MKV — not Default Receiver supported', () => {
    expect(castContentType({ ...goodFile, container: 'mkv' })).toBe('');
  });

  it('handles audio-only files', () => {
    expect(
      castContentType({
        ...goodFile,
        video_codec: '',
        audio_codec: 'aac',
        container: 'm4a',
      }),
    ).toBe('audio/mp4');
    expect(
      castContentType({
        ...goodFile,
        video_codec: '',
        audio_codec: 'mp3',
        container: 'mp3',
      }),
    ).toBe('audio/mp3');
    expect(
      castContentType({
        ...goodFile,
        video_codec: '',
        audio_codec: 'flac',
        container: 'flac',
      }),
    ).toBe('audio/flac');
    expect(
      castContentType({
        ...goodFile,
        video_codec: '',
        audio_codec: 'opus',
        container: 'ogg',
      }),
    ).toBe('audio/ogg');
  });

  it('case-insensitive on container + codec', () => {
    expect(
      castContentType({ ...goodFile, container: 'MP4', video_codec: 'H264' }),
    ).toBe('video/mp4');
  });

  it('returns "" when both codecs missing', () => {
    expect(
      castContentType({
        ...goodFile,
        video_codec: null,
        audio_codec: null,
        container: 'mp4',
      }),
    ).toBe('');
  });
});

describe('castMetadataType', () => {
  it('maps each item.type to its Cast constant', () => {
    expect(castMetadataType({ ...goodItem, type: 'movie' })).toBe(CAST_METADATA_TYPE.MOVIE);
    expect(castMetadataType({ ...goodItem, type: 'episode' })).toBe(CAST_METADATA_TYPE.TV_SHOW);
    expect(castMetadataType({ ...goodItem, type: 'show' })).toBe(CAST_METADATA_TYPE.TV_SHOW);
    expect(castMetadataType({ ...goodItem, type: 'track' })).toBe(CAST_METADATA_TYPE.MUSIC_TRACK);
    expect(castMetadataType({ ...goodItem, type: 'photo' })).toBe(CAST_METADATA_TYPE.PHOTO);
  });

  it('falls back to GENERIC for unknown types', () => {
    expect(castMetadataType({ ...goodItem, type: 'audiobook' })).toBe(
      CAST_METADATA_TYPE.GENERIC,
    );
    expect(castMetadataType({ ...goodItem, type: 'wat' })).toBe(CAST_METADATA_TYPE.GENERIC);
  });
});

describe('isCastable', () => {
  it('true for an active direct-play H.264 mp4', () => {
    expect(isCastable(goodFile)).toBe(true);
  });

  it('false when file is not active (missing / pending)', () => {
    expect(isCastable({ ...goodFile, status: 'missing' })).toBe(false);
  });

  it('false when stream_token is empty', () => {
    expect(isCastable({ ...goodFile, stream_token: null })).toBe(false);
    expect(isCastable({ ...goodFile, stream_token: '' })).toBe(false);
  });

  it('false for unsupported codec (AV1)', () => {
    expect(isCastable({ ...goodFile, video_codec: 'av1' })).toBe(false);
  });

  it('false for MKV container', () => {
    expect(isCastable({ ...goodFile, container: 'mkv' })).toBe(false);
  });
});

describe('buildCastMediaInfo', () => {
  it('returns null for a non-castable file', () => {
    expect(
      buildCastMediaInfo(goodItem, { ...goodFile, status: 'missing' }, 'https://o.example'),
    ).toBeNull();
  });

  it('builds an absolute stream URL with the token', () => {
    const got = buildCastMediaInfo(goodItem, goodFile, 'https://o.example');
    expect(got).not.toBeNull();
    expect(got!.contentId).toBe(
      'https://o.example/media/stream/file-uuid?token=tok-abc',
    );
  });

  it('strips trailing slashes from baseUrl', () => {
    const got = buildCastMediaInfo(goodItem, goodFile, 'https://o.example/');
    expect(got!.contentId).toBe('https://o.example/media/stream/file-uuid?token=tok-abc');
    const trimmed = buildCastMediaInfo(goodItem, goodFile, 'https://o.example///');
    expect(trimmed!.contentId).toBe(
      'https://o.example/media/stream/file-uuid?token=tok-abc',
    );
  });

  it('URL-encodes file id and stream token', () => {
    const got = buildCastMediaInfo(
      goodItem,
      { ...goodFile, id: 'a/b c', stream_token: 'tok with space' },
      'https://o.example',
    );
    expect(got!.contentId).toBe(
      'https://o.example/media/stream/a%2Fb%20c?token=tok%20with%20space',
    );
  });

  it('sets contentType from castContentType', () => {
    const got = buildCastMediaInfo(goodItem, goodFile, 'https://o.example');
    expect(got!.contentType).toBe('video/mp4');
  });

  it('streamType is BUFFERED (VOD, not Live TV)', () => {
    const got = buildCastMediaInfo(goodItem, goodFile, 'https://o.example');
    expect(got!.streamType).toBe('BUFFERED');
  });

  it('metadata.type matches castMetadataType', () => {
    const got = buildCastMediaInfo(goodItem, goodFile, 'https://o.example');
    expect(got!.metadata.type).toBe(CAST_METADATA_TYPE.MOVIE);
    expect(got!.metadata.metadataType).toBe(CAST_METADATA_TYPE.MOVIE);
  });

  it('includes poster image as absolute URL when poster_path present', () => {
    const got = buildCastMediaInfo(goodItem, goodFile, 'https://o.example');
    expect(got!.metadata.images).toEqual([
      { url: 'https://o.example/artwork/poster.jpg' },
    ]);
  });

  it('omits images array when poster_path is null', () => {
    const got = buildCastMediaInfo(
      { ...goodItem, poster_path: null },
      goodFile,
      'https://o.example',
    );
    expect(got!.metadata.images).toEqual([]);
  });

  it('passes parent_title through as subtitle (e.g. show title for episode)', () => {
    const ep: CastableItem = {
      id: 'e1',
      type: 'episode',
      title: 'The Episode',
      parent_title: 'The Show',
    };
    const got = buildCastMediaInfo(ep, goodFile, 'https://o.example');
    expect(got!.metadata.subtitle).toBe('The Show');
  });

  it('attaches duration when available', () => {
    const got = buildCastMediaInfo(goodItem, goodFile, 'https://o.example');
    expect(got!.duration).toBe(5400);
  });

  it('customData carries item + file ids for the receiver', () => {
    const got = buildCastMediaInfo(goodItem, goodFile, 'https://o.example');
    expect(got!.customData).toEqual({ itemId: 'item-uuid', fileId: 'file-uuid' });
  });
});
