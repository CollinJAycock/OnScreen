package tv.onscreen.android.ui.watchlist

import com.google.common.truth.Truth.assertThat
import io.mockk.Runs
import io.mockk.coEvery
import io.mockk.coVerify
import io.mockk.just
import io.mockk.mockk
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.ExperimentalCoroutinesApi
import kotlinx.coroutines.launch
import kotlinx.coroutines.test.StandardTestDispatcher
import kotlinx.coroutines.test.advanceUntilIdle
import kotlinx.coroutines.test.resetMain
import kotlinx.coroutines.test.runTest
import kotlinx.coroutines.test.setMain
import okhttp3.ResponseBody.Companion.toResponseBody
import org.junit.After
import org.junit.Before
import org.junit.Test
import retrofit2.HttpException
import retrofit2.Response
import tv.onscreen.android.data.model.WatchStatusValues
import tv.onscreen.android.data.model.WatchlistCounts
import tv.onscreen.android.data.model.WatchlistItem
import tv.onscreen.android.data.model.WatchlistStatusCount
import tv.onscreen.android.data.repository.WatchlistRepository

@OptIn(ExperimentalCoroutinesApi::class)
class WatchlistViewModelTest {

    private val dispatcher = StandardTestDispatcher()

    @Before fun setUp() { Dispatchers.setMain(dispatcher) }
    @After fun tearDown() { Dispatchers.resetMain() }

    private fun wl(id: String, status: String = WatchStatusValues.PlanToWatch) =
        WatchlistItem(id = id, type = "movie", title = "t-$id", status = status)

    private fun items(prefix: String, from: Int, to: Int, status: String = WatchStatusValues.PlanToWatch) =
        (from until to).map { wl("$prefix$it", status) }

    private fun counts(vararg byStatus: Pair<String, Int>) = WatchlistCounts(
        total = byStatus.sumOf { it.second },
        statuses = byStatus.associate { (s, n) -> s to WatchlistStatusCount(total = n) },
    )

    @Test
    fun `rows come in status order and skip empty statuses`() = runTest(dispatcher) {
        val repo = mockk<WatchlistRepository>()
        coEvery { repo.counts() } returns counts(
            WatchStatusValues.Dropped to 1,
            WatchStatusValues.Watching to 0,
            WatchStatusValues.PlanToWatch to 2,
        )
        coEvery { repo.page(WatchStatusValues.PlanToWatch, 50, 0) } returns (listOf(wl("a"), wl("b")) to 2)
        coEvery { repo.page(WatchStatusValues.Dropped, 50, 0) } returns
            (listOf(wl("c", WatchStatusValues.Dropped)) to 1)

        val vm = WatchlistViewModel(repo)
        advanceUntilIdle()

        val state = vm.uiState.value
        assertThat(state.rows.map { it.status })
            .containsExactly(WatchStatusValues.PlanToWatch, WatchStatusValues.Dropped).inOrder()
        assertThat(state.rows[0].items.map { it.id }).containsExactly("a", "b").inOrder()
        assertThat(state.rows[0].total).isEqualTo(2)
        assertThat(state.loaded).isTrue()
        assertThat(state.error).isNull()
        // Zero / missing statuses are never listed.
        coVerify(exactly = 0) { repo.page(WatchStatusValues.Watching, any(), any()) }
        coVerify(exactly = 0) { repo.page(WatchStatusValues.OnHold, any(), any()) }
    }

    @Test
    fun `an empty watchlist loads to no rows without listing anything`() = runTest(dispatcher) {
        val repo = mockk<WatchlistRepository>()
        coEvery { repo.counts() } returns WatchlistCounts()

        val vm = WatchlistViewModel(repo)
        advanceUntilIdle()

        val state = vm.uiState.value
        assertThat(state.rows).isEmpty()
        assertThat(state.loaded).isTrue()
        assertThat(state.error).isNull()
        coVerify(exactly = 0) { repo.page(any(), any(), any()) }
    }

    @Test
    fun `a row whose listing comes back empty is dropped`() = runTest(dispatcher) {
        val repo = mockk<WatchlistRepository>()
        coEvery { repo.counts() } returns counts(WatchStatusValues.OnHold to 1)
        coEvery { repo.page(WatchStatusValues.OnHold, 50, 0) } returns (emptyList<WatchlistItem>() to 0)

        val vm = WatchlistViewModel(repo)
        advanceUntilIdle()

        assertThat(vm.uiState.value.rows).isEmpty()
    }

