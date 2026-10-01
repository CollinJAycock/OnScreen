import { describe, expect, it } from 'vitest';
import {
  CLOCK_JUMP_MS,
  END_TOLERANCE_MAX_MS,
  END_TOLERANCE_MS,
  FALSE_END_REFILL_MS,
  clearsSpinnerOnFragBuffered,
  directAudioFailure,
  endToleranceMs,
  endedAction,
  isClockJump,
  navigatesAfterWait,
  probeOutcome,
  PlayheadWatch,
  shouldHandleEnded,
  type EndedState,
  type ProbeState,
} from './gates';

const probe = (extra: Partial<ProbeState>): ProbeState => ({
  refused: false,
  ended: false,
  genAtStart: 4,
  genNow: 4,
  opening: false,
  suspended: false,
  errorShown: false,
  ...extra,
});

describe('probeOutcome (a dead-stream probe answering)', () => {
  it('shows the error for the stream that died, still bound', () => {
    expect(probeOutcome(probe({}))).toBe('fatal');
  });

  it('drops the generic error once another stream was bound meanwhile', () => {
    // An audio switch or Home-and-back replaced the dead stream while the
    // probe waited behind a hung heartbeat.
    expect(probeOutcome(probe({ genNow: 5 }))).toBe('ignore');
  });

  it('drops it while a new stream is on its way, in the background or over an error', () => {
    expect(probeOutcome(probe({ opening: true }))).toBe('ignore');
    expect(probeOutcome(probe({ suspended: true }))).toBe('ignore');
    expect(probeOutcome(probe({ errorShown: true }))).toBe('ignore');
  });

  it('ends playback on a refusal even for a replaced stream (it is about the user)', () => {
    expect(probeOutcome(probe({ refused: true, genNow: 9, opening: true }))).toBe('refusal');
  });

  it('does nothing once the player ended (the stop event landed first) or left', () => {
    expect(probeOutcome(probe({ ended: true, refused: true }))).toBe('ignore');
    expect(probeOutcome(probe({ ended: true }))).toBe('ignore');
  });
});

describe('shouldHandleEnded', () => {
  const ok = { streamReady: true, opening: false, refused: false, leaving: false, errorShown: false };

  it('takes the end of the stream that is playing', () => {
    expect(shouldHandleEnded(ok)).toBe(true);
  });

  it('ignores the old session ending while a re-issue is out', () => {
    // A chapter jump back from the last seconds: the old stream ran out of
    // buffer before the new one attached.
    expect(shouldHandleEnded({ ...ok, opening: true })).toBe(false);
  });

  it('ignores a source being swapped in, and a player stopped, leaving or in error', () => {
    expect(shouldHandleEnded({ ...ok, streamReady: false })).toBe(false);
    expect(shouldHandleEnded({ ...ok, refused: true })).toBe(false);
    expect(shouldHandleEnded({ ...ok, leaving: true })).toBe(false);
    expect(shouldHandleEnded({ ...ok, errorShown: true })).toBe(false);
  });

  it('ignores the late events of a page already torn down', () => {
    expect(shouldHandleEnded({ ...ok, destroyed: true })).toBe(false);
    expect(shouldHandleEnded({ ...ok, destroyed: false })).toBe(true);
  });
});

