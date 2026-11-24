/** When the preferred subtitle language comes on by itself (users.
 *  subtitle_mode, migration 00066; docs/foreign-audio-subtitles.md):
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
