package tv.onscreen.mobile.ui.item

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
import tv.onscreen.mobile.data.model.ChildItem
import tv.onscreen.mobile.data.model.ItemDetail
import tv.onscreen.mobile.data.model.UpNext
import tv.onscreen.mobile.data.model.UpNextEpisode
import tv.onscreen.mobile.data.repository.ItemRepository

@OptIn(ExperimentalCoroutinesApi::class)
class ItemWatchViewModelTest {

    private val dispatcher = StandardTestDispatcher()

    @Before fun setUp() { Dispatchers.setMain(dispatcher) }
    @After fun tearDown() { Dispatchers.resetMain() }

    private fun detail(id: String, type: String, watchState: String? = null) = ItemDetail(
        id = id, library_id = "lib", title = "T-$id", type = type, watch_state = watchState,
    )

    private fun season(id: String, index: Int) = ChildItem(id = id, title = "Season $index", type = "season", index = index)

    private fun episode(id: String, index: Int, watched: Boolean = false, offset: Long = 0) =
        ChildItem(id = id, title = "Ep $id", type = "episode", index = index, watched = watched, view_offset_ms = offset, duration_ms = 1000)

    private fun upNext(mode: String, epId: String, seasonId: String, s: Int, e: Int) =
        UpNext(mode, UpNextEpisode(id = epId, title = "Ep $epId", season_id = seasonId, season_number = s, episode_number = e))

    private fun http(code: Int) = HttpException(Response.error<Unit>(code, "".toResponseBody(null)))

    @Test
    fun `show opens on the season holding the up-next episode`() = runTest(dispatcher) {
        val repo = mockk<ItemRepository>()
        // Out of order on purpose — the VM sorts by index.
        coEvery { repo.getChildren("show") } returns listOf(season("s2", 2), season("s1", 1), season("s3", 3))
        coEvery { repo.getUpNext("show") } returns upNext("resume", "e24", "s2", 2, 4)
        coEvery { repo.getChildren("s2") } returns listOf(episode("e25", 5), episode("e24", 4, offset = 300))

        val vm = ItemWatchViewModel(repo)
        vm.bind(detail("show", "show"))
        advanceUntilIdle()

        val s = vm.state.value
        assertThat(s.seasons.map { it.id }).containsExactly("s1", "s2", "s3").inOrder()
        assertThat(s.selectedSeasonId).isEqualTo("s2")
        assertThat(s.selectedEpisodes.map { it.id }).containsExactly("e24", "e25").inOrder()
        assertThat(s.upNext!!.episode!!.id).isEqualTo("e24")
        assertThat(s.upNextLoaded).isTrue()
        assertThat(s.loadingContainer).isFalse()
        // Only the selected season is fetched up front.
        coVerify(exactly = 0) { repo.getChildren("s1") }
    }

    @Test
    fun `up-next failure falls back to the first season and still loads`() = runTest(dispatcher) {
        val repo = mockk<ItemRepository>()
        coEvery { repo.getChildren("show") } returns listOf(season("s1", 1), season("s2", 2))
        coEvery { repo.getUpNext("show") } throws http(404)
        coEvery { repo.getChildren("s1") } returns listOf(episode("e11", 1))

        val vm = ItemWatchViewModel(repo)
        vm.bind(detail("show", "show"))
        advanceUntilIdle()

        val s = vm.state.value
        assertThat(s.upNext).isNull()
        assertThat(s.upNextLoaded).isTrue()
        assertThat(s.selectedSeasonId).isEqualTo("s1")
        assertThat(s.selectedEpisodes.map { it.id }).containsExactly("e11")
    }

    @Test
    fun `selecting another season loads it once`() = runTest(dispatcher) {
        val repo = mockk<ItemRepository>()
        coEvery { repo.getChildren("show") } returns listOf(season("s1", 1), season("s2", 2))
        coEvery { repo.getUpNext("show") } returns upNext("start", "e11", "s1", 1, 1)
        coEvery { repo.getChildren("s1") } returns listOf(episode("e11", 1))
        coEvery { repo.getChildren("s2") } returns listOf(episode("e21", 1))

        val vm = ItemWatchViewModel(repo)
        vm.bind(detail("show", "show"))
        advanceUntilIdle()
        vm.selectSeason("s2")
        advanceUntilIdle()
        vm.selectSeason("s1")
        vm.selectSeason("s2")
        advanceUntilIdle()

        assertThat(vm.state.value.selectedEpisodes.map { it.id }).containsExactly("e21")
        coVerify(exactly = 1) { repo.getChildren("s2") }
        coVerify(exactly = 1) { repo.getChildren("s1") }
    }

