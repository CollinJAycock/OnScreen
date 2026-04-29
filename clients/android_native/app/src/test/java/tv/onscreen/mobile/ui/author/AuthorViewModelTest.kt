package tv.onscreen.mobile.ui.author

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
import tv.onscreen.mobile.data.model.ChildItem
import tv.onscreen.mobile.data.model.ItemDetail
import tv.onscreen.mobile.data.prefs.ServerPrefs
import tv.onscreen.mobile.data.repository.ItemRepository

@OptIn(ExperimentalCoroutinesApi::class)
class AuthorViewModelTest {

    private val dispatcher = StandardTestDispatcher()

    @Before fun setUp() { Dispatchers.setMain(dispatcher) }
    @After  fun tearDown() { Dispatchers.resetMain() }

    private fun child(id: String, type: String, year: Int? = null) =
        ChildItem(id = id, title = "t-$id", type = type, year = year)

    private fun author(id: String) = ItemDetail(
        id = id, library_id = "lib-1", title = "Brandon Sanderson",
        type = "book_author",
    )

    private fun prefs(): ServerPrefs {
        val p = mockk<ServerPrefs>(relaxed = true)
        coEvery { p.getServerUrl() } returns "http://srv"
        return p
    }

    @Test
    fun `load splits children into series and standalone books`() = runTest(dispatcher) {
        val repo = mockk<ItemRepository>()
        coEvery { repo.getItem("a1") } returns author("a1")
        coEvery { repo.getChildren("a1") } returns listOf(
            child("s1", "book_series"),
            child("b-old", "audiobook", year = 2010),
            child("b-new", "audiobook", year = 2024),
            child("s2", "book_series"),
            // Foreign types should not appear in either bucket — the
            // server may add new child types in the future and we
            // shouldn't accidentally render them as books.
            child("noise", "audiobook_chapter"),
        )

        val vm = AuthorViewModel(repo, prefs())
        vm.load("a1")
        advanceUntilIdle()

        val s = vm.state.value
        assertThat(s.loading).isFalse()
        assertThat(s.error).isNull()
        // Series sorted alphabetically by title.
        assertThat(s.series.map { it.id }).containsExactly("s1", "s2").inOrder()
        // Books sorted year-desc — newest first matches the web/TV
        // client ordering and surfaces the most relevant entry to
        // the listener at the top.
        assertThat(s.books.map { it.id }).containsExactly("b-new", "b-old").inOrder()
        assertThat(s.serverUrl).isEqualTo("http://srv")
    }

    @Test
    fun `load surfaces repo error and clears loading`() = runTest(dispatcher) {
        val repo = mockk<ItemRepository>()
        coEvery { repo.getItem("a1") } throws RuntimeException("offline")

        val vm = AuthorViewModel(repo, prefs())
        vm.load("a1")
        advanceUntilIdle()

        val s = vm.state.value
        assertThat(s.loading).isFalse()
        assertThat(s.error).isEqualTo("offline")
        assertThat(s.detail).isNull()
    }

    @Test
    fun `author with no children renders cleanly`() = runTest(dispatcher) {
        val repo = mockk<ItemRepository>()
        coEvery { repo.getItem("a1") } returns author("a1")
        coEvery { repo.getChildren("a1") } returns emptyList()

        val vm = AuthorViewModel(repo, prefs())
        vm.load("a1")
        advanceUntilIdle()

        val s = vm.state.value
        assertThat(s.detail?.id).isEqualTo("a1")
        assertThat(s.series).isEmpty()
        assertThat(s.books).isEmpty()
    }
}