describe('endedAction (is an ended the end of the item?)', () => {
  // Hells Bells: 5:12.
  const TRACK = 312_000;
  const ended = (extra: Partial<EndedState>): EndedState => ({
    streamReady: true,
    opening: false,
    refused: false,
    leaving: false,
    errorShown: false,
    destroyed: false,
    played: true,
    lastMs: TRACK - 250,
    durationMs: TRACK,
    streamEndMs: null,
    retriedAtMs: null,
    ...extra,
  });

  it('completes a track that played to its end', () => {
    expect(endedAction(ended({}))).toEqual({ kind: 'complete' });
    expect(endedAction(ended({ lastMs: TRACK - END_TOLERANCE_MS }))).toEqual({ kind: 'complete' });
    // A last reading a hair past the listed length is still the end.
    expect(endedAction(ended({ lastMs: TRACK + 40 }))).toEqual({ kind: 'complete' });
  });

  it('never completes a track that started and never got going (the C1 report)', () => {
    // CH ▼ to Hells Bells: it loaded, never played, and ~30 s later the
    // element fired 'ended'. It was reported watched at 5:12 and skipped.
    expect(endedAction(ended({ played: false, lastMs: 0 }))).toEqual({ kind: 'retry', atMs: 0 });
    // Played a moment, stalled, then the "end".
    expect(endedAction(ended({ played: true, lastMs: 1_874 }))).toEqual({ kind: 'retry', atMs: 1_874 });
  });

  it('does not complete a source that never played even at the end', () => {
    // Only the user's seek counts as getting there without playing (the
    // page passes played for it).
    expect(endedAction(ended({ played: false }))).toEqual({ kind: 'retry', atMs: TRACK - 250 });
  });

  it('completes an item of unknown length that played, never one that did not', () => {
    expect(endedAction(ended({ durationMs: 0, lastMs: 5_000 }))).toEqual({ kind: 'complete' });
    expect(endedAction(ended({ durationMs: 0, lastMs: 0, played: false }))).toEqual({ kind: 'retry', atMs: 0 });
  });

  it("completes at the end of a finished server session, short of the listed length", () => {
    // The container lists 1:52:30 but the finished session's last segment
    // ends at 1:52:10: a re-opened session would have nothing to write.
    const listed = 6_750_000;
    const streamEnd = listed - 20_000;
    expect(endedAction(ended({ durationMs: listed, lastMs: streamEnd - 300, streamEndMs: streamEnd }))).toEqual({
      kind: 'complete',
    });
    // Not when it stopped well before that end, and not without playing.
    expect(endedAction(ended({ durationMs: listed, lastMs: streamEnd - 60_000, streamEndMs: streamEnd }))).toEqual({
      kind: 'retry',
      atMs: streamEnd - 60_000,
    });
    expect(
      endedAction(ended({ durationMs: listed, lastMs: streamEnd - 300, streamEndMs: streamEnd, played: false })).kind,
    ).toBe('retry');
  });

  it('retries once, then fails, at the same spot', () => {
    expect(endedAction(ended({ lastMs: 60_000, retriedAtMs: 60_000 }))).toEqual({ kind: 'fail', atMs: 60_000 });
    // The retried stream played on a little and stopped again: same trouble.
    expect(endedAction(ended({ lastMs: 75_000, retriedAtMs: 60_000 }))).toEqual({ kind: 'fail', atMs: 75_000 });
  });

  it('gets its retry back once the retried stream played on well past the retry', () => {
    expect(endedAction(ended({ lastMs: 60_000 + FALSE_END_REFILL_MS, retriedAtMs: 60_000 }))).toEqual({
      kind: 'retry',
      atMs: 60_000 + FALSE_END_REFILL_MS,
    });
  });

  it('gets its retry back after a seek back well before the retry (a new stall)', () => {
    expect(endedAction(ended({ lastMs: 60_000, retriedAtMs: 180_000 }))).toEqual({ kind: 'retry', atMs: 60_000 });
    // A seek back of just a few seconds is still the same trouble.
    expect(endedAction(ended({ lastMs: 175_000, retriedAtMs: 180_000 }))).toEqual({ kind: 'fail', atMs: 175_000 });
  });

  it("ignores what isn't about the stream that is playing", () => {
    expect(endedAction(ended({ opening: true }))).toEqual({ kind: 'ignore' });
    expect(endedAction(ended({ streamReady: false }))).toEqual({ kind: 'ignore' });
    expect(endedAction(ended({ leaving: true, played: false, lastMs: 0 }))).toEqual({ kind: 'ignore' });
    expect(endedAction(ended({ destroyed: true }))).toEqual({ kind: 'ignore' });
    expect(endedAction(ended({ refused: true }))).toEqual({ kind: 'ignore' });
    expect(endedAction(ended({ errorShown: true }))).toEqual({ kind: 'ignore' });
  });

  it('never retries from before the start or from a broken reading', () => {
    expect(endedAction(ended({ played: false, lastMs: -40 }))).toEqual({ kind: 'retry', atMs: 0 });
    expect(endedAction(ended({ played: false, lastMs: Number.NaN }))).toEqual({ kind: 'retry', atMs: 0 });
  });
});

