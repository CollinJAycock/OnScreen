package tv.onscreen.mobile.ui.player

import com.google.common.truth.Truth.assertThat
import io.mockk.coEvery
import io.mockk.coVerify
import io.mockk.every
import io.mockk.mockk
import io.mockk.verify
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.ExperimentalCoroutinesApi
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.emptyFlow
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.flow.flowOf
import kotlinx.coroutines.test.StandardTestDispatcher
import kotlinx.coroutines.test.TestScope
import kotlinx.coroutines.test.advanceTimeBy
import kotlinx.coroutines.test.advanceUntilIdle
import kotlinx.coroutines.test.resetMain
import kotlinx.coroutines.test.runCurrent
import kotlinx.coroutines.test.runTest
import kotlinx.coroutines.test.setMain
import okhttp3.ResponseBody.Companion.toResponseBody
import org.junit.After
import org.junit.Before
import org.junit.Test
import retrofit2.HttpException
import retrofit2.Response
import tv.onscreen.mobile.data.downloads.DownloadManifest
import tv.onscreen.mobile.data.downloads.DownloadStore
import tv.onscreen.mobile.data.downloads.OnScreenDownloadManager
import tv.onscreen.mobile.data.model.Bookmark
import tv.onscreen.mobile.data.model.Chapter
import tv.onscreen.mobile.data.model.ItemDetail
import tv.onscreen.mobile.data.model.ItemFile
import tv.onscreen.mobile.data.model.UserPreferences
import tv.onscreen.mobile.data.model.WatchLimitData
import tv.onscreen.mobile.data.prefs.ServerPrefs
import tv.onscreen.mobile.data.repository.AudiobookRepository
import tv.onscreen.mobile.data.repository.ItemRepository
import tv.onscreen.mobile.data.repository.ListeningSpeed
import tv.onscreen.mobile.data.repository.NotificationsRepository
import tv.onscreen.mobile.data.repository.PreferencesRepository
import tv.onscreen.mobile.data.repository.TranscodeRepository
import tv.onscreen.mobile.data.repository.WatchLimitRepository
import tv.onscreen.mobile.playback.StopAfterItem

/**
 * Audiobooks in the now-playing ViewModel: the book's listening speed,
 * "Add bookmark", and the sleep timer's end-of-chapter mode (plus the timer
 * surviving the screen following the queue to the next chapter).
 */
@OptIn(ExperimentalCoroutinesApi::class)
class PlayerViewModelAudiobookTest {

    private val dispatcher = StandardTestDispatcher()

    @Before fun setUp() { Dispatchers.setMain(dispatcher) }

    @After fun tearDown() {
        Dispatchers.resetMain()
        listOf("book-1", "ch-2", "ch-3").forEach(StopAfterItem::disarm)
    }

    private val marks = listOf(
        Chapter("One", 0, 60_000),
        Chapter("Two", 60_000, 150_000),
        Chapter("Three", 150_000, 200_000),
    )

    private fun audioFile(chapters: List<Chapter> = emptyList()) = ItemFile(
        id = "f-1",
        stream_url = "/media/files/f-1.m4b",
        container = "m4b",
        audio_codec = "aac",
        chapters = chapters,
    )

    private val book = ItemDetail(
        id = "book-1", library_id = "lib", title = "Book", type = "audiobook",
        parent_id = "author-1", view_offset_ms = 30_000, files = listOf(audioFile(marks)),
    )

    private fun chapter(id: String) = ItemDetail(
        id = id, library_id = "lib", title = "Chapter $id", type = "audiobook_chapter",
        parent_id = "book-1", view_offset_ms = 12_000, files = listOf(audioFile()),
    )

    private val track = ItemDetail(
        id = "track-1", library_id = "lib", title = "Song", type = "track",
        parent_id = "album-1", index = 1, files = listOf(audioFile()),
    )

    private fun items(vararg details: ItemDetail): ItemRepository = mockk<ItemRepository>().also { repo ->
        coEvery { repo.getMarkers(any()) } returns emptyList()
        coEvery { repo.getChildren(any()) } returns emptyList()
        details.forEach { d -> coEvery { repo.getItem(d.id) } returns d }
    }

