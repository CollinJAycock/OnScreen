package tv.onscreen.mobile.data.repository

import com.google.common.truth.Truth.assertThat
import com.squareup.moshi.Moshi
import com.squareup.moshi.Types
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
import tv.onscreen.mobile.data.model.LibraryItemPosition

/** An item's row in a library listing (`/libraries/{id}/items/position`). */
class LibraryRepositoryPositionTest {

    private fun http(code: Int) = HttpException(Response.error<Unit>(code, "".toResponseBody(null)))

    @Test
    fun `passes the item and the listing's parameters through`() = runTest {
        val api = mockk<OnScreenApi>()
        coEvery { api.getLibraryItemPosition("L", "p1", any(), any(), any(), any(), any()) } returns
            ApiResponse(LibraryItemPosition(offset = 4_321, total = 9_000))

        assertThat(LibraryRepository(api).itemPosition("L", "p1", watch = "unwatched")).isEqualTo(4_321)
        coVerify { api.getLibraryItemPosition("L", "p1", null, null, null, "unwatched", null) }
    }

    @Test
    fun `a 404 means the server can't say`() = runTest {
        val api = mockk<OnScreenApi>()
        coEvery { api.getLibraryItemPosition(any(), any(), any(), any(), any(), any(), any()) } throws http(404)
        assertThat(LibraryRepository(api).itemPosition("L", "p1")).isNull()
    }

    @Test
    fun `other failures throw`() {
        val api = mockk<OnScreenApi>()
        coEvery { api.getLibraryItemPosition(any(), any(), any(), any(), any(), any(), any()) } throws http(500)
        assertThrows(HttpException::class.java) {
            kotlinx.coroutines.runBlocking { LibraryRepository(api).itemPosition("L", "p1") }
        }
    }

    @Test
    fun `decodes the server's offset and total`() {
        val type = Types.newParameterizedType(ApiResponse::class.java, LibraryItemPosition::class.java)
        val decoded = Moshi.Builder().build()
            .adapter<ApiResponse<LibraryItemPosition>>(type)
            .fromJson("""{"data":{"offset":120,"total":4100}}""")!!.data

        assertThat(decoded).isEqualTo(LibraryItemPosition(offset = 120, total = 4_100))
    }
}
