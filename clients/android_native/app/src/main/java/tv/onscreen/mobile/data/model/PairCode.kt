package tv.onscreen.mobile.data.model

import com.squareup.moshi.JsonClass

/**
 * Response shape of POST /api/v1/auth/pair/code. The server returns a
 * 6-digit PIN the user reads off the TV plus an opaque device_token
 * the client passes back via `Authorization: Bearer …` to poll for
 * completion.
 *
 * - poll_after is a seconds hint for cadence; we honour it but cap to
 *   a sane min/max client-side so a bad server can't stall or hammer us.
 */
@JsonClass(generateAdapter = true)
data class PairCodeResponse(
    val pin: String,
    val device_token: String,
    val expires_at: String, // ISO8601
    val poll_after: Int = 2, // seconds
)

/**
 * Response shape of GET /api/v1/auth/pair/poll while the pairing is
 * still pending. HTTP 202 status with this body. Once claimed the
 * server returns 200 with a [TokenPair] instead.
 */
@JsonClass(generateAdapter = true)
data class PairPollPending(
    val status: String, // "open" | "claimed" — anything not "done"
    val expires_at: String,
)
