import { describe, expect, it } from 'vitest';
import appinfo from '../appinfo.json';

// LG's appinfo.json reference: `icon` is an 80x80 PNG and `largeIcon` a
// 130x130 PNG. Seller Lounge QA also rejects a tile colour (`iconColor`) that
// differs from the icon's own background, which is black for this art.

// The project has no Node typings, so node:fs comes in untyped.
const fsModule = 'node:fs';
const { readFileSync } = (await import(/* @vite-ignore */ fsModule)) as {
  readFileSync(path: URL): Uint8Array;
};

/** Width and height from a PNG's IHDR chunk. */
function pngSize(file: string): { width: number; height: number } {
  const b = readFileSync(new URL(`../${file}`, import.meta.url));
  const u32 = (at: number) =>
    ((b[at] << 24) | (b[at + 1] << 16) | (b[at + 2] << 8) | b[at + 3]) >>> 0;
  expect(String.fromCharCode(b[1], b[2], b[3])).toBe('PNG');
  return { width: u32(16), height: u32(20) };
}

describe('appinfo icons (LG spec)', () => {
  it('icon is an 80x80 PNG', () => {
    expect(pngSize(appinfo.icon)).toEqual({ width: 80, height: 80 });
  });

  it('largeIcon is a 130x130 PNG', () => {
    expect(pngSize(appinfo.largeIcon)).toEqual({ width: 130, height: 130 });
  });

  it('the tile colour matches the icon background', () => {
    expect(appinfo.iconColor.toLowerCase()).toBe('#000000');
  });
});
