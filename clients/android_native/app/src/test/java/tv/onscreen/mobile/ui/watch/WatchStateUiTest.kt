package tv.onscreen.mobile.ui.watch

import com.google.common.truth.Truth.assertThat
import org.junit.Test
import tv.onscreen.mobile.data.model.HubItem
import tv.onscreen.mobile.data.model.MediaItem
import tv.onscreen.mobile.data.model.UpNext
import tv.onscreen.mobile.data.model.UpNextEpisode

class WatchStateUiTest {

    private fun ep(season: Int? = 3, episode: Int? = 4) = UpNextEpisode(
        id = "e1", title = "The One", season_id = "s3",
        season_number = season, episode_number = episode,
    )

    private fun media(
        type: String = "movie",
        watchState: String? = null,
        offset: Long? = null,
        duration: Long? = null,
        leaves: Long? = null,
        unwatched: Long? = null,
    ) = MediaItem(
        id = "m1", title = "T", type = type, duration_ms = duration,
        created_at = "2026-01-01T00:00:00Z", updated_at = "0",
        watch_state = watchState, view_offset_ms = offset,
        leaf_count = leaves, unwatched_count = unwatched,
    )

    @Test
    fun `episodeCode joins the halves and drops missing ones`() {
        assertThat(episodeCode(2, 5)).isEqualTo("S2 · E5")
        assertThat(episodeCode(2, null)).isEqualTo("S2")
        assertThat(episodeCode(null, 5)).isEqualTo("E5")
        assertThat(episodeCode(null, null)).isEmpty()
        // Server's "number unknown" episode 0 is dropped; season 0 = Specials stays.
        assertThat(episodeCode(0, 0)).isEqualTo("S0")
    }

    @Test
    fun `nextUpSubtitle prefixes the code to the episode title`() {
        val item = HubItem(
            id = "e", title = "Pilot", type = "episode",
            show_title = "Show", season_number = 1, episode_number = 1,
        )
        assertThat(nextUpSubtitle(item)).isEqualTo("S1 · E1 — Pilot")
        assertThat(nextUpSubtitle(item.copy(season_number = null, episode_number = null))).isEqualTo("Pilot")
    }

    @Test
    fun `upNextLabel covers every mode`() {
        assertThat(upNextLabel(UpNext("resume", ep()))).isEqualTo("Resume S3 · E4")
        assertThat(upNextLabel(UpNext("next", ep(3, 5)))).isEqualTo("Play S3 · E5")
        assertThat(upNextLabel(UpNext("start", ep(1, 1)))).isEqualTo("Play S1 · E1")
        assertThat(upNextLabel(UpNext("rewatch", ep(1, 1)))).isEqualTo("Watch again")
        assertThat(upNextLabel(UpNext("none"))).isNull()
        assertThat(upNextLabel(null)).isNull()
        // Unknown future mode → no button rather than a wrong label.
        assertThat(upNextLabel(UpNext("shuffle", ep()))).isNull()
        // No numbers → bare verb.
        assertThat(upNextLabel(UpNext("resume", ep(null, null)))).isEqualTo("Resume")
    }

    @Test
    fun `markAllOptions follows the up-next mode`() {
        assertThat(markAllOptions(UpNext("none"))).isEqualTo(MarkAllOptions(watched = false, unwatched = false))
        assertThat(markAllOptions(UpNext("start", ep()))).isEqualTo(MarkAllOptions(watched = true, unwatched = false))
        assertThat(markAllOptions(UpNext("rewatch", ep()))).isEqualTo(MarkAllOptions(watched = false, unwatched = true))
        assertThat(markAllOptions(UpNext("next", ep()))).isEqualTo(MarkAllOptions(watched = true, unwatched = true))
        // Unknown (call failed / older server) → offer both.
        assertThat(markAllOptions(null)).isEqualTo(MarkAllOptions(watched = true, unwatched = true))
    }

    @Test
    fun `progressFraction clamps and guards missing values`() {
        assertThat(progressFraction(30, 120)).isWithin(0.001f).of(0.25f)
        assertThat(progressFraction(500, 100)).isEqualTo(1f)
        assertThat(progressFraction(null, 100)).isEqualTo(0f)
        assertThat(progressFraction(10, 0)).isEqualTo(0f)
        assertThat(progressFraction(0, 100)).isEqualTo(0f)
    }

    @Test
    fun `cardWatchBadge - show counts win over watch_state`() {
        val left = cardWatchBadge(media(type = "show", leaves = 10, unwatched = 3, watchState = "watched"))!!
        assertThat(left.watched).isFalse()
        assertThat(left.unwatchedCount).isEqualTo(3)
        assertThat(left.label).isEqualTo("3 unwatched episodes")
        assertThat(cardWatchBadge(media(type = "show", leaves = 10, unwatched = 1))!!.label)
            .isEqualTo("1 unwatched episode")
        val done = cardWatchBadge(media(type = "show", leaves = 10, unwatched = 0))!!
        assertThat(done.watched).isTrue()
        // A show with no episodes gets no badge.
        assertThat(cardWatchBadge(media(type = "show", leaves = 0, unwatched = 0))).isNull()
    }

