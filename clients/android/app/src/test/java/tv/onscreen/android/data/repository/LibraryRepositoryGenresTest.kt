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
    fun `letters forward the listing query and map 404 to null`() = runTest {
        val api = mockk<OnScreenApi>()
        coEvery { api.getLibraryLetters("lib1", "title", "desc", "Drama", "unwatched") } returns
            ApiResponse(listOf(tv.onscreen.android.data.model.LibraryLetter("Z", 2, 0)))
        coEvery { api.getLibraryLetters("old", any(), any(), any(), any()) } throws
            retrofit2.HttpException(retrofit2.Response.error<Any>(404, "".toResponseBody(null)))
        coEvery { api.getLibraryLetters("down", any(), any(), any(), any()) } throws
            retrofit2.HttpException(retrofit2.Response.error<Any>(503, "".toResponseBody(null)))
        val repo = LibraryRepository(api)

        assertThat(repo.getLetters("lib1", "title", "desc", "Drama", "unwatched")?.map { it.letter })
            .containsExactly("Z")
        assertThat(repo.getLetters("old", "title", "asc")).isNull()
        val failure = runCatching { repo.getLetters("down", "title", "asc") }.exceptionOrNull()
        assertThat(failure).isInstanceOf(retrofit2.HttpException::class.java)
    }

    @Test
    fun `item position forwards the listing query and maps 404 to null`() = runTest {
        val api = mockk<OnScreenApi>()
        coEvery { api.getLibraryItemPosition("lib1", "p1", "created_at", "desc", null, null) } returns
            ApiResponse(tv.onscreen.android.data.model.LibraryItemPosition(offset = 4_321, total = 9_000))
        coEvery { api.getLibraryItemPosition("old", any(), any(), any(), any(), any()) } throws
            retrofit2.HttpException(retrofit2.Response.error<Any>(404, "".toResponseBody(null)))
        coEvery { api.getLibraryItemPosition("down", any(), any(), any(), any(), any()) } throws
            retrofit2.HttpException(retrofit2.Response.error<Any>(503, "".toResponseBody(null)))
        val repo = LibraryRepository(api)

        assertThat(repo.itemPosition("lib1", "p1", "created_at", "desc")).isEqualTo(4_321)
        assertThat(repo.itemPosition("old", "p1")).isNull()
        val failure = runCatching { repo.itemPosition("down", "p1") }.exceptionOrNull()
        assertThat(failure).isInstanceOf(retrofit2.HttpException::class.java)
    }

    @Test
    fun `item position decodes the server's offset and total`() {
        val type = com.squareup.moshi.Types.newParameterizedType(
            ApiResponse::class.java,
            tv.onscreen.android.data.model.LibraryItemPosition::class.java,
        )
        val decoded = com.squareup.moshi.Moshi.Builder().build()
            .adapter<ApiResponse<tv.onscreen.android.data.model.LibraryItemPosition>>(type)
            .fromJson("""{"data":{"offset":120,"total":4100}}""")!!.data

        assertThat(decoded.offset).isEqualTo(120)
        assertThat(decoded.total).isEqualTo(4_100)
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
