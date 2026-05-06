// Unit tests for the subtitle-style helper. Pure module — no DOM, no
// Svelte mount. Covers:
//   - load() returns defaults when storage empty
//   - load() returns defaults on corrupt JSON
//   - load() ignores unknown values per-field (never throws)
//   - load() migrates the legacy single-key `subtitle_size` form
//   - save() writes the combined blob
//   - subtitleCueStyle() emits the right CSS tokens for each preference

import { describe, it, expect } from 'vitest';
import {
  loadSubtitleStyle,
  saveSubtitleStyle,
  subtitleCueStyle,
  DEFAULT_SUBTITLE_STYLE,
  type SubtitleStyle,
} from './subtitle-style';

// In-memory storage stub — no jsdom localStorage required. Shape
// matches the slice of Storage the helper actually touches.
function memStorage(initial: Record<string, string> = {}) {
  const data = new Map(Object.entries(initial));
  return {
    getItem: (k: string) => data.get(k) ?? null,
    setItem: (k: string, v: string) => {
      data.set(k, v);
    },
    snapshot: () => Object.fromEntries(data),
  };
}

describe('loadSubtitleStyle', () => {
  it('returns defaults when storage is empty', () => {
    const got = loadSubtitleStyle(memStorage());
    expect(got).toEqual(DEFAULT_SUBTITLE_STYLE);
  });

  it('returns defaults on corrupt JSON', () => {
    const got = loadSubtitleStyle(memStorage({ subtitle_style: 'not-json' }));
    expect(got).toEqual(DEFAULT_SUBTITLE_STYLE);
  });

  it('returns defaults when the saved blob is not an object', () => {
    // JSON.parse("42") is valid — produces a number — but our shape
    // requires an object. Falling back here keeps an attacker (or a
    // typo) from corrupting the renderer's input.
    const got = loadSubtitleStyle(memStorage({ subtitle_style: '42' }));
    expect(got).toEqual(DEFAULT_SUBTITLE_STYLE);
  });

  it('keeps valid fields and falls back per-field on invalid ones', () => {
    const got = loadSubtitleStyle(
      memStorage({
        subtitle_style: JSON.stringify({
          size: 'large',
          color: 'bogus', // invalid → default
          background: 'opaque',
          outline: 'huge', // invalid → default
        }),
      }),
    );
    expect(got).toEqual({
      size: 'large',
      color: DEFAULT_SUBTITLE_STYLE.color,
      background: 'opaque',
      outline: DEFAULT_SUBTITLE_STYLE.outline,
    });
  });

  it('migrates the legacy single-key `subtitle_size` form', () => {
    // User upgrading from a build that only stored size — they should
    // keep their picked size, not get silently reset to medium.
    const got = loadSubtitleStyle(memStorage({ subtitle_size: 'large' }));
    expect(got.size).toBe('large');
    // Other fields fall back to defaults — the legacy key only carried
    // size, the rest weren't preferenced yet.
    expect(got.color).toBe(DEFAULT_SUBTITLE_STYLE.color);
  });

  it('prefers the new combined blob over the legacy size key', () => {
    const got = loadSubtitleStyle(
      memStorage({
        subtitle_style: JSON.stringify({ size: 'small' }),
        subtitle_size: 'large', // legacy — should be ignored
      }),
    );
    expect(got.size).toBe('small');
  });

  it('falls back to legacy key when the new blob has an invalid size', () => {
    const got = loadSubtitleStyle(
      memStorage({
        subtitle_style: JSON.stringify({ size: 'gigantic' }),
        subtitle_size: 'large',
      }),
    );
    expect(got.size).toBe('large');
  });

  it('ignores a non-string legacy key', () => {
    // Defensive: someone hand-edited localStorage to put an object in
    // the legacy slot. Don't trust it — fall through to default.
    const got = loadSubtitleStyle(memStorage({ subtitle_size: '{}' }));
    expect(got.size).toBe(DEFAULT_SUBTITLE_STYLE.size);
  });
});

