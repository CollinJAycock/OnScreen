// Unit tests for the HLS half of the Cast helpers: the Chromecast
// capability profiles, the cast plan, the LOAD payload for an OnScreen HLS
// session, subtitle tracks and the timeline math. Pure — no SDK.

import { describe, it, expect } from 'vitest';
import {
  CHROMECAST_CAPS,
  absoluteUrl,
  buildCastHlsMediaInfo,
  buildCastStartBody,
  canCastFile,
  castClientName,
  castContentTime,
  castHlsFormatFields,
  castLoadStartSec,
  castProfileForDevice,
  castProgressDecision,
  castRefusal,
  castSubtitleTrack,
  castTranscodeHeight,
  fallbackCastProfile,
  planCast,
  planCastSeek,
  sniffHlsPlaylist,
  type CastableFile,
  type CastableItem,
} from './cast';

const mp4: CastableFile = {
  id: 'f1',
  container: 'mp4',
  video_codec: 'h264',
  audio_codec: 'aac',
  stream_token: 'stok',
  resolution_w: 1920,
  resolution_h: 1080,
};
const mkv: CastableFile = { ...mp4, container: 'mkv' };
const item: CastableItem = { id: 'i1', type: 'movie', title: 'Film', poster_path: '/artwork/p.jpg' };

describe('Chromecast capability profiles', () => {
  // The phone app keeps the same strings; docs/capability-profiles.md is
  // the reference. A change here is a contract change.
  it('pins the HD and 4K profile strings', () => {
    expect(CHROMECAST_CAPS.hd).toBe(
      'videoDecoder=h264,audioDecoder=aac:mp3,protocols=mp4:hls,maxWidth=1920,maxHeight=1080,maxAudioChannels=2,maxbitdepth=8,hdr=0',
    );
    expect(CHROMECAST_CAPS['4k']).toBe(
      'videoDecoder=h264:h265,audioDecoder=aac:mp3,protocols=mp4:hls,maxWidth=3840,maxHeight=2160,maxAudioChannels=6,maxbitdepth=10,hdr=1',
    );
  });

  it('picks 4K by model/device name, HD otherwise', () => {
    for (const n of ['Chromecast Ultra', 'Chromecast with Google TV', 'Google TV Streamer', 'SHIELD', 'Android TV', 'BRAVIA 4K VH2']) {
      expect(castProfileForDevice(n)).toBe('4k');
    }
    for (const n of ['Chromecast', 'Living Room TV', '', null, undefined]) {
      expect(castProfileForDevice(n)).toBe('hd');
    }
  });

  it('never picks 4K for a receiver without video_out', () => {
    expect(castProfileForDevice({ friendlyName: 'SHIELD', capabilities: ['audio_out'] })).toBe('hd');
    expect(castProfileForDevice({ friendlyName: 'Kitchen speaker group', capabilities: ['audio_out', 'multizone_group'] })).toBe('hd');
    expect(castProfileForDevice({ friendlyName: 'SHIELD', capabilities: ['video_out', 'audio_out'] })).toBe('4k');
    // No capability list (older SDK, fake): the name decides.
    expect(castProfileForDevice({ friendlyName: 'SHIELD', capabilities: [] })).toBe('4k');
    expect(castProfileForDevice({ friendlyName: 'SHIELD' })).toBe('4k');
  });

  it('a model name, when present, decides before the friendly name', () => {
    expect(castProfileForDevice({ modelName: 'MITV-MSSP2', friendlyName: 'Bedroom' })).toBe('4k');
    expect(castProfileForDevice({ modelName: 'Bose Smart Ultra Soundbar', friendlyName: 'Den' })).toBe('hd');
    expect(castProfileForDevice({ modelName: 'Chromecast HD', friendlyName: 'Google TV room' })).toBe('hd');
    expect(castProfileForDevice({ modelName: 'Google TV Streamer', friendlyName: 'Living Room TV' })).toBe('4k');
    // Ambiguous "Chromecast" (gen 3 or Chromecast with Google TV): the name decides.
    expect(castProfileForDevice({ modelName: 'Chromecast', friendlyName: 'Den Google TV' })).toBe('4k');
    expect(castProfileForDevice({ modelName: 'Chromecast', friendlyName: 'Den' })).toBe('hd');
  });

  it('falls back from 4K to HD once', () => {
    expect(fallbackCastProfile('4k')).toBe('hd');
    expect(fallbackCastProfile('hd')).toBeNull();
  });
});