    @Test
    fun `loadMore appends the next page and stops at the total`() = runTest(dispatcher) {
        val repo = mockk<WatchlistRepository>()
        coEvery { repo.counts() } returns counts(WatchStatusValues.PlanToWatch to 70)
        coEvery { repo.page(WatchStatusValues.PlanToWatch, 50, 0) } returns (items("p", 0, 50) to 70)
        coEvery { repo.page(WatchStatusValues.PlanToWatch, 50, 50) } returns (items("p", 50, 70) to 70)

        val vm = WatchlistViewModel(repo)
        advanceUntilIdle()
        assertThat(vm.uiState.value.rows.single().hasMore).isTrue()

        vm.loadMore(WatchStatusValues.PlanToWatch)
        vm.loadMore(WatchStatusValues.PlanToWatch) // in flight: ignored
        advanceUntilIdle()

        val row = vm.uiState.value.rows.single()
        assertThat(row.items).hasSize(70)
        assertThat(row.items.last().id).isEqualTo("p69")
        assertThat(row.hasMore).isFalse()
        coVerify(exactly = 1) { repo.page(WatchStatusValues.PlanToWatch, 50, 50) }

        vm.loadMore(WatchStatusValues.PlanToWatch) // all loaded: no request
        advanceUntilIdle()
        coVerify(exactly = 2) { repo.page(WatchStatusValues.PlanToWatch, any(), any()) }
    }

    @Test
    fun `a reload keeps each row as deep as it was paged`() = runTest(dispatcher) {
        val repo = mockk<WatchlistRepository>()
        coEvery { repo.counts() } returns counts(WatchStatusValues.Watching to 120)
        coEvery { repo.page(WatchStatusValues.Watching, 50, 0) } returns (items("w", 0, 50) to 120)
        coEvery { repo.page(WatchStatusValues.Watching, 50, 50) } returns (items("w", 50, 100) to 120)
        coEvery { repo.page(WatchStatusValues.Watching, 100, 0) } returns (items("w", 0, 100) to 120)

        val vm = WatchlistViewModel(repo)
        advanceUntilIdle()
        vm.loadMore(WatchStatusValues.Watching)
        advanceUntilIdle()
        assertThat(vm.uiState.value.rows.single().items).hasSize(100)

        vm.load()
        advanceUntilIdle()

        assertThat(vm.uiState.value.rows.single().items).hasSize(100)
        coVerify(exactly = 1) { repo.page(WatchStatusValues.Watching, 100, 0) }
    }

    @Test
    fun `a deep reload pages within the server's 200 cap`() = runTest(dispatcher) {
        val repo = mockk<WatchlistRepository>()
        coEvery { repo.counts() } returns counts(WatchStatusValues.Completed to 300)
        // Grow the row to 250 cards one page at a time.
        coEvery { repo.page(WatchStatusValues.Completed, 50, any()) } answers {
            val offset = thirdArg<Int>()
            items("c", offset, offset + 50, WatchStatusValues.Completed) to 300
        }
        coEvery { repo.page(WatchStatusValues.Completed, 200, 0) } returns
            (items("c", 0, 200, WatchStatusValues.Completed) to 300)

        val vm = WatchlistViewModel(repo)
        advanceUntilIdle()
        repeat(4) {
            vm.loadMore(WatchStatusValues.Completed)
            advanceUntilIdle()
        }
        assertThat(vm.uiState.value.rows.single().items).hasSize(250)

        vm.load()
        advanceUntilIdle()

        assertThat(vm.uiState.value.rows.single().items).hasSize(250)
        coVerify(exactly = 1) { repo.page(WatchStatusValues.Completed, 200, 0) }
        coVerify(atLeast = 2) { repo.page(WatchStatusValues.Completed, 50, 200) }
    }

    @Test
    fun `a page that lands after a reload is dropped`() = runTest(dispatcher) {
        val repo = mockk<WatchlistRepository>()
        coEvery { repo.counts() } returns counts(WatchStatusValues.PlanToWatch to 60)
        coEvery { repo.page(WatchStatusValues.PlanToWatch, 50, 0) } returns (items("p", 0, 50) to 60)
        coEvery { repo.page(WatchStatusValues.PlanToWatch, 50, 50) } returns (items("stale", 0, 10) to 60)

        val vm = WatchlistViewModel(repo)
        advanceUntilIdle()
        vm.loadMore(WatchStatusValues.PlanToWatch)
        vm.load() // supersedes the page in flight
        advanceUntilIdle()

        assertThat(vm.uiState.value.rows.single().items.none { it.id.startsWith("stale") }).isTrue()
    }

