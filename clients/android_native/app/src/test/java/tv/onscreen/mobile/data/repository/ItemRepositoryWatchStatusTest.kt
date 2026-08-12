package tv.onscreen.mobile.data.repository

import com.google.common.truth.Truth.assertThat
import io.mockk.coEvery
import io.mockk.coVerify
import io.mockk.mockk
import kotlinx.coroutines.ExperimentalCoroutinesApi
import kotlinx.coroutines.test.runTest
import org.junit.Test
import tv.onscreen.mobile.data.api.OnScreenApi
import tv.onscreen.mobile.data.model.WatchStatus
import tv.onscreen.mobile.data.model.WatchStatusRequest
import tv.onscreen.mobile.data.model.WatchStatusResponse

/**
 * Tests for the watching-status path through [ItemRepository]. Three
 * behaviours under test:
 *   - getWatchStatus maps the wire string to the enum
 *   - getWatchStatus turns the server's "no row yet" sentinel
 *     (status="") into null — caller relies on null for "no status set"
 *   - setWatchStatus serialises the enum's `.wire` into the request body
 *
 * The server (post-PR #27) always returns 200 with `status = ""` for the
 * no-row case; we previously caught a 404 there, which is now dead code on
 * both sides. The contract this test pins is the new one.
 *
 * NOTE: the watch-status endpoint returns a BARE body (respond.JSON), not
 * the `{data:{…}}` envelope, so the Retrofit methods deserialize
 * WatchStatusResponse directly — the mocks below return it unwrapped.
 */
@OptIn(ExperimentalCoroutinesApi::class)
class ItemRepositoryWatchStatusTest {

    @Test
    fun `getWatchStatus parses the wire string into the enum`() = runTest {
        val api = mockk<OnScreenApi>()
        coEvery { api.getWatchStatus("item-1") } returns WatchStatusResponse(
            status = "watching",
            created_at = "2026-05-05T00:00:00Z",
            updated_at = "2026-05-05T00:00:00Z",
        )
        val repo = ItemRepository(api)
        assertThat(repo.getWatchStatus("item-1")).isEqualTo(WatchStatus.WATCHING)
    }

    @Test
    fun `getWatchStatus turns the server's empty-status sentinel into null`() = runTest {
        // Post-PR-#27 the no-row case is 200 with status="" (server-side
        // fix for a browser-console-noise issue on /watch). The
        // repository collapses that to null so callers see the same
        // shape they did before, without pattern-matching on HTTP code.
        val api = mockk<OnScreenApi>()
        coEvery { api.getWatchStatus("item-2") } returns WatchStatusResponse(
            status = "",
            created_at = "0001-01-01T00:00:00Z",
            updated_at = "0001-01-01T00:00:00Z",
        )
        val repo = ItemRepository(api)
        assertThat(repo.getWatchStatus("item-2")).isNull()
    }

    @Test
    fun `getWatchStatus returns null when the server emits an unknown status`() = runTest {
        // Forwards-compat: a future server adding a sixth status string
        // shouldn't crash an older client. Repo + enum together let
        // the UI treat unknowns as "no row."
        val api = mockk<OnScreenApi>()
        coEvery { api.getWatchStatus("item-3") } returns WatchStatusResponse(
            status = "rewatching", // not in the v1 set
            created_at = "2026-05-05T00:00:00Z",
            updated_at = "2026-05-05T00:00:00Z",
        )
        val repo = ItemRepository(api)
        assertThat(repo.getWatchStatus("item-3")).isNull()
    }

    @Test
    fun `setWatchStatus sends the wire string and parses the response`() = runTest {
        val api = mockk<OnScreenApi>()
        coEvery {
            api.setWatchStatus("item-4", WatchStatusRequest("on_hold"))
        } returns WatchStatusResponse(
            status = "on_hold",
            created_at = "2026-05-05T00:00:00Z",
            updated_at = "2026-05-05T00:00:00Z",
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
