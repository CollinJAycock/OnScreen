package tv.onscreen.android.data.model

import com.squareup.moshi.JsonClass

// Audiobook listening speed — the server's internal/api/v1/audiobook.go. A
// chapter's speed is its book's.

/** GET /items/{id}/playback-rate. [source] says where the rate came from:
 *  "book" (set on this book), "recent" (the user's latest speed on another
 *  book) or "default" (1.0, nothing set yet). */
@JsonClass(generateAdapter = true)
data class PlaybackRate(
    val rate: Double,
    val source: String = "default",
)

/** Body for PUT /items/{id}/playback-rate (0.5 – 3.0). */
@JsonClass(generateAdapter = true)
data class PlaybackRateRequest(val rate: Double)
