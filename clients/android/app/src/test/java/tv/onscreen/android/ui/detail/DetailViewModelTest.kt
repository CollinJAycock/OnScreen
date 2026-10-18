package tv.onscreen.android.ui.detail

import com.google.common.truth.Truth.assertThat
import io.mockk.Runs
import io.mockk.coEvery
import io.mockk.coVerify
import io.mockk.just
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
import tv.onscreen.android.data.model.ChildItem
import tv.onscreen.android.data.model.ItemDetail
import tv.onscreen.android.data.repository.FavoritesRepository
import tv.onscreen.android.data.repository.ItemRepository
import tv.onscreen.android.data.repository.MusicRepository
import tv.onscreen.android.playback.PlayQueue

@OptIn(ExperimentalCoroutinesApi::class)
class DetailViewModelTest {

    private val dispatcher = StandardTestDispatcher()

    @Before
    fun setUp() {
        Dispatchers.setMain(dispatcher)
    }

    @After
    fun tearDown() {
        Dispatchers.resetMain()
    }

    private fun movie(isFavorite: Boolean) = ItemDetail(
        id = "m1",
        library_id = "lib-1",
        title = "Movie",
        type = "movie",
        is_favorite = isFavorite,
    )

    @Test
    fun `load copies is_favorite from item detail`() = runTest(dispatcher) {
        val itemRepo = mockk<ItemRepository>()
        val favRepo = mockk<FavoritesRepository>()
        coEvery { itemRepo.getItem("m1") } returns movie(isFavorite = true)

        val vm = DetailViewModel(itemRepo, favRepo, mockk(relaxed = true), mockk(relaxed = true))
        vm.load("m1")
        advanceUntilIdle()

        assertThat(vm.uiState.value.isFavorite).isTrue()
    }

    @Test
    fun `toggleFavorite flips state and calls add when not already favorited`() = runTest(dispatcher) {
        val itemRepo = mockk<ItemRepository>()
        val favRepo = mockk<FavoritesRepository>()
        coEvery { itemRepo.getItem("m1") } returns movie(isFavorite = false)
        coEvery { favRepo.add("m1") } just Runs

        val vm = DetailViewModel(itemRepo, favRepo, mockk(relaxed = true), mockk(relaxed = true))
        vm.load("m1")
        advanceUntilIdle()

        vm.toggleFavorite()
        advanceUntilIdle()

        assertThat(vm.uiState.value.isFavorite).isTrue()
        coVerify(exactly = 1) { favRepo.add("m1") }
    }

    @Test
    fun `toggleFavorite calls remove when already favorited`() = runTest(dispatcher) {
        val itemRepo = mockk<ItemRepository>()
        val favRepo = mockk<FavoritesRepository>()
        coEvery { itemRepo.getItem("m1") } returns movie(isFavorite = true)
        coEvery { favRepo.remove("m1") } just Runs

        val vm = DetailViewModel(itemRepo, favRepo, mockk(relaxed = true), mockk(relaxed = true))
        vm.load("m1")
        advanceUntilIdle()

        vm.toggleFavorite()
        advanceUntilIdle()

        assertThat(vm.uiState.value.isFavorite).isFalse()
        coVerify(exactly = 1) { favRepo.remove("m1") }
    }

    @Test
    fun `toggleFavorite reverts state when network call fails`() = runTest(dispatcher) {
        val itemRepo = mockk<ItemRepository>()
        val favRepo = mockk<FavoritesRepository>()
        coEvery { itemRepo.getItem("m1") } returns movie(isFavorite = false)
        coEvery { favRepo.add("m1") } throws RuntimeException("offline")

        val vm = DetailViewModel(itemRepo, favRepo, mockk(relaxed = true), mockk(relaxed = true))
        vm.load("m1")
        advanceUntilIdle()

        vm.toggleFavorite()
        advanceUntilIdle()

        assertThat(vm.uiState.value.isFavorite).isFalse()
    }

    @Test
    fun `toggleFavorite before load is a no-op`() = runTest(dispatcher) {
        val itemRepo = mockk<ItemRepository>()
        val favRepo = mockk<FavoritesRepository>()

        val vm = DetailViewModel(itemRepo, favRepo, mockk(relaxed = true), mockk(relaxed = true))
        vm.toggleFavorite()
        advanceUntilIdle()

        coVerify(exactly = 0) { favRepo.add(any()) }
        coVerify(exactly = 0) { favRepo.remove(any()) }
    }

