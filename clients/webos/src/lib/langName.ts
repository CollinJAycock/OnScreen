// Human names for the language codes on audio and subtitle tracks, and the
// labels the player's pickers show. Mirrors the Android TV client's
// SubtitleLabel (and its audio picker rows), so a track reads the same on
// every TV: "English", "English · Commentary", "Spanish · forced",
// "Portuguese (Brazil) · downloaded".
//
// The pickers used to show the raw code ("jpn · aac · 2ch", "eng · forced"):
// ffprobe reports ISO 639-2 codes, and a downloaded subtitle carries
// OpenSubtitles' tag ("en", "pt-BR"). Android names them through
// java.util.Locale; Chromium 79 has no Intl.DisplayNames (Chrome 81), so the
// names live in a table here, in English (the app's only UI language).
// Anything the table doesn't know shows as the code itself, never as a wrong
// language.
//
// Pure, no $lib imports: unit-tested in langName.test.ts.

// [639-1, 639-2/T, 639-2/B (when it differs), English name]. Every code form
// a track realistically carries: ffprobe writes the B form for some files
// ("fre", "ger", "chi") and the T form for others ("fra", "deu", "zho").
const LANGUAGES: ReadonlyArray<readonly [string, string, string | null, string]> = [
  ['af', 'afr', null, 'Afrikaans'],
  ['am', 'amh', null, 'Amharic'],
  ['ar', 'ara', null, 'Arabic'],
  ['az', 'aze', null, 'Azerbaijani'],
  ['be', 'bel', null, 'Belarusian'],
  ['bg', 'bul', null, 'Bulgarian'],
  ['bn', 'ben', null, 'Bengali'],
  ['bo', 'bod', 'tib', 'Tibetan'],
  ['bs', 'bos', null, 'Bosnian'],
  ['ca', 'cat', null, 'Catalan'],
  ['cs', 'ces', 'cze', 'Czech'],
  ['cy', 'cym', 'wel', 'Welsh'],
  ['da', 'dan', null, 'Danish'],
  ['de', 'deu', 'ger', 'German'],
  ['el', 'ell', 'gre', 'Greek'],
  ['en', 'eng', null, 'English'],
  ['eo', 'epo', null, 'Esperanto'],
  ['es', 'spa', null, 'Spanish'],
  ['et', 'est', null, 'Estonian'],
  ['eu', 'eus', 'baq', 'Basque'],
  ['fa', 'fas', 'per', 'Persian'],
  ['fi', 'fin', null, 'Finnish'],
  ['fo', 'fao', null, 'Faroese'],
  ['fr', 'fra', 'fre', 'French'],
  ['ga', 'gle', null, 'Irish'],
  ['gl', 'glg', null, 'Galician'],
  ['gu', 'guj', null, 'Gujarati'],
  ['he', 'heb', null, 'Hebrew'],
  ['hi', 'hin', null, 'Hindi'],
  ['hr', 'hrv', null, 'Croatian'],
  ['ht', 'hat', null, 'Haitian Creole'],
  ['hu', 'hun', null, 'Hungarian'],
  ['hy', 'hye', 'arm', 'Armenian'],
  ['id', 'ind', null, 'Indonesian'],
  ['is', 'isl', 'ice', 'Icelandic'],
  ['it', 'ita', null, 'Italian'],
  ['ja', 'jpn', null, 'Japanese'],
  ['ka', 'kat', 'geo', 'Georgian'],
  ['kk', 'kaz', null, 'Kazakh'],
  ['km', 'khm', null, 'Khmer'],
  ['kn', 'kan', null, 'Kannada'],
  ['ko', 'kor', null, 'Korean'],
  ['ku', 'kur', null, 'Kurdish'],
  ['ky', 'kir', null, 'Kyrgyz'],
  ['la', 'lat', null, 'Latin'],
  ['lb', 'ltz', null, 'Luxembourgish'],
  ['lo', 'lao', null, 'Lao'],
  ['lt', 'lit', null, 'Lithuanian'],
  ['lv', 'lav', null, 'Latvian'],
  ['mi', 'mri', 'mao', 'Maori'],
  ['mk', 'mkd', 'mac', 'Macedonian'],
  ['ml', 'mal', null, 'Malayalam'],
  ['mn', 'mon', null, 'Mongolian'],
  ['mr', 'mar', null, 'Marathi'],
  ['ms', 'msa', 'may', 'Malay'],
  ['mt', 'mlt', null, 'Maltese'],
  ['my', 'mya', 'bur', 'Burmese'],
  ['nb', 'nob', null, 'Norwegian Bokmål'],
  ['ne', 'nep', null, 'Nepali'],
  ['nl', 'nld', 'dut', 'Dutch'],
  ['nn', 'nno', null, 'Norwegian Nynorsk'],
  ['no', 'nor', null, 'Norwegian'],
  ['pa', 'pan', null, 'Punjabi'],
  ['pl', 'pol', null, 'Polish'],
  ['ps', 'pus', null, 'Pashto'],
  ['pt', 'por', null, 'Portuguese'],
  ['ro', 'ron', 'rum', 'Romanian'],
  ['ru', 'rus', null, 'Russian'],
  ['si', 'sin', null, 'Sinhala'],
  ['sk', 'slk', 'slo', 'Slovak'],
  ['sl', 'slv', null, 'Slovenian'],
  ['so', 'som', null, 'Somali'],
  ['sq', 'sqi', 'alb', 'Albanian'],
  ['sr', 'srp', null, 'Serbian'],
  ['sv', 'swe', null, 'Swedish'],
  ['sw', 'swa', null, 'Swahili'],
  ['ta', 'tam', null, 'Tamil'],
  ['te', 'tel', null, 'Telugu'],
  ['th', 'tha', null, 'Thai'],
  ['tl', 'tgl', null, 'Tagalog'],
  ['tr', 'tur', null, 'Turkish'],
  ['uk', 'ukr', null, 'Ukrainian'],
  ['ur', 'urd', null, 'Urdu'],
  ['uz', 'uzb', null, 'Uzbek'],
  ['vi', 'vie', null, 'Vietnamese'],
  ['xh', 'xho', null, 'Xhosa'],
  ['yi', 'yid', null, 'Yiddish'],
  ['zh', 'zho', 'chi', 'Chinese'],
  ['zu', 'zul', null, 'Zulu'],
];

