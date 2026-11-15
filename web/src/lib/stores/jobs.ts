// Polling store for /api/v1/jobs — surfaces in-flight scans + items
// missing art / unmatched. JobsBanner reads this; the store handles
// the polling interval and stops cleanly when the user logs out.
//
// Polls every 30s; a single scan completes in seconds for small libs
// or 30+ minutes for large ones, so 30s gives the banner enough
// freshness without hammering the API. A hidden tab skips its ticks and
// polls once as soon as it is shown again.

import { writable, type Writable } from 'svelte/store';
import { jobsApi, type JobsStatus } from '$lib/api';

const POLL_MS = 30_000;

export const jobsStatus: Writable<JobsStatus | null> = writable(null);

let timer: ReturnType<typeof setInterval> | null = null;
let inFlight = false;
// A tick was skipped while the tab was hidden.
let missed = false;

function hidden(): boolean {
  return typeof document !== 'undefined' && document.visibilityState === 'hidden';
}

function onVisibility() {
  if (timer !== null && missed && !hidden()) {
    missed = false;
    void tick();
  }
}

async function tick() {
  if (hidden()) { missed = true; return; }
  if (inFlight) return; // never overlap polls
  inFlight = true;
  try {
    const s = await jobsApi.get();
    jobsStatus.set(s);
  } catch {
    // Auth lapse / network blip / 5xx — leave the previous value in
    // place rather than blanking the banner. Next tick retries.
  } finally {
    inFlight = false;
  }
}

export function startJobsPolling() {
  if (timer !== null) return;
  timer = setInterval(tick, POLL_MS);
  if (typeof document !== 'undefined') document.addEventListener('visibilitychange', onVisibility);
  void tick();
}

export function stopJobsPolling() {
  if (timer !== null) {
    clearInterval(timer);
    timer = null;
  }
  if (typeof document !== 'undefined') document.removeEventListener('visibilitychange', onVisibility);
  missed = false;
  jobsStatus.set(null);
}
