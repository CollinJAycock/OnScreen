package tv.onscreen.android.data.model

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
