package tv.onscreen.mobile.data.model

import com.squareup.moshi.JsonClass

// Reading position sync, v2.6 — internal/api/v1/reading_progress.go.
// GET / PUT /items/{id}/reading-progress, books only (404 otherwise, and on an
// older server). The newest position by client time wins.

/** Where the user is in a book. [locator] is opaque to the server: a page
 *  number ("40") for CBZ / CBR / PDF, an EPUB CFI for EPUB. [updated_at] is
 *  the client time the position was reached (RFC 3339). */
@JsonClass(generateAdapter = true)
data class ReadingProgress(
    val locator: String,
    val progression: Double = 0.0,
    val device_name: String? = null,
    val updated_at: String? = null,
    /** PUT only: false when the server kept a newer position than the one
     *  sent (and returned that instead). */
    val applied: Boolean? = null,
)

/** A reading position as the reader weighs it: [updatedAtMs] is epoch millis
 *  (0 = unknown). See ReadingSync. */
data class ReadingPosition(val locator: String, val progression: Double, val updatedAtMs: Long)

/** GET's envelope: `data` is null when nothing is saved for the book. */
@JsonClass(generateAdapter = true)
data class ReadingProgressEnvelope(val data: ReadingProgress? = null)

/** PUT body. [updated_at] omitted means "now" on the server. */
@JsonClass(generateAdapter = true)
data class ReadingProgressRequest(
    val locator: String,
    val progression: Double,
    val device_name: String? = null,
    val updated_at: String? = null,
)