describe('saveSubtitleStyle', () => {
  it('writes the full blob as JSON under the combined key', () => {
    const store = memStorage();
    const style: SubtitleStyle = {
      size: 'small',
      color: 'yellow',
      background: 'none',
      outline: 'heavy',
    };
    saveSubtitleStyle(style, store);
    const written = JSON.parse(store.snapshot().subtitle_style);
    expect(written).toEqual(style);
  });

  it('round-trips through save → load', () => {
    const store = memStorage();
    const style: SubtitleStyle = {
      size: 'large',
      color: 'red',
      background: 'opaque',
      outline: 'heavy',
    };
    saveSubtitleStyle(style, store);
    expect(loadSubtitleStyle(store)).toEqual(style);
  });

  it('does not throw when setItem fails (quota / private mode)', () => {
    const throwing = {
      setItem: () => {
        throw new Error('QuotaExceededError');
      },
    };
    expect(() => saveSubtitleStyle(DEFAULT_SUBTITLE_STYLE, throwing)).not.toThrow();
  });
});

describe('subtitleCueStyle', () => {
  it('emits the small / medium / large font-size tokens', () => {
    const small = subtitleCueStyle({ ...DEFAULT_SUBTITLE_STYLE, size: 'small' });
    const med = subtitleCueStyle({ ...DEFAULT_SUBTITLE_STYLE, size: 'medium' });
    const large = subtitleCueStyle({ ...DEFAULT_SUBTITLE_STYLE, size: 'large' });
    expect(small).toContain('font-size: 1rem');
    expect(med).toContain('font-size: 1.4rem');
    expect(large).toContain('font-size: 2rem');
  });

  it('emits the right color hex per token', () => {
    expect(subtitleCueStyle({ ...DEFAULT_SUBTITLE_STYLE, color: 'white' })).toContain(
      'color: #ffffff',
    );
    expect(subtitleCueStyle({ ...DEFAULT_SUBTITLE_STYLE, color: 'yellow' })).toContain(
      'color: #ffeb3b',
    );
    expect(subtitleCueStyle({ ...DEFAULT_SUBTITLE_STYLE, color: 'black' })).toContain(
      'color: #000000',
    );
    expect(subtitleCueStyle({ ...DEFAULT_SUBTITLE_STYLE, color: 'red' })).toContain(
      'color: #ff5252',
    );
  });

  it('emits transparent / translucent / opaque backgrounds', () => {
    expect(
      subtitleCueStyle({ ...DEFAULT_SUBTITLE_STYLE, background: 'none' }),
    ).toContain('background: transparent');
    expect(
      subtitleCueStyle({ ...DEFAULT_SUBTITLE_STYLE, background: 'translucent' }),
    ).toContain('background: rgba(0, 0, 0, 0.75)');
    expect(
      subtitleCueStyle({ ...DEFAULT_SUBTITLE_STYLE, background: 'opaque' }),
    ).toContain('background: rgba(0, 0, 0, 1)');
  });

  it('emits text-shadow=none / single-shadow / multi-shadow per outline', () => {
    expect(subtitleCueStyle({ ...DEFAULT_SUBTITLE_STYLE, outline: 'none' })).toContain(
      'text-shadow: none',
    );
    const light = subtitleCueStyle({ ...DEFAULT_SUBTITLE_STYLE, outline: 'light' });
    expect(light).toMatch(/text-shadow:.*1px 1px 2px/);
    const heavy = subtitleCueStyle({ ...DEFAULT_SUBTITLE_STYLE, outline: 'heavy' });
    // Heavy stacks four directions — assert four "px 0 #000" hits.
    expect(heavy.match(/px 0 #000/g)?.length ?? 0).toBeGreaterThanOrEqual(4);
  });

  it('joins all four properties with semicolons (valid inline style)', () => {
    const got = subtitleCueStyle(DEFAULT_SUBTITLE_STYLE);
    // 4 properties → 3 joining semicolons.
    expect(got.split(';').length).toBe(4);
    expect(got).toContain('font-size:');
    expect(got).toContain('color:');
    expect(got).toContain('background:');
    expect(got).toContain('text-shadow:');
  });
});
