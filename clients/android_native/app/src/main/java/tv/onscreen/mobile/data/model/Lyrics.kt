package tv.onscreen.mobile.data.model

import com.squareup.moshi.JsonClass

/**
 * Lyrics for a single track. Either field may be empty:
 *   - `plain` is unstamped text — line breaks but no timing.
 *   - `synced` is LRC format — `[mm:ss.xx]Line text` per line. The
 *     player overlay prefers synced when present so it can highlight
 *     the current line.
 *
 * Both empty = the server fetched-and-cached "no lyrics" for this
 * track. UI shows a placeholder rather than re-fetching.
 */
@JsonClass(generateAdapter = true)
data class LyricsResponse(
    val plain: String = "",
    val synced: String = "",
)
