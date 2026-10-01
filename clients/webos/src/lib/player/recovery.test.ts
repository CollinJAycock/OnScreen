import { describe, expect, it } from 'vitest';
import {
  NETWORK_RETRY_DELAYS_MS,
  applyPlan,
  freshRecovery,
  planFatal,
  type FatalContext,
  type FatalPlan,
} from './recovery';

const ctx = (extra: Partial<FatalContext>): FatalContext => ({
  kind: 'network',
  details: 'fragLoadError',
  manifestLoaded: true,
  canDemoteCodec: false,
  canFallBackToTranscode: false,
  ...extra,
});

/** Run the same fatal until the ladder gives up, recording each step. */
function ladder(c: FatalContext, cap = 10): string[] {
  const state = freshRecovery();
  const steps: string[] = [];
  for (let i = 0; i < cap; i++) {
    const plan: FatalPlan = planFatal(c, state);
    steps.push(
      plan.kind === 'retryLoad' || plan.kind === 'reloadManifest' ? `${plan.kind}:${plan.delayMs}` : plan.kind,
    );
    if (plan.kind === 'fail' || plan.kind === 'probe' || plan.kind === 'demote' || plan.kind === 'transcodeFallback') break;
    applyPlan(plan, state);
  }
  return steps;
}

describe('planFatal', () => {
  it('retries a failing load with backoff, then gives up (bounded)', () => {
    expect(ladder(ctx({ httpStatus: 503 }))).toEqual([
      ...NETWORK_RETRY_DELAYS_MS.map((d) => `retryLoad:${d}`),
      'fail',
    ]);
    expect(ladder(ctx({ details: 'levelLoadTimeOut' }))).toEqual(['retryLoad:2000', 'retryLoad:4000', 'retryLoad:8000', 'fail']);
  });

  it('reloads a playlist that never loaded (startLoad cannot), bounded the same way', () => {
    // The server's 503 'playlist not ready' while ffmpeg opens the source:
    // hls.js has no levels yet, and startLoad() would do nothing at all.
    expect(ladder(ctx({ details: 'manifestLoadError', httpStatus: 503, manifestLoaded: false }))).toEqual([
      ...NETWORK_RETRY_DELAYS_MS.map((d) => `reloadManifest:${d}`),
      'fail',
    ]);
    expect(ladder(ctx({ details: 'manifestLoadTimeOut', manifestLoaded: false }))[0]).toBe('reloadManifest:2000');
    // Named a manifest failure even if a level was somehow listed.
    expect(ladder(ctx({ details: 'manifestParsingError' }))[0]).toBe('reloadManifest:2000');
    // Anything before the first parse is the playlist's problem too.
    expect(ladder(ctx({ details: 'levelLoadTimeOut', manifestLoaded: false }))[0]).toBe('reloadManifest:2000');
    // A loaded playlist's refresh or a segment restarts the load in place.
    expect(ladder(ctx({ details: 'levelLoadError' }))[0]).toBe('retryLoad:2000');
  });

  it('asks the server about a dead session (403 / 404) instead of retrying it', () => {
    expect(ladder(ctx({ httpStatus: 404, details: 'levelLoadError' }))).toEqual(['probe']);
    expect(ladder(ctx({ httpStatus: 403 }))).toEqual(['probe']);
    expect(ladder(ctx({ httpStatus: 404, details: 'manifestLoadError', manifestLoaded: false }))).toEqual(['probe']);
  });

  it('demotes the codec claim on a rejected append', () => {
    expect(ladder(ctx({ kind: 'media', details: 'bufferAppendError', canDemoteCodec: true }))).toEqual(['demote']);
  });

  it('recovers a media error once, then falls back from a remux to a transcode', () => {
    expect(ladder(ctx({ kind: 'media', details: 'bufferStalledError', canFallBackToTranscode: true }))).toEqual([
      'recoverMedia',
      'transcodeFallback',
    ]);
  });

  it('re-inits a transcode once after recovery, then shows the error', () => {
    expect(ladder(ctx({ kind: 'media', details: 'bufferStalledError' }))).toEqual(['recoverMedia', 'reinit', 'fail']);
    expect(ladder(ctx({ kind: 'media', details: 'bufferAppendError' }))).toEqual(['recoverMedia', 'reinit', 'fail']);
  });

  it('re-inits once for anything else', () => {
    expect(ladder(ctx({ kind: 'other', details: 'internalException' }))).toEqual(['reinit', 'fail']);
  });
});
