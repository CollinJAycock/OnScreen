package tv.onscreen.android.ui.browse

import com.google.common.truth.Truth.assertThat
import io.mockk.coEvery
import io.mockk.every
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
import tv.onscreen.android.data.model.HubData
import tv.onscreen.android.data.model.HubItem
import tv.onscreen.android.data.model.Library
import tv.onscreen.android.data.model.MediaCollection
import tv.onscreen.android.data.model.MediaItem
import tv.onscreen.android.data.model.NotificationItem
import tv.onscreen.android.data.repository.CollectionRepository
import tv.onscreen.android.data.repository.HubRepository
import tv.onscreen.android.data.repository.LibraryRepository
import tv.onscreen.android.data.repository.NotificationsRepository

@OptIn(ExperimentalCoroutinesApi::class)
class HomeViewModelTest {

    private val dispatcher = StandardTestDispatcher()

    @Before
    fun setUp() { Dispatchers.setMain(dispatcher) }

    @After
    fun tearDown() { Dispatchers.resetMain() }

    private fun hub(id: String) = HubItem(id = id, title = "t-$id", type = "movie")
    private fun item(id: String) = MediaItem(
        id = id, title = "t-$id", type = "movie",
        created_at = "2026-01-01T00:00:00Z", updated_at = "2026-01-01T00:00:00Z",
    )
    private fun lib(id: String) = Library(
        id = id, name = "L-$id", type = "movies",
        created_at = "2026-01-01T00:00:00Z", updated_at = "2026-01-01T00:00:00Z",
    )

    private fun mocks(
        hubData: HubData = HubData(continue_watching = emptyList(), recently_added = emptyList()),
        libraries: List<Library> = emptyList(),
        collections: List<MediaCollection> = emptyList(),
        unread: Long = 0,
    ): Quad {
        val hubRepo = mockk<HubRepository>()
        val libRepo = mockk<LibraryRepository>()
        val colRepo = mockk<CollectionRepository>()
        val notifRepo = mockk<NotificationsRepository>()

        coEvery { hubRepo.getHub() } returns hubData
        coEvery { libRepo.getLibraries() } returns libraries
        coEvery { colRepo.getCollections() } returns collections
        coEvery { notifRepo.unreadCount() } returns unread
        libraries.forEach { l ->
            coEvery { libRepo.getItems(l.id, limit = 20) } returns (emptyList<MediaItem>() to 0)
        }
        every { notifRepo.subscribe() } returns emptyFlow()

        return Quad(hubRepo, libRepo, colRepo, notifRepo)
    }

    private data class Quad(
        val hub: HubRepository,
        val lib: LibraryRepository,
        val col: CollectionRepository,
        val notif: NotificationsRepository,
    )

    @Test
    fun `load composes hub, recently added, library previews, and unread count`() = runTest(dispatcher) {
        val m = mocks(
            hubData = HubData(
                continue_watching = listOf(hub("cw1")),
                recently_added = listOf(hub("ra1"), hub("ra2")),
            ),
            libraries = listOf(lib("l1")),
            unread = 7,
        )
        coEvery { m.lib.getItems("l1", limit = 20) } returns (listOf(item("a"), item("b")) to 2)

        val vm = HomeViewModel(m.hub, m.lib, m.col, m.notif)
        advanceUntilIdle()

        val state = vm.uiState.value
        assertThat(state.isLoading).isFalse()
        assertThat(state.continueWatching.map { it.id }).containsExactly("cw1")
        assertThat(state.recentlyAdded.map { it.id }).containsExactly("ra1", "ra2").inOrder()
        assertThat(state.libraryPreviews).hasSize(1)
        assertThat(state.libraryPreviews[0].second.map { it.id }).containsExactly("a", "b").inOrder()
        assertThat(state.unreadNotifications).isEqualTo(7)
        assertThat(state.error).isNull()
    }

    @Test
    fun `load records error when hub repo throws`() = runTest(dispatcher) {
        val m = mocks()
        coEvery { m.hub.getHub() } throws RuntimeException("offline")

        val vm = HomeViewModel(m.hub, m.lib, m.col, m.notif)
        advanceUntilIdle()

        assertThat(vm.uiState.value.error).isEqualTo("offline")
        assertThat(vm.uiState.value.isLoading).isFalse()
    }

    @Test
    fun `library preview failure leaves the row empty without failing the whole load`() = runTest(dispatcher) {
        val m = mocks(libraries = listOf(lib("l1"), lib("l2")))
        coEvery { m.lib.getItems("l1", limit = 20) } returns (listOf(item("a")) to 1)
        coEvery { m.lib.getItems("l2", limit = 20) } throws RuntimeException("lib2 down")

        val vm = HomeViewModel(m.hub, m.lib, m.col, m.notif)
        advanceUntilIdle()

        val state = vm.uiState.value
        assertThat(state.error).isNull()
        val byId = state.libraryPreviews.associate { it.first.id to it.second }
        assertThat(byId["l1"]!!.map { it.id }).containsExactly("a")
        assertThat(byId["l2"]).isEmpty()
    }

    @Test
    fun `SSE notification emit bumps unread count`() = runTest(dispatcher) {
        val hubRepo = mockk<HubRepository>()
        val libRepo = mockk<LibraryRepository>()
        val colRepo = mockk<CollectionRepository>()
        val notifRepo = mockk<NotificationsRepository>()
        coEvery { hubRepo.getHub() } returns HubData(emptyList(), emptyList())
        coEvery { libRepo.getLibraries() } returns emptyList()
        coEvery { colRepo.getCollections() } returns emptyList()
        coEvery { notifRepo.unreadCount() } returns 2
        val ch = Channel<NotificationItem>(Channel.UNLIMITED)
        every { notifRepo.subscribe() } returns ch.receiveAsFlow()

        val vm = HomeViewModel(hubRepo, libRepo, colRepo, notifRepo)
        advanceUntilIdle()

        assertThat(vm.uiState.value.unreadNotifications).isEqualTo(2)

        ch.send(NotificationItem(
            id = "n1", type = "new_content", title = "New", created_at = "2026-01-01T00:00:00Z",
        ))
        advanceUntilIdle()
        assertThat(vm.uiState.value.unreadNotifications).isEqualTo(3)

        ch.close()
    }
}
