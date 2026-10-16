package tv.onscreen.mobile.data.model

import com.squareup.moshi.JsonClass

@JsonClass(generateAdapter = true)
data class TranscodeSession(
    val session_id: String,
    val playlist_url: String,
    val token: String,
    /** Content time (seconds) the session's stream really opens at. A remux
     *  (video copied) can only start on a keyframe, so this can be several
     *  seconds EARLIER than the position asked for — or 0, when the nearest
     *  keyframe is the file's first. Null (absent) only from a server that
     *  predates the field. See PlayerViewModel.adoptSession. */
    val start_offset_sec: Double? = null,
    /** The session's HLS segment container, "ts" | "fmp4" — what a Cast
     *  receiver must be told before it loads the playlist. Null (absent) from
     *  a server that predates the field: CastPlaybackController then reads the
     *  playlist itself (CastHls.inferSegmentFormat). */
    val segment_format: String? = null,
    /** Seconds of segment 0 with no audio yet (an AC3 -> AAC re-encode after a
     *  mid-stream seek): a Cast receiver is LOADed past it. Null / 0 = none. */
    val seg0_audio_gap_sec: Double? = null,
)

@JsonClass(generateAdapter = true)
data class TranscodeRequest(
    val file_id: String? = null,
    val height: Int,
    val position_ms: Long,
    val video_copy: Boolean = false,
    val audio_stream_index: Int? = null,
    val supports_hevc: Boolean = false,
    /** v2.6. The ABSOLUTE index (SubtitleStream.index) of an image-based
     *  subtitle stream to burn into the picture: always a full re-encode, so
     *  sent with video_copy false. Null (left out) burns nothing. Refused
     *  with 400 INVALID_SUBTITLE_STREAM / SUBTITLE_NOT_IMAGE /
     *  BURN_IN_UNSUPPORTED (a stacked movie). */
    val burn_subtitle_stream_index: Int? = null,
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
    val file_id: String? = null,
    /** Why the server overrode the capability verdict, when it did:
     *  remote_bitrate_limit / user_bitrate_limit / quality_profile (a forced
     *  transcode), or optimized_version (see [optimized_version]). */
    val reason: String? = null,
    /** With reason "optimized_version" and decision "directPlay" (v2.6, only
     *  for a client that sends optimized=1): the pre-made MP4 to play instead
     *  of the source. file_id stays the source's. */
    val optimized_version: OptimizedVersion? = null,
    /** The verdict for the source itself when an optimized version replaced
     *  it — what to fall back to if the version won't play. */
    val source_decision: String? = null,
    val source_reason: String? = null,
)

@JsonClass(generateAdapter = true)
data class ProgressRequest(
    val view_offset_ms: Long,
    /** Null (omitted on the wire) when the player can't tell: the server then
     *  keeps the duration it knows for the item rather than storing none.
     *  Only sent to a server that says so (ServerFeatures
     *  .progress_without_duration) — see PlayerViewModel.reportProgress. */
    val duration_ms: Long?,
    val state: String,
    /** Playback decision for the analytics direct-vs-transcode split.
     *  Null (omitted on the wire) when unknown. */
    val decision: String? = null,
    /** The device the report is for, when not this phone's default — a
     *  Chromecast it casts to ("Chromecast — Living Room"). Null (omitted) for
     *  the phone's own playback. */
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

@JsonClass(generateAdapter = true)
data class TotpCodeRequest(
    val code: String,
)

@JsonClass(generateAdapter = true)
data class TotpSetupResponse(
    val otpauth_url: String,
    val secret: String,
    // base64-encoded PNG QR rendered server-side; may be absent if the
    // render failed (clients fall back to the secret / otpauth_url).
    val qr_png: String? = null,
)

@JsonClass(generateAdapter = true)
data class TotpActivateResponse(
    val recovery_codes: List<String> = emptyList(),
)

@JsonClass(generateAdapter = true)
data class TotpStatusResponse(
    val enabled: Boolean = false,
    val recovery_codes_remaining: Int = 0,
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
