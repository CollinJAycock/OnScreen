package tv.onscreen.android.ui.common

import com.google.common.truth.Truth.assertThat
import org.junit.Test
import tv.onscreen.android.data.model.HubItem
import tv.onscreen.android.data.model.MediaItem
import tv.onscreen.android.data.model.UpNext
import tv.onscreen.android.data.model.UpNextEpisode
import tv.onscreen.android.ui.common.WatchStateUi.CardBadge
import tv.onscreen.android.ui.common.WatchStateUi.UpNextAction.Kind

class WatchStateUiTest {

    private fun ep(season: Int? = 3, episode: Int? = 4, offset: Long? = null) = UpNextEpisode(
        id = "ep", title = "The One", season_id = "se",
        season_number = season, episode_number = episode, view_offset_ms = offset,
    )

    private fun media(
        type: String = "movie",
        watchState: String? = null,
        offset: Long? = null,
        duration: Long? = null,
        leaves: Long? = null,
        unwatched: Long? = null,
    ) = MediaItem(
        id = "m", title = "M", type = type, duration_ms = duration,
        created_at = "", updated_at = "",
        watch_state = watchState, view_offset_ms = offset, leaf_count = leaves, unwatched_count = unwatched,
    )

    // ── episode code / Next Up subtitle ─────────────────────────────────────

    @Test
    fun `episode code drops unknown halves`() {
        assertThat(WatchStateUi.episodeCode(2, 5)).isEqualTo("S2 · E5")
        assertThat(WatchStateUi.episodeCode(null, 5)).isEqualTo("E5")
        assertThat(WatchStateUi.episodeCode(2, null)).isEqualTo("S2")
        // The hub sends 0 for "the scanner recorded no number".
        assertThat(WatchStateUi.episodeCode(2, 0)).isEqualTo("S2")
        assertThat(WatchStateUi.episodeCode(null, null)).isEmpty()
    }

    @Test
    fun `next up tiles show the show on top and the episode code below`() {
        val tile = HubItem(
            id = "e1", title = "Pilot", type = "episode",
            show_title = "The Show", season_number = 1, episode_number = 1,
        )
        assertThat(WatchStateUi.episodeTileTitles(tile)).isEqualTo("The Show" to "S1 · E1 — Pilot")
        assertThat(WatchStateUi.nextUpSubtitle(tile.copy(season_number = null, episode_number = null)))
            .isEqualTo("Pilot")
    }

    @Test
    fun `non-episode tiles or tiles without a show title keep the plain title`() {
        assertThat(WatchStateUi.episodeTileTitles(HubItem(id = "m", title = "Movie", type = "movie"))).isNull()
        assertThat(WatchStateUi.episodeTileTitles(HubItem(id = "e", title = "Orphan", type = "episode"))).isNull()
        assertThat(
            WatchStateUi.episodeTileTitles(HubItem(id = "e", title = "E", type = "episode", show_title = " ")),
        ).isNull()
    }

    // ── Up Next → Play button ───────────────────────────────────────────────

    @Test
    fun `resume maps to Resume with the episode's offset`() {
        val a = WatchStateUi.upNextAction(UpNext("resume", ep(3, 4, offset = 90_000)))!!
        assertThat(a.kind).isEqualTo(Kind.Resume)
        assertThat(a.code).isEqualTo("S3 · E4")
        assertThat(a.episodeId).isEqualTo("ep")
        assertThat(a.startMs).isEqualTo(90_000)
    }

    @Test
    fun `next and start map to Play from the top`() {
        val next = WatchStateUi.upNextAction(UpNext("next", ep(3, 5)))!!
        assertThat(next.kind).isEqualTo(Kind.Play)
        assertThat(next.code).isEqualTo("S3 · E5")
        assertThat(next.startMs).isEqualTo(0)

        val start = WatchStateUi.upNextAction(UpNext("start", ep(1, 1)))!!
        assertThat(start.kind).isEqualTo(Kind.Play)
        assertThat(start.code).isEqualTo("S1 · E1")
    }

    @Test
    fun `rewatch maps to Watch again from the top`() {
        // Even if a stray offset came back, rewatch starts at 0.
        val a = WatchStateUi.upNextAction(UpNext("rewatch", ep(1, 1, offset = 5_000)))!!
        assertThat(a.kind).isEqualTo(Kind.WatchAgain)
        assertThat(a.startMs).isEqualTo(0)
    }

    @Test
    fun `none, a missing episode or an unknown mode means no play action`() {
        assertThat(WatchStateUi.upNextAction(UpNext("none"))).isNull()
        assertThat(WatchStateUi.upNextAction(UpNext("next", episode = null))).isNull()
        assertThat(WatchStateUi.upNextAction(UpNext("shuffle", ep()))).isNull()
        assertThat(WatchStateUi.upNextAction(null)).isNull()
    }

    @Test
    fun `resume without a recorded offset starts at zero`() {
        assertThat(WatchStateUi.upNextAction(UpNext("resume", ep(offset = null)))!!.startMs).isEqualTo(0)
    }

