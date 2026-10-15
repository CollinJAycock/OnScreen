package tv.onscreen.android.data.model

import com.squareup.moshi.JsonClass

@JsonClass(generateAdapter = true)
data class TranscodeSession(
    val session_id: String,
    val playlist_url: String,
    val token: String,
    /** Content position the stream actually opens at, keyframe-aligned.
     *  May be earlier than the requested `position_ms` when video is
     *  being stream-copied — input-side -ss snaps back to the previous
     *  keyframe. Use this for the scrubber-time mapping so the seek bar
     *  matches what's on screen instead of advertising an exact resume
     *  the codec can't honor. A real 0 is kept: the nearest keyframe is the
     *  file's first, or the stream covers the whole file (a pre-encoded
     *  ladder), and the player seeks to the resume point inside it. Null
     *  (absent) only from a server older than v2.1. See
     *  StreamSession.opened. */
    val start_offset_sec: Double? = null,
    /** Skip this many seconds of segment 0 on startup. With AC3 → AAC
     *  re-encode after a mid-stream seek, the AAC encoder's first valid
     *  frame lands a few seconds after video's first packet — starting
     *  playback here gets A/V synced from the first audible frame
     *  instead of showing silent video while the audio pipeline warms
     *  up. Zero when no gap was measurable. v2.1+. */
    val seg0_audio_gap_sec: Double = 0.0,
)

@JsonClass(generateAdapter = true)
data class TranscodeRequest(
    val file_id: String? = null,
    val height: Int,
    val position_ms: Long,
    val video_copy: Boolean = false,
    val audio_stream_index: Int? = null,
    val supports_hevc: Boolean = false,
    /** v2.1. Tell the server we can decode AV1. When the source file
     *  is AV1 and the client supports AV1, the server prefers the
     *  AV1 fMP4 remux path (av01 tag, .m4s segments, #EXT-X-MAP) over
     *  an H.264 NVENC/QSV/AMF re-encode. Off by default — Media3
     *  ExoPlayer's AV1 support depends on the chipset's hardware
     *  decoder; the playback layer should set this true only when
     *  the device's MediaCodec list reports a working AV1 decoder. */
    val supports_av1: Boolean = false,
)

/** Body for POST /items/{id}/playback-decision. The capability profile rides the
 *  X-Client-Capabilities header (AuthInterceptor); the body just picks the file. */
@JsonClass(generateAdapter = true)
data class PlaybackDecisionRequest(
    val file_id: String? = null,
)

/** Server-authoritative play decision: "directPlay" | "directStream" |
 *  "transcode" | "unsupported" (Dolby Vision — can't be played; show a message).
 *  See docs/capability-profiles.md and docs/dolby-vision.md. */
@JsonClass(generateAdapter = true)
data class PlaybackDecision(
    val decision: String,
    /** The file the decision is for: a stacked movie's first part when the
     *  request named any of its parts. */
    val file_id: String? = null,
    /** Why the server overrode the capability verdict: remote_bitrate_limit,
     *  quality_profile, or [REASON_OPTIMIZED_VERSION] (v2.6). */
    val reason: String? = null,
    /** v2.6, with reason optimized_version and decision directPlay: the
     *  pre-made MP4 to play instead of transcoding the source. Only sent to a
     *  client that declares optimized=1 (PlaybackHelper's header). */
    val optimized_version: OptimizedVersion? = null,
    /** v2.6. The verdict for the source itself when [optimized_version]
     *  replaced it — what to fall back to if the version can't be played. */
    val source_decision: String? = null,
) {
    /** The optimized version to play, when the server picked one. */
    val optimized: OptimizedVersion?
        get() = optimized_version?.takeIf { reason == REASON_OPTIMIZED_VERSION && it.stream_url.isNotBlank() }

    companion object {
        const val REASON_OPTIMIZED_VERSION = "optimized_version"
    }
}

/** A pre-made optimized version of an item (v2.6, "Optimize…" on the web):
 *  a progressive MP4, H.264 + stereo AAC, served from [stream_url]
 *  (/media/optimized/{id}). Never one of the item's files. */
@JsonClass(generateAdapter = true)
data class OptimizedVersion(
    val id: String,
    /** The source file it was made from (a stacked movie's first part). */
    val file_id: String? = null,
    val label: String? = null,
    val stream_url: String = "",
    /** 24 h token bound to this version, the URL credential for players that
     *  can't send a header — like ItemFile.stream_token. */
    val stream_token: String? = null,
    val height: Int? = null,
    val bitrate_kbps: Int? = null,
    val duration_ms: Long? = null,
)

@JsonClass(generateAdapter = true)
data class ProgressRequest(
    val view_offset_ms: Long,
    val duration_ms: Long,
    val state: String,
    /** Stable per-device label. Drives the cross-device "play on
     *  Living Room TV" picker (the server populates that list from
     *  `MAX(last_seen) GROUP BY client_name` on watch_events) and
     *  the "Resume from <Living Room TV>" UX. Server caps at 64
     *  chars; the ClientName provider stays under 60. */
    val client_name: String? = null,
)

@JsonClass(generateAdapter = true)
data class LoginRequest(
    val username: String,
    val password: String,
)

@JsonClass(generateAdapter = true)
data class RefreshRequest(
    val refresh_token: String,
)

@JsonClass(generateAdapter = true)
data class LogoutRequest(
    val refresh_token: String,
)

@JsonClass(generateAdapter = true)
data class TotpVerifyRequest(
    val login_challenge_token: String,
    val code: String,
)

// ── Scrobbling (per-user ListenBrainz link) ──────────────────────────────────

/** Client-safe scrobble status — never carries the token, only whether one is
 *  linked and whether export is on. */
@JsonClass(generateAdapter = true)
data class ScrobbleStatusResponse(
    val listenbrainz_linked: Boolean = false,
    val listenbrainz_enabled: Boolean = false,
)

/** Body for PUT /users/me/scrobble/listenbrainz. An empty token unlinks. */
@JsonClass(generateAdapter = true)
data class SetListenBrainzRequest(
    val token: String,
    val enabled: Boolean,
)
