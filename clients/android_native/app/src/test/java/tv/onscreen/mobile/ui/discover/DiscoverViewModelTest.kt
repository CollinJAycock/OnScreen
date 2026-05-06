package tv.onscreen.mobile.ui.discover

import com.google.common.truth.Truth.assertThat
import io.mockk.coEvery
import io.mockk.coVerify
import io.mockk.mockk
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.ExperimentalCoroutinesApi
import kotlinx.coroutines.test.StandardTestDispatcher
import kotlinx.coroutines.test.advanceUntilIdle
import kotlinx.coroutines.test.resetMain
import kotlinx.coroutines.test.runTest
import kotlinx.coroutines.test.setMain
import org.junit.After
import org.junit.Before
import org.junit.Test
import tv.onscreen.mobile.data.model.DiscoverItem
import tv.onscreen.mobile.data.model.MediaRequest
import tv.onscreen.mobile.data.repository.DiscoverRepository

/**
 * Tests the discover/request UI state machine. We exercise:
 *   - empty/blank queries don't fire a search
 *   - successful search populates results
 *   - submit flips submitting → submitted optimistically
 *   - submit failure reverts submitting + surfaces the error
 *   - already-submitted / in-library / has-active-request items skip
 */
@OptIn(ExperimentalCoroutinesApi::class)
class DiscoverViewModelTest {

    private val dispatcher = StandardTestDispatcher()

    @Before fun setUp() { Dispatchers.setMain(dispatcher) }
    @After  fun tearDown() { Dispatchers.resetMain() }

    private fun item(
        id: Int,
        title: String = "T",
        inLibrary: Boolean = false,
        hasActive: Boolean = false,
    ) = DiscoverItem(
        type = "movie",
        tmdb_id = id,
        title = title,
        in_library = inLibrary,
        has_active_request = hasActive,
    )

    @Test
    fun `blank query does not fire a search`() = runTest {
        val repo = mockk<DiscoverRepository>(relaxed = true)
        val vm = DiscoverViewModel(repo)
        vm.onQueryChanged("   ")
        vm.search()
        advanceUntilIdle()
        coVerify(exactly = 0) { repo.search(any(), any()) }
    }

    @Test
    fun `search populates results and clears loading`() = runTest {
        val repo = mockk<DiscoverRepository>()
        val results = listOf(item(1, "A"), item(2, "B"))
        coEvery { repo.search("matrix", 12) } returns results
        val vm = DiscoverViewModel(repo)
        vm.onQueryChanged("matrix")
        vm.search()
        // Initial flip to loading
        assertThat(vm.state.value.loading).isTrue()
        advanceUntilIdle()
        assertThat(vm.state.value.loading).isFalse()
        assertThat(vm.state.value.results).isEqualTo(results)
        assertThat(vm.state.value.error).isNull()
    }

    @Test
    fun `search failure surfaces the error message`() = runTest {
        val repo = mockk<DiscoverRepository>()
        coEvery { repo.search(any(), any()) } throws RuntimeException("TMDB rate limited")
        val vm = DiscoverViewModel(repo)
        vm.onQueryChanged("anything")
        vm.search()
        advanceUntilIdle()
        assertThat(vm.state.value.error).isEqualTo("TMDB rate limited")
        assertThat(vm.state.value.loading).isFalse()
    }

    @Test
    fun `request flips submitting then submitted on success`() = runTest {
        val repo = mockk<DiscoverRepository>()
        coEvery { repo.createRequest("movie", 42) } returns MediaRequest(
            id = "r1", user_id = "u1", type = "movie", tmdb_id = 42,
            title = "X", status = "pending",
        )
        val vm = DiscoverViewModel(repo)
        vm.request(item(42))
        // Optimistic flip happens synchronously before the coroutine runs.
        assertThat(vm.state.value.submitting).contains(42)
        advanceUntilIdle()
        assertThat(vm.state.value.submitting).doesNotContain(42)
        assertThat(vm.state.value.submitted).contains(42)
        assertThat(vm.state.value.error).isNull()
    }

    @Test
    fun `request failure clears submitting and surfaces the error`() = runTest {
        val repo = mockk<DiscoverRepository>()
        coEvery { repo.createRequest(any(), any()) } throws RuntimeException("boom")
        val vm = DiscoverViewModel(repo)
        vm.request(item(99))
        advanceUntilIdle()
        assertThat(vm.state.value.submitting).doesNotContain(99)
        // Submitted set must NOT have the id — request failed.
        assertThat(vm.state.value.submitted).doesNotContain(99)
        assertThat(vm.state.value.error).isEqualTo("boom")
    }

    @Test
    fun `request skips items that are already in library`() = runTest {
        val repo = mockk<DiscoverRepository>(relaxed = true)
        val vm = DiscoverViewModel(repo)
        vm.request(item(1, inLibrary = true))
        advanceUntilIdle()
        coVerify(exactly = 0) { repo.createRequest(any(), any()) }
    }

    @Test
    fun `request skips items with an existing active request`() = runTest {
        val repo = mockk<DiscoverRepository>(relaxed = true)
        val vm = DiscoverViewModel(repo)
        vm.request(item(1, hasActive = true))
        advanceUntilIdle()
        coVerify(exactly = 0) { repo.createRequest(any(), any()) }
    }

    @Test
    fun `request is idempotent within a session`() = runTest {
        // Tap "Request" twice in quick succession — the second tap
        // should no-op rather than firing a duplicate POST. Same
        // story for tapping a card that already has has_active_request
        // (separate test above) — both rely on the submitting-set
        // gate.
        val repo = mockk<DiscoverRepository>()
        coEvery { repo.createRequest("movie", 7) } returns MediaRequest(
            id = "r", user_id = "u", type = "movie", tmdb_id = 7,
            title = "T", status = "pending",
        )
        val vm = DiscoverViewModel(repo)
        vm.request(item(7))
        vm.request(item(7)) // double-tap before the first POST resolves
        advanceUntilIdle()
        coVerify(exactly = 1) { repo.createRequest("movie", 7) }
    }
}
