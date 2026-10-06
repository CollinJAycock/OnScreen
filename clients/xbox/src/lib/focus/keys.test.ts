import { describe, expect, it } from 'vitest';
import { GAMEPAD, isGamepadKeyCode, toRemoteKey } from './keys';

// Only key and keyCode are read: Xbox delivers the controller as Windows'
// gamepad key codes (key may be 'Unidentified'); lib/gamepad's synthetic
// events carry the button's name and no code.
function ev(key: string, keyCode: number): KeyboardEvent {
  return { key, keyCode } as KeyboardEvent;
}

describe('toRemoteKey', () => {
  it("maps Xbox's gamepad key codes: A is OK, B is Back, D-pad and left stick move", () => {
    expect(toRemoteKey(ev('Unidentified', 195))).toBe('enter');
    expect(toRemoteKey(ev('Unidentified', 196))).toBe('back');
    expect(toRemoteKey(ev('Unidentified', 203))).toBe('up');
    expect(toRemoteKey(ev('Unidentified', 206))).toBe('right');
    expect(toRemoteKey(ev('Unidentified', 211))).toBe('up');
    expect(toRemoteKey(ev('Unidentified', 214))).toBe('left');
  });

  it('maps the same buttons by name (lib/gamepad on a PC)', () => {
    expect(toRemoteKey(ev('GamepadA', 0))).toBe('enter');
    expect(toRemoteKey(ev('GamepadB', 0))).toBe('back');
    expect(toRemoteKey(ev('GamepadDPadDown', 0))).toBe('down');
  });

  it('gives X / Y the pickers, the bumpers next / previous and the triggers skip', () => {
    expect(toRemoteKey(ev('Unidentified', 197))).toBe('blue');
    expect(toRemoteKey(ev('Unidentified', 198))).toBe('yellow');
    expect(toRemoteKey(ev('Unidentified', 199))).toBe('channelUp');
    expect(toRemoteKey(ev('Unidentified', 200))).toBe('channelDown');
    expect(toRemoteKey(ev('Unidentified', 201))).toBe('rewind');
    expect(toRemoteKey(ev('Unidentified', 202))).toBe('forward');
  });

  it('leaves Menu, View and the right stick unmapped', () => {
    for (const code of [207, 208, 209, 210, 215, 216, 217, 218]) expect(toRemoteKey(ev('Unidentified', code))).toBeNull();
  });

  it('maps a keyboard and the media keys', () => {
    expect(toRemoteKey(ev('ArrowUp', 38))).toBe('up');
    expect(toRemoteKey(ev('Enter', 13))).toBe('enter');
    expect(toRemoteKey(ev('Escape', 27))).toBe('back');
    expect(toRemoteKey(ev('MediaPlayPause', 179))).toBe('playpause');
    expect(toRemoteKey(ev('MediaFastForward', 417))).toBe('forward');
  });

  it('ignores keys the app has no use for', () => {
    expect(toRemoteKey(ev('a', 65))).toBeNull();
    expect(toRemoteKey(ev('Unidentified', 0))).toBeNull();
  });
});

describe('the gamepad table', () => {
  it('covers 195-214 in order, all inside the gamepad range', () => {
    const codes = Object.values(GAMEPAD).map((g) => g.code);
    expect(codes).toEqual(Array.from({ length: 20 }, (_, i) => 195 + i));
    expect(codes.every(isGamepadKeyCode)).toBe(true);
    expect(isGamepadKeyCode(194)).toBe(false);
    expect(isGamepadKeyCode(219)).toBe(false);
  });
});
