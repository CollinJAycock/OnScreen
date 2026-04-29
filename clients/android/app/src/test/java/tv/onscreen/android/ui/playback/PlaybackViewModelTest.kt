package tv.onscreen.android.ui.playback

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
import tv.onscreen.android.data.model.AudioStream
import tv.onscreen.android.data.model.ChildItem
import tv.onscreen.android.data.model.ItemDetail
import tv.onscreen.android.data.model.ItemFile
import tv.onscreen.android.data.model.Marker
import tv.onscreen.android.data.model.SubtitleStream
import tv.onscreen.android.data.model.TranscodeSession
import tv.onscreen.android.data.model.UserPreferences
import tv.onscreen.android.data.repository.ItemRepository
import tv.onscreen.android.data.repository.PreferencesRepository
import tv.onscreen.android.data.repository.TranscodeRepository

@OptIn(ExperimentalCoroutinesApi::class)
class PlaybackViewModelTest {

    private val dispatcher = StandardTestDispatcher()

    @Before
    fun setUp() {
        Dispatchers.setMain(dispatcher)
    }

    @After
    fun tearDown() {
        Dispatchers.resetMain()
    }

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

    private fun movieDetail(file: ItemFile) = ItemDetail(
        id = "movie-1",
        library_id = "lib-1",
        title = "Test Movie",
        type = "movie",
        files = listOf(file),
    )

    private fun prefs(): PreferencesRepository {
        val p = mockk<PreferencesRepository>()
        coEvery { p.get() } returns UserPreferences()
        return p
    }

    /** ServerPrefs stub for the player VM's direct-play URL builder.
     *  Concrete-class mocking goes through mockk-agent;
     *  `relaxed = true` returns null/empty defaults for the suspend
     *  getters without per-call coEvery wiring (none of the VM's
     *  test scenarios exercise the access-token URL append path —
     *  if a future test does, layer a coEvery on top). */
    private fun serverPrefs(): tv.onscreen.android.data.prefs.ServerPrefs =
        mockk(relaxed = true)

    /** ItemRepository mock with [getMarkers] pre-stubbed to an empty
     *  list — every PlaybackViewModel.prepare() call hits the markers
     *  endpoint regardless of item type, so any test that gets past
     *  the file-presence check needs it answered. Tests that exercise
     *  a specific marker payload can layer a coEvery on top. */
    private fun itemRepo(): ItemRepository {
        val repo = mockk<ItemRepository>()
        coEvery { repo.getMarkers(any()) } returns emptyList()
        return repo
    }

    private fun episodeDetail(file: ItemFile, parentId: String, index: Int) = ItemDetail(
        id = "ep-$index",
        library_id = "lib-1",
        title = "Episode $index",
        type = "episode",
        parent_id = parentId,
        index = index,
        files = listOf(file),
    )

    @Test
    fun `direct play movie produces DirectPlay source with start position`() = runTest(dispatcher) {
        val itemRepo = itemRepo()
        val transcodeRepo = mockk<TranscodeRepository>()
        coEvery { itemRepo.getItem("movie-1") } returns movieDetail(directPlayFile())

        val vm = PlaybackViewModel(itemRepo, transcodeRepo, prefs(), serverPrefs())
        vm.prepare("movie-1", startMs = 12_000L, serverUrl = "http://srv")
        advanceUntilIdle()

        val state = vm.uiState.value
        assertThat(state.error).isNull()
        assertThat(state.source).isInstanceOf(PlaybackSource.DirectPlay::class.java)
        val src = state.source as PlaybackSource.DirectPlay
        assertThat(src.url).isEqualTo("http://srv/media/files/f1.mp4")
        assertThat(src.startMs).isEqualTo(12_000L)
        assertThat(vm.hlsOffsetMs).isEqualTo(0L)
        assertThat(state.audioStreams).hasSize(1)
        assertThat(state.subtitles).hasSize(1)
    }

    @Test
    fun `unsupported codec triggers transcode and produces Hls source`() = runTest(dispatcher) {
        val itemRepo = itemRepo()
        val transcodeRepo = mockk<TranscodeRepository>()
        coEvery { itemRepo.getItem("movie-1") } returns movieDetail(transcodeFile())
        coEvery {
            transcodeRepo.start(
                itemId = "movie-1",
                height = 1080,
                positionMs = 30_000L,
                fileId = "f2",
                videoCopy = false,
                supportsHevc = true,
            )
        } returns TranscodeSession(
            session_id = "sess-1",
            token = "tok",
            playlist_url = "/transcode/sess-1.m3u8",
        )

        val vm = PlaybackViewModel(itemRepo, transcodeRepo, prefs(), serverPrefs())
        vm.prepare("movie-1", startMs = 30_000L, serverUrl = "http://srv")
        advanceUntilIdle()

        val src = vm.uiState.value.source
        assertThat(src).isInstanceOf(PlaybackSource.Hls::class.java)
        val hls = src as PlaybackSource.Hls
        assertThat(hls.playlistUrl).isEqualTo("http://srv/transcode/sess-1.m3u8")
        assertThat(hls.offsetMs).isEqualTo(30_000L)
        assertThat(vm.hlsOffsetMs).isEqualTo(30_000L)
    }

