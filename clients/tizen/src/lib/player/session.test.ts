import { describe, expect, it } from 'vitest';
import {
  REISSUE_END_GUARD_MS,
  classifySeek,
  clampToItem,
  contentDurationMs,
  firstLoadPositionSec,
  hlsSessionConfig,
  hlsStartPositionSec,
  inStreamStartMs,
  isStarved,
  listedDurationMs,
  nudgeScrub,
  planContentSeek,
  playlistWindow,
  seg0GapMs,
  sessionOffsetMs,
  syncSeekStreamSec,
  toContentMs,
  validAudioOrdinal,
} from './session';

const MIN = 60_000;

describe('sessionOffsetMs', () => {
  it('keeps a real 0 (a full-timeline stream opens at 0:00)', () => {
    expect(sessionOffsetMs({ start_offset_sec: 0 }, 45 * MIN)).toBe(0);
  });

  it('falls back to the requested position only when the field is absent', () => {
    expect(sessionOffsetMs({}, 45 * MIN)).toBe(45 * MIN);
    expect(sessionOffsetMs({ start_offset_sec: undefined }, 1234)).toBe(1234);
  });

  it('ignores a negative or non-finite value', () => {
    expect(sessionOffsetMs({ start_offset_sec: -1 }, 5000)).toBe(5000);
    expect(sessionOffsetMs({ start_offset_sec: Number.NaN }, 5000)).toBe(5000);
  });

  it('converts the keyframe-aligned seconds to ms', () => {
    expect(sessionOffsetMs({ start_offset_sec: 2695.04 }, 2700_000)).toBe(2695_040);
  });
});

describe('seg0GapMs', () => {
  it('reads the gap, 0 when absent or not positive', () => {
    expect(seg0GapMs({ seg0_audio_gap_sec: 0.4 })).toBe(400);
    expect(seg0GapMs({})).toBe(0);
    expect(seg0GapMs({ seg0_audio_gap_sec: 0 })).toBe(0);
    expect(seg0GapMs({ seg0_audio_gap_sec: -2 })).toBe(0);
  });
});

describe('inStreamStartMs', () => {
  it('starts a remux at the request, past its keyframe-early head', () => {
    expect(inStreamStartMs(2700_000, 2695_000, 0)).toBe(5000);
  });

  it('seeks a full-timeline stream (offset 0) all the way to the resume point', () => {
    expect(inStreamStartMs(45 * MIN, 0, 0)).toBe(45 * MIN);
  });

  it('never starts before the first audible frame (the seg0 gap dominates)', () => {
    expect(inStreamStartMs(2700_000, 2700_000, 1200)).toBe(1200);
    expect(inStreamStartMs(2700_500, 2700_000, 1200)).toBe(1200);
    expect(inStreamStartMs(2705_000, 2700_000, 1200)).toBe(5000);
  });

  it('is never negative', () => {
    expect(inStreamStartMs(1000, 3000, 0)).toBe(0);
  });
});

describe('hlsStartPositionSec', () => {
  it('lets hls.js pick the head for a start at (or within 0.5 s of) 0', () => {
    expect(hlsStartPositionSec(0, 0)).toBe(-1);
    expect(hlsStartPositionSec(400, 0)).toBe(-1);
  });

  it('seeks for anything further in', () => {
    expect(hlsStartPositionSec(5000, 0)).toBe(5);
  });

  it('honours a short measured audio gap', () => {
    expect(hlsStartPositionSec(400, 400)).toBe(0.4);
  });
});

describe('resume at 45:00 on a remux (offset 2695 s, seg0 gap 0.4 s)', () => {
  const resp = { start_offset_sec: 2695, seg0_audio_gap_sec: 0.4 };
  const requested = 45 * MIN;
  const offset = sessionOffsetMs(resp, requested);
  const gap = seg0GapMs(resp);
  const startSec = hlsStartPositionSec(inStreamStartMs(requested, offset, gap), gap);

  it('starts hls.js ~5 s into the stream', () => {
    expect(startSec).toBeCloseTo(5.0, 3);
    expect(hlsSessionConfig(startSec).startPosition).toBeCloseTo(5.0, 3);
  });

  it('shows 45:00 once the player sits at its start', () => {
    expect(toContentMs(startSec, offset)).toBe(45 * MIN);
  });
});

