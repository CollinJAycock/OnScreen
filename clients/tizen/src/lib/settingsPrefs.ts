// Settings-page logic that doesn't need the page: the language choices and
// how a stored code finds its row, the language rows' save queue, and the
// quality profile ("Playback quality" and "Forced subtitles only"): its
// choices, its body and its save queue. Pure (the requests are injected), so
// it's unit-tested.

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
 * The PUT /users/me/quality-profile body: `changes` over the profile in
 * `prefs`, a fresh read. That endpoint replaces the whole profile (a null
 * clears a cap), so every field it doesn't change is echoed. A codec the
 * endpoint would refuse (only h264 / hevc pass its check; an older row can
 * hold anything) goes as null, or nothing on the page could ever save.
 */
export function qualityProfileUpdate(
  prefs: UserPreferences,
  changes: Partial<QualityProfileUpdate> = {}
): QualityProfileUpdate {
  const codec = prefs.preferred_video_codec ?? null;
  const body: QualityProfileUpdate = {
    max_video_bitrate_kbps: intOrNull(prefs.max_video_bitrate_kbps),
    max_audio_bitrate_kbps: intOrNull(prefs.max_audio_bitrate_kbps),
    max_video_height: intOrNull(prefs.max_video_height),
    preferred_video_codec: codec !== null && VIDEO_CODECS.has(codec) ? codec : null,
    forced_subtitles_only: !!prefs.forced_subtitles_only,
  };
  return { ...body, ...changes };
}

/** The body that changes forced_subtitles_only and nothing else. */
export function qualityProfileBody(prefs: UserPreferences, forcedOnly: boolean): QualityProfileUpdate {
  return qualityProfileUpdate(prefs, { forced_subtitles_only: forcedOnly });
}

// ── Playback quality ─────────────────────────────────────────────────
//
// The user's own quality profile, which the server enforces on every device
// (the play decision transcodes anything above it). The choices match the
// web app's Account > Playback quality.

/** The profile's caps, as the Settings rows edit them. */
export type QualityField =
  | 'max_video_height'
  | 'max_video_bitrate_kbps'
  | 'max_audio_bitrate_kbps'
  | 'preferred_video_codec';

/** One choice; null = no limit / automatic. */
export interface QualityOption {
  value: number | string | null;
  label: string;
}

export interface QualityRow {
  field: QualityField;
  title: string;
  options: readonly QualityOption[];
}

export const QUALITY_ROWS: readonly QualityRow[] = [
  {
    field: 'max_video_height',
    title: 'Maximum resolution',
    options: [
      { value: null, label: 'Original' },
      { value: 2160, label: '4K' },
      { value: 1080, label: '1080p' },
      { value: 720, label: '720p' },
      { value: 480, label: '480p' },
    ],
  },
  {
    field: 'max_video_bitrate_kbps',
    title: 'Maximum video bitrate',
    options: [
      { value: null, label: 'No limit' },
      ...[40, 20, 12, 8, 4, 2, 1].map((m) => ({ value: m * 1000, label: `${m} Mbps` })),
    ],
  },
  {
    field: 'max_audio_bitrate_kbps',
    title: 'Maximum audio bitrate',
    options: [
      { value: null, label: 'Original' },
      ...[320, 256, 192, 128, 96].map((k) => ({ value: k, label: `${k} kbps` })),
    ],
  },
  {
    field: 'preferred_video_codec',
    title: 'Video format when converting',
    options: [
      { value: null, label: 'Automatic' },
      { value: 'h264', label: 'H.264 (plays everywhere)' },
      { value: 'hevc', label: 'HEVC (smaller, where supported)' },
    ],
  },
];

/** A field's stored value as the rows read it: null for no limit (a 0 or
 *  an empty codec included). */
export function qualityValue(
  profile: Partial<Pick<UserPreferences, QualityField>>,
  field: QualityField
): number | string | null {
  const v = profile[field];
  if (v == null || v === '' || v === 0) return null;
  return v;
}

