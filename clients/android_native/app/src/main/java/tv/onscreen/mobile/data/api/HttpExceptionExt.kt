package tv.onscreen.mobile.data.api

import com.squareup.moshi.Moshi
import retrofit2.HttpException

// Lazily-built Moshi for parsing error envelopes off the failure path.
// Independent of the app's configured instance — the generated adapter for
// ApiError/ErrorBody is found by reflection, so this needs no DI wiring.
private val errorMoshi: Moshi by lazy { Moshi.Builder().build() }

/**
 * Parse the server error envelope (`{"error":{"code","message"}}`) from a
 * Retrofit [HttpException], when present. Lets callers branch on a specific
 * condition — e.g. a parental-limit `PARENTAL_LIMIT` 403 vs a content-rating
 * 403 — instead of treating every 403 the same.
 *
 * Best-effort: returns null when there's no body or it doesn't match the
 * envelope. The underlying errorBody() is single-read, so call this once per
 * exception (the failure path is terminal, so that's fine).
 */
fun HttpException.apiError(): ErrorBody? = try {
    val raw = response()?.errorBody()?.string() ?: return null
    errorMoshi.adapter(ApiError::class.java).fromJson(raw)?.error
} catch (_: Exception) {
    null
}