describe('firstLoadPositionSec', () => {
  const seg = (start: number, duration: number) => ({ start, duration });
  const live = (targetduration: number, ...fragments: { start: number; duration: number }[]) => ({
    live: true,
    targetduration,
    fragments,
  });

  it('starts the 4K remux resume on its only listed segment (1917, ~27 s to the first frame)', () => {
    // Resume at 2:48.669 on a long-GOP remux: the session opens on the
    // keyframe at 2:38.241, so the start is 10.43 s in, and the first
    // playlist lists one segment that ends right there.
    const offset = sessionOffsetMs({ start_offset_sec: 158.241 }, 168_669);
    const want = hlsStartPositionSec(inStreamStartMs(168_669, offset, 0), 0);
    expect(want).toBeCloseTo(10.428, 3);
    const at = firstLoadPositionSec(want, live(10, seg(0, 10.428)));
    expect(at).toBe(0);
    // The playhead moves with it: content time is still stream + offset.
    expect(toContentMs(at, offset)).toBe(158_241);
  });

  it('starts at the request when enough is listed past it', () => {
    expect(firstLoadPositionSec(5, live(4, seg(0, 4), seg(4, 4), seg(8, 4)))).toBe(5);
    expect(firstLoadPositionSec(10, live(4, seg(0, 4), seg(4, 4), seg(8, 4)))).toBe(10);
  });

  it('counts a start within a second of the edge as at the edge', () => {
    expect(firstLoadPositionSec(11.5, live(4, seg(0, 4), seg(4, 4), seg(8, 4)))).toBe(8);
    expect(firstLoadPositionSec(12, live(4, seg(0, 4), seg(4, 4), seg(8, 4)))).toBe(8);
  });

  it('replays at most one target duration: further past the edge it waits as before', () => {
    expect(firstLoadPositionSec(12.4, live(4, seg(0, 4), seg(4, 4), seg(8, 4)))).toBe(8);
    expect(firstLoadPositionSec(12.6, live(4, seg(0, 4), seg(4, 4), seg(8, 4)))).toBe(12.6);
    expect(firstLoadPositionSec(25, live(10, seg(0, 10.4)))).toBe(25);
  });

  it('never starts before the first audible frame', () => {
    expect(firstLoadPositionSec(3.6, live(4, seg(0, 4)), 0.4)).toBe(0.4);
    // The gap is the start itself: nothing earlier to begin at.
    expect(firstLoadPositionSec(0.4, live(4, seg(0, 0.9)), 0.4)).toBe(0.4);
  });

  it("leaves hls.js's own pick, a finished playlist and an empty one alone", () => {
    expect(firstLoadPositionSec(-1, live(10, seg(0, 2)))).toBe(-1);
    expect(firstLoadPositionSec(10.43, { live: false, targetduration: 10, fragments: [seg(0, 10.428)] })).toBe(10.43);
    expect(firstLoadPositionSec(10.43, live(10))).toBe(10.43);
    expect(firstLoadPositionSec(10.43, null)).toBe(10.43);
    expect(firstLoadPositionSec(10.43, undefined)).toBe(10.43);
  });

  it('starts at the head of a playlist whose first segment ends at the start (no gap measured)', () => {
    expect(firstLoadPositionSec(4.2, live(4, seg(0, 4.2)))).toBe(0);
  });
});

describe('toContentMs', () => {
  it('adds the session offset', () => {
    expect(toContentMs(12.5, 60_000)).toBe(72_500);
  });

  it('treats a non-finite clock as 0', () => {
    expect(toContentMs(Number.NaN, 60_000)).toBe(60_000);
  });
});

