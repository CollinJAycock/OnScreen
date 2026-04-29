package tv.onscreen.mobile.ui.player

import com.google.common.truth.Truth.assertThat
import io.mockk.coEvery
import io.mockk.coVerify
import io.mockk.every
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
import tv.onscreen.mobile.data.downloads.DownloadStore
import tv.onscreen.mobile.data.downloads.OnScreenDownloadManager
import tv.onscreen.mobile.data.model.AudioStream
import tv.onscreen.mobile.data.model.ChildItem
import tv.onscreen.mobile.data.model.ItemDetail
import tv.onscreen.mobile.data.model.ItemFile
import tv.onscreen.mobile.data.model.SubtitleStream
import tv.onscreen.mobile.data.model.TranscodeSession
import tv.onscreen.mobile.data.model.UserPreferences
import tv.onscreen.mobile.data.prefs.ServerPrefs
import kotlinx.coroutines.flow.emptyFlow
import tv.onscreen.mobile.data.repository.ItemRepository
import tv.onscreen.mobile.data.repository.NotificationsRepository
import tv.onscreen.mobile.data.repository.OnlineSubtitleRepository
import tv.onscreen.mobile.data.repository.PreferencesRepository
import tv.onscreen.mobile.data.repository.TranscodeRepository

@OptIn(ExperimentalCoroutinesApi::class)
class PlayerViewModelTest {

    private val dispatcher = StandardTestDispatcher()

    @Before fun setUp() { Dispatchers.setMain(dispatcher) }
    @After  fun tearDown() { Dispatchers.resetMain() }

    private fun directPlayFile() = ItemFile(
        id = "f1",
        stream_url = "/media/files/f1.mp4",
        container = "mp4",
        video_codec = "h264",
        audio_codec = "aac",
        resolution_h = 1080,
        audio_streams = listOf(AudioStream(0, "aac", 2, "en", "English")),
        subtitle_streams = listOf(SubtitleStream(1, "subrip", "en", "English", false)),
    )

    private fun transcodeFile() = ItemFile(
        id = "f2",
        stream_url = "/media/files/f2.avi",
        container = "avi",
        video_codec = "mpeg2",
        audio_codec = "mp2",
        resolution_h = 1080,
    )

    private fun movieDetail(file: ItemFile, viewOffsetMs: Long = 0) = ItemDetail(
        id = "movie-1",
        library_id = "lib-1",
        title = "Test Movie",
        type = "movie",
        files = listOf(file),
        view_offset_ms = viewOffsetMs,
    )

    private fun episodeDetail(file: ItemFile, parentId: String, index: Int) = ItemDetail(
        id = "ep-$index",
        library_id = "lib-1",
        title = "Episode $index",
        type = "episode",
        parent_id = parentId,
        index = index,
        files = listOf(file),
    )

    /** ItemRepository mock with [getMarkers] pre-stubbed so prepare()'s
     *  unconditional markers fetch doesn't blow up the test. */
    private fun itemRepo(): ItemRepository {
        val repo = mockk<ItemRepository>()
        coEvery { repo.getMarkers(any()) } returns emptyList()
        return repo
    }

    private fun prefs(): PreferencesRepository {
        val p = mockk<PreferencesRepository>()
        coEvery { p.get() } returns UserPreferences()
        return p
    }

    private fun serverPrefs(url: String? = "http://srv"): ServerPrefs {
        val p = mockk<ServerPrefs>(relaxed = true)
        coEvery { p.getServerUrl() } returns url
        coEvery { p.getAccessToken() } returns null
        return p
    }

    /** Online-subtitle repo with no behavior wired — only the tests
     *  that exercise the search/download path override. */
    private fun stubSubtitles(): OnlineSubtitleRepository = mockk(relaxed = true)

    /** Notifications repo whose SSE stream emits nothing — keeps the
     *  cross-device resume path silent during tests that don't exercise
     *  it. Tests that *do* (the SSE ones below) override per-test. */
    private fun emptyNotifications(): NotificationsRepository {
        val n = mockk<NotificationsRepository>()
        coEvery { n.subscribeProgressUpdates() } returns emptyFlow()
        return n
    }

    /** Wires a download manager whose store reports no completed download
     *  for any file_id — the offline-first short-circuit in prepare() then
     *  falls through to the normal direct/transcode decision. */
    private fun emptyDownloads(): OnScreenDownloadManager {
        val store = mockk<DownloadStore>(relaxed = true)
        coEvery { store.load() } returns Unit
        coEvery { store.get(any()) } returns null
        val mgr = mockk<OnScreenDownloadManager>()
        every { mgr.store } returns store
        return mgr
    }

