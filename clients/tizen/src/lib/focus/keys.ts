// Samsung TV remote → semantic keys. The same RemoteKey names as the webOS
// app, so the focus manager, the player and every page are shared; only the
// codes differ (Samsung's TV Web App guide, "Remote Control": the VK_*
// values tizen.tvinputdevice reports).
//
// Return (Back) reaches the app only with `hwkey-event="enable"` in
// config.xml; the media keys, the colour keys and the channel rocker only
// once registered (registerTizenKeys, at boot). Exit and Smart Hub stay the
// system's: Exit closes the app, Smart Hub sends it to the background.

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
  // The channel rocker (CH ▲ / CH ▼). Samsung's Smart Remote has one but no
  // ◀◀ ▶▶ and no colour keys: it is the remote's next / previous. The player
  // steps tracks or chapters with it, Live TV zaps channels.
  | 'channelUp'
  | 'channelDown'
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
  // A desktop browser's keys for Back (vite dev, the emulator).
  Backspace: 'back',
  Escape: 'back',
  XF86Back: 'back',
  MediaPlay: 'play',
  MediaPause: 'pause',
  MediaPlayPause: 'playpause',
  MediaStop: 'stop',
  MediaTrackNext: 'forward',
  MediaTrackPrevious: 'rewind',
  MediaFastForward: 'forward',
  MediaRewind: 'rewind',
  ChannelUp: 'channelUp',
  ChannelDown: 'channelDown',
  // A desktop keyboard's stand-ins for the rocker.
  PageUp: 'channelUp',
  PageDown: 'channelDown'
};

const BY_CODE: Record<number, RemoteKey> = {
  10009: 'back', // Return
  13: 'enter',
  37: 'left',
  38: 'up',
  39: 'right',
  40: 'down',
  415: 'play',
  19: 'pause',
  10252: 'playpause',
  413: 'stop',
  417: 'forward',
  412: 'rewind',
  427: 'channelUp',
  428: 'channelDown',
  33: 'channelUp',
  34: 'channelDown',
  403: 'red',
  404: 'green',
  405: 'yellow',
  406: 'blue'
};

export function toRemoteKey(e: KeyboardEvent): RemoteKey | null {
  return BY_KEY[e.key] ?? BY_CODE[e.keyCode] ?? null;
}

/** The keys the app asks the TV for: without registering them they never
 *  reach the page (the TV keeps the channel rocker for its tuner, for one).
 *  The arrows, OK and Return always come. */
export const TIZEN_KEYS = [
  'MediaPlay',
  'MediaPause',
  'MediaPlayPause',
  'MediaStop',
  'MediaFastForward',
  'MediaRewind',
  'ChannelUp',
  'ChannelDown',
  'ColorF0Red',
  'ColorF1Green',
  'ColorF2Yellow',
  'ColorF3Blue'
] as const;

interface TvInputDevice {
  registerKeyBatch?(keys: string[]): void;
  registerKey?(key: string): void;
}

/** Register TIZEN_KEYS with the TV (once, at boot). A key this model's
 *  remote lacks fails alone: the batch call throws on the first unknown
 *  name on some firmware, so each is registered by itself when it does.
 *  No-op outside a Tizen webview (vite dev). */
export function registerTizenKeys(
  device: TvInputDevice | undefined = (globalThis as { tizen?: { tvinputdevice?: TvInputDevice } }).tizen
    ?.tvinputdevice,
): void {
  if (!device) return;
  try {
    device.registerKeyBatch?.([...TIZEN_KEYS]);
    if (device.registerKeyBatch) return;
  } catch {
    // One by one below.
  }
  for (const k of TIZEN_KEYS) {
    try {
      device.registerKey?.(k);
    } catch {
      // Not on this remote.
    }
  }
}
