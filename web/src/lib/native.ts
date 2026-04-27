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
