package tv.onscreen.android.ui.notifications

import com.google.common.truth.Truth.assertThat
import io.mockk.Runs
import io.mockk.coEvery
import io.mockk.coVerify
import io.mockk.every
import io.mockk.just
import io.mockk.mockk
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.ExperimentalCoroutinesApi
import kotlinx.coroutines.channels.Channel
import kotlinx.coroutines.flow.emptyFlow
import kotlinx.coroutines.flow.receiveAsFlow
import kotlinx.coroutines.test.StandardTestDispatcher
import kotlinx.coroutines.test.advanceUntilIdle
import kotlinx.coroutines.test.resetMain
import kotlinx.coroutines.test.runTest
import kotlinx.coroutines.test.setMain
import org.junit.After
import org.junit.Before
import org.junit.Test
import tv.onscreen.android.data.model.NotificationItem
import tv.onscreen.android.data.repository.NotificationsRepository

@OptIn(ExperimentalCoroutinesApi::class)
class NotificationsViewModelTest {

    private val dispatcher = StandardTestDispatcher()

    @Before
    fun setUp() { Dispatchers.setMain(dispatcher) }

    @After
    fun tearDown() { Dispatchers.resetMain() }

    private fun note(id: String, read: Boolean = false) = NotificationItem(
        id = id, type = "system", title = "t-$id", read = read, created_at = "2026-01-01T00:00:00Z",
    )

    @Test
    fun `load populates items and unreadCount`() = runTest(dispatcher) {
        val repo = mockk<NotificationsRepository>()
        coEvery { repo.list(limit = 100) } returns listOf(note("a"), note("b", read = true))
        every { repo.subscribe() } returns emptyFlow()

        val vm = NotificationsViewModel(repo)
        advanceUntilIdle()

        assertThat(vm.uiState.value.items).hasSize(2)
        assertThat(vm.uiState.value.unreadCount).isEqualTo(1L)
    }

    @Test
    fun `markRead flips read flag and decrements unread`() = runTest(dispatcher) {
        val repo = mockk<NotificationsRepository>()
        coEvery { repo.list(limit = 100) } returns listOf(note("a"), note("b"))
        every { repo.subscribe() } returns emptyFlow()
        coEvery { repo.markRead("a") } just Runs

        val vm = NotificationsViewModel(repo)
        advanceUntilIdle()

        vm.markRead("a")
        advanceUntilIdle()

        val state = vm.uiState.value
        assertThat(state.items.first { it.id == "a" }.read).isTrue()
        assertThat(state.unreadCount).isEqualTo(1L)
        coVerify(exactly = 1) { repo.markRead("a") }
    }

    @Test
    fun `markAllRead flips every item and zeroes unread`() = runTest(dispatcher) {
        val repo = mockk<NotificationsRepository>()
        coEvery { repo.list(limit = 100) } returns listOf(note("a"), note("b"))
        every { repo.subscribe() } returns emptyFlow()
        coEvery { repo.markAllRead() } just Runs

        val vm = NotificationsViewModel(repo)
        advanceUntilIdle()

        vm.markAllRead()
        advanceUntilIdle()

        assertThat(vm.uiState.value.items.all { it.read }).isTrue()
        assertThat(vm.uiState.value.unreadCount).isEqualTo(0L)
    }

    @Test
    fun `stream emits merge into state and bump unread`() = runTest(dispatcher) {
        val repo = mockk<NotificationsRepository>()
        coEvery { repo.list(limit = 100) } returns emptyList()
        val channel = Channel<NotificationItem>(Channel.UNLIMITED)
        every { repo.subscribe() } returns channel.receiveAsFlow()

        val vm = NotificationsViewModel(repo)
        advanceUntilIdle()

        channel.send(note("x"))
        advanceUntilIdle()

        assertThat(vm.uiState.value.items.map { it.id }).containsExactly("x")
        assertThat(vm.uiState.value.unreadCount).isEqualTo(1L)

        // Duplicate id ignored.
        channel.send(note("x"))
        advanceUntilIdle()
        assertThat(vm.uiState.value.items).hasSize(1)

        channel.close()
    }
}
