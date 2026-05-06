// Subtitle styling preferences for the in-page WebVTT renderer.
//
// Lives next to the watch page so the player can import the helper
// directly. Pure module — no DOM, no Svelte deps — so the load /
// save / CSS-string emit can be unit-tested without mounting the
// player.
//
// Design notes:
//   - Persisted to localStorage under a single key so we can grow the
//     shape without juggling per-field keys.
//   - load() merges a saved partial blob with defaults so a v1 install
//     that only ever stored `size` upgrades cleanly when the user lands
//     on a build that knows about color / background / outline.
//   - Invalid values (operator typo, hand-edited localStorage, schema
//     drift) fall back to the default for that field — never throw.
//     The renderer has to keep working under any input.

export type SubtitleSize = 'small' | 'medium' | 'large';
export type SubtitleColor = 'white' | 'yellow' | 'black' | 'red';
export type SubtitleBackground = 'none' | 'translucent' | 'opaque';
export type SubtitleOutline = 'none' | 'light' | 'heavy';

export interface SubtitleStyle {
  size: SubtitleSize;
  color: SubtitleColor;
  background: SubtitleBackground;
  outline: SubtitleOutline;
}

export const DEFAULT_SUBTITLE_STYLE: SubtitleStyle = {
  size: 'medium',
  color: 'white',
  background: 'translucent',
  outline: 'light',
};

const SIZES: readonly SubtitleSize[] = ['small', 'medium', 'large'];
const COLORS: readonly SubtitleColor[] = ['white', 'yellow', 'black', 'red'];
const BACKGROUNDS: readonly SubtitleBackground[] = ['none', 'translucent', 'opaque'];
const OUTLINES: readonly SubtitleOutline[] = ['none', 'light', 'heavy'];

const STORAGE_KEY = 'subtitle_style';
// Legacy key from the v1 size-only build. We migrate it transparently
// on first load so users don't lose their picked size.
const LEGACY_SIZE_KEY = 'subtitle_size';

function isOneOf<T extends string>(set: readonly T[], v: unknown): v is T {
  return typeof v === 'string' && (set as readonly string[]).includes(v);
}

// loadSubtitleStyle pulls saved prefs from localStorage and merges with
// defaults. Falls back per-field when a value is invalid or missing —
// never throws. Pass a custom storage object for tests.
export function loadSubtitleStyle(storage?: Pick<Storage, 'getItem'>): SubtitleStyle {
  const s = storage ?? (typeof localStorage !== 'undefined' ? localStorage : undefined);
  if (!s) return { ...DEFAULT_SUBTITLE_STYLE };

  // Try the new combined blob first.
  const raw = s.getItem(STORAGE_KEY);
  let parsed: Partial<SubtitleStyle> = {};
  if (raw) {
    try {
      const obj = JSON.parse(raw);
      if (obj && typeof obj === 'object') parsed = obj as Partial<SubtitleStyle>;
    } catch {
      // Corrupt JSON — fall through to defaults. Don't surface the
      // parse error to the user; subtitles still render, just with
      // default styling, which is the right failure mode.
    }
  }

  // Migration: if the new blob has no size, fall back to the legacy
  // single-field key so users upgrading from a size-only build keep
  // their preference instead of getting reset to medium.
  let size: SubtitleSize = isOneOf(SIZES, parsed.size)
    ? parsed.size
    : DEFAULT_SUBTITLE_STYLE.size;
  if (!isOneOf(SIZES, parsed.size)) {
    const legacy = s.getItem(LEGACY_SIZE_KEY);
    if (isOneOf(SIZES, legacy)) size = legacy;
  }

  return {
    size,
    color: isOneOf(COLORS, parsed.color) ? parsed.color : DEFAULT_SUBTITLE_STYLE.color,
    background: isOneOf(BACKGROUNDS, parsed.background)
      ? parsed.background
      : DEFAULT_SUBTITLE_STYLE.background,
    outline: isOneOf(OUTLINES, parsed.outline)
      ? parsed.outline
      : DEFAULT_SUBTITLE_STYLE.outline,
  };
}

// saveSubtitleStyle writes the full prefs blob. Errors (quota / private
// mode) are swallowed — the in-memory state still drives the next
// render, the user just doesn't get persistence. Same posture as the
// rest of the player.
export function saveSubtitleStyle(
  style: SubtitleStyle,
  storage?: Pick<Storage, 'setItem'>,
): void {
  const s = storage ?? (typeof localStorage !== 'undefined' ? localStorage : undefined);
  if (!s) return;
  try {
    s.setItem(STORAGE_KEY, JSON.stringify(style));
  } catch {
    // Private-browsing / quota exceeded. Swallow.
  }
}

// Tailwind-free CSS values per token. Kept inline so the helper has
// no styling-system dependency and the test can compare strings.
const SIZE_PX: Record<SubtitleSize, string> = {
  small: '1rem',
  medium: '1.4rem',
  large: '2rem',
};

const COLOR_HEX: Record<SubtitleColor, string> = {
  white: '#ffffff',
  yellow: '#ffeb3b',
  black: '#000000',
  red: '#ff5252',
};

// Background opacity tiers. 'none' clears the background entirely so
// just the outline carries the cue; 'opaque' is for high-contrast
// accessibility (e.g. light backgrounds where translucent leaks).
const BACKGROUND_CSS: Record<SubtitleBackground, string> = {
  none: 'transparent',
  translucent: 'rgba(0, 0, 0, 0.75)',
  opaque: 'rgba(0, 0, 0, 1)',
};

// Outline as text-shadow stack. 'heavy' uses 4 directions to keep the
// cue legible against busy backgrounds; 'light' keeps the v1 single-
// shadow look from the original .subtitle-cue rule.
const OUTLINE_CSS: Record<SubtitleOutline, string> = {
  none: 'none',
  light: '1px 1px 2px rgba(0,0,0,0.8)',
  heavy:
    '-1px -1px 0 #000, 1px -1px 0 #000, -1px 1px 0 #000, 1px 1px 0 #000, 0 0 4px rgba(0,0,0,0.9)',
};

// subtitleCueStyle emits an inline-style string for the cue span. The
// renderer applies it via `style={subtitleCueStyle(prefs)}` so the
// preference change reflects immediately without a page-level CSS
// rebuild. Return value is the property list only (no leading/trailing
// `style="..."` wrapper) so it can be passed to Svelte's `style=`
// directive directly.
export function subtitleCueStyle(s: SubtitleStyle): string {
  return [
    `font-size: ${SIZE_PX[s.size]}`,
    `color: ${COLOR_HEX[s.color]}`,
    `background: ${BACKGROUND_CSS[s.background]}`,
    `text-shadow: ${OUTLINE_CSS[s.outline]}`,
  ].join('; ');
}
