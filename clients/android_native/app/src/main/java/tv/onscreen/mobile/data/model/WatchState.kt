package tv.onscreen.mobile.data.model

import com.squareup.moshi.JsonClass

/**
 * GET /api/v1/items/{id}/up-next (show or season), `{data:{…}}` envelope —
 * what Play on a show / season page should start. [mode] is one of the
 * [UpNextMode] strings; [episode] is omitted only for "none". Mirrors
 * internal/api/v1/items_watched.go → UpNextResponse.
 */
@JsonClass(generateAdapter = true)
data class UpNext(
    val mode: String = UpNextMode.NONE,
    val episode: UpNextEpisode? = null,
)

/** The episode an [UpNext] points at. The server sends season / episode
 *  numbers as 0 when the scanner recorded none; [view_offset_ms] only for
 *  mode "resume". */
@JsonClass(generateAdapter = true)
data class UpNextEpisode(
    val id: String,
    val title: String = "",
    val season_id: String = "",
    val season_number: Int? = null,
    val episode_number: Int? = null,
    val view_offset_ms: Long? = null,
    val duration_ms: Long? = null,
    val thumb_path: String? = null,
)

/** Wire values of [UpNext.mode]. Strings (not an enum) so a future mode
 *  from a newer server parses instead of failing the whole response. */
object UpNextMode {
    const val RESUME = "resume"
    const val NEXT = "next"
    const val START = "start"
    const val REWATCH = "rewatch"
    const val NONE = "none"
}

/** GET /api/v1/libraries/{id}/random ("Surprise me"), `{data:{id,type}}`. */
@JsonClass(generateAdapter = true)
data class RandomLibraryItem(
    val id: String,
    val type: String,
)

/** Per-user watch state wire values carried by items (`watch_state`) and
 *  accepted by the library listing / random pick as `?watch=`. */
object WatchStateValue {
    const val WATCHED = "watched"
    const val IN_PROGRESS = "in_progress"
    const val UNWATCHED = "unwatched"
}

/**
 * Library grid watch filter. [wire] is the `?watch=` value; null = no
 * filter ("All"). The three buckets partition the listing server-side.
 */
enum class WatchFilter(val wire: String?, val label: String) {
    ALL(null, "All"),
    UNWATCHED(WatchStateValue.UNWATCHED, "Unwatched"),
    IN_PROGRESS(WatchStateValue.IN_PROGRESS, "In progress"),
    WATCHED(WatchStateValue.WATCHED, "Watched"),
}
