package tv.onscreen.mobile.data.api

import com.squareup.moshi.JsonClass

@JsonClass(generateAdapter = true)
data class ApiResponse<T>(val data: T)

@JsonClass(generateAdapter = true)
data class ApiListResponse<T>(val data: List<T>, val meta: Meta)

@JsonClass(generateAdapter = true)
data class Meta(val total: Int, val cursor: String?)

@JsonClass(generateAdapter = true)
data class ApiError(val error: ErrorBody?)

@JsonClass(generateAdapter = true)
data class ErrorBody(val code: String?, val message: String?, val request_id: String?)
