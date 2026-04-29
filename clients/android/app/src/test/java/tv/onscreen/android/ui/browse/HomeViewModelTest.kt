package tv.onscreen.android.ui.browse

import com.google.common.truth.Truth.assertThat
import io.mockk.coEvery
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
import tv.onscreen.android.data.model.HubData
import tv.onscreen.android.data.model.HubItem
import tv.onscreen.android.data.model.Library
import tv.onscreen.android.data.model.MediaCollection
import tv.onscreen.android.data.model.MediaItem
import tv.onscreen.android.data.repository.CollectionRepository
import tv.onscreen.android.data.repository.HubRepository
import tv.onscreen.android.data.repository.LibraryRepository

@OptIn(ExperimentalCoroutinesApi::class)
class HomeViewModelTest {

    private val dispatcher = StandardTestDispatcher()

    @Before fun setUp() { Dispatchers.setMain(dispatcher) }
    @After  fun tearDown() { Dispatchers.resetMain() }

    private fun hub(id: String, type: String = "movie") =
        HubItem(id = id, title = "t-$id", type = type)
    private fun item(id: String) = MediaItem(
        id = id, title = "t-$id", type = "movie",
        created_at = "2026-01-01T00:00:00Z", updated_at = "2026-01-01T00:00:00Z",
    )
    private fun lib(id: String) = Library(
        id = id, name = "L-$id", type = "movies",
        created_at = "2026-01-01T00:00:00Z", updated_at = "2026-01-01T00:00:00Z",
    )

    private fun mocks(
        hubData: HubData = HubData(),
        libraries: List<Library> = emptyList(),
        collections: List<MediaCollection> = emptyList(),
    ): Mocks {
        val hubRepo = mockk<HubRepository>()
        val libRepo = mockk<LibraryRepository>()
        val colRepo = mockk<CollectionRepository>()
        coEvery { hubRepo.getHub() } returns hubData
        coEvery { libRepo.getLibraries() } returns libraries
        coEvery { colRepo.getCollections() } returns collections
        libraries.forEach { l ->
            coEvery { libRepo.getItems(l.id, limit = 20) } returns (emptyList<MediaItem>() to 0)
        }
        return Mocks(hubRepo, libRepo, colRepo)
    }

    private data class Mocks(
        val hub: HubRepository,
        val lib: LibraryRepository,
        val col: CollectionRepository,
    )

    @Test
    fun `load composes hub + recently added + library previews`() = runTest(dispatcher) {
        val m = mocks(
            hubData = HubData(
                continue_watching = listOf(hub("cw1", type = "movie"), hub("cw2", type = "episode")),
                recently_added = listOf(hub("ra1"), hub("ra2")),
            ),
            libraries = listOf(lib("l1")),
        )
        coEvery { m.lib.getItems("l1", limit = 20) } returns (listOf(item("a"), item("b")) to 2)

        val vm = HomeViewModel(m.hub, m.lib, m.col)
        advanceUntilIdle()

        val state = vm.uiState.value
        assertThat(state.isLoading).isFalse()
        assertThat(state.continueWatchingMovies.map { it.id }).containsExactly("cw1")
        assertThat(state.continueWatchingTV.map { it.id }).containsExactly("cw2")
        assertThat(state.recentlyAdded.map { it.id }).containsExactly("ra1", "ra2").inOrder()
        assertThat(state.libraryPreviews).hasSize(1)
        assertThat(state.libraryPreviews[0].second.map { it.id }).containsExactly("a", "b").inOrder()
        assertThat(state.error).isNull()
    }

    @Test
    fun `load records error when hub repo throws`() = runTest(dispatcher) {
        val m = mocks()
        coEvery { m.hub.getHub() } throws RuntimeException("offline")

        val vm = HomeViewModel(m.hub, m.lib, m.col)
        advanceUntilIdle()

        assertThat(vm.uiState.value.error).isEqualTo("offline")
        assertThat(vm.uiState.value.isLoading).isFalse()
    }

    @Test
    fun `library preview failure leaves the row empty without failing the whole load`() = runTest(dispatcher) {
        val m = mocks(libraries = listOf(lib("l1"), lib("l2")))
        coEvery { m.lib.getItems("l1", limit = 20) } returns (listOf(item("a")) to 1)
        coEvery { m.lib.getItems("l2", limit = 20) } throws RuntimeException("lib2 down")

        val vm = HomeViewModel(m.hub, m.lib, m.col)
        advanceUntilIdle()

        val state = vm.uiState.value
        assertThat(state.error).isNull()
        val byId = state.libraryPreviews.associate { it.first.id to it.second }
        assertThat(byId["l1"]!!.map { it.id }).containsExactly("a")
        assertThat(byId["l2"]).isEmpty()
    }

    @Test
    fun `Continue Watching split prefers server-side fields when present`() = runTest(dispatcher) {
        // Newer server: pre-split arrays populated. The
        // client-side filter on continue_watching is skipped, so the
        // legacy combined feed can stay empty without affecting
        // what the UI renders.
        val m = mocks(
            hubData = HubData(
                continue_watching = emptyList(),
                continue_watching_tv = listOf(hub("ep1", type = "episode")),
                continue_watching_movies = listOf(hub("mv1", type = "movie")),
                continue_watching_other = listOf(hub("ab1", type = "audiobook")),
            ),
        )
        val vm = HomeViewModel(m.hub, m.lib, m.col)
        advanceUntilIdle()
        val state = vm.uiState.value
        assertThat(state.continueWatchingTV.map { it.id }).containsExactly("ep1")
        assertThat(state.continueWatchingMovies.map { it.id }).containsExactly("mv1")
        assertThat(state.continueWatchingOther.map { it.id }).containsExactly("ab1")
    }
}
