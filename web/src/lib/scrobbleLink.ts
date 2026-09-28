// Polling for the Last.fm / Trakt account-link flows. The user approves
// OnScreen on the service's own site (another tab, or another device for
// Trakt's code), so the settings page can't be told when that happens — it
// asks the server's "complete" endpoint until the link settles.

import type { ScrobbleLinkResult } from '$lib/api';

/** How a poll ended. 'expired' also covers giving up at the deadline. */
export type LinkOutcome = Exclude<ScrobbleLinkResult['status'], 'pending' | 'slow_down'>;

export interface PollLinkOptions {
  /** One "complete" call (scrobbleApi.completeLastFM / completeTrakt). */
  complete: () => Promise<ScrobbleLinkResult>;
  intervalMs: number;
  /** Stop asking after this long; the link reports 'expired'. */
  timeoutMs: number;
  onDone: (outcome: LinkOutcome, username?: string) => void;
  onError: (err: unknown) => void;
}

/** Trakt's device flow asks clients that poll too fast to add this much. */
export const SLOW_DOWN_STEP_MS = 5_000;

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