describe('duration rules', () => {
  const file = { duration_ms: 7_200_000 };
  const item = { duration_ms: 7_080_000 }; // TMDB runtime, whole minutes

  it("prefers the file's probed length over the item's runtime", () => {
    expect(listedDurationMs(file, item)).toBe(7_200_000);
    expect(listedDurationMs({}, item)).toBe(7_080_000);
    expect(listedDurationMs({ duration_ms: 0 }, { duration_ms: 0 })).toBe(0);
  });

  it("never trusts a growing session's player duration", () => {
    const ctx = { playerDurationMs: 180_000, offsetMs: 2_695_000, directAudio: false, ended: false };
    expect(contentDurationMs(file, item, ctx)).toBe(7_200_000);
    expect(contentDurationMs({}, {}, ctx)).toBe(0);
  });

  it('re-absolutises an ended session when nothing is listed', () => {
    const ctx = { playerDurationMs: 4_505_000, offsetMs: 2_695_000, directAudio: false, ended: true };
    expect(contentDurationMs({}, {}, ctx)).toBe(7_200_000);
    expect(contentDurationMs(file, item, ctx)).toBe(7_200_000);
  });

  it("uses direct audio's settled player duration first", () => {
    const ctx = { playerDurationMs: 245_300, offsetMs: 0, directAudio: true, ended: false };
    expect(contentDurationMs({ duration_ms: 245_000 }, {}, ctx)).toBe(245_300);
  });

  it('falls back to the listed length while direct audio is unknown or infinite', () => {
    expect(contentDurationMs({ duration_ms: 245_000 }, {}, {
      playerDurationMs: Number.POSITIVE_INFINITY, offsetMs: 0, directAudio: true, ended: false,
    })).toBe(245_000);
    expect(contentDurationMs({}, {}, { playerDurationMs: 0, offsetMs: 0, directAudio: true, ended: false })).toBe(0);
  });
});

describe('playlistWindow', () => {
  it('reads the last fragment end and ENDLIST', () => {
    const frags = [{ start: 0, duration: 6 }, { start: 6, duration: 6 }, { start: 12, duration: 4.5 }];
    expect(playlistWindow({ fragments: frags, live: true })).toEqual({ producedEndMs: 16_500, ended: false });
    expect(playlistWindow({ fragments: frags, live: false })).toEqual({ producedEndMs: 16_500, ended: true });
  });

  it('is null before the playlist loads and 0 with no fragments yet', () => {
    expect(playlistWindow(null)).toBeNull();
    expect(playlistWindow({ fragments: [], live: true })).toEqual({ producedEndMs: 0, ended: false });
  });
});