    @Test
    fun `show with seasons loads episodes in the season map`() = runTest(dispatcher) {
        val itemRepo = mockk<ItemRepository>()
        val favRepo = mockk<FavoritesRepository>()
        val show = ItemDetail(
            id = "s1", library_id = "lib-1", title = "Show", type = "show",
        )
        coEvery { itemRepo.getItem("s1") } returns show
        coEvery { itemRepo.getChildren("s1") } returns listOf(
            ChildItem(id = "se1", title = "S1", type = "season", index = 1),
        )
        coEvery { itemRepo.getChildren("se1") } returns listOf(
            ChildItem(id = "ep1", title = "E1", type = "episode", index = 1),
            ChildItem(id = "ep2", title = "E2", type = "episode", index = 2),
        )

        val vm = DetailViewModel(itemRepo, favRepo, mockk(relaxed = true), mockk(relaxed = true))
        vm.load("s1")
        advanceUntilIdle()

        val seasons = vm.uiState.value.seasons
        assertThat(seasons).hasSize(1)
        val episodes = seasons.values.first()
        assertThat(episodes.map { it.id }).containsExactly("ep1", "ep2").inOrder()
    }

    @Test
    fun `book_author renders series before standalone books`() = runTest(dispatcher) {
        // Series alphabetical first, books year-desc after. The single-
        // bucket render leans on this ordering — without it the user
        // would see series and books shuffled together randomly.
        val itemRepo = mockk<ItemRepository>()
        val favRepo = mockk<FavoritesRepository>()
        val author = ItemDetail(
            id = "auth-1", library_id = "lib", title = "Brandon Sanderson",
            type = "book_author",
        )
        coEvery { itemRepo.getItem("auth-1") } returns author
        coEvery { itemRepo.getChildren("auth-1") } returns listOf(
            ChildItem(id = "b-old", title = "Elantris", type = "audiobook", year = 2005),
            ChildItem(id = "s-mistborn", title = "Mistborn", type = "book_series"),
            ChildItem(id = "b-new", title = "Tress", type = "audiobook", year = 2023),
            ChildItem(id = "s-stormlight", title = "Stormlight Archive", type = "book_series"),
            // Foreign type — chapter rows belong under their book
            // parent, never on the author detail.
            ChildItem(id = "noise", title = "Ch1", type = "audiobook_chapter"),
        )

        val vm = DetailViewModel(itemRepo, favRepo, mockk(relaxed = true), mockk(relaxed = true))
        vm.load("auth-1")
        advanceUntilIdle()

        val seasons = vm.uiState.value.seasons
        assertThat(seasons).hasSize(1)
        val children = seasons.values.first()
        assertThat(children.map { it.id }).containsExactly(
            "s-mistborn", "s-stormlight", "b-new", "b-old",
        ).inOrder()
    }

    @Test
    fun `book_series surfaces children for the books list`() = runTest(dispatcher) {
        // Same shape as season / album — VM hands children through;
        // the rendering layer sorts. Locks the route through the
        // multi-type case so a refactor that changes the type-switch
        // can't drop book_series into the default empty branch.
        val itemRepo = mockk<ItemRepository>()
        val favRepo = mockk<FavoritesRepository>()
        val series = ItemDetail(
            id = "ser-1", library_id = "lib", title = "Mistborn",
            type = "book_series",
        )
        coEvery { itemRepo.getItem("ser-1") } returns series
        coEvery { itemRepo.getChildren("ser-1") } returns listOf(
            ChildItem(id = "b1", title = "The Final Empire", type = "audiobook", year = 2006),
            ChildItem(id = "b2", title = "The Well of Ascension", type = "audiobook", year = 2007),
        )

        val vm = DetailViewModel(itemRepo, favRepo, mockk(relaxed = true), mockk(relaxed = true))
        vm.load("ser-1")
        advanceUntilIdle()

        val seasons = vm.uiState.value.seasons
        assertThat(seasons).hasSize(1)
        val children = seasons.values.first()
        assertThat(children.map { it.id }).containsExactly("b1", "b2").inOrder()
    }

    @Test
    fun `artist Play All starts on disc 1 track 1 of the first album`() = runTest(dispatcher) {
        // Every disc has a track 1, so the lowest index alone can't tell
        // disc 1's opener from disc 2's.
        val itemRepo = mockk<ItemRepository>()
        coEvery { itemRepo.getChildren("artist-1") } returns listOf(
            ChildItem(id = "alb-late", title = "Late", type = "album", year = 2010),
            ChildItem(id = "alb-early", title = "Early", type = "album", year = 1999),
        )
        coEvery { itemRepo.getChildren("alb-early") } returns listOf(
            ChildItem(id = "d2t1", title = "D2T1", type = "track", index = 1, disc_number = 2),
            ChildItem(id = "d1t2", title = "D1T2", type = "track", index = 2, disc_number = 1),
            ChildItem(id = "d1t1", title = "D1T1", type = "track", index = 1, disc_number = 1),
        )

        var started: String? = null
        DetailViewModel(itemRepo, mockk(), mockk(relaxed = true), mockk(relaxed = true)).resolvePlayAllStart("artist-1") { started = it }
        advanceUntilIdle()

        assertThat(started).isEqualTo("d1t1")
    }

