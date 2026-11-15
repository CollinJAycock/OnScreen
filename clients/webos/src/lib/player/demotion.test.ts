import { describe, expect, it } from 'vitest';
import { codecToDemote, deliveredVideo, videoFamily, type RefusedSession } from './demotion';

const session = (over: Partial<RefusedSession>): RefusedSession => ({
  remux: false,
  claimedHEVC: true,
  claimedAV1: false,
  ...over,
});

describe('videoFamily', () => {
  it('reads file codecs and RFC 6381 lists, skipping audio', () => {
    expect(videoFamily('hevc')).toBe('hevc');
    expect(videoFamily('H265')).toBe('hevc');
    expect(videoFamily('h264')).toBe('h264');
    expect(videoFamily('av1')).toBe('av1');
    expect(videoFamily('vp9')).toBe('other');
    expect(videoFamily('mp4a.40.2,hvc1.2.4.L150.B0')).toBe('hevc');
    expect(videoFamily('dvh1.08.06')).toBe('hevc');
    expect(videoFamily('avc1.640028,mp4a.40.2')).toBe('h264');
    expect(videoFamily('av01.0.05M.08')).toBe('av1');
    expect(videoFamily('mp4a.40.2')).toBeNull();
    expect(videoFamily('')).toBeNull();
    expect(videoFamily(undefined)).toBeNull();
  });
});

describe('codecToDemote', () => {
  it('a refused H.264 remux demotes nothing (it used to take HEVC away for good)', () => {
    expect(codecToDemote(session({ remux: true, fileCodec: 'h264' }))).toBeNull();
  });

  it('a refused HEVC remux demotes HEVC', () => {
    expect(codecToDemote(session({ remux: true, fileCodec: 'hevc' }))).toBe('hevc');
  });

  it('an AV1 version demotes AV1, not HEVC (the first file no longer decides)', () => {
    expect(codecToDemote(session({ remux: true, fileCodec: 'av1', claimedAV1: true }))).toBe('av1');
    // Not claimed: nothing to take back.
    expect(codecToDemote(session({ remux: true, fileCodec: 'av1', claimedAV1: false }))).toBeNull();
  });

  it('what the player says it was handed wins over the file', () => {
    expect(codecToDemote(session({ remux: true, fileCodec: 'hevc', playerCodecs: 'avc1.640028' }))).toBeNull();
    expect(codecToDemote(session({ remux: false, fileCodec: 'h264', playerCodecs: 'mp4a.40.2,hvc1.1.6.L120.90' }))).toBe('hevc');
  });

  it("the audio SourceBuffer's refusal says nothing about the video decoders", () => {
    expect(codecToDemote(session({ remux: true, fileCodec: 'hevc', refusedTrack: 'audio', playerCodecs: 'mp4a.40.2' }))).toBeNull();
  });

  it('a transcode is H.264 when neither HEVC nor AV1 was claimed', () => {
    expect(codecToDemote(session({ fileCodec: 'hevc', claimedHEVC: false }))).toBeNull();
  });

  it("reads a transcode's codec from the start response's segment format", () => {
    // HEVC goes into MPEG-TS only for a client that asks for TS.
    expect(deliveredVideo(session({ fileCodec: 'hevc', segmentFormat: 'ts' }))).toBe('h264');
    expect(codecToDemote(session({ fileCodec: 'hevc', segmentFormat: 'ts' }))).toBeNull();
    expect(codecToDemote(session({ fileCodec: 'h264', segmentFormat: 'fmp4' }))).toBe('hevc');
    expect(codecToDemote(session({ fileCodec: 'av1', segmentFormat: 'fmp4', claimedAV1: true }))).toBe('av1');
  });

  it("follows the server's rule where the response can't say (a client that always asks for TS)", () => {
    const ts = { tsSegments: true, segmentFormat: 'ts' };
    expect(codecToDemote(session({ ...ts, fileCodec: 'hevc', fileHeight: 1080 }))).toBe('hevc');
    expect(codecToDemote(session({ ...ts, fileCodec: 'h264', fileHeight: 2160 }))).toBe('hevc');
    expect(codecToDemote(session({ ...ts, fileCodec: 'h264', fileHeight: 1080 }))).toBeNull();
    // An AV1 source on a client that doesn't claim AV1: re-encoded, HEVC only at 4K.
    expect(codecToDemote(session({ ...ts, fileCodec: 'av1', fileHeight: 1080 }))).toBeNull();
    expect(codecToDemote(session({ ...ts, fileCodec: 'av1', fileHeight: 2160 }))).toBe('hevc');
  });

  it('a remux whose file has no codec on record demotes nothing', () => {
    expect(codecToDemote(session({ remux: true }))).toBeNull();
  });
});