    @Test
    fun `missing files surfaces error in ui state`() = runTest(dispatcher) {
        val itemRepo = itemRepo()
        val transcodeRepo = mockk<TranscodeRepository>()
        coEvery { itemRepo.getItem("movie-1") } returns movieDetail(directPlayFile()).copy(files = emptyList())

        val vm = PlaybackViewModel(itemRepo, transcodeRepo, prefs(), serverPrefs())
        vm.prepare("movie-1", startMs = 0L, serverUrl = "http://srv")
        advanceUntilIdle()

        val state = vm.uiState.value
        assertThat(state.error).isEqualTo("No playable file")
        assertThat(state.source).isNull()
    }

    @Test
    fun `repository failure surfaces error message`() = runTest(dispatcher) {
        val itemRepo = itemRepo()
        val transcodeRepo = mockk<TranscodeRepository>()
        coEvery { itemRepo.getItem(any()) } throws RuntimeException("api 500")

        val vm = PlaybackViewModel(itemRepo, transcodeRepo, prefs(), serverPrefs())
        vm.prepare("movie-1", 0L, "http://srv")
        advanceUntilIdle()

        assertThat(vm.uiState.value.error).isEqualTo("api 500")
    }

    @Test
    fun `episode load fetches next episode by index plus one`() = runTest(dispatcher) {
        val itemRepo = itemRepo()
        val transcodeRepo = mockk<TranscodeRepository>()
        coEvery { itemRepo.getItem("ep-1") } returns episodeDetail(directPlayFile(), "season-1", 1)
        coEvery { itemRepo.getChildren("season-1") } returns listOf(
            ChildItem(id = "ep-1", title = "E1", type = "episode", index = 1),
            ChildItem(id = "ep-2", title = "E2", type = "episode", index = 2),
            ChildItem(id = "ep-3", title = "E3", type = "episode", index = 3),
        )

        val vm = PlaybackViewModel(itemRepo, transcodeRepo, prefs(), serverPrefs())
        vm.prepare("ep-1", 0L, "http://srv")
        advanceUntilIdle()

        val next = vm.uiState.value.nextEpisode
        assertThat(next).isNotNull()
        assertThat(next!!.id).isEqualTo("ep-2")
    }

    @Test
    fun `final episode in season has no next episode`() = runTest(dispatcher) {
        val itemRepo = itemRepo()
        val transcodeRepo = mockk<TranscodeRepository>()
        coEvery { itemRepo.getItem("ep-3") } returns episodeDetail(directPlayFile(), "season-1", 3)
        coEvery { itemRepo.getChildren("season-1") } returns listOf(
            ChildItem(id = "ep-1", title = "E1", type = "episode", index = 1),
            ChildItem(id = "ep-2", title = "E2", type = "episode", index = 2),
            ChildItem(id = "ep-3", title = "E3", type = "episode", index = 3),
        )

        val vm = PlaybackViewModel(itemRepo, transcodeRepo, prefs(), serverPrefs())
        vm.prepare("ep-3", 0L, "http://srv")
        advanceUntilIdle()

        assertThat(vm.uiState.value.nextEpisode).isNull()
    }

    @Test
    fun `non-episode items do not query siblings`() = runTest(dispatcher) {
        val itemRepo = itemRepo()
        val transcodeRepo = mockk<TranscodeRepository>()
        coEvery { itemRepo.getItem("movie-1") } returns movieDetail(directPlayFile())

        val vm = PlaybackViewModel(itemRepo, transcodeRepo, prefs(), serverPrefs())
        vm.prepare("movie-1", 0L, "http://srv")
        advanceUntilIdle()

        coVerify(exactly = 0) { itemRepo.getChildren(any()) }
        assertThat(vm.uiState.value.nextEpisode).isNull()
    }

    @Test
    fun `getChildren failure does not break main playback flow`() = runTest(dispatcher) {
        val itemRepo = itemRepo()
        val transcodeRepo = mockk<TranscodeRepository>()
        coEvery { itemRepo.getItem("ep-1") } returns episodeDetail(directPlayFile(), "season-1", 1)
        coEvery { itemRepo.getChildren("season-1") } throws RuntimeException("offline")

        val vm = PlaybackViewModel(itemRepo, transcodeRepo, prefs(), serverPrefs())
        vm.prepare("ep-1", 0L, "http://srv")
        advanceUntilIdle()

        val state = vm.uiState.value
        assertThat(state.error).isNull()
        assertThat(state.source).isInstanceOf(PlaybackSource.DirectPlay::class.java)
        assertThat(state.nextEpisode).isNull()
    }

    @Test
    fun `stopActiveTranscode is a no-op without an active session`() = runTest(dispatcher) {
        val itemRepo = itemRepo()
        val transcodeRepo = mockk<TranscodeRepository>(relaxed = true)
        val vm = PlaybackViewModel(itemRepo, transcodeRepo, prefs(), serverPrefs())

        vm.stopActiveTranscode()
        advanceUntilIdle()

        coVerify(exactly = 0) { transcodeRepo.stop(any(), any()) }
    }

    @Test
    fun `stopActiveTranscode sends stop request when session is active`() = runTest(dispatcher) {
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

        val vm = PlaybackViewModel(itemRepo, transcodeRepo, prefs(), serverPrefs())
        vm.prepare("movie-1", 0L, "http://srv")
        advanceUntilIdle()

        vm.stopActiveTranscode()
        advanceUntilIdle()

        coVerify(exactly = 1) { transcodeRepo.stop("sess-9", "tok-9") }

        // Calling again should not re-issue the stop.
        vm.stopActiveTranscode()
        advanceUntilIdle()
        coVerify(exactly = 1) { transcodeRepo.stop("sess-9", "tok-9") }
    }
}
