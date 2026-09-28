package tv.onscreen.android.playback

import com.google.common.truth.Truth.assertThat
import io.mockk.coEvery
import io.mockk.mockk
import kotlinx.coroutines.test.runTest
import org.junit.Test
import tv.onscreen.android.data.model.ChildItem
import tv.onscreen.android.data.model.ItemDetail
import tv.onscreen.android.data.repository.ItemRepository

/**
 * Auto-advance resolution, shared by the in-fragment Up Next overlay and the
 * MediaSessionService's background chain.
 *
 * The load-bearing case is a GAP in numbering: libraries are routinely missing
 * an episode or a track. Matching `currentIndex + 1` exactly finds nothing
 * there and falls through to the cross-container branch, which jumps to the
 * next season / album entirely — so a missing episode 4 sent the viewer from
 * S01E03 to S02E01, skipping the rest of the season.
 */
class NextSiblingResolverTest {

    private fun ep(id: String, index: Int) =
        ChildItem(id = id, title = id, type = "episode", index = index)

    private fun track(id: String, index: Int, disc: Int? = null) =
        ChildItem(id = id, title = id, type = "track", index = index, disc_number = disc)

    private fun repo() = mockk<ItemRepository>()

    // ── In-container resolution ─────────────────────────────────────────────

    @Test
    fun `resolves the immediate next episode`() = runTest {
        val repo = repo()
        coEvery { repo.getChildren("season-1") } returns
            listOf(ep("e1", 1), ep("e2", 2), ep("e3", 3))

        val next = NextSiblingResolver(repo).resolve("e1", "episode", "season-1", 1)

        assertThat(next?.id).isEqualTo("e2")
    }

    @Test
    fun `steps over a numbering gap instead of leaving the season`() = runTest {
        val repo = repo()
        // Episode 2 is missing from the library.
        coEvery { repo.getChildren("season-1") } returns
            listOf(ep("e1", 1), ep("e3", 3), ep("e4", 4))

        val next = NextSiblingResolver(repo).resolve("e1", "episode", "season-1", 1)

        assertThat(next?.id).isEqualTo("e3")
    }

    @Test
    fun `picks the nearest greater index, not merely any greater one`() = runTest {
        val repo = repo()
        // Deliberately unsorted input with a wide gap.
        coEvery { repo.getChildren("season-1") } returns
            listOf(ep("e9", 9), ep("e5", 5), ep("e7", 7))

        val next = NextSiblingResolver(repo).resolve("e1", "episode", "season-1", 1)

        assertThat(next?.id).isEqualTo("e5")
    }

    @Test
    fun `ignores siblings of a different type`() = runTest {
        val repo = repo()
        coEvery { repo.getChildren("album-1") } returns listOf(
            track("t1", 1),
            ChildItem(id = "vid", title = "vid", type = "music_video", index = 2),
            track("t3", 3),
        )

        val next = NextSiblingResolver(repo).resolve("t1", "track", "album-1", 1)

        assertThat(next?.id).isEqualTo("t3")
    }

    // ── Multi-disc albums ───────────────────────────────────────────────────
    // Each disc numbers its tracks from 1, so by index alone the discs
    // interleave (disc 2 track 1 sorts next to disc 1 track 1).

    @Test
    fun `the last track of disc 1 continues on disc 2`() = runTest {
        val repo = repo()
        coEvery { repo.getChildren("album-1") } returns listOf(
            track("d1t1", 1, disc = 1), track("d1t2", 2, disc = 1),
            track("d2t1", 1, disc = 2), track("d2t2", 2, disc = 2),
        )

        val next = NextSiblingResolver(repo).resolve("d1t2", "track", "album-1", 2)

        assertThat(next?.id).isEqualTo("d2t1")
    }

    @Test
    fun `a disc 2 track advances on disc 2, not back to disc 1`() = runTest {
        val repo = repo()
        coEvery { repo.getChildren("album-1") } returns listOf(
            track("d1t1", 1, disc = 1), track("d1t2", 2, disc = 1), track("d1t3", 3, disc = 1),
            track("d2t1", 1, disc = 2), track("d2t2", 2, disc = 2),
        )

        val next = NextSiblingResolver(repo).resolve("d2t1", "track", "album-1", 1)

        assertThat(next?.id).isEqualTo("d2t2")
    }

