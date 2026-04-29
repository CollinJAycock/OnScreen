package tv.onscreen.mobile.playback

import com.google.common.truth.Truth.assertThat
import io.mockk.coEvery
import io.mockk.mockk
import kotlinx.coroutines.ExperimentalCoroutinesApi
import kotlinx.coroutines.test.runTest
import org.junit.Test
import tv.onscreen.mobile.data.model.ChildItem
import tv.onscreen.mobile.data.model.ItemDetail
import tv.onscreen.mobile.data.repository.ItemRepository

@OptIn(ExperimentalCoroutinesApi::class)
class NextSiblingResolverTest {

    private fun child(id: String, type: String, index: Int? = null, year: Int? = null) =
        ChildItem(id = id, title = "t-$id", type = type, index = index, year = year)

    @Test
    fun `null parent or index returns null`() = runTest {
        val resolver = NextSiblingResolver(mockk())
        assertThat(resolver.resolve("a", "track", null, 1)).isNull()
        assertThat(resolver.resolve("a", "track", "p", null)).isNull()
    }

    @Test
    fun `next index in same container is preferred`() = runTest {
        val repo = mockk<ItemRepository>()
        coEvery { repo.getChildren("alb") } returns listOf(
            child("t1", "track", index = 1),
            child("t2", "track", index = 2),
            child("t3", "track", index = 3),
        )
        val next = NextSiblingResolver(repo).resolve("t2", "track", "alb", 2)
        assertThat(next?.id).isEqualTo("t3")
    }

    @Test
    fun `track at end of album falls through to first track of next album by year`() = runTest {
        val repo = mockk<ItemRepository>()
        // album A finishes — only one track.
        coEvery { repo.getChildren("alb-a") } returns listOf(
            child("t-last", "track", index = 1),
        )
        // album A's parent is the artist, with two albums by year.
        coEvery { repo.getItem("alb-a") } returns ItemDetail(
            id = "alb-a", library_id = "lib", title = "A", type = "album",
            parent_id = "artist", year = 2020,
        )
        coEvery { repo.getChildren("artist") } returns listOf(
            child("alb-a", "album", year = 2020, index = 1),
            child("alb-b", "album", year = 2022, index = 2),
        )
        coEvery { repo.getChildren("alb-b") } returns listOf(
            child("first-of-b", "track", index = 1),
            child("second-of-b", "track", index = 2),
        )

        val next = NextSiblingResolver(repo).resolve("t-last", "track", "alb-a", 1)
        assertThat(next?.id).isEqualTo("first-of-b")
    }

    @Test
    fun `episode at end of season falls through to first episode of next season`() = runTest {
        val repo = mockk<ItemRepository>()
        coEvery { repo.getChildren("s4") } returns listOf(
            child("s4e12", "episode", index = 12),
        )
        coEvery { repo.getItem("s4") } returns ItemDetail(
            id = "s4", library_id = "lib", title = "S4", type = "season",
            parent_id = "show", index = 4,
        )
        coEvery { repo.getChildren("show") } returns listOf(
            child("s4", "season", index = 4),
            child("s5", "season", index = 5),
        )
        coEvery { repo.getChildren("s5") } returns listOf(
            child("s5e1", "episode", index = 1),
            child("s5e2", "episode", index = 2),
        )

        val next = NextSiblingResolver(repo).resolve("s4e12", "episode", "s4", 12)
        assertThat(next?.id).isEqualTo("s5e1")
    }

    @Test
    fun `last item of last container returns null`() = runTest {
        val repo = mockk<ItemRepository>()
        coEvery { repo.getChildren("alb-z") } returns listOf(
            child("only", "track", index = 1),
        )
        coEvery { repo.getItem("alb-z") } returns ItemDetail(
            id = "alb-z", library_id = "lib", title = "Z", type = "album",
            parent_id = "artist", year = 2024,
        )
        coEvery { repo.getChildren("artist") } returns listOf(
            child("alb-z", "album", year = 2024),
        )
        val next = NextSiblingResolver(repo).resolve("only", "track", "alb-z", 1)
        assertThat(next).isNull()
    }

    @Test
    fun `non-track non-episode types do not fall through`() = runTest {
        // Movies don't auto-advance — last "movie" in a list returns null.
        val repo = mockk<ItemRepository>()
        coEvery { repo.getChildren("col") } returns listOf(
            child("m1", "movie", index = 1),
        )
        val next = NextSiblingResolver(repo).resolve("m1", "movie", "col", 1)
        assertThat(next).isNull()
    }
}
