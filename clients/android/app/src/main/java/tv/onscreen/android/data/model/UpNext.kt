package tv.onscreen.android.data.model

import com.squareup.moshi.JsonClass

/**
 * GET /api/v1/items/{id}/up-next (show or season) — what the Play button
 * starts. [mode] is one of [UpNextMode]; [episode] is omitted only for
 * "none". Unknown future modes are treated like "none" by the UI helpers.
 */
@JsonClass(generateAdapter = true)
data class UpNext(
    val mode: String,
    val episode: UpNextEpisode? = null,
)

@JsonClass(generateAdapter = true)
data class UpNextEpisode(
    val id: String,
    val title: String = "",
    val season_id: String = "",
    val season_number: Int? = null,
    val episode_number: Int? = null,
    /** Only present for mode "resume". */
    val view_offset_ms: Long? = null,
    val duration_ms: Long? = null,
    val thumb_path: String? = null,
)

object UpNextMode {
    /** An episode has a resume point — the most recently touched one. */
    const val Resume = "resume"
    /** The first unwatched episode after the last one finished. */
    const val Next = "next"
    /** Nothing watched yet: the first episode. */
    const val Start = "start"
    /** Everything watched: the first episode again. */
    const val Rewatch = "rewatch"
    /** No episodes. */
    const val None = "none"
}

/** GET /api/v1/libraries/{id}/random — the picked item's id and type. */
@JsonClass(generateAdapter = true)
data class RandomLibraryItem(val id: String, val type: String)

/** `?watch=` values accepted by GET /libraries/{id}/items (and /random). */
object WatchFilter {
    const val Unwatched = "unwatched"
    const val InProgress = "in_progress"
    const val Watched = "watched"
}

/** Values of `watch_state` on library items and item detail. */
object WatchStateValue {
    const val Watched = "watched"
    const val InProgress = "in_progress"
    const val Unwatched = "unwatched"
}
