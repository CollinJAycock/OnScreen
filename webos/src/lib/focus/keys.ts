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
  MediaRewind: 'rewind',
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
  403: 'red',
  404: 'green',
  405: 'yellow',
  406: 'blue'
};

export function toRemoteKey(e: KeyboardEvent): RemoteKey | null {
  return BY_KEY[e.key] ?? BY_CODE[e.keyCode] ?? null;
}
