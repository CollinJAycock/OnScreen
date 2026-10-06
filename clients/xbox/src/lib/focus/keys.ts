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
  // Next / previous (the controller's bumpers, RB / LB; a TV remote's
  // channel rocker). The player steps tracks or chapters with it, Live TV
  // zaps channels.
  | 'channelUp'
  | 'channelDown'
  | 'home'
  | 'red'
  | 'green'
  | 'yellow'
  | 'blue';

/**
 * The controller's buttons as Xbox delivers them to a web view: key events
 * with Windows' gamepad virtual-key codes (VK_GAMEPAD_*, 195-218), and the
 * names lib/gamepad gives the same buttons when it turns the Gamepad API
 * into key events (a PC with a controller). B is Back, A is OK. X and Y open
 * the player's Subtitles and Audio pickers (the TV remotes' blue and yellow
 * keys), the bumpers step next / previous (track, chapter, channel), the
 * triggers skip like ◀◀ ▶▶. The left stick moves the focus like the D-pad;
 * the right stick, Menu and View do nothing yet.
 */
export const GAMEPAD: Record<string, { code: number; key: RemoteKey | null }> = {
  GamepadA: { code: 195, key: 'enter' },
  GamepadB: { code: 196, key: 'back' },
  GamepadX: { code: 197, key: 'blue' },
  GamepadY: { code: 198, key: 'yellow' },
  GamepadRightShoulder: { code: 199, key: 'channelUp' },
  GamepadLeftShoulder: { code: 200, key: 'channelDown' },
  GamepadLeftTrigger: { code: 201, key: 'rewind' },
  GamepadRightTrigger: { code: 202, key: 'forward' },
  GamepadDPadUp: { code: 203, key: 'up' },
  GamepadDPadDown: { code: 204, key: 'down' },
  GamepadDPadLeft: { code: 205, key: 'left' },
  GamepadDPadRight: { code: 206, key: 'right' },
  GamepadMenu: { code: 207, key: null },
  GamepadView: { code: 208, key: null },
  GamepadLeftThumbstickButton: { code: 209, key: null },
  GamepadRightThumbstickButton: { code: 210, key: null },
  GamepadLeftThumbstickUp: { code: 211, key: 'up' },
  GamepadLeftThumbstickDown: { code: 212, key: 'down' },
  GamepadLeftThumbstickRight: { code: 213, key: 'right' },
  GamepadLeftThumbstickLeft: { code: 214, key: 'left' },
};

/** A key code in Windows' gamepad range (the controller, not a keyboard). */
export function isGamepadKeyCode(code: number): boolean {
  return code >= 195 && code <= 218;
}

const BY_KEY: Record<string, RemoteKey> = {
  ArrowUp: 'up',
  ArrowDown: 'down',
  ArrowLeft: 'left',
  ArrowRight: 'right',
  Enter: 'enter',
  Backspace: 'back',
  Escape: 'back',
  // The Xbox media remote and a keyboard's media keys.
  MediaPlay: 'play',
  MediaPause: 'pause',
  MediaPlayPause: 'playpause',
  MediaStop: 'stop',
  MediaTrackNext: 'forward',
  MediaTrackPrevious: 'rewind',
  MediaFastForward: 'forward',
  MediaRewind: 'rewind',
  PageUp: 'channelUp',
  PageDown: 'channelDown',
  Home: 'home',
};

const BY_CODE: Record<number, RemoteKey> = {
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
  33: 'channelUp',
  34: 'channelDown',
};

for (const [name, { code, key }] of Object.entries(GAMEPAD)) {
  if (!key) continue;
  BY_KEY[name] = key;
  BY_CODE[code] = key;
}

export function toRemoteKey(e: KeyboardEvent): RemoteKey | null {
  return BY_KEY[e.key] ?? BY_CODE[e.keyCode] ?? null;
}
