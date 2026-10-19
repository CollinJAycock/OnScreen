package tv.onscreen.android.ui.detail

import com.google.common.truth.Truth.assertThat
import io.mockk.Runs
import io.mockk.coEvery
import io.mockk.coVerify
import io.mockk.just
import io.mockk.mockk
import kotlinx.coroutines.CompletableDeferred
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.ExperimentalCoroutinesApi
import kotlinx.coroutines.launch
import kotlinx.coroutines.test.StandardTestDispatcher
import kotlinx.coroutines.test.TestScope
import kotlinx.coroutines.test.UnconfinedTestDispatcher
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
import tv.onscreen.android.data.model.ItemDetail
import tv.onscreen.android.data.model.WatchStatusValues
import tv.onscreen.android.data.repository.ItemRepository
import tv.onscreen.android.data.repository.WatchlistRepository

/** The Status action of [DetailViewModel]: the caller's watch-status. */
@OptIn(ExperimentalCoroutinesApi::class)
class DetailViewModelStatusTest {

    private val dispatcher = StandardTestDispatcher()

    @Before fun setUp() { Dispatchers.setMain(dispatcher) }
    @After fun tearDown() { Dispatchers.resetMain() }

    private val itemRepo = mockk<ItemRepository>()
    private val watchlistRepo = mockk<WatchlistRepository>()

    private val movie = ItemDetail(id = "m1", library_id = "lib", title = "Movie", type = "movie")

    private fun vm() = DetailViewModel(itemRepo, mockk(relaxed = true), mockk(relaxed = true), watchlistRepo)

    private fun http(code: Int) = HttpException(Response.error<Any>(code, "".toResponseBody(null)))

    private fun TestScope.collectEvents(vm: DetailViewModel): MutableList<DetailEvent> {
        val events = mutableListOf<DetailEvent>()
        backgroundScope.launch(UnconfinedTestDispatcher(testScheduler)) { vm.events.collect { events.add(it) } }
        return events
    }

    /** A movie whose page has loaded with [status] set. */
    private fun TestScope.loaded(status: String? = null): DetailViewModel {
        coEvery { itemRepo.getItem("m1") } returns movie
        coEvery { watchlistRepo.status("m1") } returns status
        val vm = vm()
        vm.load("m1")
        advanceUntilIdle()
        return vm
    }

    // ── load ────────────────────────────────────────────────────────────────

    @Test
    fun `load reads the status`() = runTest(dispatcher) {
        val vm = loaded(WatchStatusValues.OnHold)

        assertThat(vm.status.value).isEqualTo(DetailStatusState(status = WatchStatusValues.OnHold))
    }

    @Test
    fun `a slow status read doesn't hold up the page`() = runTest(dispatcher) {
        coEvery { itemRepo.getItem("m1") } returns movie
        val gate = CompletableDeferred<String?>()
        coEvery { watchlistRepo.status("m1") } coAnswers { gate.await() }
        val vm = vm()
        vm.load("m1")
        advanceUntilIdle()

        assertThat(vm.uiState.value.item?.id).isEqualTo("m1")
        assertThat(vm.status.value.status).isNull()

        gate.complete(WatchStatusValues.Watching)
        advanceUntilIdle()
        assertThat(vm.status.value.status).isEqualTo(WatchStatusValues.Watching)
    }

    @Test
    fun `a failed status read leaves the page loaded with no status`() = runTest(dispatcher) {
        coEvery { itemRepo.getItem("m1") } returns movie
        coEvery { watchlistRepo.status("m1") } throws http(500)
        val vm = vm()
        vm.load("m1")
        advanceUntilIdle()

        assertThat(vm.uiState.value.item?.id).isEqualTo("m1")
        assertThat(vm.uiState.value.error).isNull()
        assertThat(vm.status.value).isEqualTo(DetailStatusState())
    }

    @Test
    fun `types without a Status action don't read one`() = runTest(dispatcher) {
        coEvery { itemRepo.getItem("t1") } returns movie.copy(id = "t1", type = "track")
        val vm = vm()
        vm.load("t1")
        advanceUntilIdle()

        coVerify(exactly = 0) { watchlistRepo.status(any()) }
    }

