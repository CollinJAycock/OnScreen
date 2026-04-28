package tv.onscreen.android.ui.history

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
import tv.onscreen.android.data.model.HistoryItem
import tv.onscreen.android.data.repository.HistoryRepository

@OptIn(ExperimentalCoroutinesApi::class)
class HistoryViewModelTest {

    private val dispatcher = StandardTestDispatcher()

    @Before
    fun setUp() { Dispatchers.setMain(dispatcher) }

    @After
    fun tearDown() { Dispatchers.resetMain() }

    private fun hist(id: String) = HistoryItem(
        id = id, media_id = "m-$id", title = "t-$id", type = "movie", occurred_at = "2026-01-01T00:00:00Z",
    )

    @Test
    fun `load populates items on success`() = runTest(dispatcher) {
        val repo = mockk<HistoryRepository>()
        coEvery { repo.list(limit = 100) } returns listOf(hist("a"), hist("b"))

        val vm = HistoryViewModel(repo)
        advanceUntilIdle()

        val state = vm.uiState.value
        assertThat(state.items.map { it.id }).containsExactly("a", "b").inOrder()
        assertThat(state.error).isNull()
    }

    @Test
    fun `load surfaces repository error`() = runTest(dispatcher) {
        val repo = mockk<HistoryRepository>()
        coEvery { repo.list(limit = 100) } throws RuntimeException("bad")

        val vm = HistoryViewModel(repo)
        advanceUntilIdle()

        assertThat(vm.uiState.value.items).isEmpty()
        assertThat(vm.uiState.value.error).isEqualTo("bad")
    }
}
