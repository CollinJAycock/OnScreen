package tv.onscreen.mobile.data.model

import com.squareup.moshi.JsonClass

/**
 * GET /api/v1/system/capabilities — what the server can do. Only the parts
 * the phone reads are decoded; the server adds fields over time and never
 * removes one, and every field here defaults to "not supported", so an older
 * server (or one that leaves a flag out) reads as lacking the feature.
 */
@JsonClass(generateAdapter = true)
data class ServerCapabilities(
    val features: ServerFeatures = ServerFeatures(),
)

@JsonClass(generateAdapter = true)
data class ServerFeatures(
    /** PUT /items/{id}/progress with duration_ms left out keeps the duration
     *  the server has for the item (and fills it from the file when it knows
     *  it) instead of storing none. An older server stored the missing one:
     *  the item then read "unwatched" and dropped out of Continue Watching. */
    val progress_without_duration: Boolean = false,
)
