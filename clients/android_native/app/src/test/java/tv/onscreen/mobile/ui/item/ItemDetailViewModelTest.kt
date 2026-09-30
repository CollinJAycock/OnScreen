package tv.onscreen.mobile.ui.item

import com.google.common.truth.Truth.assertThat
import io.mockk.coEvery
import io.mockk.coVerify
import io.mockk.every
import io.mockk.mockk
import kotlinx.coroutines.CompletableDeferred
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.ExperimentalCoroutinesApi
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.test.StandardTestDispatcher
import kotlinx.coroutines.test.advanceUntilIdle
import kotlinx.coroutines.test.resetMain
import kotlinx.coroutines.test.runTest
import kotlinx.coroutines.test.setMain
import org.junit.After
import org.junit.Before
import org.junit.Test
import tv.onscreen.mobile.data.downloads.DownloadManifest
import tv.onscreen.mobile.data.downloads.DownloadStore
import tv.onscreen.mobile.data.downloads.OnScreenDownloadManager
import tv.onscreen.mobile.data.model.ChildItem
import tv.onscreen.mobile.data.model.ItemDetail
import tv.onscreen.mobile.data.prefs.ServerPrefs
import tv.onscreen.mobile.data.repository.FavoritesRepository
import tv.onscreen.mobile.data.repository.ItemRepository

/**
 * The detail page's load / refresh. The case that matters: coming back from
 * the player re-runs load() for the same item, and the page must keep its
 * children and album start meanwhile — resetting them drew a greyed-out Play
 * and "No playable files" until the children came back.
 */
@OptIn(ExperimentalCoroutinesApi::class)
class ItemDetailViewModelTest {

    private val dispatcher = StandardTestDispatcher()

    @Before fun setUp() { Dispatchers.setMain(dispatcher) }
    @After fun tearDown() { Dispatchers.resetMain() }

    private val repo = mockk<ItemRepository>().also {
        coEvery { it.getWatchStatus(any()) } returns null
    }
    private val store = mockk<DownloadStore>(relaxed = true).also {
        coEvery { it.load() } returns Unit
        // A real StateFlow: a relaxed one hands back an Object that fails
        // the DownloadManifest cast.
        every { it.state } returns MutableStateFlow(DownloadManifest(entries = emptyList()))
    }
    private val downloads = mockk<OnScreenDownloadManager>().also { every { it.store } returns store }
    private val prefs = mockk<ServerPrefs>(relaxed = true).also {
        coEvery { it.getServerUrl() } returns "http://srv/"
    }

    private fun vm() = ItemDetailViewModel(repo, downloads, mockk<FavoritesRepository>(relaxed = true), prefs)

    private fun detail(id: String, type: String, offset: Long = 0) =
        ItemDetail(id = id, library_id = "lib", title = "T-$id", type = type, view_offset_ms = offset)

    private fun track(id: String, index: Int) = ChildItem(id = id, title = "T-$id", type = "track", index = index)

    private fun chapter(id: String, index: Int) =
        ChildItem(id = id, title = "Ch-$id", type = "audiobook_chapter", index = index)

    @Test
    fun `an album's Play waits on its tracks, fetched before the download manifest`() = runTest(dispatcher) {
        val gate = CompletableDeferred<List<ChildItem>>()
        coEvery { repo.getItem("al") } returns detail("al", "album")
        coEvery { repo.getChildren("al") } coAnswers { gate.await() }

        val vm = vm()
        vm.load("al")
        advanceUntilIdle()

        var s = vm.state.value
        assertThat(s.loading).isFalse()
        assertThat(s.detail!!.id).isEqualTo("al")
        assertThat(s.serverUrl).isEqualTo("http://srv")
        assertThat(s.loadSeq).isEqualTo(1)
        assertThat(s.childrenLoaded).isFalse()
        assertThat(s.playStartResolved).isFalse()
        coVerify(exactly = 0) { store.load() }

        gate.complete(listOf(track("t1", 1), track("t2", 2)))
        advanceUntilIdle()

        s = vm.state.value
        assertThat(s.childrenLoaded).isTrue()
        assertThat(s.children.map { it.id }).containsExactly("t1", "t2").inOrder()
        assertThat(s.playStartId).isEqualTo("t1")
        assertThat(s.playStartResolved).isTrue()
        coVerify(exactly = 1) { store.load() }
    }

