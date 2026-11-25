package tv.onscreen.android.ui.playback

import tv.onscreen.android.data.model.AudioStream
import tv.onscreen.android.data.model.SubtitleMode
import tv.onscreen.android.data.model.SubtitleStream

// The saved language preferences, resolved against one file: which audio
// track a server session starts with, and which subtitle comes on by itself.
// The rule is the web's (web/src/routes/watch/[id]/subtitle-select.ts and
// web/src/lib/subtitleMode.ts) and the phone app's (SubtitleTracks.preferred);
// docs/foreign-audio-subtitles.md §2.

/**
 * The audio track the preferred language names: the 0-based ORDINAL of the
 * first track in [lang] (what the server's audio_stream_index takes,
 * `-map 0:a:N`), or null — no preference, or no track in it — to leave the
 * track to the server's default, the file's first. Among same-language
 * tracks (5.1 + stereo, commentary) the file's order wins.
 */
fun pickPreferredAudio(streams: List<AudioStream>, lang: String?): Int? {
    if (lang.isNullOrBlank()) return null
    return streams.indexOfFirst { langMatches(it.language, lang) }.takeIf { it >= 0 }
}

// Tags that name no single language: an untagged or mixed audio track is not
// "foreign" (most untagged files are in the viewer's own language).
private val NO_LANGUAGE = setOf("", "und", "mul", "zxx", "mis")

/** The audio is in a known language other than the subtitle language. */
fun isForeignAudio(audioLang: String?, subtitleLang: String?): Boolean {
    val a = SubtitleLabel.normalizeLanguage(audioLang)
    return a !in NO_LANGUAGE && a != SubtitleLabel.normalizeLanguage(subtitleLang)
}

/**
 * The preferred subtitle among [inLang] (the tracks in the preferred
 * language the player can show, in order), or null for none:
 *  - always  → the first full track (plain before SDH), else a forced one;
 *  - forced  → a forced track;
 *  - foreign → as "always" when [audioLang] (the track that plays) is in a
 *              known other language than [subtitleLang], else as "forced".
 */
internal fun <T> pickPreferredSubtitle(
    inLang: List<T>,
    subtitleLang: String,
    mode: SubtitleMode,
    audioLang: String?,
    forced: (T) -> Boolean,
    sdh: (T) -> Boolean,
): T? {
    val forcedTrack = inLang.firstOrNull(forced)
    val full = inLang.firstOrNull { !forced(it) && !sdh(it) } ?: inLang.firstOrNull { !forced(it) }
    val wantFull = mode == SubtitleMode.ALWAYS ||
        (mode == SubtitleMode.FOREIGN && isForeignAudio(audioLang, subtitleLang))
    return if (wantFull) full ?: forcedTrack else forcedTrack
}

/**
 * The saved preference's subtitle, as a SubtitleRow key, or null when there
 * is none in [lang] (subtitles stay off). Selected as itself rather than by
 * language: once the language matches, Media3 ranks a default-flagged track,
 * then a full one, above a forced one (DefaultTrackSelector.TextTrackInfo),
 * so its own matching can't follow the modes.
 *
 * What the player can show: a server session side-loads every text track
 * ("id:"), image-based ones excepted (picking one of those burns it in,
 * never automatic; its OCR'd text version is a side-load like any other).
 * Direct play shows the container's tracks by ordinal ("ord:"), image-based
 * ones included, then the attached files.
 */
internal fun preferredSubtitleChoice(
    sideLoadsAll: Boolean,
    streams: List<SubtitleStream>,
    sources: List<SubtitleTrackSource>,
    lang: String,
    mode: SubtitleMode,
    audioLang: String?,
): String? {
    val candidates: List<Candidate> = if (sideLoadsAll) {
        sources.map { Candidate("id:${it.trackId}", it.language, it.forced, it.sdh) }
    } else {
        streams.mapIndexed { ord, s -> Candidate("ord:$ord", s.language, s.forced, s.sdh) } +
            sources.filter { it.embeddedIndex == null }
                .map { Candidate("id:${it.trackId}", it.language, it.forced, it.sdh) }
    }
    val inLang = candidates.filter { langMatches(it.language, lang) }
    return pickPreferredSubtitle(inLang, lang, mode, audioLang, { it.forced }, { it.sdh })?.key
}

private data class Candidate(val key: String, val language: String, val forced: Boolean, val sdh: Boolean)

/** True when two language tags resolve to the same 639-1 primary subtag
 *  (SubtitleLabel.normalizeLanguage): ffprobe's "eng" and a saved "en" are
 *  the same language. */
internal fun langMatches(a: String?, b: String?): Boolean {
    val na = SubtitleLabel.normalizeLanguage(a)
    return na.isNotEmpty() && na == SubtitleLabel.normalizeLanguage(b)
}
