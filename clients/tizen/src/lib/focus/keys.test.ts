import { describe, expect, it, vi } from 'vitest';
import { TIZEN_KEYS, registerTizenKeys, toRemoteKey } from './keys';

// Only key and keyCode are read: Tizen's webview sends the VK_* code for the
// remote's keys, often with key 'Unidentified'.
function ev(key: string, keyCode: number): KeyboardEvent {
  return { key, keyCode } as KeyboardEvent;
}

describe('toRemoteKey', () => {
  it('maps Return, the d-pad and OK', () => {
    expect(toRemoteKey(ev('Unidentified', 10009))).toBe('back');
    expect(toRemoteKey(ev('XF86Back', 10009))).toBe('back');
    expect(toRemoteKey(ev('ArrowUp', 38))).toBe('up');
    expect(toRemoteKey(ev('Enter', 13))).toBe('enter');
  });

  it('maps the channel rocker (the Smart Remote has no ◀◀ ▶▶)', () => {
    expect(toRemoteKey(ev('ChannelUp', 427))).toBe('channelUp');
    expect(toRemoteKey(ev('ChannelDown', 428))).toBe('channelDown');
    expect(toRemoteKey(ev('Unidentified', 427))).toBe('channelUp');
    expect(toRemoteKey(ev('Unidentified', 428))).toBe('channelDown');
  });

  it('keeps ◀◀ ▶▶ apart from CH, and maps the colour keys', () => {
    expect(toRemoteKey(ev('Unidentified', 417))).toBe('forward');
    expect(toRemoteKey(ev('Unidentified', 412))).toBe('rewind');
    expect(toRemoteKey(ev('Unidentified', 403))).toBe('red');
    expect(toRemoteKey(ev('Unidentified', 406))).toBe('blue');
  });

  it('ignores keys the app has no use for', () => {
    expect(toRemoteKey(ev('a', 65))).toBeNull();
    expect(toRemoteKey(ev('Unidentified', 0))).toBeNull();
  });
});

describe('registerTizenKeys', () => {
  it('asks for the media, colour and channel keys in one batch', () => {
    const registerKeyBatch = vi.fn();
    registerTizenKeys({ registerKeyBatch });
    expect(registerKeyBatch).toHaveBeenCalledWith([...TIZEN_KEYS]);
    expect(TIZEN_KEYS).toContain('ChannelUp');
    expect(TIZEN_KEYS).toContain('ChannelDown');
  });

  it('registers one by one when the batch throws, skipping keys the remote lacks', () => {
    const registerKey = vi.fn((k: string) => {
      if (k === 'ColorF0Red') throw new Error('not supported');
    });
    registerTizenKeys({ registerKeyBatch: () => { throw new Error('batch'); }, registerKey });
    expect(registerKey).toHaveBeenCalledTimes(TIZEN_KEYS.length);
  });

  it('does nothing outside a Tizen webview', () => {
    expect(() => registerTizenKeys(undefined)).not.toThrow();
  });
});