    @Test
    fun `direct play movie produces DirectPlay source with view offset and stream token`() =
        runTest(dispatcher) {
            val itemRepo = itemRepo()
            val transcodeRepo = mockk<TranscodeRepository>()
            coEvery { itemRepo.getItem("movie-1") } returns
                movieDetail(directPlayFile().copy(stream_token = "st-24h"), viewOffsetMs = 12_000L)

            val vm = PlayerViewModel(itemRepo, transcodeRepo, prefs(), serverPrefs(), emptyDownloads(), emptyNotifications(), stubSubtitles())
            vm.prepare("movie-1")
            advanceUntilIdle()

            val s = vm.state.value
            assertThat(s.error).isNull()
            assertThat(s.loading).isFalse()
            val src = s.source as PlaybackSource.DirectPlay
            assertThat(src.url).isEqualTo("http://srv/media/files/f1.mp4?token=st-24h")
            assertThat(src.startMs).isEqualTo(12_000L)
            assertThat(vm.hlsOffsetMs).isEqualTo(0L)
            assertThat(s.audioStreams).hasSize(1)
            assertThat(s.subtitles).hasSize(1)
        }

    @Test
    fun `direct play falls back to access token when stream token is absent`() = runTest(dispatcher) {
        val itemRepo = itemRepo()
        val transcodeRepo = mockk<TranscodeRepository>()
        coEvery { itemRepo.getItem("movie-1") } returns movieDetail(directPlayFile())
        val sp = mockk<ServerPrefs>(relaxed = true)
        coEvery { sp.getServerUrl() } returns "http://srv"
        coEvery { sp.getAccessToken() } returns "at-1h"

        val vm = PlayerViewModel(itemRepo, transcodeRepo, prefs(), sp, emptyDownloads(), emptyNotifications(), stubSubtitles())
        vm.prepare("movie-1")
        advanceUntilIdle()

        val src = vm.state.value.source as PlaybackSource.DirectPlay
        assertThat(src.url).isEqualTo("http://srv/media/files/f1.mp4?token=at-1h")
    }

    @Test
    fun `unsupported codec triggers transcode session and produces Hls source`() = runTest(dispatcher) {
        val itemRepo = itemRepo()
        val transcodeRepo = mockk<TranscodeRepository>()
        coEvery { itemRepo.getItem("movie-1") } returns
            movieDetail(transcodeFile(), viewOffsetMs = 30_000L)
        coEvery {
            transcodeRepo.start(
                itemId = "movie-1",
                height = 1080,
                positionMs = 30_000L,
                fileId = "f2",
                videoCopy = false,
                audioStreamIndex = null,
                supportsHevc = true,
            )
        } returns TranscodeSession(
            session_id = "sess-1",
            playlist_url = "/transcode/sess-1.m3u8",
            token = "tok",
        )

        val vm = PlayerViewModel(itemRepo, transcodeRepo, prefs(), serverPrefs(), emptyDownloads(), emptyNotifications(), stubSubtitles())
        vm.prepare("movie-1")
        advanceUntilIdle()

        val src = vm.state.value.source as PlaybackSource.Hls
        assertThat(src.playlistUrl).isEqualTo("http://srv/transcode/sess-1.m3u8")
        assertThat(src.offsetMs).isEqualTo(30_000L)
        assertThat(vm.hlsOffsetMs).isEqualTo(30_000L)
    }

    @Test
    fun `missing files surfaces error in ui state`() = runTest(dispatcher) {
        val itemRepo = itemRepo()
        val transcodeRepo = mockk<TranscodeRepository>()
        coEvery { itemRepo.getItem("movie-1") } returns movieDetail(directPlayFile()).copy(files = emptyList())

        val vm = PlayerViewModel(itemRepo, transcodeRepo, prefs(), serverPrefs(), emptyDownloads(), emptyNotifications(), stubSubtitles())
        vm.prepare("movie-1")
        advanceUntilIdle()

        val s = vm.state.value
        assertThat(s.error).isEqualTo("No playable file")
        assertThat(s.source).isNull()
        assertThat(s.loading).isFalse()
    }

