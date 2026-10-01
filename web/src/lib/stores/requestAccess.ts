// The signed-in account's request allowance (GET /requests/quota: can_request
// plus per-type quota), loaded once per sign-in and shared by the sidebar and
// the Requests page so their gates (lib/featureGates) agree. Pages that show
// a live "N requests left" count (Search) still read their own copy after
// each request; this one only answers "may this account request at all".
//
// Under an admin's "view as", the fetch carries view_as like every GET, so it
// reports the viewed user's allowance.

import { get, writable, type Writable } from 'svelte/store';
import { requestsApi, type RequestQuota } from '$lib/api';

/** undefined = not loaded yet; null = the load failed (feature gates read
 *  both as "not allowed" for non-admins). */
export const requestQuota: Writable<RequestQuota | null | undefined> = writable(undefined);

let inFlight: Promise<void> | null = null;
// Bumped by reset so a load that started before a sign-out can't store the
// previous account's answer afterwards.
let generation = 0;

/** Loads the allowance unless it's already known. Concurrent callers share
 *  one request; a failed load (null) is retried on the next call. */
export function loadRequestQuota(): Promise<void> {
  if (inFlight) return inFlight;
  if (get(requestQuota)) return Promise.resolve();
  const gen = generation;
  inFlight = (async () => {
    try {
      const q = (await requestsApi.quota()) ?? null;
      if (gen === generation) requestQuota.set(q);
    } catch {
      if (gen === generation) requestQuota.set(null);
    } finally {
      if (gen === generation) inFlight = null;
    }
  })();
  return inFlight;
}

/** Forgets the allowance — on sign-out or a user switch. */
export function resetRequestQuota(): void {
  generation++;
  inFlight = null;
  requestQuota.set(undefined);
}
