package tv.onscreen.mobile.ui.watchlist

import com.google.common.truth.Truth.assertThat
import io.mockk.coEvery
import io.mockk.coVerify
import io.mockk.mockk
import kotlinx.coroutines.CompletableDeferred
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.ExperimentalCoroutinesApi
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
import tv.onscreen.mobile.data.api.ApiListResponse
import tv.onscreen.mobile.data.api.ApiResponse
import tv.onscreen.mobile.data.api.Meta
import tv.onscreen.mobile.data.api.OnScreenApi
import tv.onscreen.mobile.data.model.WatchStatus
import tv.onscreen.mobile.data.model.WatchlistCounts
import tv.onscreen.mobile.data.model.WatchlistItem
import tv.onscreen.mobile.data.model.WatchlistSort
import tv.onscreen.mobile.data.model.WatchlistStatusCount
import tv.onscreen.mobile.data.prefs.ServerPrefs
import tv.onscreen.mobile.data.repository.ItemRepository
import tv.onscreen.mobile.data.repository.WatchlistRepository

@OptIn(ExperimentalCoroutinesApi::class)
class WatchlistViewModelTest {

    private val dispatcher = StandardTestDispatcher()

    @Before fun setUp() { Dispatchers.setMain(dispatcher) }
    @After fun tearDown() { Dispatchers.resetMain() }

    private fun item(id: String, type: String = "movie", status: String = "plan_to_watch") =
        WatchlistItem(id = id, type = type, title = id, status = status)

    private fun page(prefix: String, n: Int, status: String = "plan_to_watch") =
        (1..n).map { item("$prefix$it", status = status) }

    private val counts = WatchlistCounts(
        total = 3,
        statuses = mapOf(
            "plan_to_watch" to WatchlistStatusCount(2, mapOf("movie" to 1, "show" to 1)),
            "watching" to WatchlistStatusCount(1, mapOf("movie" to 1)),
        ),
    )

    private val prefs = mockk<ServerPrefs>(relaxed = true)
    private val itemRepo = mockk<ItemRepository>()

    private fun repoWith(
        planToWatch: List<WatchlistItem> = listOf(item("a"), item("b", type = "show")),
        total: Int = planToWatch.size,
    ): WatchlistRepository {
        val repo = mockk<WatchlistRepository>()
        coEvery { repo.counts() } returns counts
        coEvery { repo.list(WatchStatus.PLAN_TO_WATCH, null, WatchlistSort.RECENT, any(), 0) } returns
            (planToWatch to total)
        return repo
    }

    private fun http(code: Int) = HttpException(Response.error<Unit>(code, "".toResponseBody(null)))

    @Test
    fun `first resume loads Plan to Watch page one and the counts`() = runTest(dispatcher) {
        val repo = repoWith()
        val vm = WatchlistViewModel(repo, itemRepo, prefs)
        assertThat(vm.state.value.loading).isTrue() // spinner, not an empty-state flash

        vm.onResume()
        advanceUntilIdle()

        val s = vm.state.value
        assertThat(s.loading).isFalse()
        assertThat(s.status).isEqualTo(WatchStatus.PLAN_TO_WATCH)
        assertThat(s.items.map { it.id }).containsExactly("a", "b").inOrder()
        assertThat(s.counts).isEqualTo(counts)
        assertThat(s.typeChoices.map { it.type }).containsExactly("movie", "show")
        assertThat(s.showTypeFilter).isTrue()
        coVerify { repo.list(WatchStatus.PLAN_TO_WATCH, null, WatchlistSort.RECENT, 50, 0) }
    }

    @Test
    fun `a counts failure leaves the list working without badges`() = runTest(dispatcher) {
        val repo = repoWith()
        coEvery { repo.counts() } throws RuntimeException("boom")
        val vm = WatchlistViewModel(repo, itemRepo, prefs)
        vm.onResume()
        advanceUntilIdle()
        assertThat(vm.state.value.items).hasSize(2)
        assertThat(vm.state.value.counts).isNull()
        assertThat(vm.state.value.error).isNull()
        assertThat(vm.state.value.showTypeFilter).isFalse()
    }

