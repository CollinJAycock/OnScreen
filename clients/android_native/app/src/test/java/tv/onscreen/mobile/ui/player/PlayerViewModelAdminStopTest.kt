package tv.onscreen.mobile.ui.player

import com.google.common.truth.Truth.assertThat
import io.mockk.coEvery
import io.mockk.every
import io.mockk.mockk
import io.mockk.verify
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.ExperimentalCoroutinesApi
import kotlinx.coroutines.flow.Flow
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.emptyFlow
import kotlinx.coroutines.flow.flowOf
import kotlinx.coroutines.test.StandardTestDispatcher
import kotlinx.coroutines.test.advanceUntilIdle
import kotlinx.coroutines.test.resetMain
import kotlinx.coroutines.test.runTest
import kotlinx.coroutines.test.setMain
import okhttp3.MediaType.Companion.toMediaTypeOrNull
import okhttp3.ResponseBody.Companion.toResponseBody
import org.junit.After
import org.junit.Before
import org.junit.Test
import tv.onscreen.mobile.data.downloads.DownloadManifest
import tv.onscreen.mobile.data.downloads.DownloadStore
import tv.onscreen.mobile.data.downloads.OnScreenDownloadManager
import tv.onscreen.mobile.data.model.ItemDetail
import tv.onscreen.mobile.data.model.ItemFile
import tv.onscreen.mobile.data.model.PlaybackStopEvent
import tv.onscreen.mobile.data.model.TranscodeSession
import tv.onscreen.mobile.data.model.UserPreferences
import tv.onscreen.mobile.data.model.WatchLimitData
import tv.onscreen.mobile.data.prefs.ServerPrefs
import tv.onscreen.mobile.data.repository.ItemRepository
import tv.onscreen.mobile.data.repository.NotificationsRepository
import tv.onscreen.mobile.data.repository.PreferencesRepository
import tv.onscreen.mobile.data.repository.TranscodeRepository
import tv.onscreen.mobile.data.repository.WatchLimitRepository

/**
 * Admin "stop this stream" on the video player: the SSE `playback.stop`
 * event, the 403 PLAYBACK_STOPPED heartbeat refusal, and a refused restart
 * inside the stop window all end playback with the admin's message.
 */
@OptIn(ExperimentalCoroutinesApi::class)
class PlayerViewModelAdminStopTest {

    private val dispatcher = StandardTestDispatcher()

    @Before fun setUp() { Dispatchers.setMain(dispatcher) }
    @After fun tearDown() { Dispatchers.resetMain() }

    private val directFile = ItemFile(
        id = "f1",
        stream_url = "/media/files/f1.mp4",
        container = "mp4",
        video_codec = "h264",
        audio_codec = "aac",
        resolution_h = 1080,
        stream_token = "st",
    )

    private val transcodeFile = ItemFile(
        id = "f2",
        stream_url = "/media/files/f2.avi",
        container = "avi",
        video_codec = "mpeg2",
        audio_codec = "mp2",
        resolution_h = 1080,
    )

    private fun movie(file: ItemFile) = ItemDetail(
        id = "movie-1",
        library_id = "lib-1",
        title = "Test Movie",
        type = "movie",
        files = listOf(file),
    )

    private fun httpError(code: Int, body: String) = retrofit2.HttpException(
        retrofit2.Response.error<Any>(code, body.toResponseBody("application/json".toMediaTypeOrNull())),
    )

    private val stoppedBody =
        """{"error":{"code":"PLAYBACK_STOPPED","message":"Playback was stopped by the server admin: maintenance"}}"""

    private fun itemRepo(file: ItemFile): ItemRepository = mockk<ItemRepository>().also {
        coEvery { it.getItem("movie-1") } returns movie(file)
        coEvery { it.getMarkers(any()) } returns emptyList()
    }

    private fun transcodeRepo(verdict: String?): TranscodeRepository = mockk<TranscodeRepository>().also {
        coEvery { it.decide(any(), any()) } returns verdict
        every { it.stopDetached(any(), any()) } returns Unit
    }

    private fun notifications(stops: Flow<PlaybackStopEvent> = emptyFlow()): NotificationsRepository =
        mockk<NotificationsRepository>().also {
            every { it.subscribeProgressUpdates() } returns emptyFlow()
            every { it.subscribePlaybackStops() } returns stops
        }

