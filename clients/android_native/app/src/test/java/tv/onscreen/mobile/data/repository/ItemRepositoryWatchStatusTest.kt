package tv.onscreen.mobile.data.repository

import com.google.common.truth.Truth.assertThat
import io.mockk.coEvery
import io.mockk.coVerify
import io.mockk.mockk
import kotlinx.coroutines.ExperimentalCoroutinesApi
import kotlinx.coroutines.test.runTest
import okhttp3.MediaType.Companion.toMediaType
import okhttp3.ResponseBody.Companion.toResponseBody
import org.junit.Test
import retrofit2.HttpException
import retrofit2.Response
import tv.onscreen.mobile.data.api.ApiResponse
import tv.onscreen.mobile.data.api.OnScreenApi
import tv.onscreen.mobile.data.model.WatchStatus
import tv.onscreen.mobile.data.model.WatchStatusRequest
import tv.onscreen.mobile.data.model.WatchStatusResponse

/**
 * Tests for the watching-status path through [ItemRepository]. Three
 * behaviours under test:
 *   - getWatchStatus maps the wire string to the enum
 *   - getWatchStatus turns server 404 (no row) into null instead of
 *     throwing — caller relies on null for "no status set"
 *   - setWatchStatus serialises the enum's `.wire` into the request body
 */
@OptIn(ExperimentalCoroutinesApi::class)
class ItemRepositoryWatchStatusTest {

    private fun http404(): HttpException {
        // Manufacture a Response with status 404 + empty body so we
        // can construct the HttpException Retrofit raises on a 4xx.
        val resp = Response.error<Any>(
            404,
            "".toResponseBody("application/json".toMediaType()),
        )
        return HttpException(resp)
    }

    @Test
    fun `getWatchStatus parses the wire string into the enum`() = runTest {
        val api = mockk<OnScreenApi>()
        coEvery { api.getWatchStatus("item-1") } returns ApiResponse(
            WatchStatusResponse(
                status = "watching",
                created_at = "2026-05-05T00:00:00Z",
                updated_at = "2026-05-05T00:00:00Z",
            ),
        )
        val repo = ItemRepository(api)
        assertThat(repo.getWatchStatus("item-1")).isEqualTo(WatchStatus.WATCHING)
    }

    @Test
    fun `getWatchStatus turns 404 into null (no row state)`() = runTest {
        val api = mockk<OnScreenApi>()
        coEvery { api.getWatchStatus("item-2") } throws http404()
        val repo = ItemRepository(api)
        assertThat(repo.getWatchStatus("item-2")).isNull()
    }

    @Test
    fun `getWatchStatus returns null when the server emits an unknown status`() = runTest {
        // Forwards-compat: a future server adding a sixth status string
        // shouldn't crash an older client. Repo + enum together let
        // the UI treat unknowns as "no row."
        val api = mockk<OnScreenApi>()
        coEvery { api.getWatchStatus("item-3") } returns ApiResponse(
            WatchStatusResponse(
                status = "rewatching", // not in the v1 set
                created_at = "2026-05-05T00:00:00Z",
                updated_at = "2026-05-05T00:00:00Z",
            ),
        )
        val repo = ItemRepository(api)
        assertThat(repo.getWatchStatus("item-3")).isNull()
    }

    @Test
    fun `setWatchStatus sends the wire string and parses the response`() = runTest {
        val api = mockk<OnScreenApi>()
        coEvery {
            api.setWatchStatus("item-4", WatchStatusRequest("on_hold"))
        } returns ApiResponse(
            WatchStatusResponse(
                status = "on_hold",
                created_at = "2026-05-05T00:00:00Z",
                updated_at = "2026-05-05T00:00:00Z",
            ),
        )
        val repo = ItemRepository(api)
        assertThat(repo.setWatchStatus("item-4", WatchStatus.ON_HOLD))
            .isEqualTo(WatchStatus.ON_HOLD)
        coVerify { api.setWatchStatus("item-4", WatchStatusRequest("on_hold")) }
    }

    @Test
    fun `clearWatchStatus delegates straight through (server is idempotent)`() = runTest {
        val api = mockk<OnScreenApi>(relaxed = true)
        val repo = ItemRepository(api)
        repo.clearWatchStatus("item-5")
        coVerify { api.clearWatchStatus("item-5") }
    }
}