    @Test
    fun `a multi-file audiobook's children are pending until they answer`() = runTest(dispatcher) {
        val gate = CompletableDeferred<List<ChildItem>>()
        coEvery { repo.getItem("book") } returns detail("book", "audiobook")
        coEvery { repo.getChildren("book") } coAnswers { gate.await() }

        val vm = vm()
        vm.load("book")
        advanceUntilIdle()
        assertThat(vm.state.value.childrenLoaded).isFalse()
        assertThat(vm.state.value.children).isEmpty()

        gate.complete(listOf(chapter("c1", 1), chapter("c2", 2)))
        advanceUntilIdle()
        assertThat(vm.state.value.childrenLoaded).isTrue()
        assertThat(vm.state.value.children.map { it.id }).containsExactly("c1", "c2").inOrder()
    }

    @Test
    fun `a leaf has its children settled at once and never fetches them`() = runTest(dispatcher) {
        coEvery { repo.getItem("m") } returns detail("m", "movie")

        val vm = vm()
        vm.load("m")
        advanceUntilIdle()

        assertThat(vm.state.value.childrenLoaded).isTrue()
        coVerify(exactly = 0) { repo.getChildren(any()) }
    }

    @Test
    fun `reloading the same item refreshes in place`() = runTest(dispatcher) {
        val gate = CompletableDeferred<List<ChildItem>>()
        var calls = 0
        coEvery { repo.getItem("al") } returns detail("al", "album")
        coEvery { repo.getChildren("al") } coAnswers {
            if (++calls == 1) listOf(track("t1", 1), track("t2", 2)) else gate.await()
        }

        val vm = vm()
        vm.load("al")
        advanceUntilIdle()
        assertThat(vm.state.value.loadSeq).isEqualTo(1)

        // Back from the player: nothing blanks, not even before the
        // detail request answers.
        vm.load("al")
        var s = vm.state.value
        assertThat(s.loading).isFalse()
        assertThat(s.detail!!.id).isEqualTo("al")
        assertThat(s.children.map { it.id }).containsExactly("t1", "t2").inOrder()
        assertThat(s.playStartId).isEqualTo("t1")

        // The new detail is out (loadSeq moves on, so the watch state and
        // bookmarks re-bind); the tracks are still on their way.
        advanceUntilIdle()
        s = vm.state.value
        assertThat(s.loading).isFalse()
        assertThat(s.loadSeq).isEqualTo(2)
        assertThat(s.childrenLoaded).isTrue()
        assertThat(s.children.map { it.id }).containsExactly("t1", "t2").inOrder()
        assertThat(s.playStartId).isEqualTo("t1")
        assertThat(s.playStartResolved).isTrue()

        gate.complete(listOf(track("t0", 1), track("t1", 2)))
        advanceUntilIdle()
        s = vm.state.value
        assertThat(s.children.map { it.id }).containsExactly("t0", "t1").inOrder()
        assertThat(s.playStartId).isEqualTo("t0")
        assertThat(s.loadSeq).isEqualTo(2)
    }

    @Test
    fun `a refresh picks up the new resume point`() = runTest(dispatcher) {
        coEvery { repo.getItem("m") } returns detail("m", "movie", offset = 1_000) andThen
            detail("m", "movie", offset = 90_000)

        val vm = vm()
        vm.load("m")
        advanceUntilIdle()
        vm.load("m")
        advanceUntilIdle()

        assertThat(vm.state.value.detail!!.view_offset_ms).isEqualTo(90_000)
        assertThat(vm.state.value.loadSeq).isEqualTo(2)
    }

