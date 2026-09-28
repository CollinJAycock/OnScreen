import { writable } from 'svelte/store';
import { clampPreamp, type ReplayGainMode } from '$lib/replaygain';

/** Per-device ReplayGain preference. Shared by the browser audio path
 *  (Web Audio gain in AudioPlayer) and the desktop native engine: the
 *  localStorage keys are the ones the /native/audio page has always
 *  written, so a desktop user's existing choice carries over and both
 *  paths read one setting. */
export interface ReplayGainSettings {
  mode: ReplayGainMode;
  preampDb: number;
}

export const RG_MODE_KEY = 'onscreen_native_rg_mode';
export const RG_PREAMP_KEY = 'onscreen_native_rg_preamp';

const DEFAULTS: ReplayGainSettings = { mode: 'off', preampDb: 0 };

function load(): ReplayGainSettings {
  try {
    const m = localStorage.getItem(RG_MODE_KEY);
    const mode: ReplayGainMode = m === 'track' || m === 'album' ? m : 'off';
    const p = parseFloat(localStorage.getItem(RG_PREAMP_KEY) ?? '');
    return { mode, preampDb: Number.isFinite(p) ? clampPreamp(p) : 0 };
  } catch {
    // SSR / storage blocked (private mode, sandboxed iframe).
    return { ...DEFAULTS };
  }
}

// Re-read storage whenever the store gains its first subscriber, so a
// value written by another surface (or a test) is picked up without a
// reload.
export const replayGainSettings = writable<ReplayGainSettings>({ ...DEFAULTS }, (set) => {
  set(load());
});

function persist(key: string, value: string) {
  try {
    localStorage.setItem(key, value);
  } catch {
    /* storage blocked — the in-memory store still applies this session */
  }
}

export function setReplayGainMode(mode: ReplayGainMode): void {
  persist(RG_MODE_KEY, mode);
  replayGainSettings.update((s) => ({ ...s, mode }));
}

export function setReplayGainPreamp(db: number): number {
  const clamped = clampPreamp(db);
  persist(RG_PREAMP_KEY, String(clamped));
  replayGainSettings.update((s) => ({ ...s, preampDb: clamped }));
  return clamped;
}
