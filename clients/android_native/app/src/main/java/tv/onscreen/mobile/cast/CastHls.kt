package tv.onscreen.mobile.cast

import java.net.URI
import java.net.URLEncoder

/**
 * Pure rules for casting an OnScreen HLS session (remux or transcode) to the
 * Default Media Receiver: which way to cast, how to size the session, which
 * segment format to tell the receiver, and the session timeline math.
 *
 * The flow (shared contract with the web sender, see docs/capability-profiles.md
 * "Chromecast (Default Media Receiver)"):
 *   1. POST /items/{id}/playback-decision with the Chromecast's profile
 *      ([ChromecastCaps]).
 *   2. A file the DMR plays as it is ([CastMediaInfo.isCastable]) with a
 *      directPlay verdict casts the file itself, as before.
 *   3. Anything else starts POST /items/{id}/transcode with the same profile —
 *      video_copy when the verdict is directPlay / directStream — and LOADs
 *      its playlist_url as application/x-mpegurl.
 *
 * A session started at position P has its own timeline from 0: the receiver's
 * position is session time, and content time is offset + that ([contentMs]).
 *
 * Zero Android imports, so the JVM unit tests cover all of it.
 */
object CastHls {

    const val CONTENT_TYPE_HLS: String = "application/x-mpegurl"

    /** The HLS segment container, as the receiver must be told it. */
    enum class SegmentFormat(val wire: String) {
        /** MPEG-TS (.ts): H.264 sessions. */
        TS("ts"),

        /** Fragmented MP4 (.m4s + init.mp4): HEVC / AV1 sessions. */
        FMP4("fmp4"),
    }

    /** The start response's `segment_format` ("ts" | "fmp4"), or null when
     *  absent (a server that predates it) or unknown. */
    fun segmentFormatOf(field: String?): SegmentFormat? = when (field?.trim()?.lowercase()) {
        "ts" -> SegmentFormat.TS
        "fmp4", "mp4", "m4s" -> SegmentFormat.FMP4
        else -> null
    }

    /**
     * Infer the segment format from a MEDIA playlist's text: an `#EXT-X-MAP`
     * init segment or `.m4s` / `.mp4` segments mean fMP4, `.ts` segments mean
     * MPEG-TS. Query strings (the server appends `?token=`) are ignored. Null
     * when it lists no segment yet, or for a master playlist ([firstVariant]).
     */
    fun inferSegmentFormat(playlist: String): SegmentFormat? {
        for (raw in playlist.lineSequence()) {
            val line = raw.trim()
            if (line.startsWith("#EXT-X-MAP")) return SegmentFormat.FMP4
            if (line.isEmpty() || line.startsWith("#")) continue
            val path = line.substringBefore('?').substringBefore('#').lowercase()
            when {
                path.endsWith(".m4s") || path.endsWith(".mp4") -> return SegmentFormat.FMP4
                path.endsWith(".ts") -> return SegmentFormat.TS
            }
        }
        return null
    }

    /**
     * Last resort when neither the start response nor the playlist says
     * (the playlist listed no segment yet): what the server's packaging
     * rules give. A remux copies the source's video — HEVC / AV1 land in
     * fMP4, H.264 in MPEG-TS; a re-encode is HEVC (fMP4) only for a profile
     * that decodes it and a source that pays it off (HEVC, or 4K), else
     * H.264 (MPEG-TS). Audio-only sessions are MPEG-TS.
     */
    fun guessSegmentFormat(
        videoCopy: Boolean,
        sourceVideoCodec: String?,
        height: Int,
        profile: ChromecastCaps.Profile,
    ): SegmentFormat {
        val codec = sourceVideoCodec?.lowercase().orEmpty()
        if (codec.isEmpty()) return SegmentFormat.TS
        val hevcOrAv1 = codec == "hevc" || codec == "h265" || codec == "av1"
        return if (videoCopy) {
            if (hevcOrAv1) SegmentFormat.FMP4 else SegmentFormat.TS
        } else if (profile.hevc && (codec == "hevc" || codec == "h265" || height >= 2160)) {
            SegmentFormat.FMP4
        } else {
            SegmentFormat.TS
        }
    }

    /** A master playlist's first variant URI (the line after the first
     *  `#EXT-X-STREAM-INF`), or null for a media playlist. */
    fun firstVariant(playlist: String): String? {
        var expectUri = false
        for (raw in playlist.lineSequence()) {
            val line = raw.trim()
            if (line.startsWith("#EXT-X-STREAM-INF")) {
                expectUri = true
                continue
            }
            if (expectUri && line.isNotEmpty() && !line.startsWith("#")) return line
        }
        return null
    }

