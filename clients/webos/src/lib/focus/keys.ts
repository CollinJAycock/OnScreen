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
  // The channel rocker (CH ▲ / CH ▼). Every LG remote has it, the Magic
  // Remote included, which has no ◀◀ ▶▶ (the C1's MR21 doesn't): it is the
  // remote's next / previous. The player steps tracks or chapters with it,
  // Live TV zaps channels.
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
  Backspace: 'back',
  Escape: 'back',
  MediaPlay: 'play',
  MediaPause: 'pause',
  MediaPlayPause: 'playpause',
  MediaStop: 'stop',
  MediaTrackNext: 'forward',
  MediaTrackPrevious: 'rewind',
  MediaFastForward: 'forward',
  MediaRewind: 'rewind',
  // What webOS's Chromium names the CH rocker's key codes (33 / 34).
  PageUp: 'channelUp',
  PageDown: 'channelDown',
  Home: 'home'
};

const BY_CODE: Record<number, RemoteKey> = {
  461: 'back',
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
  403: 'red',
  404: 'green',
  405: 'yellow',
  406: 'blue'
};

export function toRemoteKey(e: KeyboardEvent): RemoteKey | null {
  return BY_KEY[e.key] ?? BY_CODE[e.keyCode] ?? null;
}