describe('planCast', () => {
  it('casts a receiver-playable file directly when the decision is directPlay', () => {
    expect(planCast(mp4, 'directPlay')).toEqual({ mode: 'direct' });
  });

  it('remuxes on directStream, or directPlay of a file the receiver cannot open', () => {
    expect(planCast(mkv, 'directStream')).toEqual({ mode: 'hls', videoCopy: true });
    expect(planCast(mkv, 'directPlay')).toEqual({ mode: 'hls', videoCopy: true });
    expect(planCast(mp4, 'directStream')).toEqual({ mode: 'hls', videoCopy: true });
  });

  it('transcodes on a transcode decision', () => {
    expect(planCast(mp4, 'transcode')).toEqual({ mode: 'hls', videoCopy: false });
  });

  it('a non-default audio track or a burn-in never goes direct', () => {
    expect(planCast(mp4, 'directPlay', { audioTrackSelected: true })).toEqual({ mode: 'hls', videoCopy: true });
    expect(planCast(mp4, 'directPlay', { burnIn: true })).toEqual({ mode: 'hls', videoCopy: false });
  });

  it('a multi-part movie goes over as one HLS session', () => {
    expect(planCast({ ...mp4, part_index: 1 }, 'directPlay')).toEqual({ mode: 'hls', videoCopy: true });
  });

  it('casts a receiver-playable audio file directly whatever the verdict', () => {
    const mp3: CastableFile = { id: 'a', container: 'mp3', audio_codec: 'mp3', stream_token: 't' };
    expect(planCast(mp3, 'directStream')).toEqual({ mode: 'direct' });
    const dsf: CastableFile = { id: 'a', container: 'dsf', audio_codec: 'dsd_lsbf', stream_token: 't' };
    expect(planCast(dsf, 'directStream')).toEqual({ mode: 'hls', videoCopy: true });
  });

  it('refuses unsupported (Dolby Vision 5) and damaged files', () => {
    expect(planCast(mp4, 'unsupported').mode).toBe('refuse');
    expect(planCast(mp4, 'damaged').mode).toBe('refuse');
    expect(castRefusal('unsupported')).toMatch(/Dolby Vision/);
    expect(castRefusal('damaged')).toMatch(/damaged/);
    expect(castRefusal('transcode')).toBeNull();
  });

  it('maps the plan to the progress decision', () => {
    expect(castProgressDecision({ mode: 'direct' })).toBe('directPlay');
    expect(castProgressDecision({ mode: 'hls', videoCopy: true })).toBe('remux');
    expect(castProgressDecision({ mode: 'hls', videoCopy: false })).toBe('transcode');
  });
});

describe('canCastFile', () => {
  it('offers any playable file, not just receiver-playable ones', () => {
    expect(canCastFile(mkv)).toBe(true);
    expect(canCastFile({ ...mp4, video_codec: 'av1', container: 'mkv', stream_token: null })).toBe(true);
    expect(canCastFile({ id: 'a', audio_codec: 'flac' })).toBe(true);
  });
  it('not a missing file, or one with no streams', () => {
    expect(canCastFile({ ...mp4, status: 'missing' })).toBe(false);
    expect(canCastFile({ id: 'x' })).toBe(false);
  });
});