    @Test
    fun `a track with no disc number is on disc 1`() = runTest {
        val repo = repo()
        coEvery { repo.getChildren("album-1") } returns
            listOf(track("d2t1", 1, disc = 2), track("t2", 2), track("t1", 1))

        val next = NextSiblingResolver(repo).resolve("t2", "track", "album-1", 2)

        assertThat(next?.id).isEqualTo("d2t1")
    }

    // ── Cross-container fall-through ────────────────────────────────────────

    @Test
    fun `the next album starts at disc 1 track 1`() = runTest {
        val repo = repo()
        coEvery { repo.getChildren("album-a") } returns listOf(track("a1", 1))
        coEvery { repo.getItem("album-a") } returns ItemDetail(
            id = "album-a", library_id = "lib", title = "A", type = "album", parent_id = "artist-1",
        )
        coEvery { repo.getChildren("artist-1") } returns listOf(
            ChildItem(id = "album-a", title = "A", type = "album", year = 2001),
            ChildItem(id = "album-b", title = "B", type = "album", year = 2003),
        )
        // Deliberately listed disc 2 first.
        coEvery { repo.getChildren("album-b") } returns listOf(
            track("b-d2t1", 1, disc = 2), track("b-d1t1", 1, disc = 1), track("b-d1t2", 2, disc = 1),
        )

        val next = NextSiblingResolver(repo).resolve("a1", "track", "album-a", 1)

        assertThat(next?.id).isEqualTo("b-d1t1")
    }

    @Test
    fun `falls through to the next season when the current one is exhausted`() = runTest {
        val repo = repo()
        coEvery { repo.getChildren("season-1") } returns listOf(ep("e1", 1), ep("e2", 2))
        coEvery { repo.getItem("season-1") } returns ItemDetail(
            id = "season-1", library_id = "lib", title = "S1", type = "season", parent_id = "show-1",
        )
        coEvery { repo.getChildren("show-1") } returns listOf(
            ChildItem(id = "season-1", title = "S1", type = "season", index = 1),
            ChildItem(id = "season-2", title = "S2", type = "season", index = 2),
        )
        coEvery { repo.getChildren("season-2") } returns listOf(ep("s2e1", 1), ep("s2e2", 2))

        val next = NextSiblingResolver(repo).resolve("e2", "episode", "season-1", 2)

        assertThat(next?.id).isEqualTo("s2e1")
    }

    @Test
    fun `returns null at the end of the last season`() = runTest {
        val repo = repo()
        coEvery { repo.getChildren("season-2") } returns listOf(ep("s2e1", 1))
        coEvery { repo.getItem("season-2") } returns ItemDetail(
            id = "season-2", library_id = "lib", title = "S2", type = "season", parent_id = "show-1",
        )
        coEvery { repo.getChildren("show-1") } returns listOf(
            ChildItem(id = "season-1", title = "S1", type = "season", index = 1),
            ChildItem(id = "season-2", title = "S2", type = "season", index = 2),
        )

        val next = NextSiblingResolver(repo).resolve("s2e1", "episode", "season-2", 1)

        assertThat(next).isNull()
    }

    // ── Guards ──────────────────────────────────────────────────────────────

    @Test
    fun `returns null without a parent or index`() = runTest {
        val repo = repo()

        assertThat(NextSiblingResolver(repo).resolve("x", "episode", null, 1)).isNull()
        assertThat(NextSiblingResolver(repo).resolve("x", "episode", "season-1", null)).isNull()
    }

    @Test
    fun `movies never auto-advance`() = runTest {
        val repo = repo()
        coEvery { repo.getChildren("lib-1") } returns emptyList()
        coEvery { repo.getItem("lib-1") } returns ItemDetail(
            id = "lib-1", library_id = "lib", title = "L", type = "library", parent_id = null,
        )

        val next = NextSiblingResolver(repo).resolve("m1", "movie", "lib-1", 1)

        assertThat(next).isNull()
    }

    @Test
    fun `a repository failure resolves to null instead of throwing`() = runTest {
        val repo = repo()
        coEvery { repo.getChildren(any()) } throws RuntimeException("offline")

        val next = NextSiblingResolver(repo).resolve("e1", "episode", "season-1", 1)

        assertThat(next).isNull()
    }
}