    @Test
    fun `getItem failure surfaces error message`() = runTest(dispatcher) {
        val itemRepo = itemRepo()
        val transcodeRepo = mockk<TranscodeRepository>()
        coEvery { itemRepo.getItem(any()) } throws RuntimeException("api 500")

        val vm = PlayerViewModel(itemRepo, transcodeRepo, prefs(), serverPrefs(), emptyDownloads(), emptyNotifications(), stubSubtitles())
        vm.prepare("movie-1")
        advanceUntilIdle()

        assertThat(vm.state.value.error).isEqualTo("api 500")
    }

    @Test
    fun `episode load resolves next sibling by index plus one`() = runTest(dispatcher) {
        val itemRepo = itemRepo()
        val transcodeRepo = mockk<TranscodeRepository>()
        coEvery { itemRepo.getItem("ep-1") } returns episodeDetail(directPlayFile(), "season-1", 1)
        coEvery { itemRepo.getChildren("season-1") } returns listOf(
            ChildItem(id = "ep-1", title = "E1", type = "episode", index = 1),
            ChildItem(id = "ep-2", title = "E2", type = "episode", index = 2),
        )

        val vm = PlayerViewModel(itemRepo, transcodeRepo, prefs(), serverPrefs(), emptyDownloads(), emptyNotifications(), stubSubtitles())
        vm.prepare("ep-1")
        advanceUntilIdle()

        assertThat(vm.state.value.nextSibling?.id).isEqualTo("ep-2")
    }

    @Test
    fun `non-episode items do not query siblings`() = runTest(dispatcher) {
        val itemRepo = itemRepo()
        val transcodeRepo = mockk<TranscodeRepository>()
        coEvery { itemRepo.getItem("movie-1") } returns movieDetail(directPlayFile())

        val vm = PlayerViewModel(itemRepo, transcodeRepo, prefs(), serverPrefs(), emptyDownloads(), emptyNotifications(), stubSubtitles())
        vm.prepare("movie-1")
        advanceUntilIdle()

        coVerify(exactly = 0) { itemRepo.getChildren(any()) }
        assertThat(vm.state.value.nextSibling).isNull()
    }

    @Test
    fun `getChildren failure does not break main playback flow`() = runTest(dispatcher) {
        val itemRepo = itemRepo()
        val transcodeRepo = mockk<TranscodeRepository>()
        coEvery { itemRepo.getItem("ep-1") } returns episodeDetail(directPlayFile(), "season-1", 1)
        coEvery { itemRepo.getChildren("season-1") } throws RuntimeException("offline")

        val vm = PlayerViewModel(itemRepo, transcodeRepo, prefs(), serverPrefs(), emptyDownloads(), emptyNotifications(), stubSubtitles())
        vm.prepare("ep-1")
        advanceUntilIdle()

        val s = vm.state.value
        assertThat(s.error).isNull()
        assertThat(s.source).isInstanceOf(PlaybackSource.DirectPlay::class.java)
        assertThat(s.nextSibling).isNull()
    }

    @Test
    fun `stopActiveTranscode is a no-op when no session is active`() = runTest(dispatcher) {
        val itemRepo = itemRepo()
        val transcodeRepo = mockk<TranscodeRepository>(relaxed = true)
        val vm = PlayerViewModel(itemRepo, transcodeRepo, prefs(), serverPrefs(), emptyDownloads(), emptyNotifications(), stubSubtitles())

        vm.stopActiveTranscode()
        advanceUntilIdle()

        coVerify(exactly = 0) { transcodeRepo.stop(any(), any()) }
    }

    @Test
    fun `stopActiveTranscode sends stop request after a transcode session was started`() = runTest(dispatcher) {
        val itemRepo = itemRepo()
        val transcodeRepo = mockk<TranscodeRepository>(relaxed = true)
        coEvery { itemRepo.getItem("movie-1") } returns movieDetail(transcodeFile())
        coEvery {
            transcodeRepo.start(any(), any(), any(), any(), any(), any(), any())
        } returns TranscodeSession(
            session_id = "sess-9",
            playlist_url = "/transcode/sess-9.m3u8",
            token = "tok-9",
        )

        val vm = PlayerViewModel(itemRepo, transcodeRepo, prefs(), serverPrefs(), emptyDownloads(), emptyNotifications(), stubSubtitles())
        vm.prepare("movie-1")
        advanceUntilIdle()

        vm.stopActiveTranscode()
        advanceUntilIdle()

        coVerify(exactly = 1) { transcodeRepo.stop("sess-9", "tok-9") }

        // Calling again should not re-issue the stop — IDs are cleared on
        // first call so the launched coroutine has nothing to send.
        vm.stopActiveTranscode()
        advanceUntilIdle()
        coVerify(exactly = 1) { transcodeRepo.stop("sess-9", "tok-9") }
    }

