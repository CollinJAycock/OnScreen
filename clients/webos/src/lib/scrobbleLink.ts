// Last.fm / Trakt account linking — polling plus a few labels. The user
// approves OnScreen on the service's own site (on a phone: a TV can't open
// the approval page), so the Scrobbling screen can't be told when that
// happens; it asks the server's "complete" endpoint until the link settles.
// Pure; pollLink is web/src/lib/scrobbleLink.ts verbatim. Kept identical
// between the Tizen and webOS apps.

import type { ScrobbleLinkResult, ScrobbleStatus } from './api/types';

/** How a poll ended. 'expired' also covers giving up at the deadline. */
export type LinkOutcome = Exclude<ScrobbleLinkResult['status'], 'pending' | 'slow_down'>;

export interface PollLinkOptions {
  /** One "complete" call (endpoints.scrobble.completeLastFM / completeTrakt). */
  complete: () => Promise<ScrobbleLinkResult>;
  intervalMs: number;
  /** Stop asking after this long; the link reports 'expired'. */
  timeoutMs: number;
  onDone: (outcome: LinkOutcome, username?: string) => void;
  onError: (err: unknown) => void;
}

/** Trakt's device flow asks clients that poll too fast to add this much. */
export const SLOW_DOWN_STEP_MS = 5_000;

/** Last.fm's request token lives an hour, but nobody takes that long to
 *  approve; stop asking after ten minutes (the web page's numbers). */
export const LASTFM_POLL_MS = 3_000;
export const LASTFM_TIMEOUT_MS = 10 * 60 * 1000;

/**
 * Poll until the link is linked, expired or denied, the deadline passes, or
 * a call fails. The first call waits one interval (the user can't have
 * approved yet). Returns a cancel function; after it, no callback fires.
 */
export function pollLink(opts: PollLinkOptions): () => void {
  let stopped = false;
  let timer: ReturnType<typeof setTimeout> | undefined;
  let interval = opts.intervalMs;
  const deadline = Date.now() + opts.timeoutMs;

  const tick = async () => {
    if (stopped) return;
    if (Date.now() >= deadline) {
      stopped = true;
      opts.onDone('expired');
      return;
    }
    let res: ScrobbleLinkResult;
    try {
      res = await opts.complete();
    } catch (e) {
      if (stopped) return;
      stopped = true;
      opts.onError(e);
      return;
    }
    if (stopped) return;
    if (res.status === 'pending' || res.status === 'slow_down') {
      if (res.status === 'slow_down') interval += SLOW_DOWN_STEP_MS;
      timer = setTimeout(tick, interval);
      return;
    }
    stopped = true;
    opts.onDone(res.status, res.username);
  };

  timer = setTimeout(tick, interval);
  return () => {
    stopped = true;
    if (timer !== undefined) clearTimeout(timer);
  };
}

/** The sentence for a link that didn't finish. */
export function outcomeMessage(service: string, outcome: LinkOutcome): string {
  return outcome === 'denied'
    ? `Access was declined on ${service}.`
    : `The ${service} approval wasn't finished in time. Connect again to retry.`;
}

/** "https://trakt.tv/activate" reads better as "trakt.tv/activate". */
export function bareURL(u: string): string {
  return u.replace(/^https?:\/\//, '').replace(/\/$/, '');
}

/** True when the server knows about Last.fm / Trakt at all (v2.5+). Older
 *  servers omit the fields, and the screen shows ListenBrainz only. */
export function hasExtraScrobblers(s: ScrobbleStatus | null | undefined): boolean {
  return !!s && (s.lastfm_available !== undefined || s.trakt_available !== undefined);
}

/** The Settings row's badge: how many services are linked, or "Off". */
export function scrobbleSummary(s: ScrobbleStatus | null | undefined): { label: string; on: boolean } {
  if (!s) return { label: 'Off', on: false };
  if (!hasExtraScrobblers(s)) {
    if (!s.listenbrainz_linked) return { label: 'Off', on: false };
    return { label: s.listenbrainz_enabled ? 'Linked' : 'Paused', on: true };
  }
  const n = [s.listenbrainz_linked, !!s.lastfm_linked, !!s.trakt_linked].filter(Boolean).length;
  return n === 0 ? { label: 'Off', on: false } : { label: `${n} linked`, on: true };
}
