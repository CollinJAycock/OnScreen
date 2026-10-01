package tv.onscreen.mobile.ui.player

import tv.onscreen.mobile.data.model.ItemFile
import tv.onscreen.mobile.playback.StreamTokenVault

/**
 * One subtitle the player offers: an embedded stream of the file, or a
 * subtitle file attached to it on the server (a "Find more online…" download).
 *
 * @property trackId stable id: `sub:emb:{absoluteStreamIndex}` for an embedded
 *   stream, `sub:ext:{id}` for an attached file. A side-loaded track carries it
 *   as its Format id, so the picker selects and recognises THIS track rather
 *   than "a track in this language" (see [SubtitleTracks.sideLoadIdOf]).
 * @property url the server's WebVTT rendition of the track, clean: the
 *   credential is in [StreamTokenVault] and the player's resolving data source
 *   re-attaches it, like the direct-play url. Null when the track can't be
 *   side-loaded — an image-based stream (PGS / VobSub / DVB, which the server
 *   refuses to serve as text), or nothing to authenticate the request with.
 */
data class SubtitleTrack(
    val trackId: String,
    val language: String,
    val title: String,
    val forced: Boolean,
    val sdh: Boolean,
    /** An attached file rather than one of the container's streams. */
    val external: Boolean,
    val url: String?,
)

/**
 * Which subtitles the player offers and loads, and how a player track maps
 * back to one of them.
 *
 * A server HLS session (remux / transcode) carries NO text streams — the
 * server maps video and one audio stream, and serves subtitles as separate
 * WebVTT files — so on those paths the player has to side-load every subtitle
 * itself or the picker has nothing to select. Direct play reads the
 * container's own streams (image-based ones included), so only the attached
 * files are side-loaded there; side-loading the embedded ones too would list
 * every track twice. Same model as the TV client's PlaybackFragment.
 */
object SubtitleTracks {

    /** Marks our side-load ids inside a (possibly prefixed) Format id. */
    private const val ID_MARKER = "sub:"

    /**
     * Every subtitle [file] has, embedded streams first (in the file's order)
     * then attached files, with their side-load urls registered in
     * [StreamTokenVault]. [serverUrl] empty (or no token) builds the list
     * without urls — enough to show and pick container tracks.
     *
     * Embedded: `/media/subtitles/{fileId}/{absoluteIndex}` — the ABSOLUTE
     * ffprobe stream index, that endpoint's convention. It takes the file's
     * own stream token, else the asset token. Built only when
     * [sideLoadEmbedded] (an HLS session): direct play reads those streams
     * from the container, and registering a url per stream for nothing would
     * crowd the vault's bounded map — a disc rip can carry dozens.
     * Attached: `/media/external-subtitles/{id}` takes the asset token only:
     * a stream token is bound to a file id, and that route carries none, so
     * the server refuses one there.
     */
    fun build(
        serverUrl: String,
        file: ItemFile?,
        assetToken: String?,
        sideLoadEmbedded: Boolean,
    ): List<SubtitleTrack> {
        if (file == null) return emptyList()
        val base = serverUrl.trimEnd('/')
        val asset = assetToken?.takeIf { it.isNotEmpty() }
        val fileToken = file.stream_token?.takeIf { it.isNotEmpty() } ?: asset
        val embedded = file.subtitle_streams.map { s ->
            val url = if (!sideLoadEmbedded || base.isEmpty() || fileToken == null || isImageBased(s.codec)) {
                null
            } else {
                StreamTokenVault.register("$base/media/subtitles/${file.id}/${s.index}", fileToken)
            }
            SubtitleTrack(
                trackId = "sub:emb:${s.index}",
                language = s.language,
                title = s.title,
                forced = s.forced,
                sdh = s.sdh,
                external = false,
                url = url,
            )
        }
        val attached = file.external_subtitles.map { e ->
            val url = if (base.isEmpty() || asset == null || e.url.isEmpty()) {
                null
            } else {
                StreamTokenVault.register("$base${e.url}", asset)
            }
            SubtitleTrack(
                trackId = "sub:ext:${e.id}",
                language = e.language,
                title = e.title.orEmpty(),
                forced = e.forced,
                sdh = e.sdh,
                external = true,
                url = url,
            )
        }
        return embedded + attached
    }

    /** What the picker lists. HLS: only what can be side-loaded — the session
     *  has no text of its own, so an image-based stream would be a row that
     *  never shows anything. Direct play: every embedded stream (read from the
     *  container) plus the attached files that can be side-loaded. */
    fun rows(tracks: List<SubtitleTrack>, hls: Boolean): List<SubtitleTrack> =
        if (hls) tracks.filter { it.url != null } else tracks.filter { !it.external || it.url != null }

    /** What the player side-loads next to the media: see the class comment. */
    fun sideLoads(tracks: List<SubtitleTrack>, hls: Boolean): List<SubtitleTrack> =
        if (hls) tracks.filter { it.url != null } else tracks.filter { it.external && it.url != null }

