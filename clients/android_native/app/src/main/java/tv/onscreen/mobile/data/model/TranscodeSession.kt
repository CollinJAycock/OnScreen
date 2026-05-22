package tv.onscreen.mobile.data.model

import com.squareup.moshi.JsonClass

@JsonClass(generateAdapter = true)
data class TranscodeSession(
    val session_id: String,
    val playlist_url: String,
    val token: String,
)

@JsonClass(generateAdapter = true)
data class TranscodeRequest(
    val file_id: String? = null,
    val height: Int,
    val position_ms: Long,
    val video_copy: Boolean = false,
    val audio_stream_index: Int? = null,
    val supports_hevc: Boolean = false,
)

@JsonClass(generateAdapter = true)
data class ProgressRequest(
    val view_offset_ms: Long,
    val duration_ms: Long,
    val state: String,
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
