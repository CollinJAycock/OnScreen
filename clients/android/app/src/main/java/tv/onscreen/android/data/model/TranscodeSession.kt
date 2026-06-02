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
     *  the codec can't honor. Servers older than v2.1 omit this; the
     *  default 0.0 means "start at session start". */
    val start_offset_sec: Double = 0.0,
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
 *  "transcode". See docs/capability-profiles.md. */
@JsonClass(generateAdapter = true)
data class PlaybackDecision(
    val decision: String,
    val file_id: String? = null,
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
