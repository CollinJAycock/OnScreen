// What the player does about a FATAL hls.js error. hls.js has already retried
// a failed load by the time it calls one fatal (the fragment / playlist load
// policies in hlsSessionConfig), so this is the ladder above that:
//
//  NETWORK  A 403 / 404 / 410 is a dead session (stopped by an admin,
//           superseded by another start of the item, reaped): ask the server
//           with one heartbeat whether it refused playback ('probe'), else
//           show the error. Anything else (a timeout, a 5xx, the network
//           gone) restarts the load a few times with backoff, then shows the
//           error. It used to call startLoad() forever: a dead server or an
//           expired token spun silently behind "playing". A failure before
//           the playlist was ever parsed (the server's 503 "playlist not
//           ready" while ffmpeg opens the source) reloads the playlist
//           itself ('reloadManifest'): hls.js 1.6 requests a manifest only
//           from loadSource(), its startLoad() is a no-op before one, and
//           "Starting playback…" stayed up for good.
//  MEDIA    A bufferAppendError is MSE refusing the bytes (a codec the panel
//           won't take): demote the codec claim and restart, once per item.
//           Otherwise one recoverMediaError(); then, on a remux, one restart
//           as a full transcode at the same point (the stream-copied source
//           itself may be what the decoder can't take; Android's
//           fallbackFromDirectPlay); then one re-init of the same source;
//           then the error.
//  other    One re-init of the same source, then the error.
//
// Pure so the ladder is tested without hls.js; the page carries the state.

export type FatalKind = 'network' | 'media' | 'other';

/** Per-session recovery budget (a new session starts a new one, except the
 *  re-init, which inherits reinitAttempted so it can't loop). */
export interface RecoveryState {
  networkRetries: number;
  mediaRecovered: boolean;
  reinitAttempted: boolean;
}

export function freshRecovery(): RecoveryState {
  return { networkRetries: 0, mediaRecovered: false, reinitAttempted: false };
}

export interface FatalContext {
  kind: FatalKind;
  /** hls.js data.details, e.g. 'bufferAppendError', 'levelLoadError'. */
  details: string;
  /** HTTP status of the failed load, when hls.js has one. */
  httpStatus?: number;
  /** hls.js has parsed the session's playlist (it has levels). */
  manifestLoaded: boolean;
  /** The codec claim can still be demoted (bufferAppendError path). */
  canDemoteCodec: boolean;
  /** The session stream-copies the video and hasn't fallen back yet. */
  canFallBackToTranscode: boolean;
}

export type FatalPlan =
  | { kind: 'demote' }
  | { kind: 'probe' }
  /** Restart loading (segments, a playlist refresh) on the same instance. */
  | { kind: 'retryLoad'; delayMs: number }
  /** Load the playlist again (loadSource): it never loaded. */
  | { kind: 'reloadManifest'; delayMs: number }
  | { kind: 'recoverMedia' }
  | { kind: 'transcodeFallback' }
  | { kind: 'reinit' }
  | { kind: 'fail' };

/** Backoff for restarting a failed load: three tries, ~14 s in all, on top
 *  of hls.js's own retries. */
export const NETWORK_RETRY_DELAYS_MS: readonly number[] = [2_000, 4_000, 8_000];

/** A status that means the session itself is gone, not a passing failure. */
export function isDeadStreamStatus(status: number | undefined): boolean {
  return status === 403 || status === 404 || status === 410;
}

/** The failure is the playlist itself: none parsed yet, or hls.js names a
 *  manifest load / parse (manifestLoadError, manifestLoadTimeOut,
 *  manifestParsingError). */
export function isManifestFailure(ctx: Pick<FatalContext, 'details' | 'manifestLoaded'>): boolean {
  return !ctx.manifestLoaded || /^manifest/i.test(ctx.details);
}

/** The next step for a fatal error, given the session's budget. Mutates
 *  nothing; the caller records what it did (see applyPlan). */
export function planFatal(ctx: FatalContext, state: RecoveryState): FatalPlan {
  if (ctx.kind === 'network') {
    if (isDeadStreamStatus(ctx.httpStatus)) return { kind: 'probe' };
    const delayMs = NETWORK_RETRY_DELAYS_MS[state.networkRetries];
    if (delayMs === undefined) return { kind: 'fail' };
    // Same budget either way: a playlist that never comes still ends on the
    // error overlay.
    return isManifestFailure(ctx) ? { kind: 'reloadManifest', delayMs } : { kind: 'retryLoad', delayMs };
  }
  if (ctx.kind === 'media') {
    if (ctx.details === 'bufferAppendError' && ctx.canDemoteCodec) return { kind: 'demote' };
    if (!state.mediaRecovered) return { kind: 'recoverMedia' };
    if (ctx.canFallBackToTranscode) return { kind: 'transcodeFallback' };
  }
  return state.reinitAttempted ? { kind: 'fail' } : { kind: 'reinit' };
}

/** Spend the budget a plan uses. */
export function applyPlan(plan: FatalPlan, state: RecoveryState): void {
  if (plan.kind === 'retryLoad' || plan.kind === 'reloadManifest') state.networkRetries++;
  else if (plan.kind === 'recoverMedia') state.mediaRecovered = true;
  else if (plan.kind === 'reinit') state.reinitAttempted = true;
}
