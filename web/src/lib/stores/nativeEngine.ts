import { writable } from 'svelte/store';

/**
 * Per-device opt-in for the Tauri native audio engine.
 *
 * When false (default), the music player uses the browser's
 * `<audio>` element for playback — works everywhere, no surprises.
 * When true *and* the app is running inside the Tauri shell, the
 * AudioPlayer routes track playback through the Rust cpal+claxon
 * pipeline so the FLAC byte stream reaches the audio driver
 * without going through the browser's audio context (the
 * audiophile-pillar path).
 *
 * Persisted to localStorage so the choice survives reloads. Per
 * device on purpose — the same OnScreen account on a phone
 * (browser) and desktop (native) can pick independently.
 *
 * Browser-only builds can read this store too; the toggle just
 * has no effect there because the AudioPlayer's native branch
 * gates on isTauri() before consulting the preference.
 */
const KEY = 'onscreen_native_audio_engine';

function load(): boolean {
  if (typeof localStorage === 'undefined') return false;
  return localStorage.getItem(KEY) === '1';
}

function persist(v: boolean) {
  if (typeof localStorage === 'undefined') return;
  try { localStorage.setItem(KEY, v ? '1' : '0'); } catch { /* private mode */ }
}

function createStore() {
  const { subscribe, set } = writable<boolean>(load());
  return {
    subscribe,
    set: (v: boolean) => { persist(v); set(v); },
    toggle: () => {
      const next = !load();
      persist(next);
      set(next);
    },
  };
}

export const nativeEngine = createStore();