    @Test
    fun `reportProgress no-ops when duration is zero`() = runTest(dispatcher) {
        val itemRepo = itemRepo()
        val transcodeRepo = mockk<TranscodeRepository>()
        val vm = PlayerViewModel(itemRepo, transcodeRepo, prefs(), serverPrefs(), emptyDownloads(), emptyNotifications(), stubSubtitles())

        vm.reportProgress("movie-1", 1_000L, 0L, "playing")
        advanceUntilIdle()

        coVerify(exactly = 0) { itemRepo.updateProgress(any(), any(), any(), any()) }
    }

    @Test
    fun `remote progress for the active item flows into remoteResumeMs`() = runTest(dispatcher) {
        val itemRepo = itemRepo()
        val transcodeRepo = mockk<TranscodeRepository>()
        coEvery { itemRepo.getItem("movie-1") } returns movieDetail(directPlayFile())
        val notif = mockk<NotificationsRepository>()
        coEvery { notif.subscribeProgressUpdates() } returns kotlinx.coroutines.flow.flowOf(
            tv.onscreen.mobile.data.model.ProgressUpdateData(
                item_id = "movie-1",
                position_ms = 60_000L,
                duration_ms = 600_000L,
                state = "playing",
            ),
        )

        val vm = PlayerViewModel(itemRepo, transcodeRepo, prefs(), serverPrefs(), emptyDownloads(), notif, stubSubtitles())
        vm.prepare("movie-1")
        advanceUntilIdle()

        assertThat(vm.remoteResumeMs.value).isEqualTo(60_000L)
    }

    @Test
    fun `remote progress for a different item is ignored`() = runTest(dispatcher) {
        val itemRepo = itemRepo()
        val transcodeRepo = mockk<TranscodeRepository>()
        coEvery { itemRepo.getItem("movie-1") } returns movieDetail(directPlayFile())
        val notif = mockk<NotificationsRepository>()
        coEvery { notif.subscribeProgressUpdates() } returns kotlinx.coroutines.flow.flowOf(
            tv.onscreen.mobile.data.model.ProgressUpdateData(
                item_id = "some-other-item",
                position_ms = 60_000L,
                duration_ms = 600_000L,
                state = "playing",
            ),
        )

        val vm = PlayerViewModel(itemRepo, transcodeRepo, prefs(), serverPrefs(), emptyDownloads(), notif, stubSubtitles())
        vm.prepare("movie-1")
        advanceUntilIdle()

        assertThat(vm.remoteResumeMs.value).isNull()
    }

    @Test
    fun `same-device echo within 3s window is dropped`() = runTest(dispatcher) {
        // Server broadcasts our own progress writes back to us — without
        // a debounce the player would seek to the position it just
        // reported, looping. The 3 s window absorbs jitter from the
        // 10 s ticker + transcode-offset math.
        val itemRepo = itemRepo()
        val transcodeRepo = mockk<TranscodeRepository>()
        coEvery { itemRepo.getItem("movie-1") } returns movieDetail(directPlayFile())
        coEvery { itemRepo.updateProgress(any(), any(), any(), any()) } returns Unit
        val notif = mockk<NotificationsRepository>()
        coEvery { notif.subscribeProgressUpdates() } returns kotlinx.coroutines.flow.flowOf(
            tv.onscreen.mobile.data.model.ProgressUpdateData(
                item_id = "movie-1",
                position_ms = 60_500L,
                duration_ms = 600_000L,
                state = "playing",
            ),
        )

        val vm = PlayerViewModel(itemRepo, transcodeRepo, prefs(), serverPrefs(), emptyDownloads(), notif, stubSubtitles())
        // Record a local report at 60_000ms; the SSE event lands at
        // 60_500ms which is within the 3 s same-device echo window.
        vm.reportProgress("movie-1", 60_000L, 600_000L, "playing")
        vm.prepare("movie-1")
        advanceUntilIdle()

        assertThat(vm.remoteResumeMs.value).isNull()
    }