    @Test
    fun `season page lists its own episodes`() = runTest(dispatcher) {
        val repo = mockk<ItemRepository>()
        coEvery { repo.getChildren("s1") } returns listOf(episode("e2", 2), episode("e1", 1, watched = true))
        coEvery { repo.getUpNext("s1") } returns upNext("next", "e2", "s1", 1, 2)

        val vm = ItemWatchViewModel(repo)
        vm.bind(detail("s1", "season"))
        advanceUntilIdle()

        val s = vm.state.value
        assertThat(s.seasons).isEmpty()
        assertThat(s.selectedSeasonId).isEqualTo("s1")
        assertThat(s.selectedEpisodes.map { it.id }).containsExactly("e1", "e2").inOrder()
    }

    @Test
    fun `container load failure surfaces an error and retry recovers`() = runTest(dispatcher) {
        val repo = mockk<ItemRepository>()
        coEvery { repo.getChildren("s1") } throws RuntimeException("offline") andThen listOf(episode("e1", 1))
        coEvery { repo.getUpNext("s1") } returns UpNext("start")

        val vm = ItemWatchViewModel(repo)
        vm.bind(detail("s1", "season"))
        advanceUntilIdle()
        assertThat(vm.state.value.containerError).isEqualTo("offline")

        vm.retry()
        advanceUntilIdle()
        assertThat(vm.state.value.containerError).isNull()
        assertThat(vm.state.value.selectedEpisodes.map { it.id }).containsExactly("e1")
    }

    @Test
    fun `episode toggle is optimistic then refreshes the season and up-next`() = runTest(dispatcher) {
        val repo = mockk<ItemRepository>()
        coEvery { repo.getChildren("s1") } returns listOf(episode("e1", 1), episode("e2", 2)) andThen
            listOf(episode("e1", 1, watched = true), episode("e2", 2))
        coEvery { repo.getUpNext("s1") } returns upNext("start", "e1", "s1", 1, 1) andThen upNext("next", "e2", "s1", 1, 2)
        val gate = CompletableDeferred<Unit>()
        coEvery { repo.setWatched("e1", true) } coAnswers { gate.await() }

        val vm = ItemWatchViewModel(repo)
        vm.bind(detail("s1", "season"))
        advanceUntilIdle()

        vm.toggleEpisodeWatched(vm.state.value.selectedEpisodes.first())
        // Flipped before the server answers, and marked busy.
        assertThat(vm.state.value.selectedEpisodes.first().watched).isTrue()
        assertThat(vm.state.value.episodeBusy).containsExactly("e1")

        gate.complete(Unit)
        advanceUntilIdle()
        val s = vm.state.value
        assertThat(s.episodeBusy).isEmpty()
        assertThat(s.selectedEpisodes.first().watched).isTrue()
        assertThat(s.upNext!!.episode!!.id).isEqualTo("e2")
    }

    @Test
    fun `episode toggle failure rolls back with the rate-limit message on 429`() = runTest(dispatcher) {
        val repo = mockk<ItemRepository>()
        coEvery { repo.getChildren("s1") } returns listOf(episode("e1", 1, offset = 400))
        coEvery { repo.getUpNext("s1") } returns upNext("resume", "e1", "s1", 1, 1)
        coEvery { repo.setWatched("e1", true) } throws http(429)

        val vm = ItemWatchViewModel(repo)
        vm.bind(detail("s1", "season"))
        advanceUntilIdle()
        vm.toggleEpisodeWatched(vm.state.value.selectedEpisodes.first())
        advanceUntilIdle()

        val ep = vm.state.value.selectedEpisodes.first()
        assertThat(ep.watched).isFalse()
        assertThat(ep.view_offset_ms).isEqualTo(400)
        assertThat(vm.state.value.message).isEqualTo("Too many changes at once — try again in a minute")
    }