describe('endToleranceMs', () => {
  it('is 2 s for anything up to 200 s long, and for an unknown length', () => {
    expect(endToleranceMs(0)).toBe(END_TOLERANCE_MS);
    expect(endToleranceMs(180_000)).toBe(END_TOLERANCE_MS);
    expect(endToleranceMs(Number.NaN)).toBe(END_TOLERANCE_MS);
  });

  it('grows with the length (1%) and stops at 10 s', () => {
    expect(endToleranceMs(312_000)).toBe(3_120);
    expect(endToleranceMs(7_200_000)).toBe(END_TOLERANCE_MAX_MS);
  });

  it('keeps an early end on a film far outside it', () => {
    // Two hours, "ended" at 1:58:00: two minutes short is not the end.
    const film = 7_200_000;
    expect(endedAction({
      streamReady: true, opening: false, refused: false, leaving: false, errorShown: false, destroyed: false,
      played: true, lastMs: film - 120_000, durationMs: film, streamEndMs: null, retriedAtMs: null,
    })).toEqual({ kind: 'retry', atMs: film - 120_000 });
  });
});

describe('isClockJump (a timeupdate further than playback can move)', () => {
  it('takes ordinary playback ticks', () => {
    expect(isClockJump({ fromMs: 10_000, toMs: 10_250, rate: 1, seeked: false })).toBe(false);
    // A slow tick (a busy main thread) still is playback.
    expect(isClockJump({ fromMs: 10_000, toMs: 10_000 + CLOCK_JUMP_MS, rate: 1, seeked: false })).toBe(false);
  });

  it('flags the clock jumping to the end on its own', () => {
    // 0:01.9 straight to 5:12 with no seek: the early end's clock.
    expect(isClockJump({ fromMs: 1_874, toMs: 312_000, rate: 1, seeked: false })).toBe(true);
  });

  it('takes any move after a seek, and going back', () => {
    expect(isClockJump({ fromMs: 1_874, toMs: 312_000, rate: 1, seeked: true })).toBe(false);
    expect(isClockJump({ fromMs: 60_000, toMs: 10_000, rate: 1, seeked: false })).toBe(false);
  });

  it('allows for a faster listening speed', () => {
    expect(isClockJump({ fromMs: 0, toMs: 2 * CLOCK_JUMP_MS, rate: 2, seeked: false })).toBe(false);
    expect(isClockJump({ fromMs: 0, toMs: 2 * CLOCK_JUMP_MS + 1, rate: 2, seeked: false })).toBe(true);
    // A slower (or broken) rate never narrows it below 1x.
    expect(isClockJump({ fromMs: 0, toMs: CLOCK_JUMP_MS, rate: 0.5, seeked: false })).toBe(false);
    expect(isClockJump({ fromMs: 0, toMs: CLOCK_JUMP_MS, rate: Number.NaN, seeked: false })).toBe(false);
  });
});

describe('clearsSpinnerOnFragBuffered', () => {
  const s = { buffering: true, liveInstance: true, streamReady: true, opening: false, starved: false };

  it('clears a spinner a recovered load left over playing video', () => {
    expect(clearsSpinnerOnFragBuffered(s)).toBe(true);
  });

  it('keeps it while the element is still starved', () => {
    expect(clearsSpinnerOnFragBuffered({ ...s, starved: true })).toBe(false);
  });

  it("keeps a re-issue's spinner, and ignores a replaced instance or a stream not ready", () => {
    expect(clearsSpinnerOnFragBuffered({ ...s, opening: true })).toBe(false);
    expect(clearsSpinnerOnFragBuffered({ ...s, liveInstance: false })).toBe(false);
    expect(clearsSpinnerOnFragBuffered({ ...s, streamReady: false })).toBe(false);
  });

  it('does nothing without a spinner', () => {
    expect(clearsSpinnerOnFragBuffered({ ...s, buffering: false })).toBe(false);
  });
});

describe('navigatesAfterWait (Back / the next item / the end, after a wait)', () => {
  it('navigates when the instance is still the live one', () => {
    expect(navigatesAfterWait({ destroyed: false })).toBe(true);
    expect(navigatesAfterWait({ destroyed: false, refused: false, leaving: false })).toBe(true);
  });

  it('does nothing once a transfer or a remount destroyed it meanwhile', () => {
    // goBack would pop the hub the transfer put under the new player;
    // replaceTo would navigate away from it and end its session.
    expect(navigatesAfterWait({ destroyed: true })).toBe(false);
    expect(navigatesAfterWait({ destroyed: true, refused: false, leaving: false })).toBe(false);
  });

  it('the end-of-item lookup yields to a refusal or a Back taken during it', () => {
    expect(navigatesAfterWait({ destroyed: false, refused: true, leaving: false })).toBe(false);
    expect(navigatesAfterWait({ destroyed: false, refused: false, leaving: true })).toBe(false);
  });
});