    /** [ref] resolved against [base] (an absolute playlist URL). */
    fun resolve(base: String, ref: String): String =
        runCatching { URI(base).resolve(ref.trim()).toString() }.getOrElse { ref }

    /** [path] as an absolute URL on [origin]: the Chromecast fetches on its
     *  own, so a server-relative playlist_url needs the configured origin.
     *  An already-absolute URL is returned as it is. */
    fun absolute(origin: String, path: String): String {
        val p = path.trim()
        if (p.startsWith("http://", ignoreCase = true) || p.startsWith("https://", ignoreCase = true)) return p
        val base = origin.trimEnd('/')
        return if (p.startsWith("/")) "$base$p" else "$base/$p"
    }

    // ── route ────────────────────────────────────────────────────────────────

    enum class Refusal {
        /** Verdict "unsupported": Dolby Vision profile 5. */
        DOLBY_VISION,

        /** Verdict "damaged": the integrity probe found the file undecodable. */
        DAMAGED,
    }

    sealed interface Route {
        /** Cast the file itself (/media/stream), as before. */
        data object Direct : Route

        /** Start an HLS session; [videoCopy] remuxes instead of re-encoding. */
        data class Hls(val videoCopy: Boolean) : Route

        data class Refuse(val reason: Refusal) : Route
    }

    /**
     * How to cast, from the server's [verdict] for the Chromecast profile
     * (null: no answer — an older server, or the call failed) and whether the
     * DMR plays the file as it is ([directCastable], today's rule).
     *
     * An audio-only file the DMR plays (mp3 / aac / flac / ogg) keeps the
     * direct cast whatever the verdict: the profile's `protocols` name no
     * audio container, so the server answers directStream for every one.
     * A non-default audio track ([audioTrackSelected]) can't go direct — the
     * receiver plays the file's default one — and a subtitle burn-in
     * ([burnIn]) is always a re-encode. Same rules as the web's planCast.
     */
    fun route(
        verdict: String?,
        directCastable: Boolean,
        audioOnly: Boolean,
        audioTrackSelected: Boolean = false,
        burnIn: Boolean = false,
    ): Route {
        if (verdict == "unsupported") return Route.Refuse(Refusal.DOLBY_VISION)
        if (verdict == "damaged") return Route.Refuse(Refusal.DAMAGED)
        if (burnIn) return Route.Hls(videoCopy = false)
        val direct = directCastable && !audioTrackSelected
        if (direct && (audioOnly || verdict == null || verdict == "directPlay")) return Route.Direct
        return Route.Hls(videoCopy = verdict == "directPlay" || verdict == "directStream")
    }

    /** The playback mode reported with progress beats for a route (the
     *  web's castProgressDecision). */
    fun decisionOf(route: Route, burning: Boolean): String = when (route) {
        Route.Direct -> "directPlay"
        is Route.Hls -> if (route.videoCopy && !burning) "remux" else "transcode"
        is Route.Refuse -> "transcode"
    }

    /** The client_name a cast's progress reports go under (Now Playing,
     *  "Play on…"): "Chromecast — <device name>". */
    fun clientName(friendlyName: String?): String {
        val n = friendlyName?.trim().orEmpty()
        return if (n.isEmpty()) "Chromecast" else "Chromecast — $n"
    }

    /** The height a transcode start falls back to when the server refuses
     *  the profile's (400: its TRANSCODE_MAX_HEIGHT is lower). */
    const val FALLBACK_HEIGHT: Int = 1080

    /**
     * The explicit `height` of the transcode start, remux included: the
     * source's tier — by width or height, so a 3840×1600 scope counts as
     * 2160 — capped at what [profile] decodes. Never 0 ("auto"): that may
     * start an ABR ladder, whose master playlist can't state one segment
     * format and whose rung switches restart encodes under the receiver.
     * Same as the web's castTranscodeHeight.
     */
    fun transcodeHeight(sourceWidth: Int?, sourceHeight: Int?, profile: ChromecastCaps.Profile): Int {
        val cap = profile.maxHeight
        val w = sourceWidth ?: 0
        val h = sourceHeight ?: 0
        val tier = when {
            w >= 3840 || h >= 2160 -> 2160
            w >= 1920 || h >= 1080 -> 1080
            w >= 1280 || h >= 720 -> 720
            h > 0 -> h
            else -> cap
        }
        return minOf(tier, cap)
    }

    // ── timeline ─────────────────────────────────────────────────────────────

