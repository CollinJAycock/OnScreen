package tv.onscreen.mobile.ui.watch

import tv.onscreen.mobile.data.model.HubItem
import tv.onscreen.mobile.data.model.MediaItem
import tv.onscreen.mobile.data.model.UpNext
import tv.onscreen.mobile.data.model.UpNextMode
import tv.onscreen.mobile.data.model.WatchStateValue

/*
 * Pure helpers behind the phone's watch-state UI: the hub's Next Up tiles,
 * the show / season primary button, the library-grid badges and which
 * mark actions an item offers. No Compose, no network — unit-tested in
 * WatchStateUiTest. Ported from the web client's $lib/watchState.ts so the
 * labels read the same on every client.
 */

/** "S2 · E5". A missing half is dropped ("S2", "E5"); both missing → "".
 *  The server sends 0 for an unrecorded episode number, so E0 is dropped
 *  too (season 0 is kept — that's Specials). */
fun episodeCode(season: Int?, episode: Int?): String {
    val parts = mutableListOf<String>()
    if (season != null) parts += "S$season"
    if (episode != null && episode > 0) parts += "E$episode"
    return parts.joinToString(" · ")
}

/** Second line of a Next Up tile: "S2 · E5 — Episode title". */
fun nextUpSubtitle(item: HubItem): String {
    val code = episodeCode(item.season_number, item.episode_number)
    return if (code.isEmpty()) item.title else "$code — ${item.title}"
}

/**
 * Label for the primary button on a show / season page, or null when
 * there's nothing to play (mode "none", an unknown mode, or no episode):
 *   resume  → "Resume S3 · E4"
 *   next    → "Play S3 · E5"
 *   start   → "Play S1 · E1"
 *   rewatch → "Watch again"
 */
fun upNextLabel(u: UpNext?): String? {
    val ep = u?.episode ?: return null
    val code = episodeCode(ep.season_number, ep.episode_number)
    return when (u.mode) {
        UpNextMode.RESUME -> if (code.isEmpty()) "Resume" else "Resume $code"
        UpNextMode.NEXT, UpNextMode.START -> if (code.isEmpty()) "Play" else "Play $code"
        UpNextMode.REWATCH -> "Watch again"
        else -> null
    }
}

/**
 * Primary button on a playable item's own page (movie, episode, track, …):
 *   resume point      → "Resume from 20:04" (the player starts there)
 *   watched, no point → "Watch again" (the show page's rewatch wording)
 *   otherwise         → "Play"
 * A rewatch in progress keeps its "watched" state but has a resume point,
 * so the resume point wins.
 */
fun leafPlayLabel(resumeMs: Long, watched: Boolean): String = when {
    resumeMs > 0 -> "Resume from ${formatTimecode(resumeMs)}"
    watched -> "Watch again"
    else -> "Play"
}

/** "20:04", "1:20:04" — same shape as the TV client's Resume label. */
fun formatTimecode(ms: Long): String {
    val totalSec = ms.coerceAtLeast(0) / 1000
    val h = totalSec / 3600
    val m = (totalSec % 3600) / 60
    val s = totalSec % 60
    return if (h > 0) {
        String.format(java.util.Locale.ROOT, "%d:%02d:%02d", h, m, s)
    } else {
        String.format(java.util.Locale.ROOT, "%d:%02d", m, s)
    }
}

/** Which "Mark all …" buttons a show / season page offers. */
data class MarkAllOptions(val watched: Boolean, val unwatched: Boolean)

/**
 * From the page's up-next state: nothing watched yet → only "watched",
 * everything watched → only "unwatched", no episodes → neither. Unknown
 * (the up-next call failed / older server) or part-way → both.
 */
fun markAllOptions(u: UpNext?): MarkAllOptions = when (u?.mode) {
    UpNextMode.NONE -> MarkAllOptions(watched = false, unwatched = false)
    UpNextMode.START -> MarkAllOptions(watched = true, unwatched = false)
    UpNextMode.REWATCH -> MarkAllOptions(watched = false, unwatched = true)
    else -> MarkAllOptions(watched = true, unwatched = true)
}

/** 0f..1f progress for a resume bar; 0f when either side is missing. */
fun progressFraction(offsetMs: Long?, durationMs: Long?): Float {
    if (offsetMs == null || durationMs == null || offsetMs <= 0 || durationMs <= 0) return 0f
    return (offsetMs.toFloat() / durationMs.toFloat()).coerceIn(0f, 1f)
}