    @Test
    fun `cardWatchBadge - videos`() {
        assertThat(cardWatchBadge(media(watchState = "watched"))!!.watched).isTrue()
        val prog = cardWatchBadge(media(watchState = "in_progress", offset = 50, duration = 100))!!
        assertThat(prog.progress).isWithin(0.001f).of(0.5f)
        assertThat(prog.label).isEqualTo("In progress, 50% watched")
        // In progress without a resume point → nothing to draw.
        assertThat(cardWatchBadge(media(watchState = "in_progress"))).isNull()
        assertThat(cardWatchBadge(media(watchState = "unwatched"))).isNull()
        // Older server: no fields at all → no badge.
        assertThat(cardWatchBadge(media())).isNull()
    }

    @Test
    fun `applyWatchedMark updates state, counts and clears the resume point`() {
        val movie = media(watchState = "in_progress", offset = 10, duration = 100)
        val watched = applyWatchedMark(movie, true)
        assertThat(watched.watch_state).isEqualTo("watched")
        assertThat(watched.view_offset_ms).isNull()
        assertThat(watched.unwatched_count).isNull()

        val show = media(type = "show", leaves = 8, unwatched = 5)
        assertThat(applyWatchedMark(show, true).unwatched_count).isEqualTo(0)
        assertThat(applyWatchedMark(show, false).unwatched_count).isEqualTo(8)
    }

    @Test
    fun `watchMenuActions offers what applies`() {
        assertThat(watchMenuActions(media())).containsExactly(true)
        assertThat(watchMenuActions(media(watchState = "watched"))).containsExactly(false)
        assertThat(watchMenuActions(media(watchState = "in_progress"))).containsExactly(true, false).inOrder()
        assertThat(watchMenuActions(media(type = "show", leaves = 4, unwatched = 4))).containsExactly(true)
        assertThat(watchMenuActions(media(type = "show", leaves = 4, unwatched = 0))).containsExactly(false)
        assertThat(watchMenuActions(media(type = "show", leaves = 4, unwatched = 2))).containsExactly(true, false).inOrder()
    }

    @Test
    fun `type gates`() {
        assertThat(supportsWatchState("movie")).isTrue()
        assertThat(supportsWatchState("show")).isTrue()
        assertThat(supportsWatchState("music")).isFalse()
        assertThat(supportsWatchState("photo")).isFalse()
        assertThat(supportsWatchState(null)).isFalse()

        assertThat(isWatchLeafType("movie")).isTrue()
        assertThat(isWatchLeafType("episode")).isTrue()
        assertThat(isWatchLeafType("show")).isFalse()
        assertThat(isWatchContainerType("season")).isTrue()
        assertThat(canMarkWatched("track")).isFalse()
        assertThat(canDismissContinueWatching("show")).isTrue()
        assertThat(canDismissContinueWatching("album")).isFalse()
    }

    @Test
    fun `leafPlayLabel - resume point, watched, fresh`() {
        assertThat(leafPlayLabel(resumeMs = 1_204_000, watched = false)).isEqualTo("Resume from 20:04")
        assertThat(leafPlayLabel(resumeMs = 4_804_000, watched = false)).isEqualTo("Resume from 1:20:04")
        // Rewatch in progress: still "watched", but the player resumes.
        assertThat(leafPlayLabel(resumeMs = 30_000, watched = true)).isEqualTo("Resume from 0:30")
        assertThat(leafPlayLabel(resumeMs = 0, watched = true)).isEqualTo("Watch again")
        assertThat(leafPlayLabel(resumeMs = 0, watched = false)).isEqualTo("Play")
    }

    @Test
    fun `formatTimecode pads minutes and seconds`() {
        assertThat(formatTimecode(0)).isEqualTo("0:00")
        assertThat(formatTimecode(65_000)).isEqualTo("1:05")
        assertThat(formatTimecode(3_600_000)).isEqualTo("1:00:00")
        assertThat(formatTimecode(-5)).isEqualTo("0:00")
    }

    @Test
    fun `a resume point without a duration draws no bar rather than failing`() {
        // The server omits duration_ms when it doesn't know it; neither the
        // hub tile nor the library card carries a file duration to fall back on.
        assertThat(progressFraction(1_204_000, null)).isEqualTo(0f)
        assertThat(progressFraction(1_204_000, 0)).isEqualTo(0f)
        assertThat(cardWatchBadge(media(watchState = "in_progress", offset = 1_204_000))).isNull()
    }
}
