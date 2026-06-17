package tv.onscreen.mobile.data.api

import com.squareup.moshi.JsonClass

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