    @Test
    fun `a failed first load shows the error, a failed refresh keeps the rows`() = runTest(dispatcher) {
        val repo = mockk<WatchlistRepository>()
        coEvery { repo.counts() } throws RuntimeException("nope")

        val vm = WatchlistViewModel(repo)
        advanceUntilIdle()
        assertThat(vm.uiState.value.error).isEqualTo("nope")
        assertThat(vm.uiState.value.rows).isEmpty()

        coEvery { repo.counts() } returns counts(WatchStatusValues.PlanToWatch to 1)
        coEvery { repo.page(WatchStatusValues.PlanToWatch, 50, 0) } returns (listOf(wl("a")) to 1)
        vm.load()
        advanceUntilIdle()
        assertThat(vm.uiState.value.rows.single().items.map { it.id }).containsExactly("a")

        coEvery { repo.counts() } throws RuntimeException("offline")
        vm.load()
        advanceUntilIdle()
        val state = vm.uiState.value
        assertThat(state.error).isNull()
        assertThat(state.isLoading).isFalse()
        assertThat(state.rows.single().items.map { it.id }).containsExactly("a")
    }

    @Test
    fun `moving a card writes the status and reloads`() = runTest(dispatcher) {
        val repo = mockk<WatchlistRepository>()
        coEvery { repo.counts() } returns counts(WatchStatusValues.PlanToWatch to 1)
        coEvery { repo.page(WatchStatusValues.PlanToWatch, 50, 0) } returns (listOf(wl("a")) to 1)
        coEvery { repo.setStatus("a", WatchStatusValues.Watching) } just Runs
        coEvery { repo.clearStatus("a") } just Runs

        val vm = WatchlistViewModel(repo)
        advanceUntilIdle()

        coEvery { repo.counts() } returns counts(WatchStatusValues.Watching to 1)
        coEvery { repo.page(WatchStatusValues.Watching, 50, 0) } returns
            (listOf(wl("a", WatchStatusValues.Watching)) to 1)
        vm.changeStatus(wl("a"), WatchStatusValues.Watching)
        advanceUntilIdle()

        coVerify(exactly = 1) { repo.setStatus("a", WatchStatusValues.Watching) }
        assertThat(vm.uiState.value.rows.map { it.status }).containsExactly(WatchStatusValues.Watching)

        // Remove → DELETE.
        coEvery { repo.counts() } returns WatchlistCounts()
        vm.changeStatus(wl("a", WatchStatusValues.Watching), null)
        advanceUntilIdle()
        coVerify(exactly = 1) { repo.clearStatus("a") }
        assertThat(vm.uiState.value.rows).isEmpty()

        // Same status: nothing to write.
        vm.changeStatus(wl("a", WatchStatusValues.Watching), WatchStatusValues.Watching)
        advanceUntilIdle()
        coVerify(exactly = 0) { repo.setStatus("a", WatchStatusValues.PlanToWatch) }
        coVerify(exactly = 1) { repo.setStatus(any(), any()) }
    }

    @Test
    fun `a failed move emits ChangeFailed and does not reload`() = runTest(dispatcher) {
        val repo = mockk<WatchlistRepository>()
        coEvery { repo.counts() } returns counts(WatchStatusValues.PlanToWatch to 1)
        coEvery { repo.page(WatchStatusValues.PlanToWatch, 50, 0) } returns (listOf(wl("a")) to 1)
        coEvery { repo.setStatus("a", any()) } throws
            HttpException(Response.error<Unit>(429, "".toResponseBody()))

        val vm = WatchlistViewModel(repo)
        advanceUntilIdle()

        val events = mutableListOf<WatchlistEvent>()
        val job = launch { vm.events.collect { events += it } }
        advanceUntilIdle()
        vm.changeStatus(wl("a"), WatchStatusValues.Dropped)
        advanceUntilIdle()
        job.cancel()

        assertThat(events).containsExactly(WatchlistEvent.ChangeFailed(rateLimited = true))
        coVerify(exactly = 1) { repo.counts() }
        assertThat(vm.uiState.value.rows.single().items.map { it.id }).containsExactly("a")
    }
}
