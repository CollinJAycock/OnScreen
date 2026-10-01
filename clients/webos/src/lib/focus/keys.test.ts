import { describe, expect, it } from 'vitest';
import { toRemoteKey } from './keys';

// Only key and keyCode are read: webOS's Chromium sends both for most keys,
// some remotes and firmwares only the code (key 'Unidentified').
function ev(key: string, keyCode: number): KeyboardEvent {
  return { key, keyCode } as KeyboardEvent;
}

describe('toRemoteKey', () => {
  it('maps the CH rocker (the Magic Remote has no ◀◀ ▶▶)', () => {
    expect(toRemoteKey(ev('PageUp', 33))).toBe('channelUp');
    expect(toRemoteKey(ev('PageDown', 34))).toBe('channelDown');
  });

  it('maps CH by its code alone', () => {
    expect(toRemoteKey(ev('Unidentified', 33))).toBe('channelUp');
    expect(toRemoteKey(ev('Unidentified', 34))).toBe('channelDown');
  });

  it('keeps ◀◀ ▶▶ apart from CH', () => {
    expect(toRemoteKey(ev('MediaFastForward', 417))).toBe('forward');
    expect(toRemoteKey(ev('MediaRewind', 412))).toBe('rewind');
    expect(toRemoteKey(ev('Unidentified', 417))).toBe('forward');
    expect(toRemoteKey(ev('Unidentified', 412))).toBe('rewind');
  });

  it('maps the d-pad, OK and the webOS Back code', () => {
    expect(toRemoteKey(ev('ArrowUp', 38))).toBe('up');
    expect(toRemoteKey(ev('Enter', 13))).toBe('enter');
    expect(toRemoteKey(ev('Unidentified', 461))).toBe('back');
  });

  it('ignores keys the app has no use for', () => {
    expect(toRemoteKey(ev('a', 65))).toBeNull();
    expect(toRemoteKey(ev('Unidentified', 0))).toBeNull();
  });
});