// Three-letter codes with no two-letter form that still turn up on tracks.
const THREE_LETTER_ONLY: Record<string, string> = {
  fil: 'Filipino',
  yue: 'Cantonese',
  cmn: 'Mandarin',
};

/** Code (any form, lowercased) → its 639-1 primary subtag, or the three-letter
 *  code itself for THREE_LETTER_ONLY. */
const TO_PRIMARY: Record<string, string> = {};
/** Primary subtag → English name. */
const NAMES: Record<string, string> = {};
for (const [one, t, b, name] of LANGUAGES) {
  TO_PRIMARY[one] = one;
  TO_PRIMARY[t] = one;
  if (b) TO_PRIMARY[b] = one;
  NAMES[one] = name;
}
for (const code of Object.keys(THREE_LETTER_ONLY)) {
  TO_PRIMARY[code] = code;
  NAMES[code] = THREE_LETTER_ONLY[code];
}

// The regions subtitle tags carry ("pt-BR", "es-419", "zh-TW"), named as
// java.util.Locale names them in English.
const REGIONS: Record<string, string> = {
  '419': 'Latin America',
  AR: 'Argentina',
  AT: 'Austria',
  AU: 'Australia',
  BE: 'Belgium',
  BR: 'Brazil',
  CA: 'Canada',
  CH: 'Switzerland',
  CL: 'Chile',
  CN: 'China',
  CO: 'Colombia',
  DE: 'Germany',
  DK: 'Denmark',
  ES: 'Spain',
  FI: 'Finland',
  FR: 'France',
  GB: 'United Kingdom',
  HK: 'Hong Kong',
  IE: 'Ireland',
  IN: 'India',
  IT: 'Italy',
  JP: 'Japan',
  KR: 'South Korea',
  LU: 'Luxembourg',
  MO: 'Macao',
  MX: 'Mexico',
  NL: 'Netherlands',
  NO: 'Norway',
  NZ: 'New Zealand',
  PE: 'Peru',
  PL: 'Poland',
  PT: 'Portugal',
  RU: 'Russia',
  SE: 'Sweden',
  SG: 'Singapore',
  TW: 'Taiwan',
  UK: 'United Kingdom',
  US: 'United States',
  VE: 'Venezuela',
  ZA: 'South Africa',
};