/** What watch indicator a library poster shows. */
data class CardWatchBadge(
    /** Fully watched: render the check. */
    val watched: Boolean,
    /** Show / season with episodes left: render the count. */
    val unwatchedCount: Long?,
    /** In-progress video: 0..1 for the bar under the poster. */
    val progress: Float?,
    /** Screen-reader text for whichever of the above is shown. */
    val label: String,
)

/**
 * The badge for a library card, or null for none. Derives only from the
 * fields the server actually sent, so listings without watch state (older
 * servers, music, photos) render exactly as before.
 */
fun cardWatchBadge(item: MediaItem): CardWatchBadge? {
    // Show-like: the counts win over watch_state.
    val unwatched = item.unwatched_count
    if (unwatched != null) {
        if (item.leaf_count == 0L) return null
        if (unwatched > 0) {
            val noun = if (unwatched == 1L) "episode" else "episodes"
            return CardWatchBadge(false, unwatched, null, "$unwatched unwatched $noun")
        }
        return CardWatchBadge(true, null, null, "Watched")
    }
    return when (item.watch_state) {
        WatchStateValue.WATCHED -> CardWatchBadge(true, null, null, "Watched")
        WatchStateValue.IN_PROGRESS -> {
            val p = progressFraction(item.view_offset_ms, item.duration_ms)
            if (p > 0f) CardWatchBadge(false, null, p, "In progress, ${(p * 100).toInt()}% watched")
            else null
        }
        else -> null
    }
}

/**
 * The card as it will look after a successful mark — the optimistic grid
 * update (the caller keeps the original for rollback). The resume point
 * goes away either way, as it does server-side.
 */
fun applyWatchedMark(item: MediaItem, watched: Boolean): MediaItem {
    val containerCounts = item.unwatched_count != null || item.leaf_count != null
    return item.copy(
        watch_state = if (watched) WatchStateValue.WATCHED else WatchStateValue.UNWATCHED,
        view_offset_ms = null,
        unwatched_count = when {
            !containerCounts -> item.unwatched_count
            watched -> 0L
            else -> item.leaf_count ?: item.unwatched_count
        },
    )
}

/** Which of Mark watched / Mark unwatched a library card offers. A
 *  part-watched item (show with some episodes seen, movie stopped half
 *  way) gets both. */
fun watchMenuActions(item: MediaItem): List<Boolean> {
    val unwatched = item.unwatched_count
    if (unwatched != null) {
        val leaves = item.leaf_count
        val out = mutableListOf<Boolean>()
        if (unwatched > 0 || leaves == null) out += true
        if (leaves == null || unwatched < leaves) out += false
        return out
    }
    return when (item.watch_state) {
        WatchStateValue.WATCHED -> listOf(false)
        WatchStateValue.IN_PROGRESS -> listOf(true, false)
        else -> listOf(true)
    }
}

// Library types whose items carry a watch state — the grid gets the watch
// filter, Surprise me and the mark menu. Music, photos, books, audiobooks
// and podcasts don't. Same set as the web client.
private val WATCHABLE_LIBRARY_TYPES = setOf("movie", "show", "anime", "cartoons", "home_video", "dvr")

fun supportsWatchState(libraryType: String?): Boolean =
    libraryType != null && libraryType in WATCHABLE_LIBRARY_TYPES

// Item types POST/DELETE /items/{id}/watched accepts (a show / season marks
// every episode); the server answers 422 for anything else.
private val MARKABLE_LEAF_TYPES = setOf("movie", "episode", "music_video", "home_video")
private val MARKABLE_CONTAINER_TYPES = setOf("show", "season")

/** A playable video with its own watch state (movie / episode / …). */
fun isWatchLeafType(type: String): Boolean = type in MARKABLE_LEAF_TYPES

/** A show or season: marks expand to the episodes; Play comes from up-next. */
fun isWatchContainerType(type: String): Boolean = type in MARKABLE_CONTAINER_TYPES

fun canMarkWatched(type: String): Boolean = isWatchLeafType(type) || isWatchContainerType(type)

/** Continue Watching tiles the dismiss endpoint accepts (same gate as marks). */
fun canDismissContinueWatching(type: String): Boolean = canMarkWatched(type)
