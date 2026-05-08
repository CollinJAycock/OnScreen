// Polling store for /api/v1/jobs — surfaces in-flight scans + items
// missing art / unmatched. JobsBanner reads this; the store handles
// the polling interval and stops cleanly when the user logs out.
//
// Polls every 30s; a single scan completes in seconds for small libs
// or 30+ minutes for large ones, so 30s gives the banner enough
// freshness without hammering the API.

import { writable, type Writable } from 'svelte/store';
import { jobsApi, type JobsStatus } from '$lib/api';

const POLL_MS = 30_000;

export const jobsStatus: Writable<JobsStatus | null> = writable(null);

let timer: ReturnType<typeof setInterval> | null = null;
let inFlight = false;

async function tick() {
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
  void tick();
  timer = setInterval(tick, POLL_MS);
}

export function stopJobsPolling() {
  if (timer !== null) {
    clearInterval(timer);
    timer = null;
  }
  jobsStatus.set(null);
}