// Chinese tracks are often tagged by script rather than region.
const SCRIPTS: Record<string, string> = {
  hans: 'Simplified',
  hant: 'Traditional',
};

/** Codes that name no language: undetermined, multiple, none, missing. */
const NO_LANGUAGE = new Set(['und', 'mul', 'zxx', 'mis']);

/** A language tag's primary subtag, lowercased, as its 639-1 code when it has
 *  one ("ENG" → "en", "fre" → "fr", "pt-BR" → "pt"); unknown codes pass
 *  through ("xyz"). '' for none. */
export function primaryLanguage(code: string | null | undefined): string {
  if (!code) return '';
  const primary = code.trim().toLowerCase().split(/[-_]/)[0];
  return TO_PRIMARY[primary] ?? primary;
}

/**
 * `code`'s language in English: "eng" → "English", "pt-BR" → "Portuguese
 * (Brazil)", "zh-Hant" → "Chinese (Traditional)". A code the table doesn't
 * know comes back as itself (trimmed); null when there is none ("", "und").
 */
export function languageName(code: string | null | undefined): string | null {
  const trimmed = (code ?? '').trim();
  const primary = primaryLanguage(trimmed);
  if (primary === '' || NO_LANGUAGE.has(primary)) return null;
  const name = NAMES[primary];
  if (!name) return trimmed;
  const sub = trimmed.split(/[-_]/)[1] ?? '';
  if (/^([a-z]{2}|\d{3})$/i.test(sub)) {
    const region = sub.toUpperCase();
    return `${name} (${REGIONS[region] ?? region})`;
  }
  const script = SCRIPTS[sub.toLowerCase()];
  return script ? `${name} (${script})` : name;
}

/** Whether `title` only names the language again: its code, its primary
 *  subtag, or its name (with or without the region). */
function repeatsLanguage(title: string, code: string, name: string | null): boolean {
  const t = title.toLowerCase();
  const primary = primaryLanguage(code);
  const candidates = [code.trim(), primary, name, NAMES[primary] ?? null];
  return candidates.some((c) => !!c && c.toLowerCase() === t);
}

const FORCED = /\bforced\b/i;
const SDH = /\b(sdh|cc|hearing[ -]?impaired)\b/i;

export interface SubtitleLabelInput {
  /** The stream's ISO 639-2 code, or a download's tag ("en", "pt-BR"). */
  language: string;
  title?: string | null;
  forced: boolean;
  sdh: boolean;
  /** An external file (downloaded from OpenSubtitles, or OCR'd). */
  downloaded: boolean;
  /** For a track with neither a language nor a title ("Track 3"). */
  fallback: string;
}

/**
 * A subtitle row's name: its language, its title, then whichever of forced /
 * SDH / downloaded the title doesn't already say. A title that only repeats
 * the language is left out, so is a flag the title already carries: the
 * picker used to read "English SDH (SDH)" and "English (Forced) (forced)".
 * Android's SubtitleLabel.of.
 */
export function subtitleLabel(s: SubtitleLabelInput): string {
  const name = languageName(s.language);
  const rawTitle = (s.title ?? '').trim();
  const ownTitle = rawTitle && !repeatsLanguage(rawTitle, s.language, name) ? rawTitle : '';
  const parts: string[] = [];
  if (name) parts.push(name);
  if (ownTitle) parts.push(ownTitle);
  if (parts.length === 0) parts.push(s.fallback);
  if (s.forced && !FORCED.test(ownTitle)) parts.push('forced');
  if (s.sdh && !SDH.test(ownTitle)) parts.push('SDH');
  if (s.downloaded) parts.push('downloaded');
  return parts.join(' · ');
}

/**
 * An audio row: "N. <title, else language, else Track N> · Nch", numbered by
 * its ordinal (the row number IS the track the server maps). Android's audio
 * picker, with the language named rather than shown as its code.
 */
export function audioTrackLabel(
  s: { title?: string | null; language?: string | null; channels?: number | null },
  ordinal: number,
): string {
  const n = ordinal + 1;
  const name = (s.title ?? '').trim() || languageName(s.language) || `Track ${n}`;
  const ch = s.channels && s.channels > 0 ? ` · ${s.channels}ch` : '';
  return `${n}. ${name}${ch}`;
}
