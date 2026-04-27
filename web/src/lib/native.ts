/**
 * Tauri (native desktop client) detection + IPC shims.
 *
 * The browser bundle and the native bundle ship from the same
 * SvelteKit codebase — `web/dist` is what `clients/desktop` loads
 * into its webview. Anything that's only meaningful inside the
 * native shell goes through this module so the import is a no-op
 * in the browser (Tauri APIs are dynamically imported behind the
 * `isTauri()` guard, so they never reach the browser bundle).
 */

declare global {
  interface Window {
    /**
     * Tauri 2 injects __TAURI_INTERNALS__ on the webview's window
     * before the SvelteKit hydration runs. We probe it rather than
     * the older `__TAURI__` global because the latter requires
     * `withGlobalTauri: true` in tauri.conf.json — opt-in legacy.
     */
    __TAURI_INTERNALS__?: unknown;
  }
}

/** True when the current page is running inside the Tauri webview. */
export function isTauri(): boolean {
  return typeof window !== 'undefined' && window.__TAURI_INTERNALS__ !== undefined;
}

/**
 * Resolves to the OnScreen server URL the user picked at first
 * launch, or null when the URL hasn't been set yet (the layout
 * uses null to gate the first-run setup screen). Returns null in
 * the browser since `isTauri()` short-circuits.
 *
 * The Tauri @tauri-apps/api package is dynamically imported so
 * Vite tree-shakes it out of the browser bundle — this module
 * stays cheap to load even on the web.
 */
export async function getServerUrl(): Promise<string | null> {
  if (!isTauri()) return null;
  const { invoke } = await import('@tauri-apps/api/core');
  return (await invoke<string | null>('get_server_url')) ?? null;
}

/**
 * Persists the server URL via the Rust side, which validates the
 * URL is parseable + http(s) before writing. Throws on validation
 * failure so the setup screen can surface the error inline rather
 * than persisting a bad value the user then has to re-fix.
 */
export async function setServerUrl(url: string): Promise<void> {
  if (!isTauri()) {
    throw new Error('setServerUrl is only meaningful inside the native client');
  }
  const { invoke } = await import('@tauri-apps/api/core');
  await invoke('set_server_url', { url });
}

/**
 * Tokens persisted by the Tauri shell. Both fields are present
 * after a successful login or refresh; both are null when the user
 * hasn't authenticated yet (or after clearTokens() — i.e. logout).
 *
 * The fetch wrapper consumes these to attach `Authorization: Bearer
 * <access_token>` on every request when running natively. Cookies
 * don't survive cross-origin from the Tauri webview to a plain-http
 * server, which is the most common dev/home-install shape — bearer
 * is the only viable path there.
 */
export type StoredTokens = {
  access_token: string | null;
  refresh_token: string | null;
};

export async function getStoredTokens(): Promise<StoredTokens> {
  if (!isTauri()) return { access_token: null, refresh_token: null };
  const { invoke } = await import('@tauri-apps/api/core');
  return await invoke<StoredTokens>('get_tokens');
}

export async function setStoredTokens(access: string, refresh: string): Promise<void> {
  if (!isTauri()) return;
  const { invoke } = await import('@tauri-apps/api/core');
  await invoke('set_tokens', { access, refresh });
}

export async function clearStoredTokens(): Promise<void> {
  if (!isTauri()) return;
  const { invoke } = await import('@tauri-apps/api/core');
  await invoke('clear_tokens');
}

// ── Native audio engine ────────────────────────────────────────────────────
// Foundations only in this layer — device enumeration + a test-tone
// path that proves the cpal output stack works on the user's box.
// Full FLAC streaming + bit-perfect transport land in subsequent
// commits on top of these primitives.

export type AudioDevice = {
  name: string;
  is_default: boolean;
  default_output_summary: string | null;
};

/** Lists every audio output device cpal can see. Only meaningful in
 *  Tauri — returns [] in the browser. */
export async function listAudioDevices(): Promise<AudioDevice[]> {
  if (!isTauri()) return [];
  const { invoke } = await import('@tauri-apps/api/core');
  return await invoke<AudioDevice[]>('list_audio_devices');
}

/** Plays a sine wave on the named device (or the host default when
 *  null) for `durationMs`. Used by the desktop client's audio
 *  diagnostic page to verify the output path works end-to-end before
 *  the user trusts the engine with their library. */
export async function playTestTone(
  device: string | null,
  frequencyHz: number,
  durationMs: number,
): Promise<void> {
  if (!isTauri()) {
    throw new Error('playTestTone is only meaningful inside the native client');
  }
  const { invoke } = await import('@tauri-apps/api/core');
  await invoke('play_test_tone', {
    deviceName: device,
    frequencyHz,
    durationMs,
  });
}

/** Stops any currently-playing tone (or, eventually, the live FLAC
 *  stream) by dropping the cpal Stream the engine holds. */
export async function stopAudio(): Promise<void> {
  if (!isTauri()) return;
  const { invoke } = await import('@tauri-apps/api/core');
  await invoke('stop_audio');
}