describe('classifySeek', () => {
  // Resumed at ~45:00 on a remux; ffmpeg has written 60 s so far.
  const growing = { offsetMs: 2_695_000, producedEndMs: 60_000, ended: false, durationMs: 7_200_000 };

  it('seeks locally inside the produced window, both edges included', () => {
    expect(classifySeek(2_700_000, growing)).toEqual({ kind: 'local', streamSec: 5 });
    expect(classifySeek(2_695_000, growing)).toEqual({ kind: 'local', streamSec: 0 });
    expect(classifySeek(2_755_000, growing)).toEqual({ kind: 'local', streamSec: 60 });
  });

  it('re-issues before the session head (rewinding a resumed session)', () => {
    expect(classifySeek(2_694_999, growing)).toEqual({ kind: 'reissue', positionMs: 2_694_999 });
    expect(classifySeek(0, growing)).toEqual({ kind: 'reissue', positionMs: 0 });
    expect(classifySeek(-30_000, growing)).toEqual({ kind: 'reissue', positionMs: 0 });
  });

  it('re-issues past a still-growing edge', () => {
    expect(classifySeek(2_755_001, growing)).toEqual({ kind: 'reissue', positionMs: 2_755_001 });
  });

  it('keeps a re-issue clear of the end of the item', () => {
    expect(classifySeek(7_200_000, growing)).toEqual({ kind: 'reissue', positionMs: 7_200_000 - REISSUE_END_GUARD_MS });
    expect(classifySeek(9_999_999, growing)).toEqual({ kind: 'reissue', positionMs: 7_200_000 - REISSUE_END_GUARD_MS });
  });

  it('clamps past the end of a finished window', () => {
    // The playlist's segments end 5 s short of the listed length: the last
    // scrub step lands past the stream, and must not re-issue at the end.
    const ended = { ...growing, producedEndMs: 4_500_000, ended: true };
    expect(classifySeek(7_200_000, ended)).toEqual({ kind: 'clamp', streamSec: 4500 });
    expect(classifySeek(7_194_000, ended)).toEqual({ kind: 'local', streamSec: 4499 });
  });

  it('still re-issues before the head of a finished window', () => {
    const ended = { ...growing, producedEndMs: 4_505_000, ended: true };
    expect(classifySeek(60_000, ended)).toEqual({ kind: 'reissue', positionMs: 60_000 });
  });

  it('serves a full-timeline stream locally from 0', () => {
    const full = { offsetMs: 0, producedEndMs: 7_200_000, ended: true, durationMs: 7_200_000 };
    expect(classifySeek(60_000, full)).toEqual({ kind: 'local', streamSec: 60 });
  });

  it('has no upper clamp while the length is unknown', () => {
    const unknown = { offsetMs: 0, producedEndMs: 30_000, ended: false, durationMs: 0 };
    expect(classifySeek(90_000, unknown)).toEqual({ kind: 'reissue', positionMs: 90_000 });
  });
});

describe('syncSeekStreamSec', () => {
  const w = { offsetMs: 600_000, producedEndMs: 120_000, ended: false, durationMs: 3_600_000 };

  it('ignores a position within 2 s of the local one (our own echo)', () => {
    expect(syncSeekStreamSec(650_000, 651_500, w)).toBeNull();
  });

  it('seeks locally, in stream time, inside the loaded window', () => {
    expect(syncSeekStreamSec(660_000, 620_000, w)).toBe(60);
  });

  it('never re-issues: outside the window it leaves the player alone', () => {
    expect(syncSeekStreamSec(100_000, 620_000, w)).toBeNull();
    expect(syncSeekStreamSec(900_000, 620_000, w)).toBeNull();
  });
});

describe('nudgeScrub / clampToItem', () => {
  it('starts from the playhead and accumulates on the pending target', () => {
    let t = nudgeScrub(null, 100_000, 10_000, 600_000);
    expect(t).toBe(110_000);
    t = nudgeScrub(t, 100_000, 10_000, 600_000);
    t = nudgeScrub(t, 100_000, 30_000, 600_000);
    expect(t).toBe(150_000);
  });

  it('clamps to the item', () => {
    expect(nudgeScrub(5_000, 0, -10_000, 600_000)).toBe(0);
    expect(nudgeScrub(595_000, 0, 30_000, 600_000)).toBe(600_000);
    expect(clampToItem(9e9, 0)).toBe(9e9);
  });
});

describe('validAudioOrdinal', () => {
  it('accepts 0..count-1 only', () => {
    expect(validAudioOrdinal(0, 2)).toBe(true);
    expect(validAudioOrdinal(1, 2)).toBe(true);
    expect(validAudioOrdinal(2, 2)).toBe(false);
    expect(validAudioOrdinal(-1, 2)).toBe(false);
    expect(validAudioOrdinal(0, 0)).toBe(false);
    expect(validAudioOrdinal(1.5, 3)).toBe(false);
    expect(validAudioOrdinal(Number.NaN, 3)).toBe(false);
  });

  it('rejects an absolute ffprobe index past the audio count', () => {
    // video 0, audio 1 + 2, subtitles 3+: the second audio track's
    // AudioStream.index is 2, its ordinal 1.
    expect(validAudioOrdinal(2, 2)).toBe(false);
  });
});