    @Test
    fun `movie toggle flips, confirms, and rolls back on failure`() = runTest(dispatcher) {
        val repo = mockk<ItemRepository>()
        coEvery { repo.setWatched("m", true) } returns Unit
        coEvery { repo.setWatched("m", false) } throws RuntimeException("nope")

        val vm = ItemWatchViewModel(repo)
        vm.bind(detail("m", "movie", watchState = "in_progress"))
        advanceUntilIdle()
        assertThat(vm.state.value.itemWatched).isFalse()

        vm.toggleItemWatched()
        advanceUntilIdle()
        assertThat(vm.state.value.itemWatched).isTrue()
        assertThat(vm.state.value.message).isEqualTo("Marked as watched")
        vm.consumeMessage()

        vm.toggleItemWatched()
        advanceUntilIdle()
        assertThat(vm.state.value.itemWatched).isTrue()
        assertThat(vm.state.value.message).isEqualTo("Couldn't update watched state")
        // Movies never fetch children / up-next.
        coVerify(exactly = 0) { repo.getChildren(any()) }
        coVerify(exactly = 0) { repo.getUpNext(any()) }
    }

    @Test
    fun `detail watch_state seeds the toggle and a rebind picks up the server value`() = runTest(dispatcher) {
        val repo = mockk<ItemRepository>()
        val vm = ItemWatchViewModel(repo)
        vm.bind(detail("ep", "episode", watchState = "watched"))
        assertThat(vm.state.value.itemWatched).isTrue()
        // Came back from the player / another device unmarked it.
        vm.bind(detail("ep", "episode", watchState = "unwatched"))
        assertThat(vm.state.value.itemWatched).isFalse()
    }

    @Test
    fun `mark all watched marks the container and refreshes loaded seasons`() = runTest(dispatcher) {
        val repo = mockk<ItemRepository>()
        coEvery { repo.getChildren("show") } returns listOf(season("s1", 1))
        coEvery { repo.getUpNext("show") } returns upNext("start", "e1", "s1", 1, 1) andThen upNext("rewatch", "e1", "s1", 1, 1)
        coEvery { repo.getChildren("s1") } returns listOf(episode("e1", 1)) andThen listOf(episode("e1", 1, watched = true))
        coEvery { repo.setWatched("show", true) } returns Unit

        val vm = ItemWatchViewModel(repo)
        vm.bind(detail("show", "show"))
        advanceUntilIdle()
        vm.markAll(true)
        assertThat(vm.state.value.markBusy).isTrue()
        advanceUntilIdle()

        val s = vm.state.value
        assertThat(s.markBusy).isFalse()
        assertThat(s.upNext!!.mode).isEqualTo("rewatch")
        assertThat(s.selectedEpisodes.single().watched).isTrue()
        assertThat(s.message).isEqualTo("Marked all as watched")
        coVerify(exactly = 1) { repo.setWatched("show", true) }
    }

    @Test
    fun `mark season marks only that season and refreshes it`() = runTest(dispatcher) {
        val repo = mockk<ItemRepository>()
        coEvery { repo.getChildren("show") } returns listOf(season("s1", 1), season("s2", 2))
        coEvery { repo.getUpNext("show") } returns upNext("start", "e11", "s1", 1, 1) andThen upNext("start", "e21", "s2", 2, 1)
        coEvery { repo.getChildren("s1") } returns listOf(episode("e11", 1)) andThen listOf(episode("e11", 1, watched = true))
        coEvery { repo.setWatched("s1", true) } returns Unit

        val vm = ItemWatchViewModel(repo)
        vm.bind(detail("show", "show"))
        advanceUntilIdle()
        vm.markSeason("s1", true)
        advanceUntilIdle()

        val s = vm.state.value
        assertThat(s.selectedEpisodes.single().watched).isTrue()
        assertThat(s.upNext!!.episode!!.id).isEqualTo("e21")
        assertThat(s.message).isEqualTo("Marked season as watched")
        coVerify(exactly = 0) { repo.setWatched("show", any()) }
        // An id that isn't one of this show's seasons is ignored.
        vm.markSeason("elsewhere", true)
        coVerify(exactly = 0) { repo.setWatched("elsewhere", any()) }
    }

    @Test
    fun `mark all failure keeps the lists and reports`() = runTest(dispatcher) {
        val repo = mockk<ItemRepository>()
        coEvery { repo.getChildren("s1") } returns listOf(episode("e1", 1, watched = true))
        coEvery { repo.getUpNext("s1") } returns upNext("rewatch", "e1", "s1", 1, 1)
        coEvery { repo.setWatched("s1", false) } throws http(500)

        val vm = ItemWatchViewModel(repo)
        vm.bind(detail("s1", "season"))
        advanceUntilIdle()
        vm.markAll(false)
        advanceUntilIdle()

        assertThat(vm.state.value.markBusy).isFalse()
        assertThat(vm.state.value.selectedEpisodes.single().watched).isTrue()
        assertThat(vm.state.value.message).isEqualTo("Couldn't update watched state")
    }

