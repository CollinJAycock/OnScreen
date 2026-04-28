// Tizen TV remote → semantic key mapping. Same exported shape as
// the webOS scaffold so the focus manager + spatial-nav code is
// drop-in identical; only the underlying keyCode integers differ.
//
// Values come from Samsung's TV Web App Programming Guide
// (developer.samsung.com → "TV remote control"). Tizen forwards
// remote presses as KeyboardEvents on the document; .key is set
// to a stringy name on most keys, .keyCode is set to the VK_*
// integer for the rest.
//
// The hardware-key forwarding requires `hwkey-event="enable"` in
// config.xml — without it, Back/Exit go straight to the launcher
// and the app never sees them.

export type RemoteKey =
  | 'up'
  | 'down'
  | 'left'
  | 'right'
  | 'enter'
  | 'back'
  | 'play'
  | 'pause'
  | 'playpause'
  | 'stop'
  | 'forward'
  | 'rewind'
  | 'home'
  | 'red'
  | 'green'
  | 'yellow'
  | 'blue';

const BY_KEY: Record<string, RemoteKey> = {
  ArrowUp: 'up',
  ArrowDown: 'down',
  ArrowLeft: 'left',
  ArrowRight: 'right',
  Enter: 'enter',
  Backspace: 'back',
  Escape: 'back',
  MediaPlay: 'play',
  MediaPause: 'pause',
  MediaPlayPause: 'playpause',
  MediaStop: 'stop',
  MediaTrackNext: 'forward',
  MediaTrackPrevious: 'rewind',
  MediaFastForward: 'forward',
  MediaRewind: 'rewind'
};

// Tizen TV VK_* keycode constants. Samsung publishes these as
// `tizen.tvinputdevice.*` integers; the values are stable across
// firmware revisions.
const BY_CODE: Record<number, RemoteKey> = {
  // D-pad
  37: 'left', // VK_LEFT
  38: 'up', // VK_UP
  39: 'right', // VK_RIGHT
  40: 'down', // VK_DOWN
  13: 'enter', // VK_ENTER
  10009: 'back', // VK_BACK / Return key on Samsung remotes

  // Media transport
  415: 'play', // VK_MEDIA_PLAY
  19: 'pause', // VK_MEDIA_PAUSE
  10252: 'playpause', // VK_MEDIA_PLAY_PAUSE
  413: 'stop', // VK_MEDIA_STOP
  417: 'forward', // VK_MEDIA_FAST_FORWARD
  412: 'rewind', // VK_MEDIA_REWIND

  // Coloured A/B/C/D buttons (some remotes label them ABCD, some
  // RGBY — same scancodes either way).
  403: 'red', // VK_COLOR_F0_RED
  404: 'green', // VK_COLOR_F1_GREEN
  405: 'yellow', // VK_COLOR_F2_YELLOW
  406: 'blue', // VK_COLOR_F3_BLUE

  // VK_HOME doesn't reach the app on Tizen — the launcher
  // intercepts it. Listed here for documentation; will never fire.
  10071: 'home'
};

export function toRemoteKey(e: KeyboardEvent): RemoteKey | null {
  return BY_KEY[e.key] ?? BY_CODE[e.keyCode] ?? null;
}

/** Register the extra Tizen remote keys we want to receive
 *  (Back, MediaPlay, MediaPause, etc.) with the firmware so they
 *  forward into the webview as KeyboardEvents. Without this only
 *  the always-on D-pad + Enter come through.
 *
 *  Call once at app boot (root layout's onMount). No-op outside
 *  the Tizen webview. */
export function registerTizenKeys(): void {
  if (typeof window === 'undefined') return;
  const tizen = (window as Window & { tizen?: { tvinputdevice?: TvInputDevice } }).tizen;
  if (!tizen?.tvinputdevice) return;
  const wanted = [
    'MediaPlay',
    'MediaPause',
    'MediaPlayPause',
    'MediaStop',
    'MediaFastForward',
    'MediaRewind',
    'ColorF0Red',
    'ColorF1Green',
    'ColorF2Yellow',
    'ColorF3Blue'
  ];
  try {
    tizen.tvinputdevice.registerKeyBatch(wanted);
  } catch {
    // Older firmware may not have all keys; ignore — registered
    // ones still take effect.
  }
}

/** Minimal type for the Tizen keys we actually call. The full
 *  surface lives in @types/samsung-tizen-tv but we don't need
 *  the dependency for one method. */
interface TvInputDevice {
  registerKeyBatch(keys: string[]): void;
  unregisterKey?(key: string): void;
}