    @Test
    fun `switching tab reloads with the new status and drops a type it lacks`() = runTest(dispatcher) {
        val repo = repoWith()
        coEvery { repo.list(WatchStatus.PLAN_TO_WATCH, "show", any(), any(), 0) } returns (listOf(item("b", type = "show")) to 1)
        coEvery { repo.list(WatchStatus.WATCHING, null, any(), any(), 0) } returns
            (listOf(item("w", status = "watching")) to 1)
        val vm = WatchlistViewModel(repo, itemRepo, prefs)
        vm.onResume()
        advanceUntilIdle()

        vm.selectType("show")
        advanceUntilIdle()
        assertThat(vm.state.value.items.map { it.id }).containsExactly("b")

        val version = vm.state.value.queryVersion
        vm.selectStatus(WatchStatus.WATCHING) // Watching has no shows
        advanceUntilIdle()
        assertThat(vm.state.value.type).isNull()
        assertThat(vm.state.value.items.map { it.id }).containsExactly("w")
        assertThat(vm.state.value.queryVersion).isGreaterThan(version)

        // Re-selecting the current tab is a no-op.
        vm.selectStatus(WatchStatus.WATCHING)
        advanceUntilIdle()
        coVerify(exactly = 1) { repo.list(WatchStatus.WATCHING, null, any(), any(), 0) }
    }

    @Test
    fun `sort change reloads page one with the sort`() = runTest(dispatcher) {
        val repo = repoWith()
        coEvery { repo.list(WatchStatus.PLAN_TO_WATCH, null, WatchlistSort.TITLE, any(), 0) } returns
            (listOf(item("b"), item("a")) to 2)
        val vm = WatchlistViewModel(repo, itemRepo, prefs)
        vm.onResume()
        advanceUntilIdle()
        vm.selectSort(WatchlistSort.TITLE)
        advanceUntilIdle()
        assertThat(vm.state.value.sort).isEqualTo(WatchlistSort.TITLE)
        assertThat(vm.state.value.items.map { it.id }).containsExactly("b", "a").inOrder()
    }

    @Test
    fun `a stale page from the previous tab is dropped`() = runTest(dispatcher) {
        val repo = mockk<WatchlistRepository>()
        coEvery { repo.counts() } returns counts
        val slow = CompletableDeferred<Pair<List<WatchlistItem>, Int>>()
        coEvery { repo.list(WatchStatus.PLAN_TO_WATCH, any(), any(), any(), 0) } coAnswers { slow.await() }
        coEvery { repo.list(WatchStatus.DROPPED, any(), any(), any(), 0) } returns (listOf(item("d", status = "dropped")) to 1)
        val vm = WatchlistViewModel(repo, itemRepo, prefs)

        vm.onResume()
        advanceUntilIdle() // Plan to Watch parked
        vm.selectStatus(WatchStatus.DROPPED)
        advanceUntilIdle()
        slow.complete(listOf(item("x"), item("y")) to 2)
        advanceUntilIdle()

        assertThat(vm.state.value.items.map { it.id }).containsExactly("d")
        assertThat(vm.state.value.total).isEqualTo(1)
    }

    @Test
    fun `load more appends the next 50 by offset and stops at the total`() = runTest(dispatcher) {
        val repo = repoWith(planToWatch = page("p", 50), total = 70)
        coEvery { repo.list(WatchStatus.PLAN_TO_WATCH, null, WatchlistSort.RECENT, 50, 50) } returns
            (page("q", 20) to 70)
        val vm = WatchlistViewModel(repo, itemRepo, prefs)
        vm.onResume()
        advanceUntilIdle()

        vm.loadMore()
        advanceUntilIdle()
        assertThat(vm.state.value.items).hasSize(70)

        vm.loadMore() // everything loaded: no request
        advanceUntilIdle()
        coVerify(exactly = 1) { repo.list(any(), any(), any(), any(), 50) }
        coVerify(exactly = 0) { repo.list(any(), any(), any(), any(), 70) }
    }

