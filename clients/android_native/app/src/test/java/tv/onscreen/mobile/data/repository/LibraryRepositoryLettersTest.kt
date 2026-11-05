package tv.onscreen.mobile.data.repository

import com.google.common.truth.Truth.assertThat
import io.mockk.coEvery
import io.mockk.coVerify
import io.mockk.mockk
import kotlinx.coroutines.test.runTest
import okhttp3.ResponseBody.Companion.toResponseBody
import org.junit.Assert.assertThrows
import org.junit.Test
import retrofit2.HttpException
import retrofit2.Response
import tv.onscreen.mobile.data.api.ApiResponse
import tv.onscreen.mobile.data.api.OnScreenApi
import tv.onscreen.mobile.data.model.LibraryLetter

/** The A-Z jump index (`/libraries/{id}/items/letters`, v2.7). */
class LibraryRepositoryLettersTest {

    private fun http(code: Int) = HttpException(Response.error<Unit>(code, "".toResponseBody(null)))

    @Test
    fun `passes the listing's parameters through`() = runTest {
        val api = mockk<OnScreenApi>()
        coEvery { api.getLibraryItemLetters("L", any(), any(), any(), any()) } returns
            ApiResponse(listOf(LibraryLetter("A", 3, 0), LibraryLetter("B", 1, 3)))
        val repo = LibraryRepository(api)

        val letters = repo.getLetters("L", watch = "unwatched")

        assertThat(letters.map { it.letter }).containsExactly("A", "B").inOrder()
        coVerify { api.getLibraryItemLetters("L", null, null, null, "unwatched") }
    }

    @Test
    fun `an older server without the endpoint means no index`() = runTest {
        val api = mockk<OnScreenApi>()
        coEvery { api.getLibraryItemLetters(any(), any(), any(), any(), any()) } throws http(404)
        assertThat(LibraryRepository(api).getLetters("L")).isEmpty()
    }

    @Test
    fun `other failures throw`() {
        val api = mockk<OnScreenApi>()
        coEvery { api.getLibraryItemLetters(any(), any(), any(), any(), any()) } throws http(500)
        assertThrows(HttpException::class.java) {
            kotlinx.coroutines.runBlocking { LibraryRepository(api).getLetters("L") }
        }
    }
}
