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

/** Removes the stored server URL so the layout's first-run gate
 *  kicks in on next reload. Used by the disconnect flow alongside
 *  clearStoredTokens. */
export async function clearServerUrl(): Promise<void> {
  if (!isTauri()) return;
  const { invoke } = await import('@tauri-apps/api/core');
  await invoke('clear_server_url');
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

/** Stops any currently-playing tone or FLAC stream by dropping
 *  the cpal Stream + signalling the decoder thread to exit. */
export async function stopAudio(): Promise<void> {
  if (!isTauri()) return;
  const { invoke } = await import('@tauri-apps/api/core');
  await invoke('stop_audio');
}

/** Snapshot of what the native engine is doing right now.
 *  `playing` is true while a source is loaded (a paused stream is
 *  still "playing" — paused independently). `ended` is true when the
 *  decoder has hit EOS — the AudioPlayer's polling loop watches
 *  this for auto-advance. `position_ms` is derived from frames
 *  written to the cpal callback (the actual audible position, not
 *  decoder progress). Other fields are null while the engine is
 *  idle. */
export type PlaybackStatus = {
  playing: boolean;
  paused: boolean;
  ended: boolean;
  position_ms: number;
  source_url: string | null;
  sample_rate_hz: number | null;
  bit_depth: number | null;
  channels: number | null;
};

/** Reports the engine's current playback shape. UI uses this to
 *  render "Playing 96 kHz / 24-bit on Topping E30" badges and to
 *  re-sync after a transport that happened outside the UI (e.g.
 *  media keys, future). */
export async function audioState(): Promise<PlaybackStatus> {
  if (!isTauri()) {
    return { playing: false, paused: false, ended: false, position_ms: 0, source_url: null, sample_rate_hz: null, bit_depth: null, channels: null };
  }
  const { invoke } = await import('@tauri-apps/api/core');
  return await invoke<PlaybackStatus>('audio_state');
}

/** Pauses the active stream. cpal callback writes silence; decoder
 *  thread back-pressures itself via the ringbuf so no extra CPU
 *  burns during the pause. No-op when nothing's playing. */
export async function audioPause(): Promise<void> {
  if (!isTauri()) return;
  const { invoke } = await import('@tauri-apps/api/core');
  await invoke('audio_pause');
}

/** Resumes a paused stream. Symmetric with `audioPause`. */
export async function audioResume(): Promise<void> {
  if (!isTauri()) return;
  const { invoke } = await import('@tauri-apps/api/core');
  await invoke('audio_resume');
}

/** Streams a FLAC file from the OnScreen server through the native
 *  engine. `bearerToken` is sent as `Authorization: Bearer …` so the
 *  server's auth middleware accepts the request. Replaces any
 *  currently-playing track. Returns the engine's status snapshot
 *  after playback has *started* (cpal stream running, decoder thread
 *  producing samples) — errors thrown synchronously cover the
 *  pre-audio failure paths (HTTP 4xx/5xx, FLAC parse, device pick).
 *
 *  **Gapless fast-path:** if the matching URL was previously prepared
 *  via [`audioPreloadUrl`], promotion skips the HTTP + FLAC-header
 *  round-trip — the decoder thread is already producing samples,
 *  and only the cpal device-activation cost remains.
 *
 *  Currently FLAC-only. Other formats (MP3, ALAC, transcoded HLS)
 *  fall through to the existing `<audio>` element in the webview.
 */
export async function audioPlayUrl(
  url: string,
  bearerToken: string | null,
  device: string | null,
): Promise<PlaybackStatus> {
  if (!isTauri()) {
    throw new Error('audioPlayUrl is only meaningful inside the native client');
  }
  const { invoke } = await import('@tauri-apps/api/core');
  return await invoke<PlaybackStatus>('audio_play_url', {
    url,
    bearerToken,
    deviceName: device,
  });
}

/** Optimistically prepare the next track so [`audioPlayUrl`] with
 *  the same URL completes near-instantly (gapless transition). The
 *  decoder thread runs in the background filling a ringbuf; idempotent
 *  per URL — calling repeatedly with the same URL is a no-op after
 *  the first prepare. Calling with a different URL drops the prior
 *  preload and starts fresh. Errors silently — preload is a perf
 *  optimisation, not a correctness requirement, so failures should
 *  not surface to the user; the play_url cold-start path covers them.
 */
export async function audioPreloadUrl(
  url: string,
  bearerToken: string | null,
): Promise<void> {
  if (!isTauri()) return;
  try {
    const { invoke } = await import('@tauri-apps/api/core');
    await invoke('audio_preload_url', { url, bearerToken });
  } catch (e) {
    console.debug('native engine preload failed (non-fatal):', e);
  }
}
