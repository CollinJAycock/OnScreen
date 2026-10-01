package tv.onscreen.android.ui.playback

import java.util.Locale

/**
 * The name a subtitle goes by in the picker: its language as the device names
 * it, its title, then whichever of forced / SDH / downloaded the title doesn't
 * already say: "English", "English · Commentary", "Spanish · forced",
 * "Portuguese (Brazil) · downloaded".
 *
 * The picker used to show the title, else the raw code: "English", "eng",
 * "English SDH (SDH)", "English (Forced) (forced)". A title that only repeats
 * the language is left out, and so is a flag the title already carries. Pure;
 * JVM-tested in SubtitleLabelTest.
 */
object SubtitleLabel {

    /**
     * [language] is a stream's ISO 639-2 code (ffprobe's "eng") or a download's
     * tag ("en", "pt-BR"); [fallback] names a track with neither a language
     * nor a title ("Track 3").
     */
    fun of(
        language: String,
        title: String?,
        forced: Boolean,
        sdh: Boolean,
        downloaded: Boolean,
        fallback: String,
        locale: Locale = Locale.getDefault(),
    ): String {
        val name = languageName(language, locale)
        val ownTitle = title?.trim().orEmpty()
            .takeUnless { it.isEmpty() || repeatsLanguage(it, language, name, locale) }
        val parts = listOfNotNull(name, ownTitle).toMutableList()
        if (parts.isEmpty()) parts += fallback
        val said = ownTitle.orEmpty()
        if (forced && !FORCED.containsMatchIn(said)) parts += "forced"
        if (sdh && !SDH.containsMatchIn(said)) parts += "SDH"
        if (downloaded) parts += "downloaded"
        return parts.joinToString(" · ")
    }

    /** [code]'s language as [locale] names it ("eng" → "English", "pt-BR" →
     *  "Portuguese (Brazil)"); the code itself when the device doesn't know
     *  it; null when there is none ("", "und"). */
    internal fun languageName(code: String, locale: Locale): String? {
        val trimmed = code.trim()
        val primary = normalizeLanguage(trimmed)
        if (primary.isEmpty() || primary in NO_LANGUAGE) return null
        val region = trimmed.split('-', '_').getOrNull(1)?.takeIf { it.length == 2 }?.uppercase()
        val name = Locale.forLanguageTag(if (region != null) "$primary-$region" else primary).getDisplayName(locale)
        // An unknown code comes back as itself (or as nothing).
        if (name.isEmpty() || name.equals(primary, ignoreCase = true)) return trimmed
        return name.replaceFirstChar { it.titlecase(locale) }
    }

    /** Whether [title] only names the language again: its code, or the
     *  language's name ([name], or plain, in the device's language or in
     *  English). */
    private fun repeatsLanguage(title: String, code: String, name: String?, locale: Locale): Boolean {
        val primary = normalizeLanguage(code)
        val plain = Locale.forLanguageTag(primary)
        return listOfNotNull(
            code.trim(),
            primary,
            name,
            plain.getDisplayLanguage(locale),
            plain.getDisplayLanguage(Locale.ENGLISH),
        ).any { it.isNotEmpty() && it.equals(title, ignoreCase = true) }
    }

    // Minimal ISO 639-2/B (and a couple of 639-2/T) → 639-1 map, mirroring
    // the web client's normalizeLang. ffprobe usually reports 3-letter codes
    // ("eng", "spa") while the saved subtitle preference is a 2-letter 639-1
    // code ("en", "es"), and Locale names the 639-1 ones. Anything not here
    // falls back to its primary subtag, so an unknown code simply won't
    // false-match.
    private val ISO6392_TO_1: Map<String, String> = mapOf(
        "eng" to "en", "spa" to "es", "fre" to "fr", "fra" to "fr", "ger" to "de",
        "deu" to "de", "ita" to "it", "por" to "pt", "rus" to "ru", "jpn" to "ja",
        "chi" to "zh", "zho" to "zh", "kor" to "ko", "ara" to "ar", "dut" to "nl",
        "nld" to "nl", "swe" to "sv", "nor" to "no", "dan" to "da", "fin" to "fi",
        "pol" to "pl", "tur" to "tr", "heb" to "he", "hin" to "hi", "tha" to "th",
        "vie" to "vi", "ces" to "cs", "cze" to "cs", "gre" to "el", "ell" to "el",
        "hun" to "hu", "ron" to "ro", "rum" to "ro", "ukr" to "uk", "ind" to "id",
    )

    /** Codes that name no language: undetermined, multiple, none. */
    private val NO_LANGUAGE = setOf("und", "mul", "zxx", "mis")

    private val FORCED = Regex("""\bforced\b""", RegexOption.IGNORE_CASE)
    private val SDH = Regex("""\b(sdh|cc|hearing[ -]?impaired)\b""", RegexOption.IGNORE_CASE)

    /** Reduce a language tag to a canonical 639-1 primary subtag (lowercased).
     *  "ENG" → "en", "en-US" → "en", "xyz" → "xyz". */
    fun normalizeLanguage(code: String?): String {
        if (code.isNullOrEmpty()) return ""
        val primary = code.trim().lowercase().split('-', '_').first()
        return ISO6392_TO_1[primary] ?: primary
    }
}
