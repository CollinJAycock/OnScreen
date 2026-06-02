import { describe, it, expect } from 'vitest';
import { canDirectPlay, canRemuxVideo, videoBitDepthOK, type ClientCaps } from './playback-decision';
import type { ItemFile } from './api';

const FULL: ClientCaps = { hevc: true, hevc10bit: true, av1: true, hdr: true };
const BASIC: ClientCaps = { hevc: false, hevc10bit: false, av1: false, hdr: false };

function f(o: Partial<ItemFile>): ItemFile {
  return { id: 'x', stream_url: '/s', faststart: true, ...o } as ItemFile;
}

describe('videoBitDepthOK', () => {
  it('8-bit always passes', () => {
    expect(videoBitDepthOK(f({ video_codec: 'h264', video_bit_depth: 8 }), BASIC)).toBe(true);
  });
  it('undefined depth assumes 8-bit', () => {
    expect(videoBitDepthOK(f({ video_codec: 'hevc' }), BASIC)).toBe(true);
  });
  it('10-bit H.264 (Hi10P) never passes', () => {
    expect(videoBitDepthOK(f({ video_codec: 'h264', video_bit_depth: 10 }), FULL)).toBe(false);
  });
  it('10-bit HEVC needs Main 10', () => {
    expect(videoBitDepthOK(f({ video_codec: 'hevc', video_bit_depth: 10 }), { ...BASIC, hevc: true, hevc10bit: false })).toBe(false);
    expect(videoBitDepthOK(f({ video_codec: 'hevc', video_bit_depth: 10 }), { ...BASIC, hevc: true, hevc10bit: true })).toBe(true);
  });
  it('10-bit AV1 tracks 8-bit support (not separately gated)', () => {
    expect(videoBitDepthOK(f({ video_codec: 'av1', video_bit_depth: 10 }), BASIC)).toBe(true);
  });
  it('12-bit HEVC (Main 12) never passes — a Main 10 decoder cannot decode it', () => {
    // Regression: Fruits Basket S1E1 is HEVC 12-bit; hevc10bit must NOT imply Main 12.
    expect(videoBitDepthOK(f({ video_codec: 'hevc', video_bit_depth: 12 }), FULL)).toBe(false);
  });
});

describe('canDirectPlay', () => {
  it('faststart mp4 h264/aac 8-bit → true', () => {
    expect(canDirectPlay(f({ container: 'mp4', video_codec: 'h264', audio_codec: 'aac', faststart: true }), BASIC)).toBe(true);
  });
  it('mkv container → false (remux territory)', () => {
    expect(canDirectPlay(f({ container: 'matroska', video_codec: 'h264', audio_codec: 'aac' }), BASIC)).toBe(false);
  });
  it('non-faststart mp4 → false', () => {
    expect(canDirectPlay(f({ container: 'mp4', video_codec: 'h264', audio_codec: 'aac', faststart: false }), BASIC)).toBe(false);
  });
  it('eac3 audio → false', () => {
    expect(canDirectPlay(f({ container: 'mp4', video_codec: 'h264', audio_codec: 'eac3' }), BASIC)).toBe(false);
  });
  it('hevc only when client supports it', () => {
    const file = f({ container: 'mp4', video_codec: 'hevc', audio_codec: 'aac' });
    expect(canDirectPlay(file, BASIC)).toBe(false);
    expect(canDirectPlay(file, { ...BASIC, hevc: true })).toBe(true);
  });
  it('10-bit HEVC needs Main 10 even with HEVC support', () => {
    const file = f({ container: 'mp4', video_codec: 'hevc', audio_codec: 'aac', video_bit_depth: 10 });
    expect(canDirectPlay(file, { ...BASIC, hevc: true, hevc10bit: false })).toBe(false);
    expect(canDirectPlay(file, { ...BASIC, hevc: true, hevc10bit: true })).toBe(true);
  });
  it('HDR on SDR display → false; HDR display → true', () => {
    const file = f({ container: 'mp4', video_codec: 'h264', audio_codec: 'aac', hdr_type: 'hdr10' });
    expect(canDirectPlay(file, { ...BASIC, hdr: false })).toBe(false);
    expect(canDirectPlay(file, { ...BASIC, hdr: true })).toBe(true);
  });
});

describe('canRemuxVideo', () => {
  it('h264 → true', () => {
    expect(canRemuxVideo(f({ video_codec: 'h264' }), BASIC)).toBe(true);
  });
  it('hevc only with client support', () => {
    expect(canRemuxVideo(f({ video_codec: 'hevc' }), BASIC)).toBe(false);
    expect(canRemuxVideo(f({ video_codec: 'hevc' }), { ...BASIC, hevc: true })).toBe(true);
  });
  it('av1 not remuxable into MPEG-TS → false', () => {
    expect(canRemuxVideo(f({ video_codec: 'av1' }), FULL)).toBe(false);
  });
  it('10-bit H.264 → false (Hi10P stays undecodable after copy)', () => {
    expect(canRemuxVideo(f({ video_codec: 'h264', video_bit_depth: 10 }), FULL)).toBe(false);
  });
  it('HDR on SDR display → false', () => {
    expect(canRemuxVideo(f({ video_codec: 'h264', hdr_type: 'hdr10' }), { ...BASIC, hdr: false })).toBe(false);
  });
});
