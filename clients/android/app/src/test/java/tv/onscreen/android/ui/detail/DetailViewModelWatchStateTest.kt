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
import tv.onscreen.android.data.model.ChildItem
import tv.onscreen.android.data.model.ItemDetail
import tv.onscreen.android.data.model.UpNext
import tv.onscreen.android.data.model.UpNextEpisode
import tv.onscreen.android.data.repository.FavoritesRepository
import tv.onscreen.android.data.repository.ItemRepository

/** Watch-state behaviour of [DetailViewModel]: Up Next, watched marks. */
@OptIn(ExperimentalCoroutinesApi::class)
class DetailViewModelWatchStateTest {

    private val dispatcher = StandardTestDispatcher()

    @Before fun setUp() { Dispatchers.setMain(dispatcher) }
    @After fun tearDown() { Dispatchers.resetMain() }

    private val itemRepo = mockk<ItemRepository>()
    private val favRepo = mockk<FavoritesRepository>()

    private fun movie(watchState: String? = null, offset: Long = 0) = ItemDetail(
        id = "m1", library_id = "lib", title = "Movie", type = "movie",
        watch_state = watchState, view_offset_ms = offset,
    )

    private val show = ItemDetail(id = "s1", library_id = "lib", title = "Show", type = "show")
    private val season1 = ChildItem(id = "se1", title = "Season 1", type = "season", index = 1)
    private val season2 = ChildItem(id = "se2", title = "Season 2", type = "season", index = 2)
    private fun ep(id: String, index: Int, watched: Boolean = false, offset: Long = 0) =
        ChildItem(id = id, title = "E$index", type = "episode", index = index, watched = watched, view_offset_ms = offset)

    private val upNextS2E1 = UpNext(
        mode = "next",
        episode = UpNextEpisode(id = "e21", title = "E1", season_id = "se2", season_number = 2, episode_number = 1),
    )

    private fun stubShow(upNext: UpNext? = upNextS2E1) {
        coEvery { itemRepo.getItem("s1") } returns show
        coEvery { itemRepo.getChildren("s1") } returns listOf(season1, season2)
        coEvery { itemRepo.getChildren("se1") } returns listOf(ep("e11", 1, watched = true), ep("e12", 2, watched = true))
        coEvery { itemRepo.getChildren("se2") } returns listOf(ep("e21", 1), ep("e22", 2))
        if (upNext != null) {
            coEvery { itemRepo.getUpNext("s1") } returns upNext
        } else {
            coEvery { itemRepo.getUpNext("s1") } throws RuntimeException("404")
        }
    }

    private fun TestScope.collectEvents(vm: DetailViewModel): MutableList<DetailEvent> {
        val events = mutableListOf<DetailEvent>()
        // Unconfined: subscribes now and receives each event as it's emitted
        // (a StandardTestDispatcher collector would be background work that
        // advanceUntilIdle never runs).
        backgroundScope.launch(UnconfinedTestDispatcher(testScheduler)) { vm.events.collect { events.add(it) } }
        return events
    }

    private fun http(code: Int) = HttpException(Response.error<Any>(code, "".toResponseBody(null)))

    // ── load ────────────────────────────────────────────────────────────────

    @Test
    fun `show load carries up-next alongside the seasons`() = runTest(dispatcher) {
        stubShow()
        val vm = DetailViewModel(itemRepo, favRepo)
        vm.load("s1")
        advanceUntilIdle()

        val state = vm.uiState.value
        assertThat(state.upNext).isEqualTo(upNextS2E1)
        assertThat(state.seasons.keys.map { it.id }).containsExactly("se1", "se2").inOrder()
    }

    @Test
    fun `up-next failure (older server) still loads the show with no up-next`() = runTest(dispatcher) {
        stubShow(upNext = null)
        val vm = DetailViewModel(itemRepo, favRepo)
        vm.load("s1")
        advanceUntilIdle()

        assertThat(vm.uiState.value.item?.id).isEqualTo("s1")
        assertThat(vm.uiState.value.upNext).isNull()
        assertThat(vm.uiState.value.error).isNull()
    }

    @Test
    fun `movies do not ask for up-next and read watch_state`() = runTest(dispatcher) {
        coEvery { itemRepo.getItem("m1") } returns movie(watchState = "watched")
        val vm = DetailViewModel(itemRepo, favRepo)
        vm.load("m1")
        advanceUntilIdle()

        assertThat(vm.uiState.value.watched).isTrue()
        assertThat(vm.uiState.value.upNext).isNull()
        coVerify(exactly = 0) { itemRepo.getUpNext(any()) }
    }

    // ── leaf toggle ─────────────────────────────────────────────────────────