    @Test
    fun `a reload keeps the status while it re-reads it`() = runTest(dispatcher) {
        val vm = loaded(WatchStatusValues.Watching)
        val gate = CompletableDeferred<String?>()
        coEvery { watchlistRepo.status("m1") } coAnswers { gate.await() }

        vm.load("m1")
        advanceUntilIdle()
        assertThat(vm.status.value.status).isEqualTo(WatchStatusValues.Watching)

        gate.complete(WatchStatusValues.Completed)
        advanceUntilIdle()
        assertThat(vm.status.value.status).isEqualTo(WatchStatusValues.Completed)
    }

    // ── setStatus ───────────────────────────────────────────────────────────

    @Test
    fun `picking a status PUTs it, optimistically`() = runTest(dispatcher) {
        val vm = loaded()
        val gate = CompletableDeferred<Unit>()
        coEvery { watchlistRepo.setStatus("m1", WatchStatusValues.PlanToWatch) } coAnswers { gate.await() }
        val events = collectEvents(vm)

        vm.setStatus(WatchStatusValues.PlanToWatch)
        assertThat(vm.status.value).isEqualTo(DetailStatusState(WatchStatusValues.PlanToWatch, busy = true))

        gate.complete(Unit)
        advanceUntilIdle()
        assertThat(vm.status.value).isEqualTo(DetailStatusState(WatchStatusValues.PlanToWatch))
        assertThat(events).isEmpty()
    }

    @Test
    fun `Remove DELETEs the status`() = runTest(dispatcher) {
        val vm = loaded(WatchStatusValues.Dropped)
        coEvery { watchlistRepo.clearStatus("m1") } just Runs

        vm.setStatus(null)
        advanceUntilIdle()

        assertThat(vm.status.value).isEqualTo(DetailStatusState())
        coVerify(exactly = 1) { watchlistRepo.clearStatus("m1") }
        coVerify(exactly = 0) { watchlistRepo.setStatus(any(), any()) }
    }

    @Test
    fun `a failed change keeps the old status and reports it`() = runTest(dispatcher) {
        val vm = loaded(WatchStatusValues.Watching)
        coEvery { watchlistRepo.setStatus("m1", WatchStatusValues.Completed) } throws http(500)
        coEvery { watchlistRepo.clearStatus("m1") } throws http(429)
        val events = collectEvents(vm)

        vm.setStatus(WatchStatusValues.Completed)
        advanceUntilIdle()
        assertThat(vm.status.value).isEqualTo(DetailStatusState(WatchStatusValues.Watching))

        vm.setStatus(null)
        advanceUntilIdle()
        assertThat(vm.status.value).isEqualTo(DetailStatusState(WatchStatusValues.Watching))

        assertThat(events).containsExactly(
            DetailEvent.StatusFailed(rateLimited = false),
            DetailEvent.StatusFailed(rateLimited = true),
        ).inOrder()
    }

    @Test
    fun `a second pick while one is in flight, or the current status again, is ignored`() = runTest(dispatcher) {
        val vm = loaded(WatchStatusValues.Watching)
        coEvery { watchlistRepo.setStatus(any(), any()) } just Runs

        vm.setStatus(WatchStatusValues.Watching)
        advanceUntilIdle()
        coVerify(exactly = 0) { watchlistRepo.setStatus(any(), any()) }

        vm.setStatus(WatchStatusValues.OnHold)
        vm.setStatus(WatchStatusValues.Dropped)
        advanceUntilIdle()
        assertThat(vm.status.value.status).isEqualTo(WatchStatusValues.OnHold)
        coVerify(exactly = 1) { watchlistRepo.setStatus("m1", any()) }
    }

    @Test
    fun `a status read that lands after a pick doesn't undo it`() = runTest(dispatcher) {
        val vm = loaded()
        val gate = CompletableDeferred<String?>()
        coEvery { watchlistRepo.status("m1") } coAnswers { gate.await() }
        coEvery { watchlistRepo.setStatus(any(), any()) } just Runs
        vm.load("m1")
        advanceUntilIdle()

        vm.setStatus(WatchStatusValues.PlanToWatch)
        advanceUntilIdle()
        gate.complete(null) // read before the PUT landed
        advanceUntilIdle()

        assertThat(vm.status.value.status).isEqualTo(WatchStatusValues.PlanToWatch)
    }
}