    // ── Mark all options ────────────────────────────────────────────────────

    @Test
    fun `mark all options follow the up-next mode`() {
        assertThat(WatchStateUi.markAllOptions(UpNext("none")))
            .isEqualTo(WatchStateUi.MarkAllOptions(watched = false, unwatched = false))
        assertThat(WatchStateUi.markAllOptions(UpNext("start", ep())))
            .isEqualTo(WatchStateUi.MarkAllOptions(watched = true, unwatched = false))
        assertThat(WatchStateUi.markAllOptions(UpNext("rewatch", ep())))
            .isEqualTo(WatchStateUi.MarkAllOptions(watched = false, unwatched = true))
        assertThat(WatchStateUi.markAllOptions(UpNext("next", ep())))
            .isEqualTo(WatchStateUi.MarkAllOptions(watched = true, unwatched = true))
        // Unknown (up-next failed / older server): offer both.
        assertThat(WatchStateUi.markAllOptions(null))
            .isEqualTo(WatchStateUi.MarkAllOptions(watched = true, unwatched = true))
    }

    @Test
    fun `markable types match the server's leaf and container types`() {
        listOf("movie", "episode", "music_video", "home_video").forEach {
            assertThat(WatchStateUi.isMarkableLeaf(it)).isTrue()
        }
        listOf("show", "season", "track", "album", "photo", "audiobook").forEach {
            assertThat(WatchStateUi.isMarkableLeaf(it)).isFalse()
        }
        assertThat(WatchStateUi.isWatchContainer("show")).isTrue()
        assertThat(WatchStateUi.isWatchContainer("season")).isTrue()
        assertThat(WatchStateUi.isWatchContainer("album")).isFalse()
    }

    // ── Library filter ──────────────────────────────────────────────────────

    @Test
    fun `watch filter is offered only for video libraries`() {
        listOf("movie", "show", "anime", "cartoons", "home_video", "dvr").forEach {
            assertThat(WatchStateUi.supportsWatchFilter(it)).isTrue()
        }
        listOf("music", "photo", "audiobook", "podcast", "book", "", null).forEach {
            assertThat(WatchStateUi.supportsWatchFilter(it)).isFalse()
        }
    }

    @Test
    fun `watch filter values parse, anything else means all`() {
        assertThat(WatchStateUi.WATCH_FILTERS).containsExactly(null, "unwatched", "in_progress", "watched").inOrder()
        assertThat(WatchStateUi.parseWatchFilter("in_progress")).isEqualTo("in_progress")
        assertThat(WatchStateUi.parseWatchFilter("bogus")).isNull()
        assertThat(WatchStateUi.parseWatchFilter(null)).isNull()
    }

    // ── Card badges ─────────────────────────────────────────────────────────

    @Test
    fun `no watch fields means no badge`() {
        assertThat(WatchStateUi.cardBadge(media())).isNull()
        assertThat(WatchStateUi.cardBadge(media(watchState = "unwatched"))).isNull()
    }

    @Test
    fun `watched videos get the check`() {
        assertThat(WatchStateUi.cardBadge(media(watchState = "watched"))).isEqualTo(CardBadge.Watched)
    }

    @Test
    fun `in-progress videos get a progress bar`() {
        assertThat(WatchStateUi.cardBadge(media(watchState = "in_progress", offset = 30_000, duration = 120_000)))
            .isEqualTo(CardBadge.Progress(25))
        // No offset / duration → nothing to draw.
        assertThat(WatchStateUi.cardBadge(media(watchState = "in_progress", offset = null, duration = 120_000)))
            .isNull()
        assertThat(WatchStateUi.cardBadge(media(watchState = "in_progress", offset = 30_000, duration = null)))
            .isNull()
    }

    @Test
    fun `shows get an unwatched count, or the check once everything is watched`() {
        assertThat(WatchStateUi.cardBadge(media(type = "show", leaves = 10, unwatched = 3)))
            .isEqualTo(CardBadge.Unwatched(3))
        assertThat(WatchStateUi.cardBadge(media(type = "show", leaves = 10, unwatched = 0)))
            .isEqualTo(CardBadge.Watched)
        // A show with no episodes (within the ceiling) shows nothing.
        assertThat(WatchStateUi.cardBadge(media(type = "show", leaves = 0, unwatched = 0))).isNull()
    }

    @Test
    fun `continue watching tiles show the episode progress`() {
        val tile = HubItem(id = "s", title = "S", type = "show", view_offset_ms = 60_000, duration_ms = 240_000)
        assertThat(WatchStateUi.cardBadge(tile)).isEqualTo(CardBadge.Progress(25))
        assertThat(WatchStateUi.cardBadge(HubItem(id = "t", title = "T", type = "movie"))).isNull()
    }

    @Test
    fun `progress is clamped`() {
        assertThat(WatchStateUi.progressPct(500, 100)).isEqualTo(100)
        assertThat(WatchStateUi.progressPct(-5, 100)).isEqualTo(0)
        assertThat(WatchStateUi.progressPct(5, 0)).isEqualTo(0)
    }
}
