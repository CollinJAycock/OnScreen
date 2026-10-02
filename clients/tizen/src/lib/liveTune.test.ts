import { describe, expect, it } from 'vitest';
import {
  MAX_LOAD_RESTARTS,
  describeTuneFailure,
  freshTuneBudget,
  isFailedTune,
  planLiveFatal,
  retuneBudget,
  spendLivePlan,
  watchdogFails,
  zapStep,
  type LiveFatal,
  type LivePlan,
  type TuneBudget,
} from './liveTune';

const fatal = (extra: Partial<LiveFatal>): LiveFatal => ({
  kind: 'network',
  details: 'fragLoadError',
  ...extra,
});

/** Feed the same fatal until the ladder gives up, the way the page does: a
 *  re-tune starts over on retuneBudget(). */
function ladder(f: LiveFatal, start: TuneBudget = freshTuneBudget(), cap = 20): LivePlan[] {
  let budget = start;
  const steps: LivePlan[] = [];
  for (let i = 0; i < cap; i++) {
    const plan = planLiveFatal(f, budget);
    steps.push(plan);
    if (plan === 'fail') break;
    spendLivePlan(plan, budget);
    if (plan === 'retune') budget = retuneBudget();
  }
  return steps;
}

const restarts = (): LivePlan[] => Array.from({ length: MAX_LOAD_RESTARTS }, () => 'restartLoad');

describe('planLiveFatal', () => {
  it('re-tunes a 404 on the playlist once, then fails (the black-screen bug)', () => {
    // On the C1: stream.m3u8 404 ('livetv: channel not found'). startLoad()
    // did nothing and the page sat black.
    expect(ladder(fatal({ details: 'manifestLoadError', httpStatus: 404 }))).toEqual(['retune', 'fail']);
  });

  it('never resumes a playlist that never loaded, whatever the status', () => {
    for (const details of ['manifestLoadError', 'manifestLoadTimeOut', 'manifestParsingError']) {
      expect(ladder(fatal({ details, httpStatus: 503 }))).toEqual(['retune', 'fail']);
      expect(ladder(fatal({ details }))).toEqual(['retune', 'fail']);
    }
  });

  it('treats any 4xx on a running stream as a failed tune, not a hiccup', () => {
    // A segment that rolled out of the live window, a reaped session: a
    // fresh tune rejoins the live edge, startLoad() wouldn't.
    expect(ladder(fatal({ details: 'fragLoadError', httpStatus: 404 }))).toEqual(['retune', 'fail']);
    expect(ladder(fatal({ details: 'levelLoadError', httpStatus: 403 }))).toEqual(['retune', 'fail']);
  });

  it('resumes fragment and playlist hiccups a few times per hls.js, then re-tunes once', () => {
    const steps = [...restarts(), 'retune', ...restarts(), 'fail'];
    expect(ladder(fatal({ details: 'fragLoadError', httpStatus: 502 }))).toEqual(steps);
    expect(ladder(fatal({ details: 'levelLoadTimeOut' }))).toEqual(steps);
    // Status 0: the network itself.
    expect(ladder(fatal({ details: 'fragLoadError', httpStatus: 0 }))).toEqual(steps);
  });

  it('recovers the decoder once per hls.js, then re-tunes once', () => {
    expect(ladder(fatal({ kind: 'media', details: 'bufferStalledError' }))).toEqual([
      'recoverMedia',
      'retune',
      'recoverMedia',
      'fail',
    ]);
  });

  it("doesn't try the decoder on a manifest-level media error", () => {
    expect(ladder(fatal({ kind: 'media', details: 'manifestIncompatibleCodecsError' }))).toEqual([
      'retune',
      'fail',
    ]);
  });

  it('re-tunes anything else once', () => {
    expect(ladder(fatal({ kind: 'other', details: 'internalException' }))).toEqual(['retune', 'fail']);
  });

  it('keeps the re-tune spent across the re-tune itself', () => {
    // The old page reset its flag whenever play() got a channel that wasn't
    // the active one, and the re-tune's teardown cleared the active channel
    // first: every unrecoverable error re-tuned forever.
    expect(planLiveFatal(fatal({ details: 'manifestLoadError' }), retuneBudget())).toBe('fail');
  });

  it('gives a channel that played its re-tune back (a fresh budget)', () => {
    // Re-tuned, then the picture came up: the page swaps in
    // freshTuneBudget() on 'playing', so a later drop may re-tune again.
    expect(planLiveFatal(fatal({ httpStatus: 404 }), retuneBudget())).toBe('fail');
    expect(planLiveFatal(fatal({ httpStatus: 404 }), freshTuneBudget())).toBe('retune');
  });

  it('records only what the plan spends', () => {
    const b = freshTuneBudget();
    spendLivePlan('restartLoad', b);
    spendLivePlan('recoverMedia', b);
    spendLivePlan('retune', b);
    spendLivePlan('fail', b);
    expect(b).toEqual({ loadRestarts: 1, mediaRecovered: true, retuned: false });
  });
});

