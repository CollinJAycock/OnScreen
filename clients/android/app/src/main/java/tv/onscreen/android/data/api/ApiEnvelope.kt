package tv.onscreen.android.data.api

import com.squareup.moshi.JsonClass
import tv.onscreen.android.data.model.TokenPair

// `data` is intentionally left non-null on the single-item envelope: making it
// nullable would ripple to ~every repository call site for a JsonDataException
// that is already caught into an error UI by the calling ViewModel (net-negative
// for the gain). The list envelope below defaults `data` so an omitted/regressed
// list page degrades to an empty page instead of a hard parse failure.
@JsonClass(generateAdapter = true)
data class ApiResponse<T>(val data: T)

@JsonClass(generateAdapter = true)
data class ApiListResponse<T>(val data: List<T> = emptyList(), val meta: Meta)

@JsonClass(generateAdapter = true)
data class Meta(val total: Int, val cursor: String?)

@JsonClass(generateAdapter = true)
data class ApiError(val error: ErrorBody?)

@JsonClass(generateAdapter = true)
data class ErrorBody(val code: String?, val message: String?, val request_id: String?)

/**
 * Envelope for GET /auth/pair/poll, whose 202 "still pending" body is
 * `{"status":...,"expires_at":...}` — no `data` key at all.
 *
 * It cannot use [ApiResponse], whose `data` is non-null: Retrofit converts the
 * body eagerly for ANY 2xx, so Moshi threw "Required value 'data' missing" on
 * every 202 and the exception surfaced as a network Failure. The pending branch
 * was therefore unreachable and the pairing screen showed "couldn't reach
 * server, will keep trying" for the entire normal wait, right up until the user
 * finished signing in on their phone.
 */
@JsonClass(generateAdapter = true)
data class PairPollResponse(
    val data: TokenPair? = null,
    val status: String? = null,
)