    @Test
    fun `a failed page re-arms load more`() = runTest(dispatcher) {
        val repo = repoWith(planToWatch = page("p", 50), total = 70)
        coEvery { repo.list(any(), any(), any(), any(), 50) } throws RuntimeException("net")
        val vm = WatchlistViewModel(repo, itemRepo, prefs)
        vm.onResume()
        advanceUntilIdle()
        vm.loadMore()
        advanceUntilIdle()
        assertThat(vm.state.value.loadMoreFailed).isTrue()
        assertThat(vm.state.value.loadingMore).isFalse()
        assertThat(vm.state.value.items).hasSize(50)
    }

    @Test
    fun `move to another status PUTs it, drops the card and shifts the counts`() = runTest(dispatcher) {
        val repo = repoWith()
        coEvery { itemRepo.setWatchStatus("a", WatchStatus.WATCHING) } returns WatchStatus.WATCHING
        val vm = WatchlistViewModel(repo, itemRepo, prefs)
        vm.onResume()
        advanceUntilIdle()

        vm.changeStatus(vm.state.value.items.first(), WatchStatus.WATCHING)
        assertThat(vm.state.value.busyId).isEqualTo("a")
        advanceUntilIdle()

        val s = vm.state.value
        assertThat(s.busyId).isNull()
        assertThat(s.items.map { it.id }).containsExactly("b")
        assertThat(s.total).isEqualTo(1)
        assertThat(s.counts!!.statuses["plan_to_watch"]!!.total).isEqualTo(1)
        assertThat(s.counts!!.statuses["watching"]!!.total).isEqualTo(2)
        assertThat(s.message).isEqualTo("Moved \"a\" to Watching")
        coVerify { itemRepo.setWatchStatus("a", WatchStatus.WATCHING) }
    }

    @Test
    fun `remove DELETEs the status and drops the overall total`() = runTest(dispatcher) {
        val repo = repoWith()
        coEvery { itemRepo.clearWatchStatus("b") } returns Unit
        val vm = WatchlistViewModel(repo, itemRepo, prefs)
        vm.onResume()
        advanceUntilIdle()

        vm.changeStatus(vm.state.value.items[1], null)
        advanceUntilIdle()

        val s = vm.state.value
        assertThat(s.items.map { it.id }).containsExactly("a")
        assertThat(s.counts!!.total).isEqualTo(2)
        assertThat(s.message).isEqualTo("Removed \"b\" from your watchlist")
        vm.consumeMessage()
        assertThat(vm.state.value.message).isNull()
    }

    @Test
    fun `a failed change keeps the card and says why`() = runTest(dispatcher) {
        val repo = repoWith()
        coEvery { itemRepo.setWatchStatus(any(), any()) } throws http(429)
        val vm = WatchlistViewModel(repo, itemRepo, prefs)
        vm.onResume()
        advanceUntilIdle()

        vm.changeStatus(vm.state.value.items.first(), WatchStatus.DROPPED)
        advanceUntilIdle()

        val s = vm.state.value
        assertThat(s.items.map { it.id }).containsExactly("a", "b")
        assertThat(s.counts).isEqualTo(counts)
        assertThat(s.busyId).isNull()
        assertThat(s.message).isEqualTo("Too many changes at once — try again in a minute")
        assertThat(statusErrorMessage(RuntimeException())).isEqualTo("Couldn't update the status")
    }