    @Test
    fun `rebinding the same show refreshes in place instead of reloading`() = runTest(dispatcher) {
        val repo = mockk<ItemRepository>()
        coEvery { repo.getChildren("show") } returns listOf(season("s1", 1))
        coEvery { repo.getUpNext("show") } returns upNext("start", "e1", "s1", 1, 1) andThen upNext("resume", "e1", "s1", 1, 1)
        coEvery { repo.getChildren("s1") } returns listOf(episode("e1", 1)) andThen listOf(episode("e1", 1, offset = 500))

        val vm = ItemWatchViewModel(repo)
        vm.bind(detail("show", "show"))
        advanceUntilIdle()
        // Back from the player.
        vm.bind(detail("show", "show"))
        assertThat(vm.state.value.selectedEpisodes).isNotEmpty() // no blanking
        advanceUntilIdle()

        assertThat(vm.state.value.upNext!!.mode).isEqualTo("resume")
        assertThat(vm.state.value.selectedEpisodes.single().view_offset_ms).isEqualTo(500)
        coVerify(exactly = 1) { repo.getChildren("show") }
    }

    @Test
    fun `selectedSeasonIndex points the chip row at the up-next season`() = runTest(dispatcher) {
        // A 17-season show part-way into S17: the chip row must scroll to
        // index 16, not open on S1.
        val repo = mockk<ItemRepository>()
        val seasons = (1..17).map { season("s$it", it) }
        coEvery { repo.getChildren("show") } returns seasons.reversed()
        coEvery { repo.getUpNext("show") } returns upNext("next", "e17-3", "s17", 17, 3)
        coEvery { repo.getChildren("s17") } returns listOf(episode("e17-3", 3))

        val vm = ItemWatchViewModel(repo)
        assertThat(vm.state.value.selectedSeasonIndex).isEqualTo(-1)
        vm.bind(detail("show", "show"))
        advanceUntilIdle()
        assertThat(vm.state.value.selectedSeasonIndex).isEqualTo(16)

        coEvery { repo.getChildren("s11") } returns listOf(episode("e11-1", 1))
        vm.selectSeason("s11")
        assertThat(vm.state.value.selectedSeasonIndex).isEqualTo(10)
    }

    @Test
    fun `selectedSeasonIndex is -1 on a season page`() = runTest(dispatcher) {
        val repo = mockk<ItemRepository>()
        coEvery { repo.getChildren("s1") } returns listOf(episode("e1", 1))
        coEvery { repo.getUpNext("s1") } returns upNext("start", "e1", "s1", 1, 1)
        val vm = ItemWatchViewModel(repo)
        vm.bind(detail("s1", "season"))
        advanceUntilIdle()
        assertThat(vm.state.value.selectedSeasonIndex).isEqualTo(-1)
    }

    @Test
    fun `resume point follows the detail and a mark clears it`() = runTest(dispatcher) {
        val repo = mockk<ItemRepository>()
        coEvery { repo.setWatched("m", true) } returns Unit
        val vm = ItemWatchViewModel(repo)

        vm.bind(detail("m", "movie", watchState = "in_progress").copy(view_offset_ms = 1_204_000))
        assertThat(vm.state.value.resumeMs).isEqualTo(1_204_000)
        // Back from the player further in.
        vm.bind(detail("m", "movie", watchState = "in_progress").copy(view_offset_ms = 1_500_000))
        assertThat(vm.state.value.resumeMs).isEqualTo(1_500_000)

        // Marking watched drops the resume point (the server does too).
        vm.toggleItemWatched()
        assertThat(vm.state.value.resumeMs).isEqualTo(0)
        advanceUntilIdle()
        assertThat(vm.state.value.resumeMs).isEqualTo(0)
        assertThat(vm.state.value.itemWatched).isTrue()
    }

    @Test
    fun `a failed mark restores the resume point`() = runTest(dispatcher) {
        val repo = mockk<ItemRepository>()
        coEvery { repo.setWatched("m", true) } throws http(500)
        val vm = ItemWatchViewModel(repo)
        vm.bind(detail("m", "movie", watchState = "in_progress").copy(view_offset_ms = 60_000))

        vm.toggleItemWatched()
        advanceUntilIdle()
        assertThat(vm.state.value.resumeMs).isEqualTo(60_000)
        assertThat(vm.state.value.itemWatched).isFalse()
    }
}
