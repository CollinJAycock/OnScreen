package tv.onscreen.android.ui.detail

import com.google.common.truth.Truth.assertThat
import io.mockk.Runs
import io.mockk.coEvery
import io.mockk.coVerify
import io.mockk.just
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
import tv.onscreen.android.data.model.ChildItem
import tv.onscreen.android.data.model.ItemDetail
import tv.onscreen.android.data.repository.FavoritesRepository
import tv.onscreen.android.data.repository.ItemRepository

@OptIn(ExperimentalCoroutinesApi::class)
class DetailViewModelTest {

    private val dispatcher = StandardTestDispatcher()

    @Before
    fun setUp() {
        Dispatchers.setMain(dispatcher)
    }

    @After
    fun tearDown() {
        Dispatchers.resetMain()
    }

    private fun movie(isFavorite: Boolean) = ItemDetail(
        id = "m1",
        library_id = "lib-1",
        title = "Movie",
        type = "movie",
        is_favorite = isFavorite,
    )

    @Test
    fun `load copies is_favorite from item detail`() = runTest(dispatcher) {
        val itemRepo = mockk<ItemRepository>()
        val favRepo = mockk<FavoritesRepository>()
        coEvery { itemRepo.getItem("m1") } returns movie(isFavorite = true)

        val vm = DetailViewModel(itemRepo, favRepo)
        vm.load("m1")
        advanceUntilIdle()

        assertThat(vm.uiState.value.isFavorite).isTrue()
    }

    @Test
    fun `toggleFavorite flips state and calls add when not already favorited`() = runTest(dispatcher) {
        val itemRepo = mockk<ItemRepository>()
        val favRepo = mockk<FavoritesRepository>()
        coEvery { itemRepo.getItem("m1") } returns movie(isFavorite = false)
        coEvery { favRepo.add("m1") } just Runs

        val vm = DetailViewModel(itemRepo, favRepo)
        vm.load("m1")
        advanceUntilIdle()

        vm.toggleFavorite()
        advanceUntilIdle()

        assertThat(vm.uiState.value.isFavorite).isTrue()
        coVerify(exactly = 1) { favRepo.add("m1") }
    }

    @Test
    fun `toggleFavorite calls remove when already favorited`() = runTest(dispatcher) {
        val itemRepo = mockk<ItemRepository>()
        val favRepo = mockk<FavoritesRepository>()
        coEvery { itemRepo.getItem("m1") } returns movie(isFavorite = true)
        coEvery { favRepo.remove("m1") } just Runs

        val vm = DetailViewModel(itemRepo, favRepo)
        vm.load("m1")
        advanceUntilIdle()

        vm.toggleFavorite()
        advanceUntilIdle()

        assertThat(vm.uiState.value.isFavorite).isFalse()
        coVerify(exactly = 1) { favRepo.remove("m1") }
    }

    @Test
    fun `toggleFavorite reverts state when network call fails`() = runTest(dispatcher) {
        val itemRepo = mockk<ItemRepository>()
        val favRepo = mockk<FavoritesRepository>()
        coEvery { itemRepo.getItem("m1") } returns movie(isFavorite = false)
        coEvery { favRepo.add("m1") } throws RuntimeException("offline")

        val vm = DetailViewModel(itemRepo, favRepo)
        vm.load("m1")
        advanceUntilIdle()

        vm.toggleFavorite()
        advanceUntilIdle()

        assertThat(vm.uiState.value.isFavorite).isFalse()
    }

    @Test
    fun `toggleFavorite before load is a no-op`() = runTest(dispatcher) {
        val itemRepo = mockk<ItemRepository>()
        val favRepo = mockk<FavoritesRepository>()

        val vm = DetailViewModel(itemRepo, favRepo)
        vm.toggleFavorite()
        advanceUntilIdle()

        coVerify(exactly = 0) { favRepo.add(any()) }
        coVerify(exactly = 0) { favRepo.remove(any()) }
    }

    @Test
    fun `show with seasons loads episodes in the season map`() = runTest(dispatcher) {
        val itemRepo = mockk<ItemRepository>()
        val favRepo = mockk<FavoritesRepository>()
        val show = ItemDetail(
            id = "s1", library_id = "lib-1", title = "Show", type = "show",
        )
        coEvery { itemRepo.getItem("s1") } returns show
        coEvery { itemRepo.getChildren("s1") } returns listOf(
            ChildItem(id = "se1", title = "S1", type = "season", index = 1),
        )
        coEvery { itemRepo.getChildren("se1") } returns listOf(
            ChildItem(id = "ep1", title = "E1", type = "episode", index = 1),
            ChildItem(id = "ep2", title = "E2", type = "episode", index = 2),
        )

        val vm = DetailViewModel(itemRepo, favRepo)
        vm.load("s1")
        advanceUntilIdle()

        val seasons = vm.uiState.value.seasons
        assertThat(seasons).hasSize(1)
        val episodes = seasons.values.first()
        assertThat(episodes.map { it.id }).containsExactly("ep1", "ep2").inOrder()
    }
}