    @Test
    fun `moving to the current status or during another change does nothing`() = runTest(dispatcher) {
        val repo = repoWith()
        val gate = CompletableDeferred<WatchStatus?>()
        coEvery { itemRepo.setWatchStatus("a", WatchStatus.ON_HOLD) } coAnswers { gate.await() }
        val vm = WatchlistViewModel(repo, itemRepo, prefs)
        vm.onResume()
        advanceUntilIdle()
        val (a, b) = vm.state.value.items

        vm.changeStatus(a, WatchStatus.PLAN_TO_WATCH)
        advanceUntilIdle()
        assertThat(vm.state.value.busyId).isNull()

        vm.changeStatus(a, WatchStatus.ON_HOLD)
        vm.changeStatus(b, WatchStatus.DROPPED) // ignored: a's change is in flight
        advanceUntilIdle()
        gate.complete(WatchStatus.ON_HOLD)
        advanceUntilIdle()
        coVerify(exactly = 0) { itemRepo.setWatchStatus("b", any()) }
        assertThat(vm.state.value.items.map { it.id }).containsExactly("b")
    }

    @Test
    fun `emptying the loaded page while more remain fetches page one again`() = runTest(dispatcher) {
        val repo = mockk<WatchlistRepository>()
        coEvery { repo.counts() } returns counts
        coEvery { repo.list(WatchStatus.PLAN_TO_WATCH, null, any(), any(), 0) } returnsMany listOf(
            listOf(item("a")) to 5,
            listOf(item("c"), item("d")) to 4,
        )
        coEvery { itemRepo.clearWatchStatus("a") } returns Unit
        val vm = WatchlistViewModel(repo, itemRepo, prefs)
        vm.onResume()
        advanceUntilIdle()

        vm.changeStatus(vm.state.value.items.single(), null)
        advanceUntilIdle()
        assertThat(vm.state.value.items.map { it.id }).containsExactly("c", "d")
        assertThat(vm.state.value.total).isEqualTo(4)
    }

    @Test
    fun `later resumes re-pull the list and counts quietly`() = runTest(dispatcher) {
        val repo = mockk<WatchlistRepository>()
        coEvery { repo.counts() } returnsMany listOf(counts, WatchlistCounts(total = 0))
        coEvery { repo.list(WatchStatus.PLAN_TO_WATCH, null, any(), any(), 0) } returnsMany listOf(
            listOf(item("a"), item("b")) to 2,
            listOf(item("b")) to 1, // "a" changed status on its detail page
        )
        val vm = WatchlistViewModel(repo, itemRepo, prefs)
        vm.onResume()
        advanceUntilIdle()

        vm.onResume()
        // Quiet: no spinner while it runs.
        assertThat(vm.state.value.loading).isFalse()
        advanceUntilIdle()
        assertThat(vm.state.value.items.map { it.id }).containsExactly("b")
        assertThat(vm.state.value.total).isEqualTo(1)
        assertThat(vm.state.value.counts!!.total).isEqualTo(0)
        // The query didn't change: the grid keeps its scroll position.
        assertThat(vm.state.value.queryVersion).isEqualTo(0)
    }

    @Test
    fun `a first-page failure shows the error`() = runTest(dispatcher) {
        val repo = mockk<WatchlistRepository>()
        coEvery { repo.counts() } returns counts
        coEvery { repo.list(any(), any(), any(), any(), any()) } throws RuntimeException("offline")
        val vm = WatchlistViewModel(repo, itemRepo, prefs)
        vm.onResume()
        advanceUntilIdle()
        assertThat(vm.state.value.loading).isFalse()
        assertThat(vm.state.value.error).isEqualTo("offline")
    }

    @Test
    fun `repository sends the wire params and returns the meta total`() = runTest(dispatcher) {
        val api = mockk<OnScreenApi>()
        coEvery { api.getWatchlist("on_hold", "episode", "title", 50, 100) } returns
            ApiListResponse(listOf(item("e", type = "episode", status = "on_hold")), Meta(total = 101, cursor = ""))
        coEvery { api.getWatchlistCounts() } returns ApiResponse(counts)
        val repo = WatchlistRepository(api)

        val (items, total) = repo.list(WatchStatus.ON_HOLD, "episode", WatchlistSort.TITLE, limit = 50, offset = 100)
        assertThat(items.map { it.id }).containsExactly("e")
        assertThat(total).isEqualTo(101)
        assertThat(repo.counts()).isEqualTo(counts)
    }
}
