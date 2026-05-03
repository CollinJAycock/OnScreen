package tv.onscreen.android.ui.browse

import com.google.common.truth.Truth.assertThat
import io.mockk.coEvery
import io.mockk.coVerify
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
import tv.onscreen.android.data.model.MediaItem
import tv.onscreen.android.data.repository.LibraryRepository

@OptIn(ExperimentalCoroutinesApi::class)
class LibraryViewModelTest {

    private val dispatcher = StandardTestDispatcher()

    @Before
    fun setUp() { Dispatchers.setMain(dispatcher) }

    @After
    fun tearDown() { Dispatchers.resetMain() }

    private fun item(id: String) = MediaItem(
        id = id, title = "t-$id", type = "movie",
        created_at = "2026-01-01T00:00:00Z", updated_at = "2026-01-01T00:00:00Z",
    )

    @Test
    fun `load fetches first page and genres`() = runTest(dispatcher) {
        val repo = mockk<LibraryRepository>()
        coEvery { repo.getItems("lib", 50, 0, "title", "asc", null) } returns
            (listOf(item("a"), item("b")) to 2)
        coEvery { repo.getGenres("lib") } returns listOf("Action", "Drama")

        val vm = LibraryViewModel(repo)
        vm.load("lib")
        advanceUntilIdle()

        assertThat(vm.items.value.map { it.id }).containsExactly("a", "b").inOrder()
        assertThat(vm.genres.value).containsExactly("Action", "Drama").inOrder()
        assertThat(vm.error.value).isNull()
    }

    @Test
    fun `load applies per-type default sort for home_video`() = runTest(dispatcher) {
        // home_video / photo / dvr libraries default to "Recently
        // added" (created_at DESC) instead of title-ASC. Locks the
        // Path-A library-screen home_video specialization.
        val repo = mockk<LibraryRepository>()
        coEvery { repo.getItems("hv", 50, 0, "created_at", "desc", null) } returns
            (listOf(item("clip-1")) to 1)
        coEvery { repo.getGenres("hv") } returns emptyList()

        val vm = LibraryViewModel(repo)
        vm.load("hv", "home_video")
        advanceUntilIdle()

        assertThat(vm.sort.value.sort).isEqualTo("created_at")
        assertThat(vm.sort.value.sortDir).isEqualTo("desc")
        assertThat(vm.items.value).hasSize(1)
    }

    @Test
    fun `load keeps title-asc default for movie libraries`() = runTest(dispatcher) {
        // Belt-and-braces — make sure the home_video branch doesn't
        // accidentally bleed into other library types.
        val repo = mockk<LibraryRepository>()
        coEvery { repo.getItems("mov", 50, 0, "title", "asc", null) } returns
            (listOf(item("a")) to 1)
        coEvery { repo.getGenres("mov") } returns emptyList()

        val vm = LibraryViewModel(repo)
        vm.load("mov", "movie")
        advanceUntilIdle()

        assertThat(vm.sort.value.sort).isEqualTo("title")
        assertThat(vm.sort.value.sortDir).isEqualTo("asc")
    }

    @Test
    fun `loadMore appends pages and stops at total`() = runTest(dispatcher) {
        val repo = mockk<LibraryRepository>()
        coEvery { repo.getItems("lib", 50, 0, "title", "asc", null) } returns
            (listOf(item("a"), item("b")) to 3)
        coEvery { repo.getItems("lib", 50, 2, "title", "asc", null) } returns
            (listOf(item("c")) to 3)
        coEvery { repo.getGenres("lib") } returns emptyList()

        val vm = LibraryViewModel(repo)
        vm.load("lib")
        advanceUntilIdle()

        vm.loadMore()
        advanceUntilIdle()

        assertThat(vm.items.value.map { it.id }).containsExactly("a", "b", "c").inOrder()

        // Further loadMore past total should not hit the repo.
        vm.loadMore()
        advanceUntilIdle()
        coVerify(exactly = 1) { repo.getItems("lib", 50, 2, "title", "asc", null) }
    }

    @Test
    fun `setSort resets list and reloads with new sort params`() = runTest(dispatcher) {
        val repo = mockk<LibraryRepository>()
        coEvery { repo.getItems("lib", 50, 0, "title", "asc", null) } returns
            (listOf(item("a"), item("b")) to 2)
        coEvery { repo.getItems("lib", 50, 0, "year", "desc", null) } returns
            (listOf(item("c")) to 1)
        coEvery { repo.getGenres("lib") } returns emptyList()

        val vm = LibraryViewModel(repo)
        vm.load("lib")
        advanceUntilIdle()

        vm.setSort("year", "desc")
        advanceUntilIdle()

        assertThat(vm.items.value.map { it.id }).containsExactly("c")
        assertThat(vm.sort.value.sort).isEqualTo("year")
        assertThat(vm.sort.value.sortDir).isEqualTo("desc")
    }

    @Test
    fun `setSort with identical params is a no-op`() = runTest(dispatcher) {
        val repo = mockk<LibraryRepository>()
        coEvery { repo.getItems("lib", 50, 0, "title", "asc", null) } returns
            (listOf(item("a")) to 1)
        coEvery { repo.getGenres("lib") } returns emptyList()

        val vm = LibraryViewModel(repo)
        vm.load("lib")
        advanceUntilIdle()

        vm.setSort("title", "asc")
        advanceUntilIdle()

        coVerify(exactly = 1) { repo.getItems("lib", 50, 0, "title", "asc", null) }
    }

    @Test
    fun `setGenre resets list and reloads with the genre filter`() = runTest(dispatcher) {
        val repo = mockk<LibraryRepository>()
        coEvery { repo.getItems("lib", 50, 0, "title", "asc", null) } returns
            (listOf(item("a"), item("b")) to 2)
        coEvery { repo.getItems("lib", 50, 0, "title", "asc", "Action") } returns
            (listOf(item("c")) to 1)
        coEvery { repo.getGenres("lib") } returns listOf("Action")

        val vm = LibraryViewModel(repo)
        vm.load("lib")
        advanceUntilIdle()

        vm.setGenre("Action")
        advanceUntilIdle()

        assertThat(vm.items.value.map { it.id }).containsExactly("c")
        assertThat(vm.genre.value).isEqualTo("Action")
    }

    @Test
    fun `error only surfaces when items list is empty`() = runTest(dispatcher) {
        val repo = mockk<LibraryRepository>()
        coEvery { repo.getItems("lib", 50, 0, "title", "asc", null) } throws RuntimeException("boom")
        coEvery { repo.getGenres("lib") } returns emptyList()

        val vm = LibraryViewModel(repo)
        vm.load("lib")
        advanceUntilIdle()

        assertThat(vm.error.value).isEqualTo("boom")
        assertThat(vm.items.value).isEmpty()
    }

    @Test
    fun `loadMore failure after success keeps existing items and clears error`() = runTest(dispatcher) {
        val repo = mockk<LibraryRepository>()
        coEvery { repo.getItems("lib", 50, 0, "title", "asc", null) } returns
            (listOf(item("a")) to 5)
        coEvery { repo.getItems("lib", 50, 1, "title", "asc", null) } throws RuntimeException("net")
        coEvery { repo.getGenres("lib") } returns emptyList()

        val vm = LibraryViewModel(repo)
        vm.load("lib")
        advanceUntilIdle()

        vm.loadMore()
        advanceUntilIdle()

        assertThat(vm.items.value.map { it.id }).containsExactly("a")
        assertThat(vm.error.value).isNull()
    }
}
