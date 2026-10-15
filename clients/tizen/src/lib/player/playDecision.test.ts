import { describe, expect, it } from 'vitest';
import type { OptimizedVersion, PlaybackDecision } from '../api/types';
import {
  TOO_MANY_STREAMS_TITLE,
  optimizedVersionOf,
  playsOptimized,
  sourceVerdict,
  tooManyStreamsMessage,
} from './playDecision';

// The shape client.ts throws (ApiError: status, code, message).
function apiError(status: number, code: string, message: string) {
  return Object.assign(new Error(message), { status, code });
}

describe('tooManyStreamsMessage', () => {
  it("shows the server's viewer-ready message for 429 TOO_MANY_SESSIONS", () => {
    const msg = 'Your account can play one stream at a time, and another device is already playing. Stop it to watch here.';
    expect(tooManyStreamsMessage(apiError(429, 'TOO_MANY_SESSIONS', msg))).toBe(msg);
    expect(TOO_MANY_STREAMS_TITLE).toBe('Too many streams');
  });

  it('has its own words when the server sent none', () => {
    expect(tooManyStreamsMessage(apiError(429, 'TOO_MANY_SESSIONS', ''))).toMatch(/streams/);
  });

  it('is null for every other failure (they keep their own handling)', () => {
    expect(tooManyStreamsMessage(apiError(429, 'RATE_LIMITED', 'slow down'))).toBeNull();
    expect(tooManyStreamsMessage(apiError(403, 'TOO_MANY_SESSIONS', 'x'))).toBeNull();
    expect(tooManyStreamsMessage(apiError(500, 'INTERNAL', 'boom'))).toBeNull();
    expect(tooManyStreamsMessage(new Error('offline'))).toBeNull();
    expect(tooManyStreamsMessage(null)).toBeNull();
  });
});

const version: OptimizedVersion = {
  id: 'v1',
  file_id: 'f1',
  profile: 'tv',
  label: 'TV',
  container: 'mp4',
  video_codec: 'h264',
  audio_codec: 'aac',
  audio_channels: 2,
  stream_url: '/media/optimized/v1',
  stream_token: 'tok',
};

describe('optimized versions', () => {
  const optimized: PlaybackDecision = {
    decision: 'directPlay',
    file_id: 'f1',
    reason: 'optimized_version',
    optimized_version: version,
    source_decision: 'transcode',
  };

  it('plays the version the decision hands out, and keeps the source verdict for the fallback', () => {
    expect(optimizedVersionOf(optimized)).toBe(version);
    expect(sourceVerdict(optimized)).toBe('transcode');
    // The verdict is not the "directPlay" of the version.
    expect(sourceVerdict({ ...optimized, source_decision: 'directStream' })).toBe('directStream');
    expect(sourceVerdict({ ...optimized, source_decision: undefined })).toBe('transcode');
  });

  it('reads an ordinary decision as before', () => {
    const plain: PlaybackDecision = { decision: 'directStream', file_id: 'f1' };
    expect(optimizedVersionOf(plain)).toBeNull();
    expect(sourceVerdict(plain)).toBe('directStream');
    expect(sourceVerdict(null)).toBeNull();
    // A version without the reason, or without a URL, is not one to play.
    expect(optimizedVersionOf({ ...plain, optimized_version: version })).toBeNull();
    expect(optimizedVersionOf({ ...optimized, optimized_version: { ...version, stream_url: '' } })).toBeNull();
  });

  it('starts the version on the default audio, until it has failed once', () => {
    expect(playsOptimized({ version, failed: false, audioOrdinal: null })).toBe(true);
    expect(playsOptimized({ version, failed: false, audioOrdinal: 0 })).toBe(true);
    // Another track: the version doesn't carry it, so the source plays.
    expect(playsOptimized({ version, failed: false, audioOrdinal: 2 })).toBe(false);
    // Failed here: the source's verdict from then on.
    expect(playsOptimized({ version, failed: true, audioOrdinal: null })).toBe(false);
    expect(playsOptimized({ version: null, failed: false, audioOrdinal: null })).toBe(false);
  });
});
