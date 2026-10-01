// Capabilities store. Fetches /system/capabilities once and caches it
// in memory so feature-gated UI (e.g. the watch-page Download button)
// can read a sync value instead of doing a fetch on every render.
//
// The capabilities endpoint is public and stable across a server's
// lifetime — admin toggles update it but only on the next reload, and
// that's fine for our use case (toggle-and-refresh).

import { get, writable, type Writable } from 'svelte/store';
import { capabilitiesApi, type CapabilitiesResponse } from '$lib/api';

export const capabilities: Writable<CapabilitiesResponse | null> = writable(null);

let inFlight: Promise<void> | null = null;

/** Resolves once capabilities are known (or the fetch failed): at once when
 *  already loaded, else with the load the layout started (or a new one).
 *  For a page that must decide a gate before acting, e.g. whether to ask
 *  TMDB at all. */
export function ensureCapabilities(): Promise<void> {
  if (get(capabilities)) return Promise.resolve();
  return loadCapabilities();
}

export async function loadCapabilities(): Promise<void> {
  if (inFlight) return inFlight;
  inFlight = (async () => {
    try {
      const c = await capabilitiesApi.get();
      capabilities.set(c);
    } catch {
      // Capabilities is best-effort — a fetch failure leaves the
      // store at null and feature-gated UI falls back to whatever
      // its `null` branch defaults to (typically "off").
    } finally {
      inFlight = null;
    }
  })();
  return inFlight;
}