    private fun newVm(
        items: ItemRepository,
        transcode: TranscodeRepository,
        notif: NotificationsRepository = notifications(),
    ): PlayerViewModel {
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
        )
    }

    @Test
    fun `untargeted stop event for the playing item ends playback with the admin message`() = runTest(dispatcher) {
        val vm = newVm(
            itemRepo(directFile),
            transcodeRepo("directPlay"),
            notifications(flowOf(PlaybackStopEvent(item_id = "movie-1", message = "maintenance"))),
        )
        vm.prepare("movie-1")
        advanceUntilIdle()

        assertThat(vm.state.value.error).isEqualTo("Playback was stopped by the server admin: maintenance")
    }

    @Test
    fun `stop event without a message shows the bare sentence`() = runTest(dispatcher) {
        val vm = newVm(
            itemRepo(directFile),
            transcodeRepo("directPlay"),
            notifications(flowOf(PlaybackStopEvent(item_id = "movie-1"))),
        )
        vm.prepare("movie-1")
        advanceUntilIdle()

        assertThat(vm.state.value.error).isEqualTo("Playback was stopped by the server admin.")
    }

    @Test
    fun `stop events aimed elsewhere are ignored`() = runTest(dispatcher) {
        val vm = newVm(
            itemRepo(directFile),
            transcodeRepo("directPlay"),
            notifications(
                flowOf(
                    PlaybackStopEvent(item_id = "another-item"),
                    // Someone else's transcode of the same title.
                    PlaybackStopEvent(item_id = "movie-1", session_id = "their-session"),
                    // Another device, named in its heartbeats.
                    PlaybackStopEvent(item_id = "movie-1", client_name = "Living Room TV"),
                ),
            ),
        )
        vm.prepare("movie-1")
        advanceUntilIdle()

        assertThat(vm.state.value.error).isNull()
        assertThat(vm.state.value.source).isInstanceOf(PlaybackSource.DirectPlay::class.java)
    }

    @Test
    fun `a stop naming our transcode session ends playback and drops the session`() = runTest(dispatcher) {
        val transcode = transcodeRepo("transcode")
        coEvery { transcode.start(any(), any(), any(), any(), any(), any(), any()) } returns
            TranscodeSession(session_id = "sess-1", playlist_url = "/t/sess-1.m3u8", token = "tok")
        val vm = newVm(
            itemRepo(transcodeFile),
            transcode,
            notifications(flowOf(PlaybackStopEvent(item_id = "movie-1", session_id = "sess-1", message = "bye"))),
        )
        vm.prepare("movie-1")
        advanceUntilIdle()

        assertThat(vm.state.value.error).isEqualTo("Playback was stopped by the server admin: bye")
        verify(exactly = 1) { transcode.stopDetached("sess-1", "tok") }
    }

    @Test
    fun `a PLAYBACK_STOPPED heartbeat refusal ends playback with the server sentence`() = runTest(dispatcher) {
        val items = itemRepo(directFile)
        coEvery { items.updateProgress(any(), any(), any(), any(), any()) } throws httpError(403, stoppedBody)
        val vm = newVm(items, transcodeRepo("directPlay"))
        vm.prepare("movie-1")
        advanceUntilIdle()

        vm.reportProgress("movie-1", 5_000L, 90_000L, "playing")
        advanceUntilIdle()

        assertThat(vm.state.value.error).isEqualTo("Playback was stopped by the server admin: maintenance")
    }

    @Test
    fun `a restart refused inside the stop window shows the admin message`() = runTest(dispatcher) {
        val transcode = transcodeRepo("directStream")
        coEvery { transcode.start(any(), any(), any(), any(), any(), any(), any()) } throws httpError(403, stoppedBody)
        val vm = newVm(itemRepo(directFile), transcode)
        vm.prepare("movie-1")
        advanceUntilIdle()

        assertThat(vm.state.value.source).isNull()
        assertThat(vm.state.value.error).isEqualTo("Playback was stopped by the server admin: maintenance")
    }

    @Test
    fun `a refused media request stops once, first message wins`() = runTest(dispatcher) {
        val vm = newVm(itemRepo(directFile), transcodeRepo("directPlay"))
        vm.prepare("movie-1")
        advanceUntilIdle()

        vm.onStreamRefusedByAdminStop("Playback was stopped by the server admin: first")
        vm.onStreamRefusedByAdminStop("Playback was stopped by the server admin: second")

        assertThat(vm.state.value.error).isEqualTo("Playback was stopped by the server admin: first")
    }

    @Test
    fun `refused media body parses only a PLAYBACK_STOPPED envelope`() {
        assertThat(playbackStoppedMessageFromBody(stoppedBody.toByteArray()))
            .isEqualTo("Playback was stopped by the server admin: maintenance")
        assertThat(playbackStoppedMessageFromBody("""{"error":{"code":"PLAYBACK_STOPPED"}}""".toByteArray()))
            .isEqualTo("Playback was stopped by the server admin.")
        assertThat(playbackStoppedMessageFromBody("""{"error":{"code":"FORBIDDEN","message":"x"}}""".toByteArray()))
            .isNull()
        assertThat(playbackStoppedMessageFromBody("<html>nope</html>".toByteArray())).isNull()
        assertThat(playbackStoppedMessageFromBody(null)).isNull()
        assertThat(playbackStoppedMessage(RuntimeException("boom", java.io.IOException("reset")))).isNull()
    }
}
