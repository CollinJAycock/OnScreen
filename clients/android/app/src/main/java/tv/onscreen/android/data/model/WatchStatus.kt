package tv.onscreen.android.data.model

import com.squareup.moshi.JsonClass

/**
 * Per-(user, item) watching-status mirror — Plan to Watch / Watching
 * / Completed / On Hold / Dropped. Anime-tracker convention
 * (MyAnimeList / AniList shape) shipped as a generic feature on the
 * server, so every library type benefits.
 *
 * Distinct from playback progress: this is the user's explicit
 * classification ("I want to watch this later", "I gave up"). The
 * two complement each other on detail rails — a "Watching" status
 * with progress 73% gives both signals to the UI.
 */
@JsonClass(generateAdapter = true)
data class WatchStatus(
    val status: String,
    val updated_at: String,
)

/** Body for PUT /api/v1/items/{id}/watch-status. */
@JsonClass(generateAdapter = true)
data class WatchStatusUpdate(val status: String)

object WatchStatusValues {
    const val PlanToWatch = "plan_to_watch"
    const val Watching = "watching"
    const val Completed = "completed"
    const val OnHold = "on_hold"
    const val Dropped = "dropped"

    /** Canonical ordered list — matches the server's `AllStatuses()`. */
    val all = listOf(PlanToWatch, Watching, Completed, OnHold, Dropped)
}
