package tv.onscreen.mobile.data.model

/**
 * When the preferred subtitle language comes on by itself (users.
 * subtitle_mode, server migration 00066; docs/foreign-audio-subtitles.md):
 *  - [ALWAYS]:  whenever the file has it, the full track before a forced one;
 *  - [FORCED]:  forced tracks only (foreign-language dialogue);
 *  - [FOREIGN]: forced while the audio is in that language, the full track
 *               when the audio is in another (known) language.
 * The web's web/src/lib/subtitleMode.ts; [wire] is the server's value.
 */
enum class SubtitleMode(val wire: String) {
    ALWAYS("always"),
    FORCED("forced"),
    FOREIGN("foreign"),
    ;

    companion object {
        /** The mode a preferences answer means: subtitle_mode, or, from a
         *  server that predates it, forced_subtitles_only (on → forced, off →
         *  always). */
        fun of(prefs: UserPreferences): SubtitleMode =
            entries.firstOrNull { it.wire == prefs.subtitle_mode }
                ?: if (prefs.forced_subtitles_only) FORCED else ALWAYS
    }
}
