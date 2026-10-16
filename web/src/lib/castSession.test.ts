// CastController against a fake Cast SDK: the sequence of server calls
// (decision → transcode-start → playlist read → LOAD) a cast makes on the
// Chromecast's behalf, the 4K → HD retry, seek restarts and teardown.

import { describe, it, expect, vi } from 'vitest';
import { CastController, type CastControllerDeps } from './castSession';
import { CHROMECAST_CAPS, type CastableFile, type CastableItem } from './cast';

const item: CastableItem = { id: 'i1', type: 'movie', title: 'Film' };
const mkv: CastableFile = {
  id: 'f1', container: 'mkv', video_codec: 'h264', audio_codec: 'ac3',
  stream_token: 'stok', resolution_w: 1920, resolution_h: 1080,
};

function fakeSdk(friendlyName: string, loadMedia: (req: any) => Promise<void> = async () => {}) {
  const loads: any[] = [];
  const player: any = { isMediaLoaded: true, isPaused: false, playerState: 'PLAYING', currentTime: 0, duration: 0 };
  const rpc = { addEventListener: vi.fn(), removeEventListener: vi.fn(), playOrPause: vi.fn(), seek: vi.fn() };
  const session = {
    getCastDevice: () => ({ friendlyName }),
    loadMedia: vi.fn(async (req: any) => {
      loads.push(req);
      await loadMedia(req);
    }),
    getMediaSession: () => ({ idleReason: undefined }),
  };
  function Ctor(this: any, ...a: any[]) { this.args = a; }
  const w = {
    cast: {
      framework: {
        CastContext: { getInstance: () => ({ getCurrentSession: () => session }) },
        RemotePlayer: function () { return player; },
        RemotePlayerController: function () { return rpc; },
        RemotePlayerEventType: { ANY_CHANGE: 'anyChanged' },
      },
    },
    chrome: {
      cast: {
        Image: Ctor,
        media: {
          MediaInfo: function (this: any, id: string, ct: string) { this.contentId = id; this.contentType = ct; },
          GenericMediaMetadata: Ctor,
          LoadRequest: function (this: any, mi: any) { this.media = mi; },
          Track: function (this: any, id: number, type: string) { this.trackId = id; this.type = type; },
          StreamType: { BUFFERED: 'BUFFERED' },
          TrackType: { TEXT: 'TEXT' },
          TextTrackType: { SUBTITLES: 'SUBTITLES' },
        },
      },
    },
  };
  return { w, loads, player, rpc, session };
}

function fakeDeps(decision = 'directStream', playlist = '#EXTM3U\n#EXTINF:4,\n/seg/seg00000.ts?token=t\n') {
  let n = 0;
  const deps = {
    decide: vi.fn(async (_item: string, _caps: string, _file: string) => ({ decision, file_id: 'f1' })),
    start: vi.fn(async (_item: string, _caps: string, body: any) => {
      n++;
      return {
        session_id: `s${n}`,
        token: `t${n}`,
        playlist_url: `/api/v1/transcode/sessions/s${n}/playlist.m3u8?token=t${n}`,
        start_offset_sec: body.position_ms / 1000,
        seg0_audio_gap_sec: 0,
        segment_format: 'ts' as const,
      };
    }),
    stop: vi.fn(async () => {}),
    progress: vi.fn(async () => {}),
    fetchText: vi.fn(async (_url: string, _signal: AbortSignal) => playlist),
  } satisfies CastControllerDeps;
  return deps;
}

