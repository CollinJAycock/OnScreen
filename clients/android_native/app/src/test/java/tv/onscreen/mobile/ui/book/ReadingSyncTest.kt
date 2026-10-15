package tv.onscreen.mobile.ui.book

import com.google.common.truth.Truth.assertThat
import io.mockk.coEvery
import io.mockk.coVerify
import io.mockk.every
import io.mockk.mockk
import io.mockk.verify
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.ExperimentalCoroutinesApi
import kotlinx.coroutines.test.StandardTestDispatcher
import kotlinx.coroutines.test.advanceTimeBy
import kotlinx.coroutines.test.advanceUntilIdle
import kotlinx.coroutines.test.resetMain
import kotlinx.coroutines.test.runTest
import kotlinx.coroutines.test.setMain
import org.junit.After
import org.junit.Before
import org.junit.Test
import tv.onscreen.mobile.data.model.ItemDetail
import tv.onscreen.mobile.data.model.ItemFile
import tv.onscreen.mobile.data.model.ReadingPosition
import tv.onscreen.mobile.data.model.ReadingProgress
import tv.onscreen.mobile.data.prefs.ServerPrefs
import tv.onscreen.mobile.data.repository.ItemRepository
import tv.onscreen.mobile.data.repository.ReadingProgressRepository

/** Reading position sync for the page reader (v2.6). */
@OptIn(ExperimentalCoroutinesApi::class)
class ReadingSyncTest {

    private val dispatcher = StandardTestDispatcher()

    @Before fun setUp() { Dispatchers.setMain(dispatcher) }
    @After fun tearDown() { Dispatchers.resetMain() }

    @Test
    fun `page locators are the 1-indexed page number, as on the web`() {
        assertThat(ReadingSync.pageLocator(40)).isEqualTo("40")
        assertThat(ReadingSync.pageFromLocator("40")).isEqualTo(40)
        // An EPUB CFI from another reader is not a page.
        assertThat(ReadingSync.pageFromLocator("epubcfi(/6/4!/4/2)")).isNull()
        assertThat(ReadingSync.pageFromLocator("0")).isNull()
        assertThat(ReadingSync.pageFromLocator("")).isNull()
    }

    @Test
    fun `progression runs to 1 on the last page`() {
        assertThat(ReadingSync.pageProgression(50, 100)).isEqualTo(0.5)
        assertThat(ReadingSync.pageProgression(100, 100)).isEqualTo(1.0)
        assertThat(ReadingSync.pageProgression(3, 0)).isEqualTo(0.0)
    }

    @Test
    fun `the newer of this device's and the server's position wins`() {
        val local = ReadingPosition("10", 0.1, updatedAtMs = 2_000)
        val server = ReadingPosition("40", 0.4, updatedAtMs = 3_000)
        assertThat(ReadingSync.newer(local, server)).isEqualTo(server)
        assertThat(ReadingSync.newer(local, server.copy(updatedAtMs = 1_000))).isEqualTo(local)
        assertThat(ReadingSync.newer(null, server)).isEqualTo(server)
        assertThat(ReadingSync.newer(local, null)).isEqualTo(local)
        assertThat(ReadingSync.newer(null, null)).isNull()
    }

    @Test
    fun `the server's time is read from RFC 3339`() {
        val p = ReadingSync.fromServer(
            ReadingProgress(locator = "12", progression = 0.2, updated_at = "2026-10-15T12:00:00.5Z"),
        )!!
        assertThat(p.updatedAtMs).isEqualTo(1_792_065_600_500L)
        assertThat(ReadingSync.formatTime(1_792_065_600_500L)).isEqualTo("2026-10-15T12:00:00.500Z")
        assertThat(ReadingSync.fromServer(null)).isNull()
    }

    @Test
    fun `the start page is the position's, clamped, else the first`() {
        assertThat(ReadingSync.startPage(ReadingPosition("40", 0.4, 0), 100)).isEqualTo(40)
        assertThat(ReadingSync.startPage(ReadingPosition("400", 1.0, 0), 100)).isEqualTo(100)
        assertThat(ReadingSync.startPage(ReadingPosition("epubcfi(/6/2)", 0.4, 0), 100)).isEqualTo(1)
        assertThat(ReadingSync.startPage(null, 100)).isEqualTo(1)
    }

    // ── The reader ────────────────────────────────────────────────────

    private fun comic(container: String = "cbz") = ItemDetail(
        id = "b1", library_id = "l", title = "Comic", type = "book", duration_ms = 100,
        files = listOf(ItemFile(id = "f1", stream_url = "/media/stream/f1", container = container)),
    )

    private fun reader(progress: ReadingProgressRepository, detail: ItemDetail = comic()): BookReaderViewModel {
        val items = mockk<ItemRepository>()
        coEvery { items.getItem("b1") } returns detail
        val prefs = mockk<ServerPrefs>(relaxed = true)
        coEvery { prefs.getServerUrl() } returns "http://srv"
        return BookReaderViewModel(mockk(relaxed = true), items, prefs, mockk(relaxed = true), progress).also {
            it.deviceName = { "Pixel" }
            it.now = { 9_000L }
        }
    }

    private fun progress(local: ReadingPosition?, server: ReadingPosition?) =
        mockk<ReadingProgressRepository>(relaxed = true).also {
            every { it.local("b1") } returns local
            coEvery { it.server("b1") } returns server
        }

    @Test
    fun `a comic opens at the newer saved position`() = runTest(dispatcher) {
        val repo = progress(
            local = ReadingPosition("10", 0.1, updatedAtMs = 1_000),
            server = ReadingPosition("40", 0.4, updatedAtMs = 2_000),
        )
        val vm = reader(repo)
        vm.load("b1")
        advanceUntilIdle()
        assertThat(vm.state.value.currentPage).isEqualTo(40)
    }

    @Test
    fun `opening is not reading, turning pages saves after the reader settles`() = runTest(dispatcher) {
        val repo = progress(local = null, server = ReadingPosition("40", 0.4, 2_000))
        val vm = reader(repo)
        vm.load("b1")
        advanceUntilIdle()

        // The pager reports the page it opened on.
        vm.setPage(40)
        advanceUntilIdle()
        verify(exactly = 0) { repo.recordLocal(any(), any()) }
        verify(exactly = 0) { repo.saveDetached(any(), any(), any()) }

        vm.setPage(41)
        vm.setPage(42)
        val expected = ReadingPosition("42", 0.42, 9_000L)
        verify { repo.recordLocal("b1", expected) }
        advanceTimeBy(ReadingSync.SAVE_DEBOUNCE_MS / 2)
        verify(exactly = 0) { repo.saveDetached(any(), any(), any()) }
        advanceUntilIdle()
        // One save for the flick through two pages: the last.
        verify(exactly = 1) { repo.saveDetached("b1", expected, "Pixel") }
    }

    @Test
    fun `an EPUB is not synced here`() = runTest(dispatcher) {
        val repo = progress(local = null, server = ReadingPosition("40", 0.4, 2_000))
        // EPUB prefetch needs a real cache dir; fail it, which is fine — the
        // point is that the reader never asked for a position.
        val vm = reader(repo, comic(container = "epub"))
        vm.load("b1")
        advanceUntilIdle()
        coVerify(exactly = 0) { repo.server(any()) }
    }
}