describe('directAudioFailure (a direct-play audio file that fails)', () => {
  it('falls back once to a server transcode when the TV cannot play the file', () => {
    // MEDIA_ERR_DECODE, MEDIA_ERR_SRC_NOT_SUPPORTED, or no code at all.
    expect(directAudioFailure(3, false)).toBe('transcode');
    expect(directAudioFailure(4, false)).toBe('transcode');
    expect(directAudioFailure(undefined, false)).toBe('transcode');
  });

  it('shows the error after the transcode was tried', () => {
    expect(directAudioFailure(3, true)).toBe('fatal');
    expect(directAudioFailure(4, true)).toBe('fatal');
  });

  it('reads a network error as the connection, transcode or not', () => {
    expect(directAudioFailure(2, false)).toBe('network');
    expect(directAudioFailure(2, true)).toBe('network');
  });
});

describe("PlayheadWatch + endedAction (the page's end-of-stream sequence)", () => {
  const TRACK = 312_000;
  const playingEl = { ended: false, playbackRate: 1 };
  const endedEl = { ended: true, playbackRate: 1 };
  const verdict = (w: PlayheadWatch, extra: Partial<EndedState> = {}) =>
    endedAction({
      streamReady: true, opening: false, refused: false, leaving: false, errorShown: false, destroyed: false,
      played: w.played, lastMs: w.lastMs, durationMs: TRACK, streamEndMs: null, retriedAtMs: null,
      ...extra,
    });

  it('a track played through ends the item', () => {
    const w = new PlayheadWatch();
    w.attach(0);
    w.playing();
    for (let t = 250; t <= TRACK - 200; t += 250) w.tick(t, playingEl);
    // Chromium's final timeupdate reads the duration, flagged ended.
    w.tick(TRACK, endedEl);
    expect(verdict(w)).toEqual({ kind: 'complete' });
  });

  it('Hells Bells: started, stalled at 0:01.874, "ended" ~30 s later: a retry, not completion', () => {
    const w = new PlayheadWatch();
    w.attach(0);
    w.playing();
    for (let t = 250; t <= 1_874; t += 250) w.tick(t, playingEl);
    w.tick(1_874, playingEl);
    // The end's clock: the duration, ended set.
    w.tick(TRACK, endedEl);
    expect(w.lastMs).toBe(1_874);
    expect(verdict(w)).toEqual({ kind: 'retry', atMs: 1_874 });
  });

  it('the same false end on a pipeline that does not flag ended yet is still caught', () => {
    const w = new PlayheadWatch();
    w.attach(0);
    w.playing();
    w.tick(1_000, playingEl);
    w.tick(TRACK, playingEl);
    expect(verdict(w)).toEqual({ kind: 'retry', atMs: 1_000 });
  });

  it('a source that never played is retried from where it was asked to start', () => {
    const w = new PlayheadWatch();
    w.attach(42_000);
    w.tick(TRACK, endedEl);
    expect(verdict(w)).toEqual({ kind: 'retry', atMs: 42_000 });
  });

  it("the user's seek to the end ends the item, played or not", () => {
    const w = new PlayheadWatch();
    w.attach(0);
    w.seeking();
    w.userSeek(TRACK);
    w.tick(TRACK, endedEl);
    expect(verdict(w)).toEqual({ kind: 'complete' });
  });

  it('a seek (hls.js start, a gap skip) moves the playhead legitimately', () => {
    const w = new PlayheadWatch();
    w.attach(0);
    w.playing();
    w.tick(500, playingEl);
    w.seeking();
    w.tick(TRACK - 300, playingEl);
    expect(w.lastMs).toBe(TRACK - 300);
    w.tick(TRACK, endedEl);
    expect(verdict(w)).toEqual({ kind: 'complete' });
  });

  it('a lone jump is skipped, and playback after it counts again', () => {
    const w = new PlayheadWatch();
    w.attach(0);
    w.playing();
    w.tick(1_000, playingEl);
    w.tick(60_000, playingEl);
    expect(w.lastMs).toBe(1_000);
    w.tick(60_250, playingEl);
    expect(w.lastMs).toBe(60_250);
  });

  it('a new source has not played, whatever the old one did', () => {
    const w = new PlayheadWatch();
    w.attach(0);
    w.playing();
    w.attach(90_000);
    expect(w.played).toBe(false);
    expect(w.lastMs).toBe(90_000);
  });
});