describe('CastController', () => {
  it('casts an MKV as an HLS remux with the HD profile', async () => {
    const sdk = fakeSdk('Living Room TV');
    const deps = fakeDeps('directStream');
    const onError = vi.fn();
    const ctl = new CastController(sdk.w, 'https://o.example', deps, () => {}, onError);
    const ok = await ctl.start({
      item, file: mkv, positionSec: 600, durationSec: 7200,
      subtitle: { origin: 'embedded', fileId: 'f1', streamIndex: 3, label: 'English', language: 'en' },
    });
    expect(ok).toBe(true);
    expect(onError).not.toHaveBeenCalled();
    expect(deps.decide).toHaveBeenCalledWith('i1', CHROMECAST_CAPS.hd, 'f1');
    expect(deps.start).toHaveBeenCalledWith('i1', CHROMECAST_CAPS.hd, {
      file_id: 'f1', height: 1080, position_ms: 600000, video_copy: true, audio_stream_index: null, supports_hevc: false,
    });
    expect(deps.fetchText.mock.calls[0][0]).toBe('https://o.example/api/v1/transcode/sessions/s1/playlist.m3u8?token=t1');
    const req = sdk.loads[0];
    expect(req.currentTime).toBe(0);
    expect(req.activeTrackIds).toEqual([1]);
    expect(req.media).toMatchObject({
      contentId: 'https://o.example/api/v1/transcode/sessions/s1/playlist.m3u8?token=t1',
      contentType: 'application/x-mpegurl',
      hlsSegmentFormat: 'ts',
      hlsVideoSegmentFormat: 'mpeg2_ts',
    });
    expect(req.media.tracks[0].trackContentId).toBe('https://o.example/media/subtitles/f1/3?token=stok&start_ms=600000');
    expect(deps.progress).toHaveBeenCalledWith('i1', 600000, 7200000, 'playing', 'remux', 'Chromecast — Living Room TV');
  });

  it('casts a receiver-playable MP4 directly', async () => {
    const sdk = fakeSdk('Chromecast');
    const deps = fakeDeps('directPlay');
    const ctl = new CastController(sdk.w, 'https://o.example', deps, () => {}, vi.fn());
    await ctl.start({ item, file: { ...mkv, container: 'mp4', audio_codec: 'aac' }, positionSec: 42, durationSec: 7200 });
    expect(deps.start).not.toHaveBeenCalled();
    expect(sdk.loads[0].media.contentId).toBe('https://o.example/media/stream/f1?token=stok');
    expect(sdk.loads[0].currentTime).toBe(42);
  });

  it('retries a 4K load the receiver rejects with the HD profile, once', async () => {
    let first = true;
    const sdk = fakeSdk('SHIELD', async () => {
      if (first) {
        first = false;
        throw { code: 'load_media_failed' };
      }
    });
    const deps = fakeDeps('transcode');
    const ctl = new CastController(sdk.w, 'https://o.example', deps, () => {}, vi.fn());
    const ok = await ctl.start({ item, file: { ...mkv, video_codec: 'hevc', resolution_w: 3840, resolution_h: 2160 }, positionSec: 0, durationSec: 7200 });
    expect(ok).toBe(true);
    expect(deps.decide.mock.calls.map((c) => c[1])).toEqual([CHROMECAST_CAPS['4k'], CHROMECAST_CAPS.hd]);
    expect((deps.start.mock.calls[0][2] as any).supports_hevc).toBe(true);
    expect((deps.start.mock.calls[1][2] as any)).toMatchObject({ supports_hevc: false, height: 1080 });
    // The first session is stopped; the second plays.
    expect(deps.stop).toHaveBeenCalledWith('s1', 't1');
    expect(sdk.loads).toHaveLength(2);
  });

  it('refuses a damaged file without starting a session', async () => {
    const sdk = fakeSdk('Chromecast');
    const deps = fakeDeps('damaged');
    const onError = vi.fn();
    const ctl = new CastController(sdk.w, 'https://o.example', deps, () => {}, onError);
    expect(await ctl.start({ item, file: mkv, positionSec: 0, durationSec: 100 })).toBe(false);
    expect(deps.start).not.toHaveBeenCalled();
    expect(onError.mock.calls[0][0]).toMatch(/damaged/);
  });

  it('a seek past what was produced starts a new session there and stops the old one', async () => {
    const sdk = fakeSdk('Chromecast');
    const deps = fakeDeps('directStream');
    const ctl = new CastController(sdk.w, 'https://o.example', deps, () => {}, vi.fn());
    await ctl.start({ item, file: mkv, positionSec: 0, durationSec: 7200 });
    sdk.player.liveSeekableRange = { start: 0, end: 300 };

    await ctl.seek(100); // inside: a receiver seek
    expect(sdk.rpc.seek).toHaveBeenCalledTimes(1);
    expect(sdk.player.currentTime).toBe(100);
    expect(deps.start).toHaveBeenCalledTimes(1);

    await ctl.seek(3600); // outside: a new session at 1:00:00
    expect(deps.start).toHaveBeenCalledTimes(2);
    expect((deps.start.mock.calls[1][2] as any).position_ms).toBe(3600000);
    expect(deps.stop).toHaveBeenCalledWith('s1', 't1');
    expect(ctl.positionSec).toBe(3600);
  });

  it('end() reports the stop as the Chromecast and stops the session', async () => {
    const sdk = fakeSdk('Den');
    const deps = fakeDeps('transcode');
    const ctl = new CastController(sdk.w, 'https://o.example', deps, () => {}, vi.fn());
    await ctl.start({ item, file: mkv, positionSec: 30, durationSec: 7200 });
    expect(await ctl.end()).toBe(30);
    expect(deps.progress).toHaveBeenLastCalledWith('i1', 30000, 7200000, 'stopped', 'transcode', 'Chromecast — Den');
    expect(deps.stop).toHaveBeenCalledWith('s1', 't1');
    expect(sdk.rpc.removeEventListener).toHaveBeenCalled();
  });
});