describe('cast transcode-start body', () => {
  it('asks for the source tier capped by the profile, never 0 (no ABR ladder)', () => {
    expect(castTranscodeHeight({ resolution_w: 3840, resolution_h: 1600 }, '4k')).toBe(2160);
    expect(castTranscodeHeight({ resolution_w: 3840, resolution_h: 2160 }, 'hd')).toBe(1080);
    expect(castTranscodeHeight({ resolution_w: 1280, resolution_h: 720 }, '4k')).toBe(720);
    expect(castTranscodeHeight({ resolution_w: 720, resolution_h: 480 }, 'hd')).toBe(480);
    expect(castTranscodeHeight({}, 'hd')).toBe(1080);
  });

  it('sends supports_hevc explicitly per profile, plus the track picks', () => {
    expect(buildCastStartBody({ fileId: 'f1', profile: 'hd', file: mp4, positionSec: 61.5, videoCopy: true })).toEqual({
      file_id: 'f1', height: 1080, position_ms: 61500, video_copy: true, audio_stream_index: null, supports_hevc: false,
    });
    expect(buildCastStartBody({
      fileId: 'f1', profile: '4k', file: { resolution_w: 3840, resolution_h: 2160 }, positionSec: 0,
      videoCopy: false, audioStreamIndex: 2, burnStreamIndex: 5,
    })).toEqual({
      file_id: 'f1', height: 2160, position_ms: 0, video_copy: false, audio_stream_index: 2, supports_hevc: true,
      burn_subtitle_stream_index: 5,
    });
  });
});

describe('HLS LOAD payload', () => {
  it('tells the receiver the segment formats', () => {
    expect(castHlsFormatFields('ts', false)).toEqual({ hlsSegmentFormat: 'ts', hlsVideoSegmentFormat: 'mpeg2_ts' });
    expect(castHlsFormatFields('fmp4', false)).toEqual({ hlsSegmentFormat: 'fmp4', hlsVideoSegmentFormat: 'fmp4' });
    expect(castHlsFormatFields('ts', true)).toEqual({ hlsSegmentFormat: 'ts_aac' });
  });

  it('builds the LOAD for a session playlist', () => {
    const url = 'https://o.example/api/v1/transcode/sessions/s1/playlist.m3u8?token=seg';
    const track = castSubtitleTrack('https://o.example', { origin: 'embedded', fileId: 'f1', streamIndex: 3, label: 'English', language: 'en' }, 'stok', 0);
    const info = buildCastHlsMediaInfo(item, mkv, { origin: 'https://o.example', playlistUrl: url, segmentFormat: 'fmp4', subtitle: track });
    expect(info).toMatchObject({
      contentId: url,
      contentUrl: url,
      contentType: 'application/x-mpegurl',
      streamType: 'BUFFERED',
      hlsSegmentFormat: 'fmp4',
      hlsVideoSegmentFormat: 'fmp4',
      customData: { itemId: 'i1', fileId: 'f1' },
    });
    expect(info.metadata.images).toEqual([{ url: 'https://o.example/artwork/p.jpg?w=600' }]);
    expect(info.tracks).toEqual([track]);
    expect(buildCastHlsMediaInfo(item, mkv, { origin: 'https://o.example', playlistUrl: url, segmentFormat: 'ts' }).tracks).toBeUndefined();
  });

  it('makes server paths absolute', () => {
    expect(absoluteUrl('https://o.example/', '/api/v1/x.m3u8?token=a')).toBe('https://o.example/api/v1/x.m3u8?token=a');
    expect(absoluteUrl('https://o.example', 'https://cdn.example/x.m3u8')).toBe('https://cdn.example/x.m3u8');
  });

  it('names the cast after the receiver', () => {
    expect(castClientName('Living Room TV')).toBe('Chromecast — Living Room TV');
    expect(castClientName('')).toBe('Chromecast');
  });
});

describe('castSubtitleTrack', () => {
  it('embedded stream: stream-token URL, rebased to the session start', () => {
    const t = castSubtitleTrack('https://o.example', { origin: 'embedded', fileId: 'f1', streamIndex: 3, label: 'English', language: 'en' }, 'stok', 1234.4);
    expect(t).toEqual({
      trackId: 1,
      type: 'TEXT',
      subtype: 'SUBTITLES',
      trackContentId: 'https://o.example/media/subtitles/f1/3?token=stok&start_ms=1234',
      trackContentType: 'text/vtt',
      name: 'English',
      language: 'en',
    });
  });

  it('external subtitle: addressed through its file so the stream token works', () => {
    const t = castSubtitleTrack('https://o.example', { origin: 'external', fileId: 'f1', subId: 'sub9', label: 'Forced', language: '' }, 'stok', 0);
    expect(t!.trackContentId).toBe('https://o.example/media/subtitles/f1/external/sub9?token=stok');
    expect(t!.language).toBe('und');
  });

  it('null without a token or a source', () => {
    expect(castSubtitleTrack('https://o', { origin: 'embedded', fileId: 'f', streamIndex: 1, label: '', language: '' }, null, 0)).toBeNull();
    expect(castSubtitleTrack('https://o', { origin: 'external', fileId: 'f', label: '', language: '' }, 't', 0)).toBeNull();
  });
});

