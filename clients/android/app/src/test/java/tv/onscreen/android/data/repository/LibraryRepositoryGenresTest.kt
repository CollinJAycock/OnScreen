package tv.onscreen.android.data.repository

import com.google.common.truth.Truth.assertThat
import io.mockk.coEvery
import io.mockk.mockk
import kotlinx.coroutines.test.runTest
import okhttp3.ResponseBody.Companion.toResponseBody
import org.junit.Test
import tv.onscreen.android.data.api.ApiResponse
import tv.onscreen.android.data.api.OnScreenApi
import tv.onscreen.android.data.model.GenreCount

class LibraryRepositoryGenresTest {

    // The endpoint returns [{"name":"Action","count":42}]. The client used to
    // declare List<String>, so Moshi threw on every response and the
    // catch-all turned it into an empty list — the genre filter was
    // permanently empty with nothing logged and nothing to debug.
    @Test
    fun `genres decode from the server's name-count objects`() = runTest {
        val api = mockk<OnScreenApi>()
        coEvery { api.getLibraryGenres("lib1") } returns ApiResponse(
            listOf(GenreCount("Action", 42), GenreCount("Drama", 7)),
        )
        val repo = LibraryRepository(api)

        assertThat(repo.getGenres("lib1")).containsExactly("Action", "Drama").inOrder()
    }

    @Test
    fun `genres fall back to empty on failure rather than failing the browse screen`() = runTest {
        val api = mockk<OnScreenApi>()
        coEvery { api.getLibraryGenres(any()) } throws RuntimeException("offline")
        val repo = LibraryRepository(api)

        assertThat(repo.getGenres("lib1")).isEmpty()
    }
    @Test
    fun `random pick maps 404 to null and passes the filters`() = runTest {
        val api = mockk<OnScreenApi>()
        coEvery { api.getRandomLibraryItem("lib1", "Drama", "unwatched") } returns
            ApiResponse(tv.onscreen.android.data.model.RandomLibraryItem("m1", "movie"))
        coEvery { api.getRandomLibraryItem("lib1", null, "watched") } throws
            retrofit2.HttpException(retrofit2.Response.error<Any>(404, "".toResponseBody(null)))
        val repo = LibraryRepository(api)

        assertThat(repo.pickRandom("lib1", "Drama", "unwatched")?.id).isEqualTo("m1")
        assertThat(repo.pickRandom("lib1", null, "watched")).isNull()
    }

    @Test
    fun `library items forward the watch filter`() = runTest {
        val api = mockk<OnScreenApi>()
        coEvery { api.getLibraryItems("lib1", 50, 0, "title", "asc", null, "in_progress") } returns
            tv.onscreen.android.data.api.ApiListResponse(emptyList(), tv.onscreen.android.data.api.Meta(0, null))
        val repo = LibraryRepository(api)

        assertThat(repo.getItems("lib1", 50, 0, "title", "asc", null, "in_progress").second).isEqualTo(0)
    }
}
