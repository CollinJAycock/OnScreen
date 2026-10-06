// Settings-page logic that doesn't need the page: the language choices and
// how a stored code finds its row, the language rows' save queue, and the
// quality-profile body the "Forced subtitles only" toggle saves. Pure (the
// requests are injected), so it's unit-tested.

import type { QualityProfileUpdate, UserPreferences } from './api/endpoints';
import { coalesce } from './latest';
import { normalizeLang } from './subtitleSelect';

export interface LangOption {
  value: string;
  label: string;
}

/** "Auto": no preference, the file's default track. */
export const AUTO_LANG = '';
/** Subtitles only: none at all. */
export const SUBS_OFF = 'off';

// ISO 639-2/T codes, the values Android's settings write (fra/deu/zho, not
// the B forms fre/ger/chi this page used to write), in Android's order and
// with its Hindi and Arabic. A value stored as a B code or a two-letter code
// (by this page before, or by another client) still finds its row through
// findLangOption; the next save writes the T code.
export const LANGUAGE_OPTIONS: readonly LangOption[] = [
  { value: AUTO_LANG, label: 'Auto / file default' },
  { value: 'eng', label: 'English' },
  { value: 'spa', label: 'Spanish' },
  { value: 'fra', label: 'French' },
  { value: 'deu', label: 'German' },
  { value: 'ita', label: 'Italian' },
  { value: 'por', label: 'Portuguese' },
  { value: 'jpn', label: 'Japanese' },
  { value: 'kor', label: 'Korean' },
  { value: 'zho', label: 'Chinese' },
  { value: 'rus', label: 'Russian' },
  { value: 'hin', label: 'Hindi' },
  { value: 'ara', label: 'Arabic' },
];

export const SUBTITLE_LANGUAGE_OPTIONS: readonly LangOption[] = [
  { value: SUBS_OFF, label: 'Off' },
  ...LANGUAGE_OPTIONS,
];

/**
 * The option a stored preference belongs to: the exact value first, else
 * the same language by normalizeLang ("fre", "fr" and "fr-CA" are all
 * French). -1 when none matches.
 */
export function findLangOption(options: readonly LangOption[], code: string | null | undefined): number {
  const value = code ?? '';
  const exact = options.findIndex((o) => o.value === value);
  if (exact >= 0 || value === '') return exact;
  const lang = normalizeLang(value);
  return options.findIndex((o) => o.value !== '' && normalizeLang(o.value) === lang);
}

/** The row's label; a set value no option covers shows as the code itself
 *  (Android does the same) rather than claiming "Auto". */
export function langLabel(options: readonly LangOption[], code: string | null | undefined): string {
  const i = findLangOption(options, code);
  if (i >= 0) return options[i].label;
  return code ? code : options[0].label;
}

/** The value after `code` when the row is pressed (wrapping round). A value
 *  no option covers moves to the first option. */
export function nextLangValue(options: readonly LangOption[], code: string | null | undefined): string {
  const i = findLangOption(options, code);
  return options[(i + 1) % options.length].value;
}

/** The two language preferences; '' is "Auto" (no preference). */
export interface LangPair {
  audio: string;
  sub: string;
}

export const LANG_SAVE_FAILED = "Couldn't save the language.";
export const LANG_SAVE_REVERTED = "Couldn't save the language. Showing what the server has.";

/** What languageSaver needs from the page. */
export interface LangSaveIO {
  /** The languages on screen now, i.e. the latest presses. */
  current(): LangPair;
  /** PUT /users/me/preferences with these two languages. */
  put(langs: LangPair): Promise<void>;
  /** The languages the server has stored. */
  read(): Promise<LangPair>;
  /** Puts the server's languages on screen after a failed save. Only the
   *  languages: "Forced subtitles only" saves through another endpoint and
   *  can be mid-save itself, so this path never touches it. */
  show(langs: LangPair): void;
  saving(on: boolean): void;
  /** The language error line; '' clears it. */
  error(text: string): void;
  /** A failure the page handles itself (signed out: off to the login
   *  screen). True stops the run there. */
  fatal(e: unknown): boolean;
}

/**
 * The language rows' save queue: call the returned function on every press.
 * Saves go one at a time, and presses made during one fold into a single
 * follow-up save of the latest values (coalesce), so the server ends on the
 * last choice.
 *
 * A failed save puts up an error that stays until a later save succeeds (a
 * follow-up starting doesn't clear it). It then re-reads what the server
 * kept and shows that, unless the user pressed again meanwhile: that press
 * is already queued and saves the newest choice, while showing the server's
 * older value would make the follow-up save the old value instead (the
 * user's last press lost, with no message once it succeeded). A press made
 * while the re-read is out drops its answer the same way.
 */
export function languageSaver(io: LangSaveIO): () => Promise<void> {
  let presses = 0;
  const run = coalesce(async () => {
    // Runs synchronously up to the PUT, so this is the press count the
    // save below carries.
    const at = presses;
    io.saving(true);
    try {
      await io.put(io.current());
      io.error('');
    } catch (e) {
      if (io.fatal(e)) return;
      io.error(LANG_SAVE_FAILED);
      if (presses !== at) return;
      try {
        const kept = await io.read();
        if (presses !== at) return;
        io.show(kept);
        io.error(LANG_SAVE_REVERTED);
      } catch (e2) {
        // The rows keep the unsaved choice under the plain error.
        io.fatal(e2);
      }
    } finally {
      io.saving(false);
    }
  });
  return () => {
    presses++;
    return run();
  };
}

// The server accepts only these for preferred_video_codec (users.go
// SetQualityProfile answers anything else with a 400).
const VIDEO_CODECS = new Set(['', 'h264', 'hevc']);

function intOrNull(v: number | null | undefined): number | null {
  return typeof v === 'number' && Number.isFinite(v) ? v : null;
}

/**
 * The PUT /users/me/quality-profile body that changes forced_subtitles_only
 * and nothing else. That endpoint replaces the whole profile (a null clears
 * a cap), so the other fields are echoed from `prefs`, a fresh read. A codec
 * the endpoint would refuse (only h264 / hevc pass its check; an older row
 * can hold anything) goes as null, or this toggle could never save.
 */
export function qualityProfileBody(prefs: UserPreferences, forcedOnly: boolean): QualityProfileUpdate {
  const codec = prefs.preferred_video_codec ?? null;
  return {
    max_video_bitrate_kbps: intOrNull(prefs.max_video_bitrate_kbps),
    max_audio_bitrate_kbps: intOrNull(prefs.max_audio_bitrate_kbps),
    max_video_height: intOrNull(prefs.max_video_height),
    preferred_video_codec: codec !== null && VIDEO_CODECS.has(codec) ? codec : null,
    forced_subtitles_only: forcedOnly,
  };
}