    private fun audiobooks(speed: ListeningSpeed = ListeningSpeed(1.5f, true)): AudiobookRepository =
        mockk<AudiobookRepository>(relaxed = true).also {
            coEvery { it.listeningSpeed(any(), any()) } returns speed
        }

    private fun newVm(items: ItemRepository, books: AudiobookRepository): PlayerViewModel {
        val transcode = mockk<TranscodeRepository>(relaxed = true).also {
            coEvery { it.decide(any(), any()) } returns "directPlay"
        }
        val prefs = mockk<PreferencesRepository>().also { coEvery { it.get() } returns UserPreferences() }
        val server = mockk<ServerPrefs>(relaxed = true).also {
            coEvery { it.getServerUrl() } returns "http://srv"
        }
        val subPrefs = mockk<tv.onscreen.mobile.data.prefs.SubtitlePrefs>(relaxed = true).also {
            every { it.style } returns flowOf(tv.onscreen.mobile.data.prefs.SubtitleStyle.DEFAULT)
        }
        val store = mockk<DownloadStore>(relaxed = true).also {
            coEvery { it.load() } returns Unit
            coEvery { it.get(any()) } returns null
            every { it.state } returns MutableStateFlow(DownloadManifest(entries = emptyList()))
        }
        val downloads = mockk<OnScreenDownloadManager>().also { every { it.store } returns store }
        val trickplay = mockk<tv.onscreen.mobile.data.repository.TrickplayRepository>(relaxed = true).also {
            coEvery { it.status(any()) } returns tv.onscreen.mobile.data.model.TrickplayStatus(status = "not_started")
        }
        val watchLimit = mockk<WatchLimitRepository>().also {
            coEvery { it.get() } returns WatchLimitData(
                daily_limit_minutes = null,
                allowed_start_minute = null,
                allowed_end_minute = null,
                used_minutes_today = 0,
                remaining_minutes = null,
                allowed = true,
                reason = null,
            )
        }
        val notif = mockk<NotificationsRepository>().also {
            every { it.subscribeProgressUpdates() } returns emptyFlow()
            every { it.subscribePlaybackStops() } returns emptyFlow()
        }
        return PlayerViewModel(
            itemRepo = items,
            transcodeRepo = transcode,
            preferencesRepo = prefs,
            serverPrefs = server,
            subtitlePrefs = subPrefs,
            playbackPrefs = mockk(relaxed = true),
            downloads = downloads,
            notifications = notif,
            onlineSubtitles = mockk(relaxed = true),
            trickplayRepo = trickplay,
            watchLimitRepo = watchLimit,
            audiobooks = books,
            serverCapabilities = mockk(relaxed = true),
        )
    }

    private fun http(code: Int) = HttpException(Response.error<Any>(code, "".toResponseBody(null)))

    // ── Listening speed ───────────────────────────────────────────────

    @Test
    fun `a book starts with its saved speed, and bookmarks on`() = runTest(dispatcher) {
        val books = audiobooks(ListeningSpeed(1.5f, serverSupport = true))
        val vm = newVm(items(book), books)
        vm.prepare("book-1")
        advanceUntilIdle()

        assertThat(vm.state.value.listeningRate).isEqualTo(1.5f)
        assertThat(vm.state.value.bookmarksSupported).isTrue()
        coVerify { books.listeningSpeed("book-1", "book-1") }
    }

    @Test
    fun `a chapter file asks for its book's speed`() = runTest(dispatcher) {
        val books = audiobooks(ListeningSpeed(2.0f, true))
        val vm = newVm(items(chapter("ch-2")), books)
        vm.prepare("ch-2")
        advanceUntilIdle()

        assertThat(vm.state.value.listeningRate).isEqualTo(2.0f)
        coVerify { books.listeningSpeed("ch-2", "book-1") }
    }

    @Test
    fun `an older server plays the book at 1x and hides bookmarks`() = runTest(dispatcher) {
        val vm = newVm(items(book), audiobooks(ListeningSpeed(null, serverSupport = false)))
        vm.prepare("book-1")
        advanceUntilIdle()

        assertThat(vm.state.value.listeningRate).isEqualTo(1.0f)
        assertThat(vm.state.value.bookmarksSupported).isFalse()
    }