    @Test
    fun `toggleWatched marks watched optimistically and clears the resume point`() = runTest(dispatcher) {
        coEvery { itemRepo.getItem("m1") } returns movie(watchState = "in_progress", offset = 60_000)
        val gate = CompletableDeferred<Unit>()
        coEvery { itemRepo.setWatched("m1", true) } coAnswers { gate.await() }
        val vm = DetailViewModel(itemRepo, favRepo)
        vm.load("m1")
        advanceUntilIdle()
        val events = collectEvents(vm)

        vm.toggleWatched()
        assertThat(vm.uiState.value.watched).isTrue()
        assertThat(vm.uiState.value.markBusy).isTrue()

        gate.complete(Unit)
        advanceUntilIdle()
        assertThat(vm.uiState.value.watched).isTrue()
        assertThat(vm.uiState.value.markBusy).isFalse()
        assertThat(vm.uiState.value.item?.view_offset_ms).isEqualTo(0)
        assertThat(events).containsExactly(DetailEvent.Marked(watched = true, all = false))
    }

    @Test
    fun `toggleWatched on a watched movie marks unwatched`() = runTest(dispatcher) {
        coEvery { itemRepo.getItem("m1") } returns movie(watchState = "watched")
        coEvery { itemRepo.setWatched("m1", false) } just Runs
        val vm = DetailViewModel(itemRepo, favRepo)
        vm.load("m1")
        advanceUntilIdle()

        vm.toggleWatched()
        advanceUntilIdle()

        assertThat(vm.uiState.value.watched).isFalse()
        coVerify(exactly = 1) { itemRepo.setWatched("m1", false) }
    }

    @Test
    fun `toggleWatched failure rolls back and reports rate limiting`() = runTest(dispatcher) {
        coEvery { itemRepo.getItem("m1") } returns movie(offset = 60_000)
        coEvery { itemRepo.setWatched("m1", true) } throws http(429)
        val vm = DetailViewModel(itemRepo, favRepo)
        vm.load("m1")
        advanceUntilIdle()
        val events = collectEvents(vm)

        vm.toggleWatched()
        advanceUntilIdle()

        assertThat(vm.uiState.value.watched).isFalse()
        assertThat(vm.uiState.value.markBusy).isFalse()
        // Resume point untouched on failure.
        assertThat(vm.uiState.value.item?.view_offset_ms).isEqualTo(60_000)
        assertThat(events).containsExactly(DetailEvent.MarkFailed(rateLimited = true))
    }

    @Test
    fun `a second toggle while one is in flight is ignored`() = runTest(dispatcher) {
        coEvery { itemRepo.getItem("m1") } returns movie()
        coEvery { itemRepo.setWatched("m1", true) } just Runs
        val vm = DetailViewModel(itemRepo, favRepo)
        vm.load("m1")
        advanceUntilIdle()

        vm.toggleWatched()
        vm.toggleWatched()
        advanceUntilIdle()

        assertThat(vm.uiState.value.watched).isTrue()
        coVerify(exactly = 1) { itemRepo.setWatched("m1", any()) }
    }

    @Test
    fun `a reload landing while a mark is in flight keeps the mark's state and busy flag`() =
        runTest(dispatcher) {
            coEvery { itemRepo.getItem("m1") } returns movie(watchState = "in_progress", offset = 60_000)
            val gate = CompletableDeferred<Unit>()
            coEvery { itemRepo.setWatched("m1", true) } coAnswers { gate.await() }
            val vm = DetailViewModel(itemRepo, favRepo)
            vm.load("m1")
            advanceUntilIdle()

            vm.toggleWatched()
            // onResume refresh: the server hasn't committed the mark yet.
            vm.load("m1")
            advanceUntilIdle()
            assertThat(vm.uiState.value.watched).isTrue()
            // Still busy — the button must not re-enable mid-request.
            assertThat(vm.uiState.value.markBusy).isTrue()

            gate.complete(Unit)
            advanceUntilIdle()
            assertThat(vm.uiState.value.watched).isTrue()
            assertThat(vm.uiState.value.markBusy).isFalse()
            assertThat(vm.uiState.value.item?.view_offset_ms).isEqualTo(0)
        }

    @Test
    fun `a reload that started before the mark settled cannot undo it`() = runTest(dispatcher) {
        coEvery { itemRepo.getItem("m1") } returns movie(watchState = "in_progress")
        coEvery { itemRepo.setWatched("m1", true) } just Runs
        val vm = DetailViewModel(itemRepo, favRepo)
        vm.load("m1")
        advanceUntilIdle()

        // The reload's read is out (pre-mark watch_state)...
        val staleRead = CompletableDeferred<ItemDetail>()
        coEvery { itemRepo.getItem("m1") } coAnswers { staleRead.await() }
        vm.load("m1")
        advanceUntilIdle()
        // ...while the mark goes through.
        vm.toggleWatched()
        advanceUntilIdle()
        assertThat(vm.uiState.value.watched).isTrue()

        staleRead.complete(movie(watchState = "in_progress"))
        advanceUntilIdle()
        assertThat(vm.uiState.value.watched).isTrue()
        assertThat(vm.uiState.value.markBusy).isFalse()

        // A load that starts afterwards reads the server again.
        coEvery { itemRepo.getItem("m1") } returns movie(watchState = "unwatched")
        vm.load("m1")
        advanceUntilIdle()
        assertThat(vm.uiState.value.watched).isFalse()
    }

