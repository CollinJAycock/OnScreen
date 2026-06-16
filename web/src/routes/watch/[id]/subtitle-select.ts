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
}

// pickPreferredSubtitle chooses the subtitle the player should auto-enable,
// or null for "leave off". Rules:
//   - No preferred language → no auto-selection (null), regardless of forcedOnly.
//   - forcedOnly: only ever auto-enable a FORCED track in the preferred
//     language (the "show foreign-dialogue subtitles but not full captions"
//     setting). If none exists, stay off.
//   - otherwise: prefer a forced track in the language if present (forced is
//     the more specific intent), else the first full track in the language.
export function pickPreferredSubtitle<T extends SelectableSubtitle>(
  subs: readonly T[],
  preferredLang: string | null | undefined,
  forcedOnly: boolean,
): T | null {
  if (!preferredLang) return null;
  const inLang = subs.filter((s) => langMatches(s.language, preferredLang));
  if (inLang.length === 0) return null;
  const forced = inLang.find((s) => s.forced);
  if (forcedOnly) return forced ?? null;
  return forced ?? inLang[0];
}
