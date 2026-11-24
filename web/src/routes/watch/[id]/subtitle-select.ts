// Preferred-subtitle auto-selection logic, extracted from the watch page so
// the language-matching + forced-only rules are unit-testable without
// mounting the player.
//
// Two real-world matching problems this solves that a naive
// `stream.language === pref` misses:
//   1. ffprobe usually reports ISO 639-2/B 3-letter codes ("eng", "spa",
//      "fre") while the preference is often a 2-letter 639-1 code ("en",
//      "es", "fr"). They must be treated as equal.
//   2. Region/script subtags + case ("en-US", "EN", "pt-BR") should match on
//      the primary language subtag.

// Minimal ISO 639-2/B (and a couple of 639-2/T) → 639-1 map covering the
// languages a subtitle track realistically carries. Anything not here falls
// back to a primary-subtag comparison, so an unknown 3-letter code simply
// won't false-match — never throws.
const ISO6392_TO_1: Record<string, string> = {
  eng: 'en', spa: 'es', fre: 'fr', fra: 'fr', ger: 'de', deu: 'de',
  ita: 'it', por: 'pt', rus: 'ru', jpn: 'ja', chi: 'zh', zho: 'zh',
  kor: 'ko', ara: 'ar', dut: 'nl', nld: 'nl', swe: 'sv', nor: 'no',
  dan: 'da', fin: 'fi', pol: 'pl', tur: 'tr', heb: 'he', hin: 'hi',
  tha: 'th', vie: 'vi', ces: 'cs', cze: 'cs', gre: 'el', ell: 'el',
  hun: 'hu', ron: 'ro', rum: 'ro', ukr: 'uk', ind: 'id',
};

// normalizeLang reduces a language tag to a canonical 639-1 primary subtag
// (lowercased). "ENG" → "en", "en-US" → "en", "pt-BR" → "pt", "xyz" → "xyz".
export function normalizeLang(code: string | null | undefined): string {
  if (!code) return '';
  const primary = code.toLowerCase().split(/[-_]/)[0];
  return ISO6392_TO_1[primary] ?? primary;
}

export function langMatches(a: string | null | undefined, b: string | null | undefined): boolean {
  const na = normalizeLang(a);
  return na !== '' && na === normalizeLang(b);
}

export interface SelectableSubtitle {
  language: string;
  forced: boolean;
  /** Captions for the deaf and hard of hearing: a full track picks a plain
   *  one before an SDH one. */
  sdh?: boolean;
  /** The file marks the track default: `default` on an API subtitle stream,
   *  `isDefault` on the TV apps' picker rows. */
  default?: boolean;
  isDefault?: boolean;
}

export { subtitleModeOf, type SubtitleMode } from '$lib/subtitleMode';
import type { SubtitleMode } from '$lib/subtitleMode';

// Tags that name no single language: an untagged or mixed audio track is
// not "foreign" (most untagged files are in the viewer's own language).
const NO_LANGUAGE = new Set(['', 'und', 'mul', 'zxx', 'mis']);

/** The audio is in a known language other than the subtitle language. */
export function isForeignAudio(audioLang: string | null | undefined, subtitleLang: string | null | undefined): boolean {
  const a = normalizeLang(audioLang);
  return !NO_LANGUAGE.has(a) && a !== normalizeLang(subtitleLang);
}

// pickPreferredSubtitle chooses the subtitle the player should auto-enable,
// or null for "leave off". Rules:
//   - No preferred language → the track the file marks default, if any;
//     in the forced and foreign modes only when that track is forced.
//     None → null.
//   - Otherwise, among the tracks in the preferred language:
//       always  → the first full track (plain before SDH), else a forced one;
//       forced  → a forced track, else off;
//       foreign → as "always" when the audio (audioLang, the track that
//                 plays) is in a known other language, else as "forced".
export function pickPreferredSubtitle<T extends SelectableSubtitle>(
  subs: readonly T[],
  preferredLang: string | null | undefined,
  mode: SubtitleMode,
  audioLang?: string | null,
): T | null {
  if (!preferredLang) {
    // No preference: the track the FILE marks default, as ExoPlayer does from
    // the same flag (the Android apps), in the forced modes only when forced.
    const def = subs.find((s) => s.isDefault ?? s.default);
    if (!def) return null;
    return mode !== 'always' && !def.forced ? null : def;
  }
  const inLang = subs.filter((s) => langMatches(s.language, preferredLang));
  if (inLang.length === 0) return null;
  const forced = inLang.find((s) => s.forced) ?? null;
  const full = inLang.find((s) => !s.forced && !s.sdh) ?? inLang.find((s) => !s.forced) ?? null;
  const wantFull = mode === 'always' || (mode === 'foreign' && isForeignAudio(audioLang, preferredLang));
  return wantFull ? (full ?? forced) : forced;
}
