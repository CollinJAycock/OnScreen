package tv.onscreen.mobile.data.api

import com.squareup.moshi.JsonClass
import tv.onscreen.mobile.data.model.TokenPair

// `data` is intentionally left non-null on the single-item envelope: making it
// nullable would ripple to ~every repository call site for a JsonDataException
// that is already caught into an error UI by the calling ViewModel (net-negative
// for the gain). The list envelope below defaults `data` so an omitted/regressed
// list page degrades to an empty page instead of a hard parse failure.
@JsonClass(generateAdapter = true)
data class ApiResponse<T>(val data: T)

/**
 * Envelope for GET /auth/pair/poll, whose 202 "still pending" body is
 * `{"status":...,"expires_at":...}`, with no `data` key at all.
 *
 * It can't be [ApiResponse], whose `data` is non-null: Retrofit converts the
 * body of ANY 2xx, so Moshi threw "Required value 'data' missing" on every
 * 202 and the poll came back as a failure. 0.4.3 swallowed that and polled on;
 * 0.5.0's poll loop (shown failures, backoff, giving up after eight) showed
 * "Couldn't reach server" for the whole wait and stopped pairing after a few
 * minutes. The TV client has had this envelope since July (b2efb43f).
 */
@JsonClass(generateAdapter = true)
data class PairPollResponse(
    val data: TokenPair? = null,
    val status: String? = null,
)

@JsonClass(generateAdapter = true)
data class ApiListResponse<T>(val data: List<T> = emptyList(), val meta: Meta)

@JsonClass(generateAdapter = true)
data class Meta(val total: Int, val cursor: String?)

@JsonClass(generateAdapter = true)
data class ApiError(val error: ErrorBody?)

@JsonClass(generateAdapter = true)
data class ErrorBody(val code: String?, val message: String?, val request_id: String?)
