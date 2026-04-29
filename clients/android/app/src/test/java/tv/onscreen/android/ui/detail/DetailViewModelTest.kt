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

    @Test
    fun `book_author renders series before standalone books`() = runTest(dispatcher) {
        // Series alphabetical first, books year-desc after. The single-
        // bucket render leans on this ordering — without it the user
        // would see series and books shuffled together randomly.
        val itemRepo = mockk<ItemRepository>()
        val favRepo = mockk<FavoritesRepository>()
        val author = ItemDetail(
            id = "auth-1", library_id = "lib", title = "Brandon Sanderson",
            type = "book_author",
        )
        coEvery { itemRepo.getItem("auth-1") } returns author
        coEvery { itemRepo.getChildren("auth-1") } returns listOf(
            ChildItem(id = "b-old", title = "Elantris", type = "audiobook", year = 2005),
            ChildItem(id = "s-mistborn", title = "Mistborn", type = "book_series"),
            ChildItem(id = "b-new", title = "Tress", type = "audiobook", year = 2023),
            ChildItem(id = "s-stormlight", title = "Stormlight Archive", type = "book_series"),
            // Foreign type — chapter rows belong under their book
            // parent, never on the author detail.
            ChildItem(id = "noise", title = "Ch1", type = "audiobook_chapter"),
        )

        val vm = DetailViewModel(itemRepo, favRepo)
        vm.load("auth-1")
        advanceUntilIdle()

        val seasons = vm.uiState.value.seasons
        assertThat(seasons).hasSize(1)
        val children = seasons.values.first()
        assertThat(children.map { it.id }).containsExactly(
            "s-mistborn", "s-stormlight", "b-new", "b-old",
        ).inOrder()
    }

    @Test
    fun `book_series surfaces children for the books list`() = runTest(dispatcher) {
        // Same shape as season / album — VM hands children through;
        // the rendering layer sorts. Locks the route through the
        // multi-type case so a refactor that changes the type-switch
        // can't drop book_series into the default empty branch.
        val itemRepo = mockk<ItemRepository>()
        val favRepo = mockk<FavoritesRepository>()
        val series = ItemDetail(
            id = "ser-1", library_id = "lib", title = "Mistborn",
            type = "book_series",
        )
        coEvery { itemRepo.getItem("ser-1") } returns series
        coEvery { itemRepo.getChildren("ser-1") } returns listOf(
            ChildItem(id = "b1", title = "The Final Empire", type = "audiobook", year = 2006),
            ChildItem(id = "b2", title = "The Well of Ascension", type = "audiobook", year = 2007),
        )

        val vm = DetailViewModel(itemRepo, favRepo)
        vm.load("ser-1")
        advanceUntilIdle()

        val seasons = vm.uiState.value.seasons
        assertThat(seasons).hasSize(1)
        val children = seasons.values.first()
        assertThat(children.map { it.id }).containsExactly("b1", "b2").inOrder()
    }
}