    /**
     * Content time (ms) where a session asked to start at [requestedMs]
     * really opens: the response's `start_offset_sec` (a remux starts on the
     * keyframe at or before the request; a pre-encoded ladder at 0), else —
     * from a server that predates the field — the request.
     */
    fun sessionOffsetMs(startOffsetSec: Double?, requestedMs: Long): Long =
        startOffsetSec?.takeIf { it >= 0.0 && !it.isNaN() }?.let { (it * 1000.0).toLong() } ?: requestedMs.coerceAtLeast(0)

    /** Where the receiver starts within the session (its own time): the
     *  request, but never inside the audio-less head of a remux's segment 0
     *  ([seg0AudioGapSec], the start response's seg0_audio_gap_sec). */
    fun receiverStartMs(requestedMs: Long, offsetMs: Long, seg0AudioGapSec: Double? = null): Long {
        val gap = seg0AudioGapSec?.takeIf { it > 0.0 && !it.isNaN() }?.let { (it * 1000.0).toLong() } ?: 0L
        return maxOf(0L, requestedMs - offsetMs, gap)
    }

    /** Content time for a receiver (session) position. */
    fun contentMs(offsetMs: Long, receiverMs: Long): Long = offsetMs + receiverMs.coerceAtLeast(0)

    sealed interface Seek {
        /** Seek the receiver within the session it plays, to [receiverMs]. */
        data class Receiver(val receiverMs: Long) : Seek

        /** Start a new session at content [positionMs] and LOAD it. */
        data class NewSession(val positionMs: Long) : Seek
    }

    /**
     * How to reach content [targetMs] on a session that opens at [offsetMs]:
     * within what the session has produced — at least up to where the
     * receiver plays ([receiverPositionMs]), further when the receiver
     * reports a seekable range ending at [seekableEndMs] — the receiver
     * seeks itself; anywhere else (before the session's start, or past what
     * ffmpeg has written) a new session starts there.
     */
    fun planSeek(targetMs: Long, offsetMs: Long, receiverPositionMs: Long, seekableEndMs: Long?): Seek {
        val target = targetMs.coerceAtLeast(0)
        val produced = maxOf(receiverPositionMs, seekableEndMs ?: 0L)
        val inSession = target - offsetMs
        return if (inSession in 0..produced) Seek.Receiver(inSession) else Seek.NewSession(target)
    }

    // ── subtitles ────────────────────────────────────────────────────────────

    /** A WebVTT text track for the LOAD. */
    data class TextTrack(
        val id: Long,
        val url: String,
        val language: String,
        val name: String,
    )

    /** Track id of the one subtitle track a LOAD carries. */
    const val TEXT_TRACK_ID: Long = 1L

    /**
     * The receiver-fetchable WebVTT URL for the phone's subtitle [trackId]
     * (`sub:emb:{absoluteIndex}` / `sub:ext:{id}`, see SubtitleTracks),
     * authenticated by the file's stream token in `?token=` — the Chromecast
     * can't be given headers or cookies:
     *  - embedded: `/media/subtitles/{fileId}/{index}`
     *  - attached: `/media/subtitles/{fileId}/external/{subId}` (the
     *    `/media/external-subtitles/{id}` route takes an asset token only).
     *
     * [startMs] > 0 adds `start_ms`: the server moves every cue that much
     * earlier and drops the ones already over, rebasing the content-timed VTT
     * onto a session whose timeline starts there (its start_offset_sec; 0 for
     * a direct cast or a whole-title ladder). Same as the web's
     * castSubtitleTrack. Null without a stream token, or for an id not ours.
     */
    fun subtitleUrl(origin: String, fileId: String, trackId: String, streamToken: String?, startMs: Long): String? {
        val base = origin.trimEnd('/')
        val token = streamToken?.takeIf { it.isNotEmpty() } ?: return null
        if (base.isEmpty()) return null
        val file = encode(fileId)
        val path = when {
            trackId.startsWith("sub:emb:") -> {
                val index = trackId.removePrefix("sub:emb:").toIntOrNull() ?: return null
                "/media/subtitles/$file/$index"
            }
            trackId.startsWith("sub:ext:") -> {
                val subId = trackId.removePrefix("sub:ext:").takeIf { it.isNotEmpty() } ?: return null
                "/media/subtitles/$file/external/${encode(subId)}"
            }
            else -> return null
        }
        val start = if (startMs > 0) "&start_ms=$startMs" else ""
        return "$base$path?token=${encode(token)}$start"
    }

    private fun encode(s: String): String = URLEncoder.encode(s, "UTF-8").replace("+", "%20")
}
