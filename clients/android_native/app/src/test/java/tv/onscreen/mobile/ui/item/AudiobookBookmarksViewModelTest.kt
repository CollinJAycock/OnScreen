package tv.onscreen.mobile.ui.item

import com.google.common.truth.Truth.assertThat
import io.mockk.coEvery
import io.mockk.coVerify
import io.mockk.mockk
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.ExperimentalCoroutinesApi
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.test.StandardTestDispatcher
import kotlinx.coroutines.test.advanceUntilIdle
import kotlinx.coroutines.test.resetMain
import kotlinx.coroutines.test.runTest
import kotlinx.coroutines.test.setMain
import okhttp3.ResponseBody.Companion.toResponseBody
import org.junit.After
import org.junit.Before
import org.junit.Test
import retrofit2.HttpException
import retrofit2.Response
import tv.onscreen.mobile.R
import tv.onscreen.mobile.data.model.Bookmark
import tv.onscreen.mobile.data.model.Chapter
import tv.onscreen.mobile.data.model.ItemDetail
import tv.onscreen.mobile.data.model.ItemFile
import tv.onscreen.mobile.data.repository.AudiobookRepository

@OptIn(ExperimentalCoroutinesApi::class)
class AudiobookBookmarksViewModelTest {

    private val dispatcher = StandardTestDispatcher()

    @Before fun setUp() { Dispatchers.setMain(dispatcher) }
    @After fun tearDown() { Dispatchers.resetMain() }

    private val marks = listOf(Chapter("One", 0, 60_000))

    private val book = ItemDetail(
        id = "book-1", library_id = "lib", title = "Book", type = "audiobook",
        files = listOf(ItemFile(id = "f", stream_url = "/s", chapters = marks)),
    )

    private val saved = listOf(
        Bookmark(id = "a", item_id = "book-1", position_ms = 5_000, note = "start"),
        Bookmark(id = "b", item_id = "book-1", position_ms = 70_000),
    )

    private fun http(code: Int) = HttpException(Response.error<Any>(code, "".toResponseBody(null)))

    private fun repo(list: List<Bookmark>? = saved): AudiobookRepository = mockk<AudiobookRepository>().also {
        coEvery { it.bookmarks("book-1") } returns list
    }

    @Test
    fun `an audiobook loads its bookmarks and chapter marks`() = runTest(dispatcher) {
        val vm = AudiobookBookmarksViewModel(repo())
        vm.bind(book)
        advanceUntilIdle()

        val ui = vm.state.value
        assertThat(ui.available).isTrue()
        assertThat(ui.bookmarks.map { it.id }).containsExactly("a", "b").inOrder()
        assertThat(ui.chapters).isEqualTo(marks)
    }

    @Test
    fun `an older server shows no section`() = runTest(dispatcher) {
        val vm = AudiobookBookmarksViewModel(repo(list = null))
        vm.bind(book)
        advanceUntilIdle()

        assertThat(vm.state.value.available).isFalse()
    }

    @Test
    fun `anything but an audiobook shows no section and asks nothing`() = runTest(dispatcher) {
        val r = repo()
        val vm = AudiobookBookmarksViewModel(r)
        vm.bind(book.copy(id = "m", type = "movie"))
        advanceUntilIdle()

        assertThat(vm.state.value.available).isFalse()
        coVerify(exactly = 0) { r.bookmarks(any()) }
    }

    @Test
    fun `a failed load offers a retry`() = runTest(dispatcher) {
        val r = mockk<AudiobookRepository>()
        coEvery { r.bookmarks("book-1") } throws http(500) andThen saved
        val vm = AudiobookBookmarksViewModel(r)
        vm.bind(book)
        advanceUntilIdle()
        assertThat(vm.state.value.error).isTrue()
        assertThat(vm.state.value.available).isTrue()

        vm.retry()
        advanceUntilIdle()
        assertThat(vm.state.value.error).isFalse()
        assertThat(vm.state.value.bookmarks).hasSize(2)
    }

    @Test
    fun `rebinding the same book refreshes in place`() = runTest(dispatcher) {
        // Back from the player with a new bookmark.
        val r = mockk<AudiobookRepository>()
        coEvery { r.bookmarks("book-1") } returns saved andThen saved + Bookmark(id = "c", item_id = "book-1", position_ms = 9_000)
        val vm = AudiobookBookmarksViewModel(r)
        vm.bind(book)
        advanceUntilIdle()

        vm.bind(book)
        // Not blanked while the refresh is in flight.
        assertThat(vm.state.value.bookmarks).hasSize(2)
        advanceUntilIdle()
        assertThat(vm.state.value.bookmarks.map { it.id }).containsExactly("a", "b", "c")
    }

    @Test
    fun `editing a note saves it trimmed`() = runTest(dispatcher) {
        val r = repo()
        coEvery { r.updateNote("b", "the storm") } returns Unit
        val vm = AudiobookBookmarksViewModel(r)
        vm.bind(book)
        advanceUntilIdle()

        vm.updateNote("b", "  the storm ")
        assertThat(vm.state.value.bookmarks.first { it.id == "b" }.note).isEqualTo("the storm")
        advanceUntilIdle()
        coVerify { r.updateNote("b", "the storm") }
    }

    @Test
    fun `a failed edit rolls back and says so`() = runTest(dispatcher) {
        val r = repo()
        coEvery { r.updateNote(any(), any()) } throws http(500)
        val vm = AudiobookBookmarksViewModel(r)
        vm.bind(book)
        advanceUntilIdle()

        vm.updateNote("a", "changed")
        advanceUntilIdle()

        assertThat(vm.state.value.bookmarks.first { it.id == "a" }.note).isEqualTo("start")
        assertThat(vm.messages.first()).isEqualTo(R.string.bookmark_update_failed)
    }

    @Test
    fun `delete removes it at once`() = runTest(dispatcher) {
        val r = repo()
        coEvery { r.deleteBookmark("a") } returns Unit
        val vm = AudiobookBookmarksViewModel(r)
        vm.bind(book)
        advanceUntilIdle()

        vm.delete("a")
        assertThat(vm.state.value.bookmarks.map { it.id }).containsExactly("b")
        advanceUntilIdle()
        coVerify { r.deleteBookmark("a") }
        assertThat(vm.state.value.bookmarks.map { it.id }).containsExactly("b")
    }

    @Test
    fun `a failed delete puts it back where it was`() = runTest(dispatcher) {
        val r = repo()
        coEvery { r.deleteBookmark(any()) } throws http(503)
        val vm = AudiobookBookmarksViewModel(r)
        vm.bind(book)
        advanceUntilIdle()

        vm.delete("a")
        advanceUntilIdle()

        assertThat(vm.state.value.bookmarks.map { it.id }).containsExactly("a", "b").inOrder()
        assertThat(vm.messages.first()).isEqualTo(R.string.bookmark_delete_failed)
    }

    @Test
    fun `deleting one already gone elsewhere is fine`() = runTest(dispatcher) {
        val r = repo()
        coEvery { r.deleteBookmark(any()) } throws http(404)
        val vm = AudiobookBookmarksViewModel(r)
        vm.bind(book)
        advanceUntilIdle()

        vm.delete("a")
        advanceUntilIdle()

        assertThat(vm.state.value.bookmarks.map { it.id }).containsExactly("b")
    }
}