    @Test
    fun `music never gets a speed`() = runTest(dispatcher) {
        val books = audiobooks()
        val vm = newVm(items(track), books)
        vm.prepare("track-1")
        advanceUntilIdle()
        vm.setListeningRate(2f)
        vm.addBookmark(1_000, "")
        advanceUntilIdle()

        assertThat(vm.state.value.listeningRate).isNull()
        assertThat(vm.state.value.bookmarksSupported).isFalse()
        coVerify(exactly = 0) { books.listeningSpeed(any(), any()) }
        verify(exactly = 0) { books.saveRate(any(), any(), any()) }
        coVerify(exactly = 0) { books.addBookmark(any(), any(), any()) }
    }

    @Test
    fun `picking a speed saves it for the book, clamped`() = runTest(dispatcher) {
        val books = audiobooks()
        val vm = newVm(items(chapter("ch-2")), books)
        vm.prepare("ch-2")
        advanceUntilIdle()
        vm.setListeningRate(1.75f)
        vm.setListeningRate(0.1f)

        verify { books.saveRate("ch-2", "book-1", 1.75f) }
        verify { books.saveRate("ch-2", "book-1", 0.5f) }
        assertThat(vm.state.value.listeningRate).isEqualTo(0.5f)
    }

    // ── Bookmarks ─────────────────────────────────────────────────────

    @Test
    fun `add bookmark posts the playable item and confirms`() = runTest(dispatcher) {
        val books = audiobooks()
        coEvery { books.addBookmark("ch-2", 61_000, "cliffhanger") } returns
            Bookmark(id = "bm-1", item_id = "ch-2", position_ms = 61_000, note = "cliffhanger")
        val vm = newVm(items(chapter("ch-2")), books)
        vm.prepare("ch-2")
        advanceUntilIdle()

        vm.addBookmark(61_000, "cliffhanger")
        advanceUntilIdle()

        assertThat(vm.bookmarkNotices.first()).isEqualTo(BookmarkNotice.Added(61_000))
    }

    @Test
    fun `a full book says so`() = runTest(dispatcher) {
        val books = audiobooks()
        coEvery { books.addBookmark(any(), any(), any()) } throws http(409)
        val vm = newVm(items(book), books)
        vm.prepare("book-1")
        advanceUntilIdle()

        vm.addBookmark(5_000, "")
        advanceUntilIdle()

        assertThat(vm.bookmarkNotices.first()).isEqualTo(BookmarkNotice.LimitReached)
        assertThat(vm.state.value.bookmarksSupported).isTrue()
    }

    @Test
    fun `a 404 on add hides the action`() = runTest(dispatcher) {
        val books = audiobooks()
        coEvery { books.addBookmark(any(), any(), any()) } throws http(404)
        val vm = newVm(items(book), books)
        vm.prepare("book-1")
        advanceUntilIdle()

        vm.addBookmark(5_000, "")
        advanceUntilIdle()

        assertThat(vm.bookmarkNotices.first()).isEqualTo(BookmarkNotice.Unsupported)
        assertThat(vm.state.value.bookmarksSupported).isFalse()
    }

    // ── Start position ────────────────────────────────────────────────

    @Test
    fun `a bookmark start position beats the resume point`() = runTest(dispatcher) {
        val vm = newVm(items(chapter("ch-2")), audiobooks())
        vm.prepare("ch-2", startAtMs = 95_000)
        advanceUntilIdle()

        val source = vm.state.value.source as PlaybackSource.DirectPlay
        assertThat(source.startMs).isEqualTo(95_000)
    }

    // ── Sleep timer: end of chapter ───────────────────────────────────

    /** A bound player at a position the test moves; records pauses. */
    private class FakePlayer {
        var positionMs = 0L
        var paused = 0
        val hooks = SleepTimerHooks(positionMs = { positionMs }, speed = { 1f }, pause = { paused++ })
    }

    private fun TestScope.playingBook(atMs: Long): Pair<PlayerViewModel, FakePlayer> {
        val vm = newVm(items(book), audiobooks())
        vm.prepare("book-1")
        advanceUntilIdle()
        val player = FakePlayer().also { it.positionMs = atMs }
        vm.setSleepTimerHooks(player.hooks)
        return vm to player
    }