    /**
     * Our side-load id inside a player Format id, or null for a track the
     * media itself carries. NOT plain equality: MergingMediaSource prefixes
     * each child's ids with its index, so "sub:emb:3" surfaces as
     * "1:sub:emb:3" — an == match fails silently, the track is never
     * selected, and its VTT never even loads (the TV client hit exactly this).
     */
    fun sideLoadIdOf(formatId: String?): String? {
        val id = formatId ?: return null
        val at = id.indexOf(ID_MARKER)
        return if (at >= 0) id.substring(at) else null
    }

    /**
     * Pair the file's embedded streams (their languages, in the file's order)
     * with the text tracks the player extracted from the container (theirs,
     * in the player's order). Returns, per stream, the index of its player
     * track, or null when the player has none for it.
     *
     * The player lists a container's text tracks in the container's order, so
     * when the counts agree they pair up one to one — the usual case, and the
     * only one where two same-language tracks (full, SDH, forced) can be told
     * apart. When they don't, the player skipped a stream it can't read; walk
     * both lists in order and pair each stream with the next track in a
     * compatible language, so the ones after the gap still line up.
     */
    fun alignContainer(streamLanguages: List<String>, trackLanguages: List<String?>): List<Int?> {
        if (streamLanguages.size == trackLanguages.size) return streamLanguages.indices.toList()
        var next = 0
        return streamLanguages.map { lang ->
            var found: Int? = null
            var j = next
            while (j < trackLanguages.size) {
                if (languagesCompatible(lang, trackLanguages[j])) {
                    found = j
                    break
                }
                j++
            }
            if (found != null) next = found + 1
            found
        }
    }

    /** Picker / track label: "eng · English SDH · forced · SDH · downloaded". */
    fun label(track: SubtitleTrack): String {
        val parts = mutableListOf<String>()
        if (track.language.isNotEmpty()) parts += track.language
        if (track.title.isNotEmpty()) parts += track.title
        if (track.forced) parts += "forced"
        if (track.sdh) parts += "SDH"
        if (track.external) parts += "downloaded"
        return parts.joinToString(" · ").ifEmpty { "Track ${track.trackId.substringAfterLast(':')}" }
    }

    /** Image-based subtitle codecs: the server can't serve them as WebVTT
     *  (it answers 415), so they can't be side-loaded. Direct play still
     *  renders them from the container. */
    fun isImageBased(codec: String): Boolean =
        when (codec.trim().lowercase()) {
            "hdmv_pgs_subtitle", "pgssub", "dvd_subtitle", "dvdsub", "dvb_subtitle", "dvbsub", "xsub" -> true
            else -> false
        }

    // Minimal ISO 639-2/B (and a couple of 639-2/T) → 639-1 map, mirroring the
    // web client's normalizeLang. ffprobe usually reports 3-letter codes
    // ("eng", "spa") while the saved subtitle preference — and every language
    // Media3 reports — is a 2-letter 639-1 code ("en", "es"). Anything not
    // here falls back to a primary-subtag comparison, so an unknown code
    // simply won't false-match.
    private val ISO6392_TO_1: Map<String, String> = mapOf(
        "eng" to "en", "spa" to "es", "fre" to "fr", "fra" to "fr", "ger" to "de",
        "deu" to "de", "ita" to "it", "por" to "pt", "rus" to "ru", "jpn" to "ja",
        "chi" to "zh", "zho" to "zh", "kor" to "ko", "ara" to "ar", "dut" to "nl",
        "nld" to "nl", "swe" to "sv", "nor" to "no", "dan" to "da", "fin" to "fi",
        "pol" to "pl", "tur" to "tr", "heb" to "he", "hin" to "hi", "tha" to "th",
        "vie" to "vi", "ces" to "cs", "cze" to "cs", "gre" to "el", "ell" to "el",
        "hun" to "hu", "ron" to "ro", "rum" to "ro", "ukr" to "uk", "ind" to "id",
    )

    /** Reduce a language tag to a canonical 639-1 primary subtag (lowercased).
     *  "ENG" → "en", "en-US" → "en", "xyz" → "xyz". */
    fun normalizeLanguage(code: String?): String {
        if (code.isNullOrEmpty()) return ""
        val primary = code.lowercase().split('-', '_').first()
        return ISO6392_TO_1[primary] ?: primary
    }

    /** True when two language tags resolve to the same 639-1 primary subtag. */
    fun languagesMatch(a: String?, b: String?): Boolean {
        val na = normalizeLanguage(a)
        return na.isNotEmpty() && na == normalizeLanguage(b)
    }

    /** Equal, or either side unknown ("", "und") — for pairing tracks, where
     *  an untagged stream mustn't stop the walk. */
    private fun languagesCompatible(a: String?, b: String?): Boolean {
        val na = normalizeLanguage(a)
        val nb = normalizeLanguage(b)
        return na.isEmpty() || nb.isEmpty() || na == "und" || nb == "und" || na == nb
    }
}