    @Test
    fun `clearRemoteResume resets the seek signal`() = runTest(dispatcher) {
        val itemRepo = itemRepo()
        val transcodeRepo = mockk<TranscodeRepository>()
        coEvery { itemRepo.getItem("movie-1") } returns movieDetail(directPlayFile())
        val notif = mockk<NotificationsRepository>()
        coEvery { notif.subscribeProgressUpdates() } returns kotlinx.coroutines.flow.flowOf(
            tv.onscreen.mobile.data.model.ProgressUpdateData(
                item_id = "movie-1",
                position_ms = 90_000L,
                duration_ms = 600_000L,
                state = "playing",
            ),
        )

        val vm = PlayerViewModel(itemRepo, transcodeRepo, prefs(), serverPrefs(), emptyDownloads(), notif, stubSubtitles())
        vm.prepare("movie-1")
        advanceUntilIdle()
        assertThat(vm.remoteResumeMs.value).isEqualTo(90_000L)

        vm.clearRemoteResume()
        assertThat(vm.remoteResumeMs.value).isNull()
    }

    @Test
    fun `reportProgress forwards a positive-duration call to the repo`() = runTest(dispatcher) {
        val itemRepo = itemRepo()
        val transcodeRepo = mockk<TranscodeRepository>()
        coEvery { itemRepo.updateProgress(any(), any(), any(), any()) } returns Unit
        val vm = PlayerViewModel(itemRepo, transcodeRepo, prefs(), serverPrefs(), emptyDownloads(), emptyNotifications(), stubSubtitles())

        vm.reportProgress("movie-1", 5_000L, 90_000L, "playing")
        advanceUntilIdle()

        coVerify(exactly = 1) { itemRepo.updateProgress("movie-1", 5_000L, 90_000L, "playing") }
    }

    @Test
    fun `searchOnlineSubtitles populates the dialog state`() = runTest(dispatcher) {
        val itemRepo = itemRepo()
        val transcodeRepo = mockk<TranscodeRepository>()
        val subs = mockk<OnlineSubtitleRepository>()
        coEvery { subs.search("movie-1", "en", null) } returns listOf(
            tv.onscreen.mobile.data.model.OnlineSubtitle(
                provider_file_id = 42, file_name = "Movie.srt", language = "en",
            ),
        )
        val vm = PlayerViewModel(itemRepo, transcodeRepo, prefs(), serverPrefs(), emptyDownloads(), emptyNotifications(), subs)

        vm.searchOnlineSubtitles("movie-1", "en", null)
        advanceUntilIdle()

        val ui = vm.onlineSubtitleSearch.value
        assertThat(ui.loading).isFalse()
        assertThat(ui.results).hasSize(1)
        assertThat(ui.results.first().provider_file_id).isEqualTo(42)
    }

    @Test
    fun `searchOnlineSubtitles surfaces error message on repo failure`() = runTest(dispatcher) {
        val itemRepo = itemRepo()
        val transcodeRepo = mockk<TranscodeRepository>()
        val subs = mockk<OnlineSubtitleRepository>()
        coEvery { subs.search(any(), any(), any()) } throws RuntimeException("rate limited")
        val vm = PlayerViewModel(itemRepo, transcodeRepo, prefs(), serverPrefs(), emptyDownloads(), emptyNotifications(), subs)

        vm.searchOnlineSubtitles("movie-1", "en", null)
        advanceUntilIdle()

        assertThat(vm.onlineSubtitleSearch.value.error).isEqualTo("rate limited")
    }

    @Test
    fun `downloadOnlineSubtitle attaches subtitle to the active file id`() = runTest(dispatcher) {
        val itemRepo = itemRepo()
        val transcodeRepo = mockk<TranscodeRepository>()
        coEvery { itemRepo.getItem("movie-1") } returns movieDetail(directPlayFile())
        val subs = mockk<OnlineSubtitleRepository>()
        coEvery { subs.download(any(), any(), any()) } returns Unit
        val vm = PlayerViewModel(itemRepo, transcodeRepo, prefs(), serverPrefs(), emptyDownloads(), emptyNotifications(), subs)
        vm.prepare("movie-1")
        advanceUntilIdle()

        var doneCalled = false
        val candidate = tv.onscreen.mobile.data.model.OnlineSubtitle(
            provider_file_id = 99, file_name = "S.srt", language = "fr",
        )
        vm.downloadOnlineSubtitle("movie-1", candidate) { doneCalled = true }
        advanceUntilIdle()

        coVerify(exactly = 1) { subs.download("movie-1", "f1", candidate) }
        assertThat(doneCalled).isTrue()
    }
}