    @Test
    fun `end of chapter pauses at the next embedded chapter mark`() = runTest(dispatcher) {
        val (vm, player) = playingBook(atMs = 10_000)
        vm.setSleepTimer(SleepTimer.EndOfChapter)
        runCurrent()
        // The background service is told to stop at the item's end too.
        assertThat(StopAfterItem.isArmedFor("book-1")).isTrue()

        player.positionMs = 59_000
        advanceTimeBy(1_001)
        assertThat(player.paused).isEqualTo(0)

        player.positionMs = 60_200
        advanceTimeBy(1_001)
        assertThat(player.paused).isEqualTo(1)
        assertThat(vm.sleepTimer.value).isNull()
        assertThat(StopAfterItem.isArmedFor("book-1")).isFalse()
    }

    @Test
    fun `a seek re-aims end of chapter at the chapter landed in`() = runTest(dispatcher) {
        val (vm, player) = playingBook(atMs = 10_000)
        vm.setSleepTimer(SleepTimer.EndOfChapter)
        runCurrent()

        // Jump into chapter three: chapter one's end no longer applies.
        player.positionMs = 160_000
        vm.onSleepTimerSeek(160_000)
        advanceTimeBy(1_001)
        assertThat(player.paused).isEqualTo(0)

        player.positionMs = 200_000
        advanceTimeBy(1_001)
        assertThat(player.paused).isEqualTo(1)
    }

    @Test
    fun `a chapter file stops when it ends, and the service won't chain on`() = runTest(dispatcher) {
        val vm = newVm(items(chapter("ch-2")), audiobooks())
        vm.prepare("ch-2")
        advanceUntilIdle()
        val player = FakePlayer()
        vm.setSleepTimerHooks(player.hooks)

        vm.setSleepTimer(SleepTimer.EndOfChapter)
        runCurrent()
        assertThat(StopAfterItem.isArmedFor("ch-2")).isTrue()
        assertThat(vm.sleepsAtItemEnd()).isTrue()

        vm.onPlayerEnded()
        assertThat(player.paused).isEqualTo(1)
        assertThat(vm.sleepTimer.value).isNull()
    }

    @Test
    fun `turning the timer off withdraws the stop`() = runTest(dispatcher) {
        val (vm, _) = playingBook(atMs = 10_000)
        vm.setSleepTimer(SleepTimer.EndOfChapter)
        runCurrent()
        vm.setSleepTimer(SleepTimer.Off)

        assertThat(StopAfterItem.isArmedFor("book-1")).isFalse()
        assertThat(vm.sleepTimer.value).isNull()
    }

    @Test
    fun `a minutes timer carries over to the next chapter's screen`() = runTest(dispatcher) {
        var clock = 0L
        val first = newVm(items(chapter("ch-2"), chapter("ch-3")), audiobooks())
        first.elapsedRealtime = { clock }
        first.prepare("ch-2")
        advanceUntilIdle()
        first.setSleepTimer(SleepTimer.Minutes(15))
        advanceTimeBy(60_001)

        // The service chained to ch-3; the screen follows it.
        first.handOffSleepTimer("ch-3")
        first.setSleepTimer(SleepTimer.Off) // its ViewModel goes away
        clock += 2_000

        val next = newVm(items(chapter("ch-2"), chapter("ch-3")), audiobooks())
        next.elapsedRealtime = { clock }
        next.prepare("ch-3")
        runCurrent()

        val carried = next.sleepTimer.value
        assertThat(carried?.mode).isEqualTo(SleepTimer.Minutes(15))
        assertThat(carried?.remainingMs).isEqualTo(15 * 60_000L - 60_000L - 2_000L)
        next.setSleepTimer(SleepTimer.Off)
    }

    @Test
    fun `end of chapter carries over armed for the next chapter`() = runTest(dispatcher) {
        val first = newVm(items(chapter("ch-2"), chapter("ch-3")), audiobooks())
        first.prepare("ch-2")
        advanceUntilIdle()
        first.setSleepTimer(SleepTimer.EndOfChapter)
        first.handOffSleepTimer("ch-3")

        val next = newVm(items(chapter("ch-2"), chapter("ch-3")), audiobooks())
        next.prepare("ch-3")
        advanceUntilIdle()

        assertThat(next.sleepTimer.value?.mode).isEqualTo(SleepTimer.EndOfChapter)
        assertThat(StopAfterItem.isArmedFor("ch-3")).isTrue()
        next.setSleepTimer(SleepTimer.Off)
        first.setSleepTimer(SleepTimer.Off)
    }
}
