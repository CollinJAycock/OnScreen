// Preferred-subtitle auto-selection logic, ported from the web client's
// web/src/routes/watch/[id]/subtitle-select.ts (and web/src/lib/
// subtitleMode.ts) so the language matching and the three subtitle modes are
// unit-testable without mounting the HTML5 player. The contract must stay in
// lock-step with the web helper.
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
  /** The file marks the track default (embedded tracks only). */
  isDefault?: boolean;
}

/** When the preferred subtitle language comes on by itself (users.
 *  subtitle_mode, server migration 00066; docs/foreign-audio-subtitles.md):
 *  - always:  whenever the file has it, the full track before a forced one;
 *  - forced:  forced tracks only (foreign-language dialogue);
 *  - foreign: forced while the audio is in that language, the full track
 *             when the audio is in another (known) language. */
export type SubtitleMode = 'always' | 'forced' | 'foreign';

/** The mode a preferences answer means: subtitle_mode, or, from a server
 *  that predates it, forced_subtitles_only (on → forced, off → always). */
export function subtitleModeOf(prefs: {
  subtitle_mode?: string | null;
  forced_subtitles_only?: boolean | null;
}): SubtitleMode {
  const m = prefs.subtitle_mode;
  if (m === 'always' || m === 'forced' || m === 'foreign') return m;
  return prefs.forced_subtitles_only ? 'forced' : 'always';
}

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
//   - No preferred language → the track the FILE marks default, if any (as
//     ExoPlayer does from the same flag, so the Android TV app and this one
//     agree); in the forced and foreign modes only when that track is
//     forced. None → null.
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
    const def = subs.find((s) => s.isDefault);
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

// pickPreferredAudio resolves preferred_audio_lang to the audio track the
// first start should map: the 0-based ORDINAL of the first track in that
// language (what the server's audio_stream_index takes, `-map 0:a:N`), or
// null to leave the track to the server's default. Rules (Android's
// applyPreferredTracks):
//   - No preferred language → null.
//   - The first track whose language matches (normalized as above, so a
//     preference of "fr" or "fre" finds an ffprobe "fra" track); among
//     same-language tracks (5.1 + stereo, commentary) the file's order wins.
//   - No track in the language → null: the server's default track, never an
//     arbitrary other one.
export function pickPreferredAudio(
  streams: readonly { language: string }[],
  preferredLang: string | null | undefined,
): number | null {
  if (!preferredLang) return null;
  const i = streams.findIndex((s) => langMatches(s.language, preferredLang));
  return i >= 0 ? i : null;
}