/** What a value no option covers (set through the API or another client)
 *  reads as, rather than "no limit". */
function storedLabel(field: QualityField, v: number | string): string {
  if (typeof v === 'string') return v;
  switch (field) {
    case 'max_video_height':
      return `${v}p`;
    case 'max_video_bitrate_kbps':
      return `${Math.round(v / 100) / 10} Mbps`;
    default:
      return `${v} kbps`;
  }
}

/** A row's choices: its options, plus the stored value when no option
 *  covers it (so it shows, and picking it again keeps it). */
export function qualityOptions(row: QualityRow, value: number | string | null): QualityOption[] {
  if (value === null || row.options.some((o) => o.value === value)) return [...row.options];
  return [...row.options, { value, label: storedLabel(row.field, value) }];
}

/** A row's current label. */
export function qualityLabel(row: QualityRow, value: number | string | null): string {
  const opts = qualityOptions(row, value);
  return (opts.find((o) => o.value === value) ?? opts[0]).label;
}

export const QUALITY_SAVE_FAILED = "Couldn't save the playback setting.";
export const QUALITY_SAVE_INVALID = "The server didn't accept that setting.";

/** What qualitySaver needs from the page. */
export interface QualitySaveIO {
  /** GET /users/me/preferences. */
  read(): Promise<UserPreferences>;
  /** PUT /users/me/quality-profile. */
  put(body: QualityProfileUpdate): Promise<void>;
  /** What the server holds after a save (or the read before a refused
   *  one): the rows show it, apart from the fields in `pending`, which
   *  later presses changed and a save is still to carry. */
  show(stored: QualityProfileUpdate, pending: ReadonlySet<keyof QualityProfileUpdate>): void;
  saving(on: boolean): void;
  /** The error line; '' clears it. */
  error(text: string): void;
  /** A failure the page handles itself (signed out). True stops there. */
  fatal(e: unknown): boolean;
}

/**
 * The quality profile's save queue: call the returned function with each
 * change (a quality row's pick, the forced-subtitles toggle). The endpoint
 * replaces the whole profile, so every save reads the profile first and
 * sends it back with the changes made since the last save over it: fields
 * nobody touched here keep what the server has (another device may have
 * changed them since the page opened). Saves go one at a time, and changes
 * made during one ride the next (coalesce).
 *
 * A refused save (400 = invalid) or a failed one puts the server's values
 * back on the rows it carried, unless a later press changed them again.
 * A save whose read failed keeps its changes for the next one.
 */
export function qualitySaver(io: QualitySaveIO): (change: Partial<QualityProfileUpdate>) => Promise<void> {
  const pending: Partial<QualityProfileUpdate> = {};
  const pendingKeys = () => new Set(Object.keys(pending) as (keyof QualityProfileUpdate)[]);
  // Drop what a save carried, unless pressed again since.
  const settle = (sent: Partial<QualityProfileUpdate>) => {
    for (const k of Object.keys(sent) as (keyof QualityProfileUpdate)[]) {
      if (pending[k] === sent[k]) delete pending[k];
    }
  };
  const run = coalesce(async () => {
    if (Object.keys(pending).length === 0) return;
    const sent = { ...pending };
    let fresh: UserPreferences | null = null;
    io.saving(true);
    try {
      fresh = await io.read();
      const body = qualityProfileUpdate(fresh, sent);
      await io.put(body);
      settle(sent);
      io.error('');
      io.show(body, pendingKeys());
    } catch (e) {
      if (io.fatal(e)) return;
      const status = (e as { status?: unknown } | null)?.status;
      io.error(status === 400 ? QUALITY_SAVE_INVALID : QUALITY_SAVE_FAILED);
      if (fresh) {
        settle(sent);
        io.show(qualityProfileUpdate(fresh), pendingKeys());
      }
    } finally {
      io.saving(false);
    }
  });
  return (change) => {
    Object.assign(pending, change);
    return run();
  };
}