describe('sniffHlsPlaylist', () => {
  it('reads an MPEG-TS media playlist', () => {
    const p = '#EXTM3U\n#EXT-X-PLAYLIST-TYPE:EVENT\n#EXTINF:4.0,\n/api/v1/transcode/sessions/s/seg/seg00000.ts?token=t\n';
    expect(sniffHlsPlaylist(p)).toEqual({ kind: 'media', format: 'ts' });
  });
  it('reads an fMP4 media playlist', () => {
    const p = '#EXTM3U\n#EXT-X-MAP:URI="/seg/init.mp4?token=t"\n#EXTINF:4.0,\n/seg/seg00000.m4s?token=t\n';
    expect(sniffHlsPlaylist(p)).toEqual({ kind: 'media', format: 'fmp4' });
  });
  it('reads a master playlist and its variants', () => {
    const p = '#EXTM3U\n#EXT-X-STREAM-INF:BANDWIDTH=8000000,RESOLUTION=1920x1080\n/abr/1080p/index.m3u8?token=t\n#EXT-X-STREAM-INF:BANDWIDTH=3000000\n/abr/720p/index.m3u8?token=t\n';
    expect(sniffHlsPlaylist(p)).toEqual({ kind: 'master', variants: ['/abr/1080p/index.m3u8?token=t', '/abr/720p/index.m3u8?token=t'] });
  });
  it('null format when no segment is listed yet', () => {
    expect(sniffHlsPlaylist('#EXTM3U\n')).toEqual({ kind: 'media', format: null });
  });
});

describe('cast timeline', () => {
  it('LOAD currentTime is the position on the session timeline', () => {
    // Re-encode started at the position: stream 0 = content 600.
    expect(castLoadStartSec(600, 600)).toBe(0);
    // Remux snapped to the keyframe at 596: 4 s in.
    expect(castLoadStartSec(600, 596)).toBe(4);
    // ...but never inside segment 0's silent head.
    expect(castLoadStartSec(600, 599, 2.5)).toBe(2.5);
    // Whole-title stream (offset 0): seek to the position.
    expect(castLoadStartSec(600, 0)).toBe(600);
  });

  it('content time = offset + receiver time', () => {
    expect(castContentTime(600, 12.5)).toBe(612.5);
    expect(castContentTime(600, NaN)).toBe(600);
    expect(castContentTime(0, -1)).toBe(0);
  });

  it('seeks on the receiver inside the produced window, else restarts', () => {
    const single = { fullTimeline: false, offsetSec: 600, seekableEndSec: 120, durationSec: 7200 };
    expect(planCastSeek(650, single)).toEqual({ kind: 'receiver', streamTime: 50 });
    expect(planCastSeek(900, single)).toEqual({ kind: 'restart', positionMs: 900000 });
    expect(planCastSeek(300, single)).toEqual({ kind: 'restart', positionMs: 300000 });
    expect(planCastSeek(650, { ...single, seekableEndSec: null })).toEqual({ kind: 'restart', positionMs: 650000 });
    expect(planCastSeek(9000, single)).toEqual({ kind: 'restart', positionMs: 7200000 });
  });

  it('a whole-title stream or a direct cast always seeks on the receiver', () => {
    expect(planCastSeek(5000, { fullTimeline: true, offsetSec: 0, seekableEndSec: null, durationSec: 7200 }))
      .toEqual({ kind: 'receiver', streamTime: 5000 });
  });
});
