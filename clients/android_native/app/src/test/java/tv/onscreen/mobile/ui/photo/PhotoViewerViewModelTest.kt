package tv.onscreen.mobile.ui.photo

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
import org.junit.After
import org.junit.Before
import org.junit.Test
import tv.onscreen.mobile.data.api.ApiResponse
import tv.onscreen.mobile.data.model.ChildItem
import tv.onscreen.mobile.data.model.ItemDetail
import tv.onscreen.mobile.data.model.MediaItem
import tv.onscreen.mobile.data.prefs.ServerPrefs
import tv.onscreen.mobile.data.repository.ItemRepository
import tv.onscreen.mobile.data.repository.LibraryRepository
import java.io.IOException

/**
 * The photo viewer showed nothing until it had paged the whole library (all
 * pages at once), and one failed page failed it. Now the photo shows at once
 * and its neighbours are a window of the listing, extended as the user pages.
 */
@OptIn(ExperimentalCoroutinesApi::class)
class PhotoViewerViewModelTest {

    private val dispatcher = StandardTestDispatcher()

    @Before fun setUp() { Dispatchers.setMain(dispatcher) }
    @After fun tearDown() { Dispatchers.resetMain() }

    private val total = 1_000
    private fun id(row: Int) = "p$row"

    private val prefs = mockk<ServerPrefs>().also { coEvery { it.getServerUrl() } returns "https://srv/" }
    private val items = mockk<ItemRepository>()
    private val library = mockk<LibraryRepository>()

    private fun detail(id: String, parent: String? = null) =
        ItemDetail(id = id, library_id = "L", title = id, type = "photo", parent_id = parent)

    /** A photo library of [total] rows; rows in [failing] pages throw. */
    private fun listing(failingOffsets: Set<Int> = emptySet()) {
        coEvery { library.getItems("L", any(), any(), any(), any(), any(), any(), any()) } answers {
            val limit = secondArg<Int>()
            val offset = thirdArg<Int>()
            if (offset in failingOffsets) throw IOException("offline")
            val rows = (offset until minOf(offset + limit, total)).map {
                MediaItem(id = id(it), title = id(it), type = "photo", created_at = "", updated_at = "")
            }
            rows to total
        }
    }

    private fun vm() = PhotoViewerViewModel(items, library, prefs)

    @Test
    fun `the tapped photo shows before anything is fetched`() = runTest(dispatcher) {
        val gate = CompletableDeferred<ApiResponse<ItemDetail>>()
        coEvery { items.getItem(id(500)) } coAnswers { gate.await().data }
        listing()

        val vm = vm()
        vm.load(id(500), position = 500)
        dispatcher.scheduler.runCurrent()

        val ui = vm.state.value
        assertThat(ui.loading).isFalse()
        assertThat(ui.photos.map { it.id }).containsExactly(id(500))
        assertThat(ui.serverUrl).isEqualTo("https://srv")
        gate.complete(ApiResponse(detail(id(500))))
    }

    @Test
    fun `neighbours are one page around the known position, not the library`() = runTest(dispatcher) {
        coEvery { items.getItem(id(500)) } returns detail(id(500))
        listing()

        val vm = vm()
        vm.load(id(500), position = 500, watch = "unwatched")
        advanceUntilIdle()

        val ui = vm.state.value
        assertThat(ui.photos.first().id).isEqualTo(id(400))
        assertThat(ui.photos.size).isEqualTo(PhotoViewerViewModel.PAGE_SIZE)
        assertThat(ui.photos[ui.startIndex].id).isEqualTo(id(500))
        assertThat(ui.total).isEqualTo(total)
        coVerify(exactly = 1) { library.getItems(any(), any(), any(), any(), any(), any(), any(), any()) }
        coVerify { library.getItems("L", 200, 400, null, null, null, "unwatched", null) }
    }