    // ── show / season mark all ──────────────────────────────────────────────

    @Test
    fun `markAll marks the show then re-reads episodes and up-next`() = runTest(dispatcher) {
        stubShow()
        coEvery { itemRepo.setWatched("s1", true) } just Runs
        val vm = DetailViewModel(itemRepo, favRepo)
        vm.load("s1")
        advanceUntilIdle()
        val events = collectEvents(vm)

        // After the mark the server reports everything watched.
        coEvery { itemRepo.getChildren("se2") } returns listOf(ep("e21", 1, watched = true), ep("e22", 2, watched = true))
        val rewatch = UpNext("rewatch", UpNextEpisode(id = "e11", season_id = "se1", season_number = 1, episode_number = 1))
        coEvery { itemRepo.getUpNext("s1") } returns rewatch

        vm.markAll(true)
        advanceUntilIdle()

        val state = vm.uiState.value
        assertThat(state.upNext).isEqualTo(rewatch)
        assertThat(state.seasons.values.flatten().all { it.watched }).isTrue()
        // Same season keys, same order — the fragment's tabs stay valid.
        assertThat(state.seasons.keys.map { it.id }).containsExactly("se1", "se2").inOrder()
        assertThat(state.markBusy).isFalse()
        assertThat(events).containsExactly(DetailEvent.Marked(watched = true, all = true))
    }

    @Test
    fun `markAll failure leaves the episodes untouched`() = runTest(dispatcher) {
        stubShow()
        coEvery { itemRepo.setWatched("s1", false) } throws RuntimeException("offline")
        val vm = DetailViewModel(itemRepo, favRepo)
        vm.load("s1")
        advanceUntilIdle()
        val before = vm.uiState.value.seasons
        val events = collectEvents(vm)

        vm.markAll(false)
        advanceUntilIdle()

        assertThat(vm.uiState.value.seasons).isEqualTo(before)
        assertThat(vm.uiState.value.markBusy).isFalse()
        assertThat(events).containsExactly(DetailEvent.MarkFailed(rateLimited = false))
    }

    @Test
    fun `markAll falls back to a local patch when a season re-read fails`() = runTest(dispatcher) {
        stubShow()
        coEvery { itemRepo.setWatched("s1", false) } just Runs
        val vm = DetailViewModel(itemRepo, favRepo)
        vm.load("s1")
        advanceUntilIdle()

        coEvery { itemRepo.getChildren("se1") } throws RuntimeException("blip")
        coEvery { itemRepo.getChildren("se2") } returns listOf(ep("e21", 1), ep("e22", 2))

        vm.markAll(false)
        advanceUntilIdle()

        val s1 = vm.uiState.value.seasons.entries.first { it.key.id == "se1" }.value
        assertThat(s1.none { it.watched }).isTrue()
    }

    // ── episode row toggle ──────────────────────────────────────────────────

    @Test
    fun `episode toggle is optimistic and refreshes that season and up-next`() = runTest(dispatcher) {
        stubShow()
        val gate = CompletableDeferred<Unit>()
        coEvery { itemRepo.setWatched("e21", true) } coAnswers { gate.await() }
        val vm = DetailViewModel(itemRepo, favRepo)
        vm.load("s1")
        advanceUntilIdle()

        val target = vm.uiState.value.seasons.values.flatten().first { it.id == "e21" }
        vm.toggleEpisodeWatched(target)
        assertThat(vm.uiState.value.seasons.values.flatten().first { it.id == "e21" }.watched).isTrue()

        val next = UpNext("next", UpNextEpisode(id = "e22", season_id = "se2", season_number = 2, episode_number = 2))
        coEvery { itemRepo.getChildren("se2") } returns listOf(ep("e21", 1, watched = true), ep("e22", 2))
        coEvery { itemRepo.getUpNext("s1") } returns next
        gate.complete(Unit)
        advanceUntilIdle()

        assertThat(vm.uiState.value.upNext).isEqualTo(next)
        assertThat(vm.uiState.value.seasons.values.flatten().first { it.id == "e21" }.watched).isTrue()
        coVerify(exactly = 2) { itemRepo.getChildren("se2") } // load + refresh
        coVerify(exactly = 1) { itemRepo.getChildren("se1") } // untouched season not re-read
    }

    @Test
    fun `episode toggle failure restores the row`() = runTest(dispatcher) {
        stubShow()
        coEvery { itemRepo.setWatched("e11", false) } throws http(500)
        val vm = DetailViewModel(itemRepo, favRepo)
        vm.load("s1")
        advanceUntilIdle()
        val events = collectEvents(vm)

        val target = vm.uiState.value.seasons.values.flatten().first { it.id == "e11" }
        vm.toggleEpisodeWatched(target)
        advanceUntilIdle()

        assertThat(vm.uiState.value.seasons.values.flatten().first { it.id == "e11" }).isEqualTo(target)
        assertThat(events).containsExactly(DetailEvent.MarkFailed(rateLimited = false))
    }
}
