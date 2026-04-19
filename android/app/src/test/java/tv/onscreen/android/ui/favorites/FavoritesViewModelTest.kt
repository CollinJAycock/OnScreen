package tv.onscreen.android.ui.favorites

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
import tv.onscreen.android.data.model.FavoriteItem
import tv.onscreen.android.data.repository.FavoritesRepository

@OptIn(ExperimentalCoroutinesApi::class)
class FavoritesViewModelTest {

    private val dispatcher = StandardTestDispatcher()

    @Before
    fun setUp() { Dispatchers.setMain(dispatcher) }

    @After
    fun tearDown() { Dispatchers.resetMain() }

    private fun fav(id: String) = FavoriteItem(
        id = id, library_id = "lib", type = "movie", title = "t-$id",
    )

    @Test
    fun `load populates items on success`() = runTest(dispatcher) {
        val repo = mockk<FavoritesRepository>()
        coEvery { repo.list(limit = 200) } returns listOf(fav("a"), fav("b"))

        val vm = FavoritesViewModel(repo)
        advanceUntilIdle()

        val state = vm.uiState.value
        assertThat(state.items.map { it.id }).containsExactly("a", "b").inOrder()
        assertThat(state.isLoading).isFalse()
        assertThat(state.error).isNull()
    }

    @Test
    fun `load sets error when repository throws`() = runTest(dispatcher) {
        val repo = mockk<FavoritesRepository>()
        coEvery { repo.list(limit = 200) } throws RuntimeException("nope")

        val vm = FavoritesViewModel(repo)
        advanceUntilIdle()

        val state = vm.uiState.value
        assertThat(state.items).isEmpty()
        assertThat(state.error).isEqualTo("nope")
    }
}
