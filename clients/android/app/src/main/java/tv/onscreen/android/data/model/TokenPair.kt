package tv.onscreen.android.data.model

import com.squareup.moshi.JsonClass

@JsonClass(generateAdapter = true)
data class TokenPair(
    val access_token: String,
    val refresh_token: String,
    // purpose=asset token for cross-origin asset `?token=`. Nullable —
    // absent on servers that predate the asset-token work.
    val asset_token: String? = null,
    // Set when a TOTP-enabled account cleared the password step but owes
    // a second factor. Tokens are blank; post the code +
    // login_challenge_token to /auth/totp/verify to finish.
    val totp_required: Boolean = false,
    val login_challenge_token: String? = null,
    val expires_at: String = "",
    val user_id: String = "",
    val username: String = "",
    val is_admin: Boolean = false,
)
