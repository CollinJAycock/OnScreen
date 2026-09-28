package tv.onscreen.android.ui.common

import tv.onscreen.android.data.model.HubItem
import tv.onscreen.android.data.model.MediaItem
import tv.onscreen.android.data.model.UpNext
import tv.onscreen.android.data.model.UpNextMode
import tv.onscreen.android.data.model.WatchFilter
import tv.onscreen.android.data.model.WatchStateValue

/**
 * Pure helpers behind the TV client's watch-state UI: Next Up tiles, the
 * show / season Play button, "Mark all" options, library-card badges and the
 * library Watch filter. No Android types — unit-tested in WatchStateUiTest.
 * Mirrors web/src/lib/watchState.ts so the two clients label things the same.
 */
object WatchStateUi {

    /** "S2 · E5". A missing season drops the S half; a missing or 0 episode
     *  number (the server's "unknown") drops the E half. */
    fun episodeCode(season: Int?, episode: Int?): String {
        val parts = mutableListOf<String>()
        if (season != null) parts.add("S$season")
        if (episode != null && episode > 0) parts.add("E$episode")
        return parts.joinToString(" · ")
    }

    /** Second line of an episode tile: "S2 · E5 — Episode title". */
    fun nextUpSubtitle(item: HubItem): String {
        val code = episodeCode(item.season_number, item.episode_number)
        return if (code.isNotEmpty()) "$code — ${item.title}" else item.title
    }

    /** Episode hub tiles (Next Up, recently-added episodes) render the show
     *  on top and the episode underneath. Null for every other tile. */
    fun episodeTileTitles(item: HubItem): Pair<String, String>? {
        val show = item.show_title?.takeIf { it.isNotBlank() } ?: return null
        if (item.type != "episode") return null
        return show to nextUpSubtitle(item)
    }

    /** What the primary button on a show / season page does. */
    data class UpNextAction(val kind: Kind, val episodeId: String, val startMs: Long, val code: String) {
        enum class Kind { Resume, Play, WatchAgain }
    }

    /**
     * Map an up-next response to the Play button, or null for no play action
     * (mode "none", no episode, or an unknown mode):
     *   resume  → Resume S3 · E4 (from the episode's resume point)
     *   next    → Play S3 · E5
     *   start   → Play S1 · E1
     *   rewatch → Watch again (first episode, from the top)
     */
    fun upNextAction(u: UpNext?): UpNextAction? {
        val ep = u?.episode ?: return null
        val code = episodeCode(ep.season_number, ep.episode_number)
        return when (u.mode) {
            UpNextMode.Resume ->
                UpNextAction(UpNextAction.Kind.Resume, ep.id, (ep.view_offset_ms ?: 0L).coerceAtLeast(0L), code)
            UpNextMode.Next, UpNextMode.Start ->
                UpNextAction(UpNextAction.Kind.Play, ep.id, 0L, code)
            UpNextMode.Rewatch ->
                UpNextAction(UpNextAction.Kind.WatchAgain, ep.id, 0L, code)
            else -> null
        }
    }

    /** Which "Mark all …" actions a show / season offers. */
    data class MarkAllOptions(val watched: Boolean, val unwatched: Boolean)

    /** From the up-next state: nothing watched → only "watched"; everything
     *  watched → only "unwatched"; no episodes → neither; unknown (the call
     *  failed / older server) or part-watched → both. */
    fun markAllOptions(u: UpNext?): MarkAllOptions = when (u?.mode) {
        UpNextMode.None -> MarkAllOptions(watched = false, unwatched = false)
        UpNextMode.Start -> MarkAllOptions(watched = true, unwatched = false)
        UpNextMode.Rewatch -> MarkAllOptions(watched = false, unwatched = true)
        else -> MarkAllOptions(watched = true, unwatched = true)
    }

    /** Item types a manual mark applies to — the server's leaf types plus the
     *  show / season containers (which mark every episode). */
    private val MARKABLE_LEAF_TYPES = setOf("movie", "episode", "music_video", "home_video")

    fun isMarkableLeaf(type: String): Boolean = type in MARKABLE_LEAF_TYPES
    fun isWatchContainer(type: String): Boolean = type == "show" || type == "season"

    /** Library types whose grid gets the Watch filter (music, photos, books,
     *  audiobooks and podcasts have no watch state). */
    private val WATCHABLE_LIBRARY_TYPES = setOf("movie", "show", "anime", "cartoons", "home_video", "dvr")

    fun supportsWatchFilter(libraryType: String?): Boolean =
        libraryType != null && libraryType in WATCHABLE_LIBRARY_TYPES

    /** The Watch filter choices in menu order; null = no filter ("All items"). */
    val WATCH_FILTERS: List<String?> = listOf(null, WatchFilter.Unwatched, WatchFilter.InProgress, WatchFilter.Watched)

    /** Anything unrecognised means "All". */
    fun parseWatchFilter(v: String?): String? = v?.takeIf { it in WATCH_FILTERS }

    fun progressPct(offsetMs: Long?, durationMs: Long?): Int {
        if (offsetMs == null || durationMs == null || offsetMs <= 0 || durationMs <= 0) return 0
        return ((offsetMs * 100) / durationMs).toInt().coerceIn(0, 100)
    }

    /** What watch indicator a poster card shows. */
    sealed interface CardBadge {
        data object Watched : CardBadge
        data class Unwatched(val count: Long) : CardBadge
        data class Progress(val pct: Int) : CardBadge
    }

    /**
     * Library card indicator, or null for none. Only derives from fields the
     * server actually sent, so listings without watch state (older servers,
     * music, photos) render exactly as before. Show-like counts win over
     * watch_state.
     */
    fun cardBadge(item: MediaItem): CardBadge? {
        val unwatched = item.unwatched_count
        if (unwatched != null) {
            if (item.leaf_count == 0L) return null
            return if (unwatched > 0) CardBadge.Unwatched(unwatched) else CardBadge.Watched
        }
        return when (item.watch_state) {
            WatchStateValue.Watched -> CardBadge.Watched
            WatchStateValue.InProgress ->
                progressPct(item.view_offset_ms, item.duration_ms).takeIf { it > 0 }?.let { CardBadge.Progress(it) }
            else -> null
        }
    }

    /** Continue Watching hub tiles carry the (next) episode's resume point:
     *  a progress bar under the poster, like the web's continue rows. */
    fun cardBadge(item: HubItem): CardBadge? =
        progressPct(item.view_offset_ms, item.duration_ms).takeIf { it > 0 }?.let { CardBadge.Progress(it) }
}
