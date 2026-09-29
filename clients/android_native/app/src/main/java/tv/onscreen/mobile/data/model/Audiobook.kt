package tv.onscreen.mobile.data.model

import com.squareup.moshi.JsonClass

// Audiobook listening speed and bookmarks — the server's
// internal/api/v1/audiobook.go. Both hang off the book: a chapter's speed is
// its book's, and a book lists every bookmark in it, whichever chapter each
// sits in.

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

/**
 * One bookmark. [item_id] is the playable item the position is in: the book
 * itself for a single-file book, else the chapter; [item_title] and
 * [item_index] name that chapter. [created_at] is RFC 3339.
 */
@JsonClass(generateAdapter = true)
data class Bookmark(
    val id: String,
    val item_id: String,
    val item_title: String = "",
    val item_index: Int? = null,
    val position_ms: Long,
    val note: String = "",
    val created_at: String? = null,
)

/** Body for POST /items/{id}/bookmarks, where {id} is the playable item. */
@JsonClass(generateAdapter = true)
data class CreateBookmarkRequest(
    val position_ms: Long,
    val note: String = "",
)

/** Body for PATCH /bookmarks/{id} ("" clears the note). */
@JsonClass(generateAdapter = true)
data class BookmarkNoteRequest(val note: String)