    @Test
    fun `the window grows a page at a time near either end, keeping the photo on screen`() = runTest(dispatcher) {
        coEvery { items.getItem(id(500)) } returns detail(id(500))
        listing()
        val vm = vm()
        vm.load(id(500), position = 500)
        advanceUntilIdle()

        vm.onPhotoShown(id(595))
        advanceUntilIdle()
        assertThat(vm.state.value.photos.last().id).isEqualTo(id(799))

        vm.onPhotoShown(id(402))
        advanceUntilIdle()
        val ui = vm.state.value
        assertThat(ui.photos.first().id).isEqualTo(id(200))
        assertThat(ui.photos[ui.startIndex].id).isEqualTo(id(402))
        assertThat(ui.photos.map { it.id }).containsNoDuplicates()

        // In the middle: nothing more is fetched.
        vm.onPhotoShown(id(500))
        advanceUntilIdle()
        coVerify(exactly = 3) { library.getItems(any(), any(), any(), any(), any(), any(), any(), any()) }
    }

    @Test
    fun `a failed page leaves the viewer as it is and is tried again later`() = runTest(dispatcher) {
        coEvery { items.getItem(id(500)) } returns detail(id(500))
        listing(failingOffsets = setOf(600))
        val vm = vm()
        vm.load(id(500), position = 500)
        advanceUntilIdle()

        vm.onPhotoShown(id(595))
        advanceUntilIdle()
        assertThat(vm.state.value.photos.size).isEqualTo(PhotoViewerViewModel.PAGE_SIZE)
        assertThat(vm.state.value.photos.any { it.id == id(500) }).isTrue()

        listing()
        vm.onPhotoShown(id(596))
        advanceUntilIdle()
        assertThat(vm.state.value.photos.last().id).isEqualTo(id(799))
    }

    @Test
    fun `without a position only the first few pages are searched`() = runTest(dispatcher) {
        coEvery { items.getItem(id(900)) } returns detail(id(900))
        listing()
        val vm = vm()
        vm.load(id(900))
        advanceUntilIdle()

        assertThat(vm.state.value.photos.map { it.id }).containsExactly(id(900))
        coVerify(exactly = PhotoViewerViewModel.MAX_SEARCH_PAGES) {
            library.getItems(any(), any(), any(), any(), any(), any(), any(), any())
        }
    }

    @Test
    fun `without a position a photo in the first pages gets its neighbours`() = runTest(dispatcher) {
        coEvery { items.getItem(id(250)) } returns detail(id(250))
        listing()
        val vm = vm()
        vm.load(id(250))
        advanceUntilIdle()

        val ui = vm.state.value
        assertThat(ui.photos.first().id).isEqualTo(id(200))
        assertThat(ui.photos[ui.startIndex].id).isEqualTo(id(250))
    }

    @Test
    fun `an album's photos come first`() = runTest(dispatcher) {
        coEvery { items.getItem("b") } returns detail("b", parent = "album")
        coEvery { items.getChildren("album") } returns listOf(
            ChildItem(id = "a", title = "a", type = "photo"),
            ChildItem(id = "v", title = "v", type = "video"),
            ChildItem(id = "b", title = "b", type = "photo"),
            ChildItem(id = "c", title = "c", type = "photo"),
        )
        val vm = vm()
        vm.load("b", position = 7)
        advanceUntilIdle()

        val ui = vm.state.value
        assertThat(ui.photos.map { it.id }).containsExactly("a", "b", "c").inOrder()
        assertThat(ui.startIndex).isEqualTo(1)
        assertThat(ui.total).isEqualTo(3)
        coVerify(exactly = 0) { library.getItems(any(), any(), any(), any(), any(), any(), any(), any()) }
    }

    @Test
    fun `a failed item fetch still shows the photo`() = runTest(dispatcher) {
        coEvery { items.getItem(any()) } throws IOException("offline")
        val vm = vm()
        vm.load(id(3), position = 3)
        advanceUntilIdle()

        assertThat(vm.state.value.photos.map { it.id }).containsExactly(id(3))
    }
}