    // ── Music discovery (v2.6) ──────────────────────────────────────────────

    private fun artist() = ItemDetail(id = "artist-1", library_id = "lib-1", title = "Artist", type = "artist")

    @Test
    fun `an artist page adds Appears On and Similar Artists tabs after its own`() = runTest(dispatcher) {
        val itemRepo = mockk<ItemRepository>()
        coEvery { itemRepo.getItem("artist-1") } returns artist()
        coEvery { itemRepo.getChildren("artist-1") } returns
            listOf(ChildItem(id = "alb", title = "Own Album", type = "album"))
        val music = mockk<MusicRepository>()
        coEvery { music.appearsOn("artist-1") } returns
            listOf(ChildItem(id = "other-alb", title = "Their Album · Them", type = "album"))
        coEvery { music.similarArtists("artist-1") } returns
            listOf(ChildItem(id = "artist-2", title = "Like Artist", type = "artist"))

        val vm = DetailViewModel(itemRepo, mockk(), music, mockk(relaxed = true))
        vm.load("artist-1")
        advanceUntilIdle()

        val tabs = vm.uiState.value.seasons.entries.toList()
        assertThat(tabs.map { it.key.title }).containsExactly("Albums", "Appears On", "Similar Artists").inOrder()
        assertThat(tabs[1].key.id).isEqualTo("artist-1" + DetailViewModel.APPEARS_ON_SUFFIX)
        assertThat(tabs[1].value.map { it.id }).containsExactly("other-alb")
        assertThat(tabs[2].value.map { it.type }).containsExactly("artist")
    }

    @Test
    fun `an artist page on an older server keeps just its own tabs`() = runTest(dispatcher) {
        val itemRepo = mockk<ItemRepository>()
        coEvery { itemRepo.getItem("artist-1") } returns artist()
        coEvery { itemRepo.getChildren("artist-1") } returns
            listOf(ChildItem(id = "alb", title = "Own Album", type = "album"))
        val music = mockk<MusicRepository>()
        coEvery { music.appearsOn(any()) } throws RuntimeException("HTTP 404")
        coEvery { music.similarArtists(any()) } throws RuntimeException("HTTP 404")

        val vm = DetailViewModel(itemRepo, mockk(), music, mockk(relaxed = true))
        vm.load("artist-1")
        advanceUntilIdle()

        assertThat(vm.uiState.value.error).isNull()
        assertThat(vm.uiState.value.seasons.keys.map { it.title }).containsExactly("Albums")
    }

    @Test
    fun `an instant mix becomes the play queue and starts on its first track`() = runTest(dispatcher) {
        val music = mockk<MusicRepository>()
        coEvery { music.instantMix("alb-1") } returns listOf(
            ChildItem(id = "t1", title = "One", type = "track"),
            ChildItem(id = "t9", title = "Nine", type = "track"),
        )
        try {
            var started: String? = "unset"
            DetailViewModel(mockk(), mockk(), music, mockk(relaxed = true)).instantMix("alb-1") { started = it }
            advanceUntilIdle()

            assertThat(started).isEqualTo("t1")
            assertThat(PlayQueue.nextAfter("t1")?.id).isEqualTo("t9")
        } finally {
            PlayQueue.clear()
        }
    }

    @Test
    fun `a failed instant mix starts nothing and leaves the queue alone`() = runTest(dispatcher) {
        val music = mockk<MusicRepository>()
        coEvery { music.instantMix(any()) } throws RuntimeException("HTTP 404")
        PlayQueue.set(listOf(ChildItem(id = "q1", title = "Q", type = "track"), ChildItem(id = "q2", title = "Q2", type = "track")))
        try {
            var started: String? = "unset"
            DetailViewModel(mockk(), mockk(), music, mockk(relaxed = true)).instantMix("artist-1") { started = it }
            advanceUntilIdle()

            assertThat(started).isNull()
            assertThat(PlayQueue.nextAfter("q1")?.id).isEqualTo("q2")
        } finally {
            PlayQueue.clear()
        }
    }
}
