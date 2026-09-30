package tv.onscreen.mobile.data.repository

import com.google.common.truth.Truth.assertThat
import io.mockk.coEvery
import io.mockk.coVerify
import io.mockk.mockk
import kotlinx.coroutines.test.runTest
import org.junit.Test
import tv.onscreen.mobile.data.api.ApiResponse
import tv.onscreen.mobile.data.api.OnScreenApi
import tv.onscreen.mobile.data.model.ItemDetail

/**
 * The in-memory copy of recently fetched items, which lets the now-playing
 * screen show the item the background service is playing at once.
 */
class ItemRepositoryRecentItemsTest {

    private fun api(): OnScreenApi = mockk<OnScreenApi>().also { api ->
        coEvery { api.getItem(any()) } answers {
            val id = firstArg<String>()
            ApiResponse(ItemDetail(id = id, library_id = "lib", title = "Item $id", type = "track"))
        }
    }

    @Test
    fun `a fetched item is there afterwards without the network`() = runTest {
        val api = api()
        val repo = ItemRepository(api)
        assertThat(repo.cachedItem("t-1")).isNull()

        repo.getItem("t-1")

        assertThat(repo.cachedItem("t-1")?.title).isEqualTo("Item t-1")
        assertThat(repo.cachedItem("t-2")).isNull()
        coVerify(exactly = 1) { api.getItem(any()) }
    }

    @Test
    fun `the copy is replaced by the next fetch`() = runTest {
        val api = mockk<OnScreenApi>()
        coEvery { api.getItem("t-1") } returnsMany listOf(
            ApiResponse(ItemDetail(id = "t-1", library_id = "lib", title = "Before", type = "track")),
            ApiResponse(ItemDetail(id = "t-1", library_id = "lib", title = "After", type = "track")),
        )
        val repo = ItemRepository(api)

        repo.getItem("t-1")
        repo.getItem("t-1")

        assertThat(repo.cachedItem("t-1")?.title).isEqualTo("After")
    }

    @Test
    fun `it holds a bounded number, dropping the least recently used`() = runTest {
        val repo = ItemRepository(api())
        repo.getItem("first")
        repo.getItem("kept")
        repeat(62) { repo.getItem("filler-$it") }
        // Touch "kept" so it is no longer the oldest, then go one past the cap.
        assertThat(repo.cachedItem("kept")).isNotNull()
        repo.getItem("one-more")

        assertThat(repo.cachedItem("first")).isNull()
        assertThat(repo.cachedItem("kept")).isNotNull()
        assertThat(repo.cachedItem("one-more")).isNotNull()
    }
}