describe('isFailedTune', () => {
  it('is the playlist failing or a 4xx', () => {
    expect(isFailedTune(fatal({ details: 'manifestLoadError' }))).toBe(true);
    expect(isFailedTune(fatal({ httpStatus: 400 }))).toBe(true);
    expect(isFailedTune(fatal({ httpStatus: 499 }))).toBe(true);
    expect(isFailedTune(fatal({ httpStatus: 500 }))).toBe(false);
    expect(isFailedTune(fatal({ httpStatus: 0 }))).toBe(false);
    expect(isFailedTune(fatal({}))).toBe(false);
  });
});

describe('describeTuneFailure', () => {
  it("says the tune failed until the picture has come up, then that it dropped", () => {
    expect(describeTuneFailure(fatal({ httpStatus: 404 }), false).title).toBe("Couldn't tune this channel.");
    expect(describeTuneFailure(fatal({ httpStatus: 404 }), true).title).toBe('This channel stopped playing.');
  });

  it("names the server's answers on the stream", () => {
    expect(describeTuneFailure(fatal({ details: 'manifestLoadError', httpStatus: 404 }), false).detail).toBe(
      "The server couldn't find this channel's stream (HTTP 404).",
    );
    expect(describeTuneFailure(fatal({ httpStatus: 503 }), false).detail).toMatch(/tuner is busy.*503/);
    expect(describeTuneFailure(fatal({ httpStatus: 504 }), false).detail).toMatch(/didn't start in time.*504/);
    expect(describeTuneFailure(fatal({ httpStatus: 403 }), false).detail).toBe(
      'The server refused the stream (HTTP 403).',
    );
    expect(describeTuneFailure(fatal({ httpStatus: 500 }), false).detail).toBe('The server answered HTTP 500.');
  });

  it('falls back to the decoder, then the hls.js detail', () => {
    expect(describeTuneFailure(fatal({ kind: 'media', details: 'bufferAppendError' }), true).detail).toBe(
      "The TV couldn't decode this stream.",
    );
    expect(describeTuneFailure(fatal({ details: 'fragLoadError' }), true).detail).toBe(
      'Playback error: fragLoadError',
    );
    expect(describeTuneFailure(fatal({ kind: 'other', details: '' }), false).detail).toBe('');
  });

  it('words the watchdog for a tune and for a stall', () => {
    expect(describeTuneFailure('timeout', false)).toEqual({
      title: "Couldn't tune this channel.",
      detail: 'No picture after 20 seconds.',
    });
    expect(describeTuneFailure('timeout', true).detail).toBe('No picture for 20 seconds.');
  });

  it("carries the app's own message", () => {
    expect(describeTuneFailure({ message: 'HLS is not supported on this TV.' }, false).detail).toBe(
      'HLS is not supported on this TV.',
    );
  });
});

describe('watchdogFails', () => {
  it('fails a tune or a stall with no picture, playing or paused', () => {
    // HAVE_NOTHING / HAVE_METADATA: the tuner never sent a frame.
    expect(watchdogFails({ paused: false, readyState: 0 })).toBe(true);
    expect(watchdogFails({ paused: false, readyState: 1 })).toBe(true);
    expect(watchdogFails({ paused: true, readyState: 0 })).toBe(true);
    expect(watchdogFails({ paused: true, readyState: 2 })).toBe(true);
  });

  it("stands down when the user paused a stream that has its data", () => {
    // OK on the black screen of a slow tune, then the stream came: no
    // 'playing' (it's paused), but nothing's wrong with the channel.
    expect(watchdogFails({ paused: true, readyState: 3 })).toBe(false);
    expect(watchdogFails({ paused: true, readyState: 4 })).toBe(false);
  });

  it("still fails a playing stream that hasn't fired 'playing'", () => {
    // Unpaused with data and still no 'playing' in 20 s: stuck.
    expect(watchdogFails({ paused: false, readyState: 4 })).toBe(true);
  });
});

describe('zapStep', () => {
  it('takes ▲ and CH ▲ the same way, up the lineup', () => {
    expect(zapStep('up')).toBe(1);
    expect(zapStep('channelUp')).toBe(1);
  });

  it('takes ▼ and CH ▼ the same way, down the lineup', () => {
    expect(zapStep('down')).toBe(-1);
    expect(zapStep('channelDown')).toBe(-1);
  });

  it('leaves every other key alone', () => {
    for (const k of ['left', 'right', 'enter', 'back', 'forward', 'rewind', 'playpause'] as const) {
      expect(zapStep(k)).toBe(0);
    }
  });
});