describe('hlsSessionConfig', () => {
  it("ports the web client's live-sync, buffer and load-policy settings", () => {
    const c = hlsSessionConfig(-1);
    expect(c.startPosition).toBe(-1);
    expect(c.lowLatencyMode).toBe(false);
    expect(c.liveSyncDurationCount).toBe(999);
    expect(c.liveMaxLatencyDurationCount).toBe(1002);
    // The page starts loading itself, once the first playlist is in
    // (firstLoadPositionSec).
    expect(c.autoStartLoad).toBe(false);
    // Inside webOS 6's MSE quota at remux bitrates: 60 s forward + 90 s back
    // ran into bufferFullError stalls mid-play.
    expect(c.maxBufferLength).toBe(30);
    expect(c.maxMaxBufferLength).toBe(30);
    expect(c.backBufferLength).toBe(30);
    // No effect without a level bitrate (the session playlist has none) and
    // under a 30 s maxMaxBufferLength: not set, so nobody tunes it again.
    expect('maxBufferSize' in c).toBe(false);
    // Left to hls.js's default (3 nudges): 0 turns the first in-buffer
    // stall into a fatal error.
    expect('nudgeMaxRetry' in c).toBe(false);
    expect(c.fragLoadPolicy?.default.maxTimeToFirstByteMs).toBe(30_000);
    expect(c.fragLoadPolicy?.default.maxLoadTimeMs).toBe(60_000);
    expect(c.manifestLoadPolicy?.default.maxTimeToFirstByteMs).toBe(30_000);
    expect(c.manifestLoadPolicy?.default.errorRetry?.maxNumRetry).toBe(4);
  });
});

describe("planContentSeek (the page's seekToContent decision)", () => {
  const live = {
    streamReady: true,
    opening: false,
    serverStream: true,
    durationMs: 120 * MIN,
    // Resumed at 45:00 on a remux: stream 0 = 44:55; 8 s produced.
    offsetMs: 2_695_000,
    window: { producedEndMs: 8_000, ended: false },
  };

  it('parks while the stream is not ready or a session is on its way', () => {
    expect(planContentSeek(50 * MIN, false, { ...live, streamReady: false })).toEqual({
      kind: 'park',
      targetMs: 50 * MIN,
      relative: false,
    });
    expect(planContentSeek(50 * MIN, true, { ...live, opening: true }).kind).toBe('park');
  });

  it('seeks inside the produced window, in stream time, and reports the content playhead', () => {
    expect(planContentSeek(2_700_000, false, live)).toEqual({ kind: 'seek', streamSec: 5, contentMs: 2_700_000 });
  });

  it('re-issues before the head, and for an absolute jump past a growing edge', () => {
    expect(planContentSeek(40 * MIN, false, live)).toEqual({ kind: 'reissue', positionMs: 40 * MIN });
    expect(planContentSeek(2_720_000, false, live)).toEqual({ kind: 'reissue', positionMs: 2_720_000 });
  });

  it('clamps a key seek a little past a growing edge to the edge', () => {
    expect(planContentSeek(2_720_000, true, live)).toEqual({ kind: 'seek', streamSec: 8, contentMs: 2_703_000 });
  });

  it('seeks direct audio as the file itself, clamped to the item', () => {
    const direct = { ...live, serverStream: false, offsetMs: 0, durationMs: 200_000 };
    expect(planContentSeek(90_000, true, direct)).toEqual({ kind: 'seek', streamSec: 90, contentMs: 90_000 });
    expect(planContentSeek(500_000, false, direct)).toEqual({ kind: 'seek', streamSec: 200, contentMs: 200_000 });
  });
});

describe('isStarved', () => {
  it('is starved only when the element cannot play on', () => {
    expect(isStarved({ readyState: 4, seeking: false })).toBe(false);
    expect(isStarved({ readyState: 3, seeking: false })).toBe(false);
    expect(isStarved({ readyState: 2, seeking: false })).toBe(true);
    expect(isStarved({ readyState: 4, seeking: true })).toBe(true);
  });
});
