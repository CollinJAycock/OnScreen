// What the player makes of the server's play decision (v2.6): the user's
// stream limit (429 TOO_MANY_SESSIONS) and an optimized version handed out
// in place of a transcode. Pure; unit-tested.

import type { OptimizedVersion, PlaybackDecision } from '../api/types';

/** The heading over the stream-limit message. */
export const TOO_MANY_STREAMS_TITLE = 'Too many streams';

const TOO_MANY_STREAMS_FALLBACK =
  'Your account is already playing as many streams as it may. Stop one on another device to watch here.';

/**
 * The viewer-ready message of a stream-limit refusal (the play decision or a
 * transcode start answering 429 TOO_MANY_SESSIONS: an admin's per-user
 * concurrent-stream limit), null for any other failure. Shown as it is,
 * never retried and never worked around with a local decision: another
 * attempt only counts against the same limit.
 */
export function tooManyStreamsMessage(e: unknown): string | null {
  const err = e as { status?: unknown; code?: unknown; message?: unknown } | null;
  if (!err || err.status !== 429 || err.code !== 'TOO_MANY_SESSIONS') return null;
  const msg = typeof err.message === 'string' ? err.message.trim() : '';
  return msg || TOO_MANY_STREAMS_FALLBACK;
}

/** The play decision's reason for an optimized version. */
export const REASON_OPTIMIZED_VERSION = 'optimized_version';

/** The optimized version to play, when the decision chose one: a direct
 *  play of its MP4 (stream_url) instead of transcoding the source. */
export function optimizedVersionOf(d: PlaybackDecision | null | undefined): OptimizedVersion | null {
  if (!d || d.reason !== REASON_OPTIMIZED_VERSION) return null;
  const v = d.optimized_version;
  return v && typeof v.stream_url === 'string' && v.stream_url !== '' ? v : null;
}

/** The verdict for the source file itself: the decision, or with an
 *  optimized version the source's own (source_decision; a transcode when
 *  an older build left it out). Null without a decision. */
export function sourceVerdict(d: PlaybackDecision | null | undefined): string | null {
  if (!d) return null;
  if (optimizedVersionOf(d)) return d.source_decision || 'transcode';
  return d.decision || null;
}

/**
 * Whether a start plays the optimized version: there is one, it hasn't
 * failed here, and the audio is the version's (it carries the default
 * track only: ordinal 0, or null for the server's default). Anything else
 * starts the source with its own verdict.
 */
export function playsOptimized(o: {
  version: OptimizedVersion | null;
  failed: boolean;
  audioOrdinal: number | null;
}): boolean {
  return !!o.version && !o.failed && (o.audioOrdinal === null || o.audioOrdinal === 0);
}