    @Test
    fun `a different item resets the page`() = runTest(dispatcher) {
        val gate = CompletableDeferred<ItemDetail>()
        coEvery { repo.getItem("a") } returns detail("a", "album")
        coEvery { repo.getChildren("a") } returns listOf(track("t1", 1))
        coEvery { repo.getItem("b") } coAnswers { gate.await() }
        coEvery { repo.getChildren("b") } returns listOf(track("u1", 1))

        val vm = vm()
        vm.load("a")
        advanceUntilIdle()

        vm.load("b")
        advanceUntilIdle()
        var s = vm.state.value
        assertThat(s.loading).isTrue()
        assertThat(s.detail).isNull()
        assertThat(s.children).isEmpty()
        assertThat(s.childrenLoaded).isFalse()
        assertThat(s.playStartId).isNull()
        assertThat(s.playStartResolved).isFalse()

        gate.complete(detail("b", "album"))
        advanceUntilIdle()
        s = vm.state.value
        assertThat(s.detail!!.id).isEqualTo("b")
        assertThat(s.playStartId).isEqualTo("u1")
        assertThat(s.loadSeq).isEqualTo(2)
    }

    @Test
    fun `a failed children fetch still settles the page`() = runTest(dispatcher) {
        coEvery { repo.getItem("al") } returns detail("al", "album")
        coEvery { repo.getChildren("al") } throws RuntimeException("offline")

        val vm = vm()
        vm.load("al")
        advanceUntilIdle()

        val s = vm.state.value
        assertThat(s.error).isNull()
        assertThat(s.detail!!.id).isEqualTo("al")
        assertThat(s.childrenLoaded).isTrue()
        assertThat(s.children).isEmpty()
        // Nothing to play: the page can now say so.
        assertThat(s.playStartResolved).isTrue()
        assertThat(s.playStartId).isNull()
    }

    @Test
    fun `a failed refresh keeps the page instead of an error`() = runTest(dispatcher) {
        coEvery { repo.getItem("al") } returns detail("al", "album") andThenThrows RuntimeException("offline")
        coEvery { repo.getChildren("al") } returns listOf(track("t1", 1))

        val vm = vm()
        vm.load("al")
        advanceUntilIdle()
        vm.load("al")
        advanceUntilIdle()

        val s = vm.state.value
        assertThat(s.error).isNull()
        assertThat(s.loading).isFalse()
        assertThat(s.detail!!.id).isEqualTo("al")
        assertThat(s.children.map { it.id }).containsExactly("t1")
        assertThat(s.playStartId).isEqualTo("t1")
    }

    @Test
    fun `a failed first load shows the error and a retry loads afresh`() = runTest(dispatcher) {
        coEvery { repo.getItem("m") } throws RuntimeException("offline") andThen detail("m", "movie")

        val vm = vm()
        vm.load("m")
        advanceUntilIdle()
        assertThat(vm.state.value.error).isEqualTo("offline")
        assertThat(vm.state.value.detail).isNull()

        vm.load("m")
        assertThat(vm.state.value.loading).isTrue()
        assertThat(vm.state.value.error).isNull()
        advanceUntilIdle()
        assertThat(vm.state.value.detail!!.id).isEqualTo("m")
    }

    @Test
    fun `a superseded load writes nothing`() = runTest(dispatcher) {
        val gate = CompletableDeferred<ItemDetail>()
        var calls = 0
        coEvery { repo.getItem("m") } coAnswers {
            if (++calls == 1) gate.await() else detail("m", "movie", offset = 5)
        }

        val vm = vm()
        vm.load("m")
        advanceUntilIdle()
        vm.load("m")
        advanceUntilIdle()
        assertThat(vm.state.value.loadSeq).isEqualTo(1)

        // The first request answering late changes nothing.
        gate.complete(detail("m", "movie", offset = 1))
        advanceUntilIdle()
        val s = vm.state.value
        assertThat(s.error).isNull()
        assertThat(s.detail!!.view_offset_ms).isEqualTo(5)
        assertThat(s.loadSeq).isEqualTo(1)
    }
}
