package tv.onscreen.mobile.ui.player

import com.google.common.truth.Truth.assertThat
import io.mockk.coEvery
import io.mockk.coVerify
import io.mockk.every
import io.mockk.mockk
import io.mockk.verify
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.ExperimentalCoroutinesApi
import kotlinx.coroutines.test.StandardTestDispatcher
import kotlinx.coroutines.test.advanceUntilIdle
import kotlinx.coroutines.test.resetMain
import kotlinx.coroutines.test.runCurrent
import kotlinx.coroutines.test.runTest
import kotlinx.coroutines.test.setMain
import org.junit.After
import org.junit.Before
import org.junit.Test
import okhttp3.MediaType.Companion.toMediaTypeOrNull
import okhttp3.ResponseBody.Companion.toResponseBody
import tv.onscreen.mobile.data.downloads.DownloadManifest
import tv.onscreen.mobile.data.downloads.DownloadStore
import tv.onscreen.mobile.data.downloads.OnScreenDownloadManager
import kotlinx.coroutines.flow.MutableStateFlow
import tv.onscreen.mobile.data.model.AudioStream
import tv.onscreen.mobile.data.model.ChildItem
import tv.onscreen.mobile.data.model.ItemDetail
import tv.onscreen.mobile.data.model.ItemFile
import tv.onscreen.mobile.data.model.SubtitleStream
import tv.onscreen.mobile.data.model.TranscodeSession
import tv.onscreen.mobile.data.model.UserPreferences
import tv.onscreen.mobile.data.prefs.ServerPrefs
import tv.onscreen.mobile.playback.StreamTokenVault
import kotlinx.coroutines.flow.emptyFlow
import tv.onscreen.mobile.data.repository.ItemRepository
import tv.onscreen.mobile.data.repository.NotificationsRepository
import tv.onscreen.mobile.data.repository.OnlineSubtitleRepository
import tv.onscreen.mobile.data.repository.PreferencesRepository
import tv.onscreen.mobile.data.repository.ServerCapabilitiesRepository
import tv.onscreen.mobile.data.repository.TranscodeRepository

@OptIn(ExperimentalCoroutinesApi::class)
class PlayerViewModelTest {

    private val dispatcher = StandardTestDispatcher()

    @Before fun setUp() { Dispatchers.setMain(dispatcher) }
    @After  fun tearDown() {
        Dispatchers.resetMain()
        io.mockk.unmockkStatic(android.net.Uri::class)
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
     *  unconditional markers fetch doesn't blow up the test, and an empty
     *  recent-items cache (read when binding to the service's item). */
    private fun itemRepo(): ItemRepository {
        val repo = mockk<ItemRepository>()
        coEvery { repo.getMarkers(any()) } returns emptyList()
        every { repo.cachedItem(any()) } returns null
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
        every { p.serverUrl } returns kotlinx.coroutines.flow.flowOf(url)
        coEvery { p.getAccessToken() } returns null
        return p
    }

    /** Online-subtitle repo with no behavior wired — only the tests
     *  that exercise the search/download path override. */
    private fun stubSubtitles(): OnlineSubtitleRepository = mockk(relaxed = true)

    /** SubtitlePrefs stub — all tests in this file are about playback
     *  decisions, not styling, so we just need a relaxed mock that
     *  swallows the init-time `subtitlePrefs.style.collect` and the
     *  `set*` setters without tracking. */
    private fun subPrefs(): tv.onscreen.mobile.data.prefs.SubtitlePrefs {
        val p = mockk<tv.onscreen.mobile.data.prefs.SubtitlePrefs>(relaxed = true)
        every { p.style } returns kotlinx.coroutines.flow.flowOf(
            tv.onscreen.mobile.data.prefs.SubtitleStyle.DEFAULT,
        )
        return p
    }

    /** Trickplay repo stub — fetch-cues yields null (no thumbnails),
     *  status returns the not_started sentinel. The playback-decision
     *  tests don't exercise the trickplay surface, so we just need to
     *  satisfy the constructor without making the prepare() flow
     *  attempt sprite fetches. */
    private fun stubTrickplay(): tv.onscreen.mobile.data.repository.TrickplayRepository {
        val t = mockk<tv.onscreen.mobile.data.repository.TrickplayRepository>(relaxed = true)
        coEvery { t.status(any()) } returns tv.onscreen.mobile.data.model.TrickplayStatus(status = "not_started")
        coEvery { t.fetchCues(any()) } returns null
        return t
    }

    /** Watch-limit repo that always reports the user is allowed, so the
     *  parental-cap check in prepare() no-ops and playback proceeds. Tests
     *  that exercise the block override this per-test. */
    private fun stubWatchLimit(): tv.onscreen.mobile.data.repository.WatchLimitRepository {
        val w = mockk<tv.onscreen.mobile.data.repository.WatchLimitRepository>()
        coEvery { w.get() } returns tv.onscreen.mobile.data.model.WatchLimitData(
            daily_limit_minutes = null,
            allowed_start_minute = null,
            allowed_end_minute = null,
            used_minutes_today = 0,
            remaining_minutes = null,
            allowed = true,
            reason = null,
        )
        return w
    }

    /** Audiobook repo for the playback-decision tests: a book's speed lookup
     *  (the bind-to-service tests play one) knows nothing, so it plays at 1×.
     *  (PlayerViewModelAudiobookTest covers speed, bookmarks and the chapter
     *  sleep timer.) */
    private fun stubAudiobooks(): tv.onscreen.mobile.data.repository.AudiobookRepository =
        mockk<tv.onscreen.mobile.data.repository.AudiobookRepository>(relaxed = true).also {
            coEvery { it.listeningSpeed(any(), any()) } returns
                tv.onscreen.mobile.data.repository.ListeningSpeed(rate = null, serverSupport = false)
        }

    /** The server's capabilities: by default one that predates
     *  progress_without_duration (or couldn't be asked). */
    private fun stubCapabilities(progressWithoutDuration: Boolean = false): ServerCapabilitiesRepository =
        mockk<ServerCapabilitiesRepository>().also {
            coEvery { it.progressWithoutDuration() } returns progressWithoutDuration
        }

    /** Notifications repo whose SSE stream emits nothing — keeps the
     *  cross-device resume path silent during tests that don't exercise
     *  it. Tests that *do* (the SSE ones below) override per-test. */
    private fun emptyNotifications(): NotificationsRepository {
        val n = mockk<NotificationsRepository>()
        coEvery { n.subscribeProgressUpdates() } returns emptyFlow()
        coEvery { n.subscribePlaybackStops() } returns emptyFlow()
        return n
    }

    /** PlaybackPrefs stub. Defaults match production (download Wi-Fi
     *  only on, warn cellular on). prepare() doesn't read these in
     *  the playback-decision path, so the relaxed mock is enough —
     *  the cellular gate lives screen-side. */
    private fun playbackPrefs(): tv.onscreen.mobile.data.prefs.PlaybackPrefs {
        val p = mockk<tv.onscreen.mobile.data.prefs.PlaybackPrefs>(relaxed = true)
        coEvery { p.getDownloadOnWifiOnly() } returns true
        coEvery { p.getWarnOnCellularStream() } returns true
        return p
    }

    /** Wires a download manager whose store reports no completed download
     *  for any file_id — the offline-first short-circuit in prepare() then
     *  falls through to the normal direct/transcode decision. */
    private fun emptyDownloads(): OnScreenDownloadManager {
        val store = mockk<DownloadStore>(relaxed = true)
        coEvery { store.load() } returns Unit
        coEvery { store.get(any()) } returns null
        // Real StateFlow with an empty manifest — the offline-fallback
        // path reads `store.state.value.entries`, and a relaxed-mock
        // StateFlow returns a default Object that fails the
        // DownloadManifest cast.
        every { store.state } returns MutableStateFlow(DownloadManifest(entries = emptyList()))
        val mgr = mockk<OnScreenDownloadManager>()
        every { mgr.store } returns store
        return mgr
    }

    @Test
    fun `direct play movie produces DirectPlay source with view offset and stream token`() =
        runTest(dispatcher) {
            val itemRepo = itemRepo()
            val transcodeRepo = mockk<TranscodeRepository>().also { repo -> coEvery { repo.decide(any(), any()) } returns null }
            coEvery { itemRepo.getItem("movie-1") } returns
                movieDetail(directPlayFile().copy(stream_token = "st-24h"), viewOffsetMs = 12_000L)

            val vm = PlayerViewModel(itemRepo, transcodeRepo, prefs(), serverPrefs(), subPrefs(), playbackPrefs(), emptyDownloads(), emptyNotifications(), stubSubtitles(), stubTrickplay(), stubWatchLimit(), stubAudiobooks(), stubCapabilities())
            vm.prepare("movie-1")
            advanceUntilIdle()

            val s = vm.state.value
            assertThat(s.error).isNull()
            assertThat(s.loading).isFalse()
            val src = s.source as PlaybackSource.DirectPlay
            // The url is CLEAN — the credential lives in StreamTokenVault and
            // is re-attached by the player's resolving data source. It must
            // not be in the url, because for audio this becomes a MediaSession
            // MediaItem whose uri media3 republishes into the platform
            // session's METADATA_KEY_MEDIA_URI, readable by any
            // notification-listener app.
            assertThat(src.url).isEqualTo("http://srv/media/files/f1.mp4")
            assertThat(src.url).doesNotContain("token=")
            assertThat(StreamTokenVault.tokenForTest(src.url)).isEqualTo("st-24h")
            assertThat(src.startMs).isEqualTo(12_000L)
            assertThat(vm.hlsOffsetMs).isEqualTo(0L)
            assertThat(s.audioStreams).hasSize(1)
            assertThat(s.subtitles).hasSize(1)
        }

    @Test
    fun `fromStart ignores the resume point`() = runTest(dispatcher) {
        // Album / artist Play hands over track 1 "from the start": a partial
        // play left a resume point on it, but the album starts at 0:00.
        val itemRepo = itemRepo()
        val transcodeRepo = mockk<TranscodeRepository>().also { repo -> coEvery { repo.decide(any(), any()) } returns null }
        coEvery { itemRepo.getItem("movie-1") } returns movieDetail(directPlayFile(), viewOffsetMs = 18_000L)

        val vm = PlayerViewModel(itemRepo, transcodeRepo, prefs(), serverPrefs(), subPrefs(), playbackPrefs(), emptyDownloads(), emptyNotifications(), stubSubtitles(), stubTrickplay(), stubWatchLimit(), stubAudiobooks(), stubCapabilities())
        vm.prepare("movie-1", fromStart = true)
        advanceUntilIdle()

        assertThat((vm.state.value.source as PlaybackSource.DirectPlay).startMs).isEqualTo(0L)
    }

    @Test
    fun `direct play falls back to asset token when stream token is absent`() = runTest(dispatcher) {
        val itemRepo = itemRepo()
        val transcodeRepo = mockk<TranscodeRepository>().also { repo -> coEvery { repo.decide(any(), any()) } returns null }
        coEvery { itemRepo.getItem("movie-1") } returns movieDetail(directPlayFile())
        val sp = mockk<ServerPrefs>(relaxed = true)
        coEvery { sp.getServerUrl() } returns "http://srv"
        // No per-file stream token → fall back to the purpose=asset token.
        // The general access token is deliberately NOT used in a query
        // string; the asset-route middleware rejects it there.
        coEvery { sp.getAssetToken() } returns "as-24h"

        val vm = PlayerViewModel(itemRepo, transcodeRepo, prefs(), sp, subPrefs(), playbackPrefs(), emptyDownloads(), emptyNotifications(), stubSubtitles(), stubTrickplay(), stubWatchLimit(), stubAudiobooks(), stubCapabilities())
        vm.prepare("movie-1")
        advanceUntilIdle()

        val src = vm.state.value.source as PlaybackSource.DirectPlay
        // Clean url + vaulted credential — see the stream-token test above.
        assertThat(src.url).isEqualTo("http://srv/media/files/f1.mp4")
        assertThat(src.url).doesNotContain("token=")
        assertThat(StreamTokenVault.tokenForTest(src.url)).isEqualTo("as-24h")
    }

    @Test
    fun `unsupported codec triggers transcode session and produces Hls source`() = runTest(dispatcher) {
        val itemRepo = itemRepo()
        val transcodeRepo = mockk<TranscodeRepository>().also { repo -> coEvery { repo.decide(any(), any()) } returns null }
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
                // Device-dependent since the hint moved to a real
                // MediaCodecList probe — there is no codec list in a JVM
                // unit test, so pinning a value here would assert the
                // absence of a decoder rather than this test's subject
                // (that an unsupported codec produces an Hls source).
                supportsHevc = any(),
            )
        } returns TranscodeSession(
            session_id = "sess-1",
            playlist_url = "/transcode/sess-1.m3u8",
            token = "tok",
        )

        val vm = PlayerViewModel(itemRepo, transcodeRepo, prefs(), serverPrefs(), subPrefs(), playbackPrefs(), emptyDownloads(), emptyNotifications(), stubSubtitles(), stubTrickplay(), stubWatchLimit(), stubAudiobooks(), stubCapabilities())
        vm.prepare("movie-1")
        advanceUntilIdle()

        val src = vm.state.value.source as PlaybackSource.Hls
        assertThat(src.playlistUrl).isEqualTo("http://srv/transcode/sess-1.m3u8")
        assertThat(src.offsetMs).isEqualTo(30_000L)
        assertThat(vm.hlsOffsetMs).isEqualTo(30_000L)
        // No start_offset_sec (an older server): the stream opens at the
        // request, so the player starts at its head.
        assertThat(src.startMs).isEqualTo(0L)
    }

    private fun alacTrack() = ItemDetail(
        id = "track-7",
        library_id = "lib-m",
        title = "Track 7",
        type = "track",
        parent_id = "album-1",
        index = 7,
        files = listOf(
            ItemFile(id = "f7", stream_url = "/media/files/f7.m4a", container = "m4a", audio_codec = "alac"),
        ),
    )

    private fun remuxingTranscodeRepo() = mockk<TranscodeRepository>().also { repo ->
        coEvery { repo.decide(any(), any()) } returns "directStream"
        coEvery {
            repo.start(any(), any(), any(), any(), any(), any(), any())
        } returns TranscodeSession(session_id = "sess-a", playlist_url = "/transcode/sess-a.m3u8", token = "tok")
    }

    @Test
    fun `a track the background service already plays starts no server transcode`() = runTest(dispatcher) {
        // The now-playing screen followed the service's queue onto a track
        // the server would remux: the screen only binds to the service (which
        // plays the file directly), so no ffmpeg session may be started — nor
        // is the decision even asked for. The source is the service's own
        // handle for the item, not a stream url.
        val itemRepo = itemRepo()
        coEvery { itemRepo.getItem("track-7") } returns alacTrack()
        coEvery { itemRepo.getChildren(any()) } returns emptyList()
        val transcodeRepo = remuxingTranscodeRepo()

        val vm = PlayerViewModel(itemRepo, transcodeRepo, prefs(), serverPrefs(), subPrefs(), playbackPrefs(), emptyDownloads(), emptyNotifications(), stubSubtitles(), stubTrickplay(), stubWatchLimit(), stubAudiobooks(), stubCapabilities())
        vm.backgroundItemId = { "track-7" }
        vm.prepare("track-7")
        advanceUntilIdle()

        val src = vm.state.value.source as PlaybackSource.DirectPlay
        assertThat(src.url).isEqualTo(tv.onscreen.mobile.playback.MusicQueue.placeholderUri("track-7"))
        assertThat(vm.state.value.playingInService).isTrue()
        assertThat(vm.state.value.item?.id).isEqualTo("track-7")
        coVerify(exactly = 0) { transcodeRepo.start(any(), any(), any(), any(), any(), any(), any()) }
        coVerify(exactly = 0) { transcodeRepo.decide(any(), any()) }
    }

    @Test
    fun `an album resolving to the track the service plays starts no server transcode`() = runTest(dispatcher) {
        // Play on the album lands on the track already playing in the
        // background: the cold path finds that out only after resolving the
        // leaf, and hands back the direct source instead of a remux.
        val itemRepo = itemRepo()
        coEvery { itemRepo.getItem("album-1") } returns ItemDetail(
            id = "album-1", library_id = "lib-m", title = "Album", type = "album",
        )
        coEvery { itemRepo.getChildren("album-1") } returns listOf(
            ChildItem(id = "track-7", title = "Track 7", type = "track", index = 7),
        )
        coEvery { itemRepo.getItem("track-7") } returns alacTrack()
        val transcodeRepo = remuxingTranscodeRepo()

        val vm = PlayerViewModel(itemRepo, transcodeRepo, prefs(), serverPrefs(), subPrefs(), playbackPrefs(), emptyDownloads(), emptyNotifications(), stubSubtitles(), stubTrickplay(), stubWatchLimit(), stubAudiobooks(), stubCapabilities())
        vm.backgroundItemId = { "track-7" }
        vm.prepare("album-1")
        advanceUntilIdle()

        val src = vm.state.value.source as PlaybackSource.DirectPlay
        assertThat(src.url).isEqualTo("http://srv/media/files/f7.m4a")
        assertThat(vm.state.value.playingInService).isTrue()
        coVerify(exactly = 0) { transcodeRepo.start(any(), any(), any(), any(), any(), any(), any()) }
    }

    // ── Reopening over the item the background service is playing ─────────

    private fun book(id: String, title: String = "Book") = ItemDetail(
        id = id, library_id = "lib-b", title = title, type = "audiobook",
        files = listOf(
            ItemFile(id = "f-$id", stream_url = "/media/files/f-$id.m4b", container = "m4b", audio_codec = "aac"),
        ),
    )

    // ── The audio player's cover ──────────────────────────────────────

    private fun track(id: String, parentId: String?, poster: String? = null) = ItemDetail(
        id = id, library_id = "lib-m", title = "Track $id", type = "track",
        parent_id = parentId, poster_path = poster,
        files = listOf(
            ItemFile(id = "f-$id", stream_url = "/media/files/f-$id.flac", container = "flac", audio_codec = "flac"),
        ),
    )

    private fun album(id: String, poster: String?) = ItemDetail(
        id = id, library_id = "lib-m", title = "Album", type = "album", poster_path = poster,
    )

    /** android.net.Uri is a stub in JVM tests (encode answers null, which
     *  ArtworkUrl can't take): let path segments through as they are. */
    private fun passThroughUriEncode() {
        io.mockk.mockkStatic(android.net.Uri::class)
        every { android.net.Uri.encode(any<String>()) } answers { firstArg() }
    }

    private fun playerFor(itemRepo: ItemRepository) = PlayerViewModel(itemRepo, mockk(relaxed = true), prefs(), serverPrefs(), subPrefs(), playbackPrefs(), emptyDownloads(), emptyNotifications(), stubSubtitles(), stubTrickplay(), stubWatchLimit(), stubAudiobooks(), stubCapabilities())

    @Test
    fun `a track without a cover of its own shows its album's`() = runTest(dispatcher) {
        passThroughUriEncode()
        val itemRepo = itemRepo()
        coEvery { itemRepo.getItem("t") } returns track("t", parentId = "al")
        coEvery { itemRepo.getItem("al") } returns album("al", poster = "Artist/Album/cover.jpg")

        val vm = playerFor(itemRepo)
        vm.backgroundItemId = { "t" }
        vm.prepare("t")
        advanceUntilIdle()

        assertThat(vm.state.value.artworkUrl).isEqualTo("http://srv/artwork/Artist/Album/cover.jpg?w=1080")
        coVerify(exactly = 1) { itemRepo.getItem("al") }
    }

    @Test
    fun `the album page the player came from answers from the cache`() = runTest(dispatcher) {
        passThroughUriEncode()
        val itemRepo = itemRepo()
        coEvery { itemRepo.getItem("t") } returns track("t", parentId = "al")
        every { itemRepo.cachedItem("al") } returns album("al", poster = "Artist/Album/cover.jpg")

        val vm = playerFor(itemRepo)
        vm.backgroundItemId = { "t" }
        vm.prepare("t")
        advanceUntilIdle()

        assertThat(vm.state.value.artworkUrl).isEqualTo("http://srv/artwork/Artist/Album/cover.jpg?w=1080")
        coVerify(exactly = 0) { itemRepo.getItem("al") }
    }

    @Test
    fun `a track with a cover of its own asks nothing of its album`() = runTest(dispatcher) {
        passThroughUriEncode()
        val itemRepo = itemRepo()
        coEvery { itemRepo.getItem("t") } returns track("t", parentId = "al", poster = "Artist/Album/t.jpg")

        val vm = playerFor(itemRepo)
        vm.backgroundItemId = { "t" }
        vm.prepare("t")
        advanceUntilIdle()

        assertThat(vm.state.value.artworkUrl).isEqualTo("http://srv/artwork/Artist/Album/t.jpg?w=1080")
        coVerify(exactly = 0) { itemRepo.getItem("al") }
    }

    @Test
    fun `no cover anywhere leaves the placeholder`() = runTest(dispatcher) {
        val itemRepo = itemRepo()
        coEvery { itemRepo.getItem("t") } returns track("t", parentId = "al")
        coEvery { itemRepo.getItem("al") } returns album("al", poster = null)

        val vm = playerFor(itemRepo)
        vm.backgroundItemId = { "t" }
        vm.prepare("t")
        advanceUntilIdle()

        assertThat(vm.state.value.artworkUrl).isNull()
        assertThat(vm.state.value.error).isNull()
    }

    @Test
    fun `a book without a cover doesn't take its author's portrait`() = runTest(dispatcher) {
        passThroughUriEncode()
        val itemRepo = itemRepo()
        coEvery { itemRepo.getItem("b") } returns book("b").copy(parent_id = "author-1")
        coEvery { itemRepo.getItem("author-1") } returns
            ItemDetail(id = "author-1", library_id = "lib-b", title = "Author", type = "author", poster_path = "Author/portrait.jpg")

        val vm = playerFor(itemRepo)
        vm.backgroundItemId = { "b" }
        vm.prepare("b")
        advanceUntilIdle()

        assertThat(vm.state.value.artworkUrl).isNull()
        coVerify(exactly = 0) { itemRepo.getItem("author-1") }
    }

    @Test
    fun `a podcast episode shows its show's cover`() = runTest(dispatcher) {
        passThroughUriEncode()
        val itemRepo = itemRepo()
        coEvery { itemRepo.getItem("e") } returns track("e", parentId = "show").copy(type = "podcast_episode")
        coEvery { itemRepo.getItem("show") } returns
            ItemDetail(id = "show", library_id = "lib-p", title = "Show", type = "podcast", poster_path = "Show/cover.jpg")

        val vm = playerFor(itemRepo)
        vm.backgroundItemId = { "e" }
        vm.prepare("e")
        advanceUntilIdle()

        assertThat(vm.state.value.artworkUrl).isEqualTo("http://srv/artwork/Show/cover.jpg?w=1080")
    }

    @Test
    fun `the next track's cover shows from its queue entry before the track loads`() = runTest(dispatcher) {
        passThroughUriEncode()
        val itemRepo = itemRepo()
        val fetched = kotlinx.coroutines.CompletableDeferred<ItemDetail>()
        coEvery { itemRepo.getItem("t2") } coAnswers { fetched.await() }
        every { itemRepo.cachedItem("al") } returns album("al", poster = "Artist/Album/cover.jpg")

        val vm = playerFor(itemRepo)
        vm.backgroundItemId = { "t2" }
        vm.prepare("t2")
        runCurrent()
        assertThat(vm.state.value.artworkChecked).isFalse()

        // What the screen reads off the service's queue entry once bound.
        vm.primeArtwork("t2", "track", "al")
        assertThat(vm.state.value.artworkUrl).isEqualTo("http://srv/artwork/Artist/Album/cover.jpg?w=1080")

        fetched.complete(track("t2", parentId = "al"))
        advanceUntilIdle()
        assertThat(vm.state.value.artworkUrl).isEqualTo("http://srv/artwork/Artist/Album/cover.jpg?w=1080")
        assertThat(vm.state.value.artworkChecked).isTrue()
    }

    @Test
    fun `a book's queue entry never primes its author's portrait`() = runTest(dispatcher) {
        val itemRepo = itemRepo()
        coEvery { itemRepo.getItem("b") } coAnswers { kotlinx.coroutines.awaitCancellation() }
        every { itemRepo.cachedItem("author-1") } returns
            ItemDetail(id = "author-1", library_id = "lib-b", title = "Author", type = "author", poster_path = "Author/portrait.jpg")

        val vm = playerFor(itemRepo)
        vm.backgroundItemId = { "b" }
        vm.prepare("b")
        runCurrent()
        vm.primeArtwork("b", "audiobook", "author-1")

        assertThat(vm.state.value.artworkUrl).isNull()
    }

    @Test
    fun `a video asks for no cover`() = runTest(dispatcher) {
        val itemRepo = itemRepo()
        val transcodeRepo = mockk<TranscodeRepository>().also { repo -> coEvery { repo.decide(any(), any()) } returns null }
        coEvery { itemRepo.getItem("ep-1") } returns episodeDetail(directPlayFile(), parentId = "season-1", index = 1)
        coEvery { itemRepo.getChildren(any()) } returns emptyList()

        val vm = PlayerViewModel(itemRepo, transcodeRepo, prefs(), serverPrefs(), subPrefs(), playbackPrefs(), emptyDownloads(), emptyNotifications(), stubSubtitles(), stubTrickplay(), stubWatchLimit(), stubAudiobooks(), stubCapabilities())
        vm.prepare("ep-1")
        advanceUntilIdle()

        assertThat(vm.state.value.artworkUrl).isNull()
        coVerify(exactly = 0) { itemRepo.getItem("season-1") }
    }

    @Test
    fun `reopening over the service's item publishes at once and asks nothing first`() = runTest(dispatcher) {
        val itemRepo = itemRepo()
        val fetched = kotlinx.coroutines.CompletableDeferred<ItemDetail>()
        coEvery { itemRepo.getItem("b") } coAnswers { fetched.await() }
        val transcodeRepo = mockk<TranscodeRepository>(relaxed = true)
        val watchLimit = stubWatchLimit()
        val prefs = prefs()

        val vm = PlayerViewModel(itemRepo, transcodeRepo, prefs, serverPrefs(), subPrefs(), playbackPrefs(), emptyDownloads(), emptyNotifications(), stubSubtitles(), stubTrickplay(), watchLimit, stubAudiobooks(), stubCapabilities())
        vm.backgroundItemId = { "b" }
        vm.prepare("b")

        // Straight away — not after the fetch: the screen binds its
        // controller now instead of spinning.
        val first = vm.state.value
        assertThat(first.loading).isFalse()
        assertThat(first.error).isNull()
        assertThat(first.source).isNotNull()
        assertThat(first.playingInService).isTrue()
        assertThat(first.item).isNull()

        runCurrent()
        fetched.complete(book("b"))
        advanceUntilIdle()

        val loaded = vm.state.value
        assertThat(loaded.item?.id).isEqualTo("b")
        // The very same instance: the controller is keyed on it, and a new
        // one would rebind it.
        assertThat(loaded.source).isSameInstanceAs(first.source)
        assertThat(loaded.loading).isFalse()
        coVerify(exactly = 0) { transcodeRepo.decide(any(), any()) }
        // Asked behind the publish, alongside the item.
        coVerify(exactly = 1) { watchLimit.get() }
        coVerify(exactly = 0) { prefs.get() }
        coVerify(exactly = 0) { itemRepo.getMarkers(any()) }
    }

    @Test
    fun `end of chapter set before the service's item loads still arms its stop`() = runTest(dispatcher) {
        val itemRepo = itemRepo()
        val fetched = kotlinx.coroutines.CompletableDeferred<ItemDetail>()
        coEvery { itemRepo.getItem("b") } coAnswers { fetched.await() }

        val vm = PlayerViewModel(itemRepo, mockk(relaxed = true), prefs(), serverPrefs(), subPrefs(), playbackPrefs(), emptyDownloads(), emptyNotifications(), stubSubtitles(), stubTrickplay(), stubWatchLimit(), stubAudiobooks(), stubCapabilities())
        vm.backgroundItemId = { "b" }
        vm.prepare("b")
        try {
            vm.setSleepTimer(SleepTimer.EndOfChapter)
            // The service must not chain past this chapter, item or no item.
            assertThat(tv.onscreen.mobile.playback.StopAfterItem.isArmedFor("b")).isTrue()

            fetched.complete(book("b"))
            advanceUntilIdle()
            assertThat(tv.onscreen.mobile.playback.StopAfterItem.isArmedFor("b")).isTrue()
            assertThat(vm.sleepTimer.value?.mode).isEqualTo(SleepTimer.EndOfChapter)
        } finally {
            vm.setSleepTimer(SleepTimer.Off)
            tv.onscreen.mobile.playback.StopAfterItem.disarm("b")
        }
    }

    @Test
    fun `a copy of the service's item fetched earlier shows until the fresh one lands`() = runTest(dispatcher) {
        val itemRepo = itemRepo()
        every { itemRepo.cachedItem("b") } returns book("b", title = "Old title")
        coEvery { itemRepo.getItem("b") } returns book("b", title = "New title")

        val vm = PlayerViewModel(itemRepo, mockk(relaxed = true), prefs(), serverPrefs(), subPrefs(), playbackPrefs(), emptyDownloads(), emptyNotifications(), stubSubtitles(), stubTrickplay(), stubWatchLimit(), stubAudiobooks(), stubCapabilities())
        vm.backgroundItemId = { "b" }
        vm.prepare("b")
        val first = vm.state.value
        assertThat(first.item?.title).isEqualTo("Old title")

        advanceUntilIdle()
        assertThat(vm.state.value.item?.title).isEqualTo("New title")
        assertThat(vm.state.value.source).isSameInstanceAs(first.source)
    }

    @Test
    fun `a bookmark in the service's item keeps its position`() = runTest(dispatcher) {
        val itemRepo = itemRepo()
        coEvery { itemRepo.getItem("b") } returns book("b")

        val vm = PlayerViewModel(itemRepo, mockk(relaxed = true), prefs(), serverPrefs(), subPrefs(), playbackPrefs(), emptyDownloads(), emptyNotifications(), stubSubtitles(), stubTrickplay(), stubWatchLimit(), stubAudiobooks(), stubCapabilities())
        vm.backgroundItemId = { "b" }
        vm.prepare("b", startAtMs = 95_000)

        assertThat((vm.state.value.source as PlaybackSource.DirectPlay).startMs).isEqualTo(95_000)
    }

    @Test
    fun `the server refusing the service's item shows the refusal`() = runTest(dispatcher) {
        val itemRepo = itemRepo()
        coEvery { itemRepo.getItem("b") } throws
            httpError(403, """{"error":{"code":"CONTENT_RESTRICTED","message":"rating"}}""")

        val vm = PlayerViewModel(itemRepo, mockk(relaxed = true), prefs(), serverPrefs(), subPrefs(), playbackPrefs(), emptyDownloads(), emptyNotifications(), stubSubtitles(), stubTrickplay(), stubWatchLimit(), stubAudiobooks(), stubCapabilities())
        vm.backgroundItemId = { "b" }
        vm.prepare("b")
        advanceUntilIdle()

        assertThat(vm.state.value.error).isEqualTo("content_restricted")
    }

    @Test
    fun `an exhausted watch limit blocks the service's item on reopening`() = runTest(dispatcher) {
        val itemRepo = itemRepo()
        coEvery { itemRepo.getItem("b") } returns book("b")
        val watchLimit = mockk<tv.onscreen.mobile.data.repository.WatchLimitRepository>()
        coEvery { watchLimit.get() } returns tv.onscreen.mobile.data.model.WatchLimitData(
            daily_limit_minutes = 60,
            allowed_start_minute = null,
            allowed_end_minute = null,
            used_minutes_today = 60,
            remaining_minutes = 0,
            allowed = false,
            reason = "daily_limit_reached",
        )

        val vm = PlayerViewModel(itemRepo, mockk(relaxed = true), prefs(), serverPrefs(), subPrefs(), playbackPrefs(), emptyDownloads(), emptyNotifications(), stubSubtitles(), stubTrickplay(), watchLimit, stubAudiobooks(), stubCapabilities())
        vm.backgroundItemId = { "b" }
        vm.prepare("b")
        assertThat(vm.state.value.error).isNull()
        advanceUntilIdle()

        assertThat(vm.state.value.error).contains("watch-time limit")
    }

    @Test
    fun `a watch limit that can't be read leaves the bound screen as it is`() = runTest(dispatcher) {
        val itemRepo = itemRepo()
        coEvery { itemRepo.getItem("b") } returns book("b")
        val watchLimit = mockk<tv.onscreen.mobile.data.repository.WatchLimitRepository>()
        coEvery { watchLimit.get() } throws java.io.IOException("unreachable")

        val vm = PlayerViewModel(itemRepo, mockk(relaxed = true), prefs(), serverPrefs(), subPrefs(), playbackPrefs(), emptyDownloads(), emptyNotifications(), stubSubtitles(), stubTrickplay(), watchLimit, stubAudiobooks(), stubCapabilities())
        vm.backgroundItemId = { "b" }
        vm.prepare("b")
        advanceUntilIdle()

        assertThat(vm.state.value.error).isNull()
        assertThat(vm.state.value.item?.id).isEqualTo("b")
    }

    @Test
    fun `an unreachable server leaves the bound screen as it is`() = runTest(dispatcher) {
        val itemRepo = itemRepo()
        coEvery { itemRepo.getItem("b") } throws java.io.IOException("unreachable")

        val vm = PlayerViewModel(itemRepo, mockk(relaxed = true), prefs(), serverPrefs(), subPrefs(), playbackPrefs(), emptyDownloads(), emptyNotifications(), stubSubtitles(), stubTrickplay(), stubWatchLimit(), stubAudiobooks(), stubCapabilities())
        vm.backgroundItemId = { "b" }
        vm.prepare("b")
        advanceUntilIdle()

        assertThat(vm.state.value.error).isNull()
        assertThat(vm.state.value.loading).isFalse()
        assertThat(vm.state.value.source).isNotNull()
    }

    @Test
    fun `the cold path asks for the decision alongside the watch limit and markers`() = runTest(dispatcher) {
        // Once the one call that everything needs (the item) is back, the
        // rest go out together rather than one after another.
        val itemRepo = itemRepo()
        coEvery { itemRepo.getItem("movie-1") } returns movieDetail(directPlayFile())
        val decided = kotlinx.coroutines.CompletableDeferred<String?>()
        val transcodeRepo = mockk<TranscodeRepository>().also { repo ->
            coEvery { repo.decide(any(), any()) } coAnswers { decided.await() }
        }
        val watchLimit = stubWatchLimit()
        val prefs = prefs()

        val vm = PlayerViewModel(itemRepo, transcodeRepo, prefs, serverPrefs(), subPrefs(), playbackPrefs(), emptyDownloads(), emptyNotifications(), stubSubtitles(), stubTrickplay(), watchLimit, stubAudiobooks(), stubCapabilities())
        vm.prepare("movie-1")
        runCurrent()

        // The decision is still out, and the other three already went.
        assertThat(vm.state.value.loading).isTrue()
        coVerify(exactly = 1) { transcodeRepo.decide("movie-1", "f1") }
        coVerify(exactly = 1) { watchLimit.get() }
        coVerify(exactly = 1) { prefs.get() }
        coVerify(exactly = 1) { itemRepo.getMarkers("movie-1") }

        decided.complete("directPlay")
        advanceUntilIdle()
        assertThat(vm.state.value.source).isInstanceOf(PlaybackSource.DirectPlay::class.java)
        assertThat(vm.state.value.playingInService).isFalse()
    }

    @Test
    fun `the same track not in the background service still gets its remux`() = runTest(dispatcher) {
        val itemRepo = itemRepo()
        coEvery { itemRepo.getItem("track-7") } returns alacTrack()
        coEvery { itemRepo.getChildren(any()) } returns emptyList()
        val transcodeRepo = remuxingTranscodeRepo()

        val vm = PlayerViewModel(itemRepo, transcodeRepo, prefs(), serverPrefs(), subPrefs(), playbackPrefs(), emptyDownloads(), emptyNotifications(), stubSubtitles(), stubTrickplay(), stubWatchLimit(), stubAudiobooks(), stubCapabilities())
        vm.backgroundItemId = { "some-other-track" }
        vm.prepare("track-7")
        advanceUntilIdle()

        assertThat(vm.state.value.source).isInstanceOf(PlaybackSource.Hls::class.java)
        coVerify(exactly = 1) { transcodeRepo.start(any(), any(), any(), any(), any(), any(), any()) }
    }

    @Test
    fun `missing files surfaces error in ui state`() = runTest(dispatcher) {
        val itemRepo = itemRepo()
        val transcodeRepo = mockk<TranscodeRepository>().also { repo -> coEvery { repo.decide(any(), any()) } returns null }
        coEvery { itemRepo.getItem("movie-1") } returns movieDetail(directPlayFile()).copy(files = emptyList())

        val vm = PlayerViewModel(itemRepo, transcodeRepo, prefs(), serverPrefs(), subPrefs(), playbackPrefs(), emptyDownloads(), emptyNotifications(), stubSubtitles(), stubTrickplay(), stubWatchLimit(), stubAudiobooks(), stubCapabilities())
        vm.prepare("movie-1")
        advanceUntilIdle()

        val s = vm.state.value
        assertThat(s.error).isEqualTo("No playable file")
        assertThat(s.source).isNull()
        assertThat(s.loading).isFalse()
    }

    @Test
    fun `play on a season picks first unwatched after the last watched episode`() = runTest(dispatcher) {
        val itemRepo = itemRepo()
        val transcodeRepo = mockk<TranscodeRepository>().also { repo -> coEvery { repo.decide(any(), any()) } returns null }
        // Season carries no files of its own.
        coEvery { itemRepo.getItem("season-1") } returns ItemDetail(
            id = "season-1", library_id = "lib-1", title = "S1", type = "season",
        )
        // E1 watched, E2 watched, E3 unwatched, E4 unwatched → next-up = E3.
        coEvery { itemRepo.getChildren("season-1") } returns listOf(
            ChildItem(id = "ep-1", title = "E1", type = "episode", index = 1, watched = true),
            ChildItem(id = "ep-2", title = "E2", type = "episode", index = 2, watched = true),
            ChildItem(id = "ep-3", title = "E3", type = "episode", index = 3),
            ChildItem(id = "ep-4", title = "E4", type = "episode", index = 4),
        )
        coEvery { itemRepo.getItem("ep-3") } returns episodeDetail(directPlayFile(), "season-1", 3)

        val vm = PlayerViewModel(itemRepo, transcodeRepo, prefs(), serverPrefs(), subPrefs(), playbackPrefs(), emptyDownloads(), emptyNotifications(), stubSubtitles(), stubTrickplay(), stubWatchLimit(), stubAudiobooks(), stubCapabilities())
        vm.prepare("season-1")
        advanceUntilIdle()

        val s = vm.state.value
        assertThat(s.error).isNull()
        assertThat(s.item?.id).isEqualTo("ep-3")
        assertThat(s.source).isInstanceOf(PlaybackSource.DirectPlay::class.java)
    }

    @Test
    fun `play on a season prefers the in-progress episode over the next unwatched`() =
        runTest(dispatcher) {
            val itemRepo = itemRepo()
            val transcodeRepo = mockk<TranscodeRepository>().also { repo -> coEvery { repo.decide(any(), any()) } returns null }
            coEvery { itemRepo.getItem("season-1") } returns ItemDetail(
                id = "season-1", library_id = "lib-1", title = "S1", type = "season",
            )
            // E1 watched, E2 in-progress, E3 unwatched. The "next after
            // the last watched" rule would point at E3, but a partially-
            // played episode should win — the user wants to resume what
            // they actually paused, not jump past it.
            coEvery { itemRepo.getChildren("season-1") } returns listOf(
                ChildItem(id = "ep-1", title = "E1", type = "episode", index = 1, watched = true),
                ChildItem(
                    id = "ep-2", title = "E2", type = "episode", index = 2,
                    view_offset_ms = 5 * 60_000L,
                ),
                ChildItem(id = "ep-3", title = "E3", type = "episode", index = 3),
            )
            coEvery { itemRepo.getItem("ep-2") } returns episodeDetail(directPlayFile(), "season-1", 2)

            val vm = PlayerViewModel(itemRepo, transcodeRepo, prefs(), serverPrefs(), subPrefs(), playbackPrefs(), emptyDownloads(), emptyNotifications(), stubSubtitles(), stubTrickplay(), stubWatchLimit(), stubAudiobooks(), stubCapabilities())
            vm.prepare("season-1")
            advanceUntilIdle()

            assertThat(vm.state.value.item?.id).isEqualTo("ep-2")
        }

    @Test
    fun `play on a show flattens through seasons to a leaf episode`() = runTest(dispatcher) {
        val itemRepo = itemRepo()
        val transcodeRepo = mockk<TranscodeRepository>().also { repo -> coEvery { repo.decide(any(), any()) } returns null }
        // Show → season → episode. Children of the show are seasons, so
        // the resolver has to recurse one level deeper before it can
        // pick a leaf.
        coEvery { itemRepo.getItem("show-1") } returns ItemDetail(
            id = "show-1", library_id = "lib-1", title = "Show", type = "show",
        )
        coEvery { itemRepo.getChildren("show-1") } returns listOf(
            ChildItem(id = "season-1", title = "S1", type = "season", index = 1),
            ChildItem(id = "season-2", title = "S2", type = "season", index = 2),
        )
        coEvery { itemRepo.getChildren("season-1") } returns listOf(
            ChildItem(id = "ep-1", title = "E1", type = "episode", index = 1, watched = true),
            ChildItem(id = "ep-2", title = "E2", type = "episode", index = 2, watched = true),
        )
        coEvery { itemRepo.getChildren("season-2") } returns listOf(
            ChildItem(id = "ep-3", title = "E3", type = "episode", index = 1),
            ChildItem(id = "ep-4", title = "E4", type = "episode", index = 2),
        )
        coEvery { itemRepo.getItem("ep-3") } returns episodeDetail(directPlayFile(), "season-2", 3)

        val vm = PlayerViewModel(itemRepo, transcodeRepo, prefs(), serverPrefs(), subPrefs(), playbackPrefs(), emptyDownloads(), emptyNotifications(), stubSubtitles(), stubTrickplay(), stubWatchLimit(), stubAudiobooks(), stubCapabilities())
        vm.prepare("show-1")
        advanceUntilIdle()

        val s = vm.state.value
        assertThat(s.error).isNull()
        assertThat(s.item?.id).isEqualTo("ep-3")
    }

    @Test
    fun `play on a fully-watched show falls back to the very first leaf for replay`() =
        runTest(dispatcher) {
            val itemRepo = itemRepo()
            val transcodeRepo = mockk<TranscodeRepository>().also { repo -> coEvery { repo.decide(any(), any()) } returns null }
            coEvery { itemRepo.getItem("season-1") } returns ItemDetail(
                id = "season-1", library_id = "lib-1", title = "S1", type = "season",
            )
            coEvery { itemRepo.getChildren("season-1") } returns listOf(
                ChildItem(id = "ep-1", title = "E1", type = "episode", index = 1, watched = true),
                ChildItem(id = "ep-2", title = "E2", type = "episode", index = 2, watched = true),
            )
            coEvery { itemRepo.getItem("ep-1") } returns episodeDetail(directPlayFile(), "season-1", 1)

            val vm = PlayerViewModel(itemRepo, transcodeRepo, prefs(), serverPrefs(), subPrefs(), playbackPrefs(), emptyDownloads(), emptyNotifications(), stubSubtitles(), stubTrickplay(), stubWatchLimit(), stubAudiobooks(), stubCapabilities())
            vm.prepare("season-1")
            advanceUntilIdle()

            assertThat(vm.state.value.item?.id).isEqualTo("ep-1")
        }

    @Test
    fun `getItem failure surfaces error message`() = runTest(dispatcher) {
        val itemRepo = itemRepo()
        val transcodeRepo = mockk<TranscodeRepository>().also { repo -> coEvery { repo.decide(any(), any()) } returns null }
        coEvery { itemRepo.getItem(any()) } throws RuntimeException("api 500")

        val vm = PlayerViewModel(itemRepo, transcodeRepo, prefs(), serverPrefs(), subPrefs(), playbackPrefs(), emptyDownloads(), emptyNotifications(), stubSubtitles(), stubTrickplay(), stubWatchLimit(), stubAudiobooks(), stubCapabilities())
        vm.prepare("movie-1")
        advanceUntilIdle()

        assertThat(vm.state.value.error).isEqualTo("api 500")
    }

    @Test
    fun `episode load resolves next sibling by index plus one`() = runTest(dispatcher) {
        val itemRepo = itemRepo()
        val transcodeRepo = mockk<TranscodeRepository>().also { repo -> coEvery { repo.decide(any(), any()) } returns null }
        coEvery { itemRepo.getItem("ep-1") } returns episodeDetail(directPlayFile(), "season-1", 1)
        coEvery { itemRepo.getChildren("season-1") } returns listOf(
            ChildItem(id = "ep-1", title = "E1", type = "episode", index = 1),
            ChildItem(id = "ep-2", title = "E2", type = "episode", index = 2),
        )

        val vm = PlayerViewModel(itemRepo, transcodeRepo, prefs(), serverPrefs(), subPrefs(), playbackPrefs(), emptyDownloads(), emptyNotifications(), stubSubtitles(), stubTrickplay(), stubWatchLimit(), stubAudiobooks(), stubCapabilities())
        vm.prepare("ep-1")
        advanceUntilIdle()

        assertThat(vm.state.value.nextSibling?.id).isEqualTo("ep-2")
    }

    @Test
    fun `non-episode items do not query siblings`() = runTest(dispatcher) {
        val itemRepo = itemRepo()
        val transcodeRepo = mockk<TranscodeRepository>().also { repo -> coEvery { repo.decide(any(), any()) } returns null }
        coEvery { itemRepo.getItem("movie-1") } returns movieDetail(directPlayFile())

        val vm = PlayerViewModel(itemRepo, transcodeRepo, prefs(), serverPrefs(), subPrefs(), playbackPrefs(), emptyDownloads(), emptyNotifications(), stubSubtitles(), stubTrickplay(), stubWatchLimit(), stubAudiobooks(), stubCapabilities())
        vm.prepare("movie-1")
        advanceUntilIdle()

        coVerify(exactly = 0) { itemRepo.getChildren(any()) }
        assertThat(vm.state.value.nextSibling).isNull()
    }

    @Test
    fun `getChildren failure does not break main playback flow`() = runTest(dispatcher) {
        val itemRepo = itemRepo()
        val transcodeRepo = mockk<TranscodeRepository>().also { repo -> coEvery { repo.decide(any(), any()) } returns null }
        coEvery { itemRepo.getItem("ep-1") } returns episodeDetail(directPlayFile(), "season-1", 1)
        coEvery { itemRepo.getChildren("season-1") } throws RuntimeException("offline")

        val vm = PlayerViewModel(itemRepo, transcodeRepo, prefs(), serverPrefs(), subPrefs(), playbackPrefs(), emptyDownloads(), emptyNotifications(), stubSubtitles(), stubTrickplay(), stubWatchLimit(), stubAudiobooks(), stubCapabilities())
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
        val vm = PlayerViewModel(itemRepo, transcodeRepo, prefs(), serverPrefs(), subPrefs(), playbackPrefs(), emptyDownloads(), emptyNotifications(), stubSubtitles(), stubTrickplay(), stubWatchLimit(), stubAudiobooks(), stubCapabilities())

        vm.stopActiveTranscode()
        advanceUntilIdle()

        io.mockk.verify(exactly = 0) { transcodeRepo.stopDetached(any(), any()) }
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

        val vm = PlayerViewModel(itemRepo, transcodeRepo, prefs(), serverPrefs(), subPrefs(), playbackPrefs(), emptyDownloads(), emptyNotifications(), stubSubtitles(), stubTrickplay(), stubWatchLimit(), stubAudiobooks(), stubCapabilities())
        vm.prepare("movie-1")
        advanceUntilIdle()

        vm.stopActiveTranscode()
        advanceUntilIdle()

        // stopDetached, not stop: the dominant caller is onCleared(), where
        // viewModelScope is already closed — a launch there never runs, so
        // the DELETE has to ride the repository's app-lifetime scope.
        io.mockk.verify(exactly = 1) { transcodeRepo.stopDetached("sess-9", "tok-9") }

        // Calling again should not re-issue the stop — IDs are cleared on
        // the first call.
        vm.stopActiveTranscode()
        advanceUntilIdle()
        io.mockk.verify(exactly = 1) { transcodeRepo.stopDetached("sess-9", "tok-9") }
    }

    @Test
    fun `reportProgress with an unknown duration still beats, leaving the duration out`() = runTest(dispatcher) {
        // Skipping it stopped a parental watch limit from counting the time;
        // this server keeps the duration it knows when the field is absent.
        val itemRepo = itemRepo()
        val transcodeRepo = mockk<TranscodeRepository>().also { repo -> coEvery { repo.decide(any(), any()) } returns null }
        coEvery { itemRepo.getItem("movie-1") } returns movieDetail(directPlayFile())
        coEvery { itemRepo.updateProgress(any(), any(), any(), any(), any()) } returns Unit
        val vm = PlayerViewModel(itemRepo, transcodeRepo, prefs(), serverPrefs(), subPrefs(), playbackPrefs(), emptyDownloads(), emptyNotifications(), stubSubtitles(), stubTrickplay(), stubWatchLimit(), stubAudiobooks(), stubCapabilities(progressWithoutDuration = true))
        vm.prepare("movie-1")
        advanceUntilIdle()

        vm.reportProgress("movie-1", 1_000L, ContentDuration.UNKNOWN, "playing")
        advanceUntilIdle()

        coVerify(exactly = 1) { itemRepo.updateProgress("movie-1", 1_000L, null, "playing", any()) }
    }

    @Test
    fun `an older server gets no report with an unknown duration`() = runTest(dispatcher) {
        // It stores each report's duration as sent: the missing one would
        // replace the one it has, and the item read "unwatched" and left
        // Continue Watching. The pre-flag behaviour — skip — stands there.
        val itemRepo = itemRepo()
        val transcodeRepo = mockk<TranscodeRepository>().also { repo -> coEvery { repo.decide(any(), any()) } returns null }
        coEvery { itemRepo.getItem("movie-1") } returns movieDetail(directPlayFile())
        coEvery { itemRepo.updateProgress(any(), any(), any(), any(), any()) } returns Unit
        every { itemRepo.reportProgressDetached(any(), any(), any(), any(), any()) } returns Unit
        val vm = PlayerViewModel(itemRepo, transcodeRepo, prefs(), serverPrefs(), subPrefs(), playbackPrefs(), emptyDownloads(), emptyNotifications(), stubSubtitles(), stubTrickplay(), stubWatchLimit(), stubAudiobooks(), stubCapabilities(progressWithoutDuration = false))
        vm.prepare("movie-1")
        advanceUntilIdle()

        vm.reportProgress("movie-1", 1_000L, ContentDuration.UNKNOWN, "playing")
        vm.reportProgressFinal("movie-1", 1_000L, ContentDuration.UNKNOWN)
        // A known duration still goes out as ever.
        vm.reportProgress("movie-1", 2_000L, 90_000L, "playing")
        advanceUntilIdle()

        coVerify(exactly = 0) { itemRepo.updateProgress(any(), any(), null, any(), any()) }
        verify(exactly = 0) { itemRepo.reportProgressDetached(any(), any(), any(), any(), any()) }
        coVerify(exactly = 1) { itemRepo.updateProgress("movie-1", 2_000L, 90_000L, "playing", any()) }
    }

    @Test
    fun `a capability lookup that fails, or hasn't answered, sends no report without a duration`() = runTest(dispatcher) {
        val itemRepo = itemRepo()
        val transcodeRepo = mockk<TranscodeRepository>().also { repo -> coEvery { repo.decide(any(), any()) } returns null }
        coEvery { itemRepo.getItem("movie-1") } returns movieDetail(directPlayFile())
        coEvery { itemRepo.updateProgress(any(), any(), any(), any(), any()) } returns Unit
        val caps = mockk<ServerCapabilitiesRepository>()
        coEvery { caps.progressWithoutDuration() } throws java.io.IOException("unreachable")
        val vm = PlayerViewModel(itemRepo, transcodeRepo, prefs(), serverPrefs(), subPrefs(), playbackPrefs(), emptyDownloads(), emptyNotifications(), stubSubtitles(), stubTrickplay(), stubWatchLimit(), stubAudiobooks(), caps)

        // Before prepare has asked.
        vm.reportProgress("movie-1", 1_000L, ContentDuration.UNKNOWN, "playing")
        vm.prepare("movie-1")
        advanceUntilIdle()
        vm.reportProgress("movie-1", 2_000L, ContentDuration.UNKNOWN, "playing")
        advanceUntilIdle()

        assertThat(vm.state.value.error).isNull()
        coVerify(exactly = 0) { itemRepo.updateProgress(any(), any(), any(), any(), any()) }
    }

    @Test
    fun `remote progress for the active item flows into remoteResumeMs`() = runTest(dispatcher) {
        val itemRepo = itemRepo()
        val transcodeRepo = mockk<TranscodeRepository>().also { repo -> coEvery { repo.decide(any(), any()) } returns null }
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

        val vm = PlayerViewModel(itemRepo, transcodeRepo, prefs(), serverPrefs(), subPrefs(), playbackPrefs(), emptyDownloads(), notif, stubSubtitles(), stubTrickplay(), stubWatchLimit(), stubAudiobooks(), stubCapabilities())
        vm.prepare("movie-1")
        advanceUntilIdle()

        assertThat(vm.remoteResumeMs.value).isEqualTo(60_000L)
    }

    @Test
    fun `remote progress for a different item is ignored`() = runTest(dispatcher) {
        val itemRepo = itemRepo()
        val transcodeRepo = mockk<TranscodeRepository>().also { repo -> coEvery { repo.decide(any(), any()) } returns null }
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

        val vm = PlayerViewModel(itemRepo, transcodeRepo, prefs(), serverPrefs(), subPrefs(), playbackPrefs(), emptyDownloads(), notif, stubSubtitles(), stubTrickplay(), stubWatchLimit(), stubAudiobooks(), stubCapabilities())
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
        val transcodeRepo = mockk<TranscodeRepository>().also { repo -> coEvery { repo.decide(any(), any()) } returns null }
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

        val vm = PlayerViewModel(itemRepo, transcodeRepo, prefs(), serverPrefs(), subPrefs(), playbackPrefs(), emptyDownloads(), notif, stubSubtitles(), stubTrickplay(), stubWatchLimit(), stubAudiobooks(), stubCapabilities())
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
        val transcodeRepo = mockk<TranscodeRepository>().also { repo -> coEvery { repo.decide(any(), any()) } returns null }
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

        val vm = PlayerViewModel(itemRepo, transcodeRepo, prefs(), serverPrefs(), subPrefs(), playbackPrefs(), emptyDownloads(), notif, stubSubtitles(), stubTrickplay(), stubWatchLimit(), stubAudiobooks(), stubCapabilities())
        vm.prepare("movie-1")
        advanceUntilIdle()
        assertThat(vm.remoteResumeMs.value).isEqualTo(90_000L)

        vm.clearRemoteResume()
        assertThat(vm.remoteResumeMs.value).isNull()
    }

    @Test
    fun `reportProgress forwards a positive-duration call to the repo`() = runTest(dispatcher) {
        val itemRepo = itemRepo()
        val transcodeRepo = mockk<TranscodeRepository>().also { repo -> coEvery { repo.decide(any(), any()) } returns null }
        coEvery { itemRepo.updateProgress(any(), any(), any(), any()) } returns Unit
        val vm = PlayerViewModel(itemRepo, transcodeRepo, prefs(), serverPrefs(), subPrefs(), playbackPrefs(), emptyDownloads(), emptyNotifications(), stubSubtitles(), stubTrickplay(), stubWatchLimit(), stubAudiobooks(), stubCapabilities())

        vm.reportProgress("movie-1", 5_000L, 90_000L, "playing")
        advanceUntilIdle()

        coVerify(exactly = 1) { itemRepo.updateProgress("movie-1", 5_000L, 90_000L, "playing") }
    }

    @Test
    fun `reportProgressFinal publishes a detached stopped event`() = runTest(dispatcher) {
        // The terminal stop must ride the repository's app-lifetime
        // scope, not viewModelScope — an auto-advancing track clears
        // this VM the instant we report, and a viewModelScope publish
        // would be cancelled before the PUT lands, so the completed
        // track would never scrobble.
        val itemRepo = itemRepo()
        val transcodeRepo = mockk<TranscodeRepository>().also { repo -> coEvery { repo.decide(any(), any()) } returns null }
        every { itemRepo.reportProgressDetached(any(), any(), any(), any()) } returns Unit
        val vm = PlayerViewModel(itemRepo, transcodeRepo, prefs(), serverPrefs(), subPrefs(), playbackPrefs(), emptyDownloads(), emptyNotifications(), stubSubtitles(), stubTrickplay(), stubWatchLimit(), stubAudiobooks(), stubCapabilities())

        vm.reportProgressFinal("track-7", 180_000L, 180_000L)
        advanceUntilIdle()

        verify(exactly = 1) { itemRepo.reportProgressDetached("track-7", 180_000L, 180_000L, "stopped") }
        coVerify(exactly = 0) { itemRepo.updateProgress(any(), any(), any(), any()) }
    }

    @Test
    fun `reportProgressFinal with an unknown duration still stops, leaving the duration out`() = runTest(dispatcher) {
        val itemRepo = itemRepo()
        val transcodeRepo = mockk<TranscodeRepository>().also { repo -> coEvery { repo.decide(any(), any()) } returns null }
        coEvery { itemRepo.getItem("track-7") } returns movieDetail(directPlayFile()).copy(id = "track-7")
        every { itemRepo.reportProgressDetached(any(), any(), any(), any(), any()) } returns Unit
        val vm = PlayerViewModel(itemRepo, transcodeRepo, prefs(), serverPrefs(), subPrefs(), playbackPrefs(), emptyDownloads(), emptyNotifications(), stubSubtitles(), stubTrickplay(), stubWatchLimit(), stubAudiobooks(), stubCapabilities(progressWithoutDuration = true))
        vm.prepare("track-7")
        advanceUntilIdle()

        vm.reportProgressFinal("track-7", 1_000L, ContentDuration.UNKNOWN)
        advanceUntilIdle()

        verify(exactly = 1) { itemRepo.reportProgressDetached("track-7", 1_000L, null, "stopped", any()) }
    }

    @Test
    fun `searchOnlineSubtitles populates the dialog state`() = runTest(dispatcher) {
        val itemRepo = itemRepo()
        val transcodeRepo = mockk<TranscodeRepository>().also { repo -> coEvery { repo.decide(any(), any()) } returns null }
        val subs = mockk<OnlineSubtitleRepository>()
        coEvery { subs.search("movie-1", "en", null) } returns listOf(
            tv.onscreen.mobile.data.model.OnlineSubtitle(
                provider_file_id = 42, file_name = "Movie.srt", language = "en",
            ),
        )
        val vm = PlayerViewModel(itemRepo, transcodeRepo, prefs(), serverPrefs(), subPrefs(), playbackPrefs(), emptyDownloads(), emptyNotifications(), subs, stubTrickplay(), stubWatchLimit(), stubAudiobooks(), stubCapabilities())

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
        val transcodeRepo = mockk<TranscodeRepository>().also { repo -> coEvery { repo.decide(any(), any()) } returns null }
        val subs = mockk<OnlineSubtitleRepository>()
        coEvery { subs.search(any(), any(), any()) } throws RuntimeException("rate limited")
        val vm = PlayerViewModel(itemRepo, transcodeRepo, prefs(), serverPrefs(), subPrefs(), playbackPrefs(), emptyDownloads(), emptyNotifications(), subs, stubTrickplay(), stubWatchLimit(), stubAudiobooks(), stubCapabilities())

        vm.searchOnlineSubtitles("movie-1", "en", null)
        advanceUntilIdle()

        assertThat(vm.onlineSubtitleSearch.value.error).isEqualTo("rate limited")
    }

    @Test
    fun `downloadOnlineSubtitle attaches subtitle to the active file id`() = runTest(dispatcher) {
        val itemRepo = itemRepo()
        val transcodeRepo = mockk<TranscodeRepository>().also { repo -> coEvery { repo.decide(any(), any()) } returns null }
        coEvery { itemRepo.getItem("movie-1") } returns movieDetail(directPlayFile())
        val subs = mockk<OnlineSubtitleRepository>()
        coEvery { subs.download(any(), any(), any()) } returns Unit
        val vm = PlayerViewModel(itemRepo, transcodeRepo, prefs(), serverPrefs(), subPrefs(), playbackPrefs(), emptyDownloads(), emptyNotifications(), subs, stubTrickplay(), stubWatchLimit(), stubAudiobooks(), stubCapabilities())
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

    // ── Offline fallback must not override a server refusal ─────────────────

    private fun httpError(code: Int, body: String = ""): retrofit2.HttpException =
        retrofit2.HttpException(
            retrofit2.Response.error<Any>(
                code,
                body.toResponseBody("application/json".toMediaTypeOrNull()),
            ),
        )

    /** Download manager with ONE completed local copy of movie-1 on disk. */
    private fun downloadsWithMovie(): OnScreenDownloadManager {
        val file = java.io.File.createTempFile("onscreen-dl", ".mp4").apply {
            writeBytes(ByteArray(64) { 1 })
            deleteOnExit()
        }
        val entry = tv.onscreen.mobile.data.downloads.DownloadEntry(
            file_id = "00000000-0000-0000-0000-0000000000f1",
            item_id = "movie-1",
            item_title = "Test Movie",
            item_type = "movie",
            container = "mp4",
            size_bytes = 64,
            downloaded_bytes = 64,
            status = "completed",
        )
        val store = mockk<DownloadStore>(relaxed = true)
        coEvery { store.load() } returns Unit
        every { store.state } returns MutableStateFlow(DownloadManifest(entries = listOf(entry)))
        every { store.fileFor(any()) } returns file
        val mgr = mockk<OnScreenDownloadManager>()
        every { mgr.store } returns store
        return mgr
    }

    @Test
    fun `a content-rating 403 does not fall back to the downloaded copy`() = runTest(dispatcher) {
        val itemRepo = itemRepo()
        val transcodeRepo = mockk<TranscodeRepository>(relaxed = true)
        coEvery { itemRepo.getItem("movie-1") } throws httpError(403, """{"error":{"code":"CONTENT_RESTRICTED","message":"rating"}}""")

        val vm = PlayerViewModel(itemRepo, transcodeRepo, prefs(), serverPrefs(), subPrefs(), playbackPrefs(), downloadsWithMovie(), emptyNotifications(), stubSubtitles(), stubTrickplay(), stubWatchLimit(), stubAudiobooks(), stubCapabilities())
        vm.prepare("movie-1")
        advanceUntilIdle()

        assertThat(vm.state.value.source).isNull()
        assertThat(vm.state.value.error).isEqualTo("content_restricted")
    }

    @Test
    fun `a 404 after access is revoked does not fall back to the downloaded copy`() = runTest(dispatcher) {
        val itemRepo = itemRepo()
        val transcodeRepo = mockk<TranscodeRepository>(relaxed = true)
        coEvery { itemRepo.getItem("movie-1") } throws httpError(404)

        val vm = PlayerViewModel(itemRepo, transcodeRepo, prefs(), serverPrefs(), subPrefs(), playbackPrefs(), downloadsWithMovie(), emptyNotifications(), stubSubtitles(), stubTrickplay(), stubWatchLimit(), stubAudiobooks(), stubCapabilities())
        vm.prepare("movie-1")
        advanceUntilIdle()

        assertThat(vm.state.value.source).isNull()
        assertThat(vm.state.value.error).isNotNull()
    }

    @Test
    fun `a transport failure still plays the downloaded copy offline`() = runTest(dispatcher) {
        val itemRepo = itemRepo()
        val transcodeRepo = mockk<TranscodeRepository>(relaxed = true)
        coEvery { itemRepo.getItem("movie-1") } throws java.io.IOException("unreachable")

        val vm = PlayerViewModel(itemRepo, transcodeRepo, prefs(), serverPrefs(), subPrefs(), playbackPrefs(), downloadsWithMovie(), emptyNotifications(), stubSubtitles(), stubTrickplay(), stubWatchLimit(), stubAudiobooks(), stubCapabilities())
        vm.prepare("movie-1")
        advanceUntilIdle()

        val src = vm.state.value.source as PlaybackSource.DirectPlay
        assertThat(src.url).startsWith("file://")
        assertThat(vm.state.value.error).isNull()
    }

    @Test
    fun `any 403 on a playing heartbeat stops playback`() = runTest(dispatcher) {
        val itemRepo = itemRepo()
        val transcodeRepo = mockk<TranscodeRepository>(relaxed = true)
        coEvery { itemRepo.updateProgress(any(), any(), any(), any(), any()) } throws
            httpError(403, """{"error":{"code":"FORBIDDEN","message":"no access"}}""")

        val vm = PlayerViewModel(itemRepo, transcodeRepo, prefs(), serverPrefs(), subPrefs(), playbackPrefs(), emptyDownloads(), emptyNotifications(), stubSubtitles(), stubTrickplay(), stubWatchLimit(), stubAudiobooks(), stubCapabilities())
        vm.reportProgress("movie-1", 1_000L, 10_000L, "playing")
        advanceUntilIdle()

        assertThat(vm.state.value.error).isEqualTo("content_restricted")
    }

    // ── Content duration (the resumed-HLS "watched after 4 minutes" bug) ──

    /** A movie the server remuxes, resumed an hour in. The API omitted the
     *  item's duration; its file has one — the device case. */
    private fun resumedRemuxMovie(itemDurationMs: Long?, fileDurationMs: Long?) = ItemDetail(
        id = "movie-1",
        library_id = "lib-1",
        title = "Test Movie",
        type = "movie",
        duration_ms = itemDurationMs,
        view_offset_ms = 3_600_000L,
        files = listOf(transcodeFile().copy(duration_ms = fileDurationMs)),
    )

    /** [startOffsetSec] null: a server that predates the field. */
    private fun remuxRepo(startOffsetSec: Double? = null) = mockk<TranscodeRepository>(relaxed = true).also { repo ->
        coEvery { repo.decide(any(), any()) } returns "directStream"
        coEvery { repo.start(any(), any(), any(), any(), any(), any(), any()) } returns
            TranscodeSession(
                session_id = "sess-r",
                playlist_url = "/transcode/sess-r.m3u8",
                token = "tok",
                start_offset_sec = startOffsetSec,
            )
    }

    @Test
    fun `content duration falls back to the file's when the item has none, never the HLS window`() = runTest(dispatcher) {
        val itemRepo = itemRepo()
        coEvery { itemRepo.getItem("movie-1") } returns resumedRemuxMovie(itemDurationMs = null, fileDurationMs = 7_200_000L)
        val vm = PlayerViewModel(itemRepo, remuxRepo(), prefs(), serverPrefs(), subPrefs(), playbackPrefs(), emptyDownloads(), emptyNotifications(), stubSubtitles(), stubTrickplay(), stubWatchLimit(), stubAudiobooks(), stubCapabilities())
        vm.prepare("movie-1")
        advanceUntilIdle()
        assertThat(vm.state.value.source).isInstanceOf(PlaybackSource.Hls::class.java)

        // The session's growing EVENT playlist reports ~4 minutes. Position
        // 1 h + 4 min against THAT read as far past 90% — watched, resume point
        // cleared. Against the file's 2 h it is ~53%.
        assertThat(vm.contentDurationMs(playerDurationMs = 240_000L, playerDurationTrusted = false))
            .isEqualTo(7_200_000L)
    }

    @Test
    fun `on HLS the file's duration wins over the item's listed runtime`() = runTest(dispatcher) {
        // The position reported is time in the file; the item's is a TMDB
        // runtime in whole minutes, here 3 min short of the file.
        val itemRepo = itemRepo()
        coEvery { itemRepo.getItem("movie-1") } returns resumedRemuxMovie(itemDurationMs = 7_020_000L, fileDurationMs = 7_200_000L)
        val vm = PlayerViewModel(itemRepo, remuxRepo(), prefs(), serverPrefs(), subPrefs(), playbackPrefs(), emptyDownloads(), emptyNotifications(), stubSubtitles(), stubTrickplay(), stubWatchLimit(), stubAudiobooks(), stubCapabilities())
        vm.prepare("movie-1")
        advanceUntilIdle()

        assertThat(vm.contentDurationMs(playerDurationMs = 240_000L, playerDurationTrusted = false))
            .isEqualTo(7_200_000L)
    }

    @Test
    fun `on direct play the settled player's duration wins`() = runTest(dispatcher) {
        val itemRepo = itemRepo()
        val transcodeRepo = mockk<TranscodeRepository>().also { repo -> coEvery { repo.decide(any(), any()) } returns null }
        coEvery { itemRepo.getItem("movie-1") } returns
            movieDetail(directPlayFile().copy(duration_ms = 7_200_000L)).copy(duration_ms = 7_020_000L)
        val vm = PlayerViewModel(itemRepo, transcodeRepo, prefs(), serverPrefs(), subPrefs(), playbackPrefs(), emptyDownloads(), emptyNotifications(), stubSubtitles(), stubTrickplay(), stubWatchLimit(), stubAudiobooks(), stubCapabilities())
        vm.prepare("movie-1")
        advanceUntilIdle()

        assertThat(vm.contentDurationMs(playerDurationMs = 7_201_000L, playerDurationTrusted = true))
            .isEqualTo(7_201_000L)
        // Not prepared yet (C.TIME_UNSET): the file's, not the runtime.
        assertThat(vm.contentDurationMs(playerDurationMs = Long.MIN_VALUE + 1, playerDurationTrusted = true))
            .isEqualTo(7_200_000L)
    }

    @Test
    fun `with no duration anywhere an HLS session reports without one rather than a wrong ratio`() = runTest(dispatcher) {
        val itemRepo = itemRepo()
        coEvery { itemRepo.getItem("movie-1") } returns resumedRemuxMovie(itemDurationMs = null, fileDurationMs = null)
        coEvery { itemRepo.updateProgress(any(), any(), any(), any(), any()) } returns Unit
        every { itemRepo.reportProgressDetached(any(), any(), any(), any(), any()) } returns Unit
        val vm = PlayerViewModel(itemRepo, remuxRepo(), prefs(), serverPrefs(), subPrefs(), playbackPrefs(), emptyDownloads(), emptyNotifications(), stubSubtitles(), stubTrickplay(), stubWatchLimit(), stubAudiobooks(), stubCapabilities(progressWithoutDuration = true))
        vm.prepare("movie-1")
        advanceUntilIdle()

        val dur = vm.contentDurationMs(playerDurationMs = 240_000L, playerDurationTrusted = false)
        assertThat(dur).isEqualTo(ContentDuration.UNKNOWN)
        // The screen passes that straight through, and the duration is left
        // out — never the ~4 min HLS window, which read as past 90%. This
        // server keeps the duration it knows when the field is absent.
        vm.reportProgress("movie-1", 3_840_000L, dur, "playing")
        vm.reportProgressFinal("movie-1", 3_840_000L, dur)
        advanceUntilIdle()
        coVerify(exactly = 1) { itemRepo.updateProgress("movie-1", 3_840_000L, null, "playing", "directStream") }
        verify(exactly = 1) { itemRepo.reportProgressDetached("movie-1", 3_840_000L, null, "stopped", "directStream") }
    }

    @Test
    fun `a direct-play file with no known duration still reports against the player's`() = runTest(dispatcher) {
        val itemRepo = itemRepo()
        val transcodeRepo = mockk<TranscodeRepository>().also { repo -> coEvery { repo.decide(any(), any()) } returns null }
        coEvery { itemRepo.getItem("movie-1") } returns movieDetail(directPlayFile())
        val vm = PlayerViewModel(itemRepo, transcodeRepo, prefs(), serverPrefs(), subPrefs(), playbackPrefs(), emptyDownloads(), emptyNotifications(), stubSubtitles(), stubTrickplay(), stubWatchLimit(), stubAudiobooks(), stubCapabilities())
        vm.prepare("movie-1")
        advanceUntilIdle()

        assertThat(vm.contentDurationMs(playerDurationMs = 5_400_000L, playerDurationTrusted = true))
            .isEqualTo(5_400_000L)
    }

    @Test
    fun `a remux session is offset by where the server really opened it`() = runTest(dispatcher) {
        // Video copied → the session can only open on a keyframe, here 1.5 s
        // before the resume point. Content time (progress, markers, subtitle
        // cues) is position + hlsOffsetMs, so the offset must be the real one.
        val itemRepo = itemRepo()
        coEvery { itemRepo.getItem("movie-1") } returns resumedRemuxMovie(itemDurationMs = null, fileDurationMs = 7_200_000L)
        val vm = PlayerViewModel(itemRepo, remuxRepo(startOffsetSec = 3_598.5), prefs(), serverPrefs(), subPrefs(), playbackPrefs(), emptyDownloads(), emptyNotifications(), stubSubtitles(), stubTrickplay(), stubWatchLimit(), stubAudiobooks(), stubCapabilities())
        vm.prepare("movie-1")
        advanceUntilIdle()

        assertThat(vm.hlsOffsetMs).isEqualTo(3_598_500L)
        val src = vm.state.value.source as PlaybackSource.Hls
        assertThat(src.offsetMs).isEqualTo(3_598_500L)
        // And the player starts 1.5 s into the stream — at the resume point,
        // not the keyframe before it.
        assertThat(src.requestedMs).isEqualTo(3_600_000L)
        assertThat(src.startMs).isEqualTo(1_500L)
    }

    @Test
    fun `a remux the server opened at 0 is not offset by the requested position`() = runTest(dispatcher) {
        // Resumed 4 s in, before the first keyframe after 0:00: the session
        // really starts at the top of the file. Treating that 0 as "no field"
        // offset everything content-timed — subtitle cues, markers, progress —
        // by the 4 s asked for.
        val itemRepo = itemRepo()
        coEvery { itemRepo.getItem("movie-1") } returns
            resumedRemuxMovie(itemDurationMs = null, fileDurationMs = 7_200_000L).copy(view_offset_ms = 4_000L)
        val vm = PlayerViewModel(itemRepo, remuxRepo(startOffsetSec = 0.0), prefs(), serverPrefs(), subPrefs(), playbackPrefs(), emptyDownloads(), emptyNotifications(), stubSubtitles(), stubTrickplay(), stubWatchLimit(), stubAudiobooks(), stubCapabilities())
        vm.prepare("movie-1")
        advanceUntilIdle()

        assertThat(vm.hlsOffsetMs).isEqualTo(0L)
        val src = vm.state.value.source as PlaybackSource.Hls
        assertThat(src.offsetMs).isEqualTo(0L)
        // The player seeks the 4 s in instead.
        assertThat(src.startMs).isEqualTo(4_000L)
    }

    @Test
    fun `a full-timeline session starts the player at the resume point`() = runTest(dispatcher) {
        // An ABR ladder's stream covers the whole file from 0:00 and says so
        // (start_offset_sec 0), whatever position was asked for. Started at
        // 0, a resume 45 minutes in played from the top — and the first beat
        // saved 0:10 over the resume point.
        val itemRepo = itemRepo()
        coEvery { itemRepo.getItem("movie-1") } returns
            movieDetail(transcodeFile(), viewOffsetMs = 2_700_000L)
        val transcodeRepo = mockk<TranscodeRepository>(relaxed = true).also { repo ->
            coEvery { repo.decide(any(), any()) } returns "transcode"
            coEvery { repo.start(any(), any(), any(), any(), any(), any(), any()) } returns
                TranscodeSession(session_id = "abr", playlist_url = "/t/abr.m3u8", token = "tok", start_offset_sec = 0.0)
        }
        val vm = PlayerViewModel(itemRepo, transcodeRepo, prefs(), serverPrefs(), subPrefs(), playbackPrefs(), emptyDownloads(), emptyNotifications(), stubSubtitles(), stubTrickplay(), stubWatchLimit(), stubAudiobooks(), stubCapabilities())
        vm.prepare("movie-1")
        advanceUntilIdle()

        coVerify { transcodeRepo.start("movie-1", 1080, 2_700_000L, "f2", false, null, any()) }
        val src = vm.state.value.source as PlaybackSource.Hls
        assertThat(src.offsetMs).isEqualTo(0L)
        assertThat(vm.hlsOffsetMs).isEqualTo(0L)
        assertThat(src.startMs).isEqualTo(2_700_000L)
    }

    @Test
    fun `an audio-track switch on a full-timeline session starts the new player where the old one was`() = runTest(dispatcher) {
        val itemRepo = itemRepo()
        coEvery { itemRepo.getItem("movie-1") } returns
            movieDetail(transcodeFile().copy(audio_streams = threeAudioStreams), viewOffsetMs = 2_700_000L)
        val transcodeRepo = mockk<TranscodeRepository>(relaxed = true).also { repo ->
            coEvery { repo.decide(any(), any()) } returns "transcode"
            coEvery { repo.start(any(), any(), any(), any(), any(), any(), any()) } returnsMany listOf(
                TranscodeSession(session_id = "abr-1", playlist_url = "/t/abr-1.m3u8", token = "tok-1", start_offset_sec = 0.0),
                TranscodeSession(session_id = "abr-2", playlist_url = "/t/abr-2.m3u8", token = "tok-2", start_offset_sec = 0.0),
            )
        }
        val vm = PlayerViewModel(itemRepo, transcodeRepo, prefs(), serverPrefs(), subPrefs(), playbackPrefs(), emptyDownloads(), emptyNotifications(), stubSubtitles(), stubTrickplay(), stubWatchLimit(), stubAudiobooks(), stubCapabilities())
        vm.prepare("movie-1")
        advanceUntilIdle()

        // A minute after the resume point, in stream time = content time.
        vm.switchAudioStream(audioRow = 2, currentPositionMs = 2_760_000L)
        advanceUntilIdle()

        coVerify { transcodeRepo.start("movie-1", 1080, 2_760_000L, "f2", false, 2, any()) }
        val src = vm.state.value.source as PlaybackSource.Hls
        assertThat(src.playlistUrl).isEqualTo("http://srv/t/abr-2.m3u8")
        assertThat(src.startMs).isEqualTo(2_760_000L)
    }

    @Test
    fun `the start position is never before the stream`() {
        // A server rounding the keyframe it opened at up past the request.
        assertThat(PlaybackSource.Hls("u", offsetMs = 3_598_500L, requestedMs = 3_598_400L).startMs).isEqualTo(0L)
        assertThat(PlaybackSource.Hls("u", offsetMs = 0L, requestedMs = 0L).startMs).isEqualTo(0L)
    }

    @Test
    fun `the source an audio-track switch replaced reports nothing on its way out`() = runTest(dispatcher) {
        val itemRepo = itemRepo()
        coEvery { itemRepo.getItem("movie-1") } returns resumedRemuxMovie(itemDurationMs = null, fileDurationMs = 7_200_000L)
            .let { it.copy(files = it.files.map { f -> f.copy(audio_streams = threeAudioStreams) }) }
        val transcodeRepo = mockk<TranscodeRepository>(relaxed = true).also { repo ->
            coEvery { repo.decide(any(), any()) } returns "directStream"
            coEvery { repo.start(any(), any(), any(), any(), any(), any(), any()) } returnsMany listOf(
                TranscodeSession(session_id = "sess-1", playlist_url = "/t/sess-1.m3u8", token = "tok-1", start_offset_sec = 3_600.0),
                TranscodeSession(session_id = "sess-2", playlist_url = "/t/sess-2.m3u8", token = "tok-2", start_offset_sec = 3_660.0),
            )
        }
        val vm = PlayerViewModel(itemRepo, transcodeRepo, prefs(), serverPrefs(), subPrefs(), playbackPrefs(), emptyDownloads(), emptyNotifications(), stubSubtitles(), stubTrickplay(), stubWatchLimit(), stubAudiobooks(), stubCapabilities())
        vm.prepare("movie-1")
        advanceUntilIdle()
        val first = vm.state.value.source!!

        vm.switchAudioStream(audioRow = 2, currentPositionMs = 60_000L)
        advanceUntilIdle()
        val second = vm.state.value.source!!
        assertThat(second).isNotSameInstanceAs(first)
        // The old session is retired by its own stop, once the new one is in hand.
        verify(exactly = 1) { transcodeRepo.stopDetached("sess-1", "tok-1") }

        // The old player's reporter asks once; the new one, and a player that
        // really ends, are not swapped out.
        assertThat(vm.swappedOut(first)).isTrue()
        assertThat(vm.swappedOut(first)).isFalse()
        assertThat(vm.swappedOut(second)).isFalse()
    }

    @Test
    fun `a server without start_offset_sec offsets by the requested position`() = runTest(dispatcher) {
        val itemRepo = itemRepo()
        coEvery { itemRepo.getItem("movie-1") } returns resumedRemuxMovie(itemDurationMs = null, fileDurationMs = 7_200_000L)
        val vm = PlayerViewModel(itemRepo, remuxRepo(startOffsetSec = null), prefs(), serverPrefs(), subPrefs(), playbackPrefs(), emptyDownloads(), emptyNotifications(), stubSubtitles(), stubTrickplay(), stubWatchLimit(), stubAudiobooks(), stubCapabilities())
        vm.prepare("movie-1")
        advanceUntilIdle()

        assertThat(vm.hlsOffsetMs).isEqualTo(3_600_000L)
    }

    // ── Audio tracks ──────────────────────────────────────────────────────

    /** A film's three audio streams as the API lists them: in the file's
     *  audio order, each with its ABSOLUTE ffprobe index (the video is #0). */
    private val threeAudioStreams = listOf(
        AudioStream(1, "ac3", 6, "eng", "English 5.1"),
        AudioStream(2, "aac", 2, "eng", "Commentary"),
        AudioStream(3, "aac", 2, "jpn", "Japanese"),
    )

    /** Remux repo whose first session ("sess-1") opens the film and whose
     *  later starts answer [next] in turn. */
    private fun audioSwitchRepo(vararg next: TranscodeSession) = mockk<TranscodeRepository>(relaxed = true).also { repo ->
        coEvery { repo.decide(any(), any()) } returns "directStream"
        coEvery { repo.start(any(), any(), any(), any(), any(), any(), any()) } returnsMany listOf(
            TranscodeSession(session_id = "sess-1", playlist_url = "/t/sess-1.m3u8", token = "tok-1", start_offset_sec = 0.0),
            *next,
        )
    }

    private fun kotlinx.coroutines.test.TestScope.preparedAudioSwitch(repo: TranscodeRepository): PlayerViewModel {
        val itemRepo = itemRepo()
        coEvery { itemRepo.getItem("movie-1") } returns movieDetail(transcodeFile().copy(audio_streams = threeAudioStreams))
        val vm = PlayerViewModel(itemRepo, repo, prefs(), serverPrefs(), subPrefs(), playbackPrefs(), emptyDownloads(), emptyNotifications(), stubSubtitles(), stubTrickplay(), stubWatchLimit(), stubAudiobooks(), stubCapabilities())
        vm.prepare("movie-1")
        advanceUntilIdle()
        return vm
    }

    @Test
    fun `a remux session starts on the server's default audio, the first row`() = runTest(dispatcher) {
        val repo = audioSwitchRepo()
        val vm = preparedAudioSwitch(repo)

        // No audio_stream_index asked for: ffmpeg maps 0:a:0.
        coVerify { repo.start("movie-1", 0, 0L, "f2", true, null, any()) }
        assertThat(vm.state.value.sessionAudioRow).isEqualTo(0)
    }

    @Test
    fun `direct play leaves the audio row to the player`() = runTest(dispatcher) {
        val itemRepo = itemRepo()
        val transcodeRepo = mockk<TranscodeRepository>().also { repo -> coEvery { repo.decide(any(), any()) } returns "directPlay" }
        coEvery { itemRepo.getItem("movie-1") } returns movieDetail(directPlayFile())
        val vm = PlayerViewModel(itemRepo, transcodeRepo, prefs(), serverPrefs(), subPrefs(), playbackPrefs(), emptyDownloads(), emptyNotifications(), stubSubtitles(), stubTrickplay(), stubWatchLimit(), stubAudiobooks(), stubCapabilities())
        vm.prepare("movie-1")
        advanceUntilIdle()

        assertThat(vm.state.value.source).isInstanceOf(PlaybackSource.DirectPlay::class.java)
        assertThat(vm.state.value.sessionAudioRow).isNull()
    }

    @Test
    fun `an audio switch asks for the picked row, not the stream's absolute index`() = runTest(dispatcher) {
        val repo = audioSwitchRepo(
            TranscodeSession(session_id = "sess-2", playlist_url = "/t/sess-2.m3u8", token = "tok-2", start_offset_sec = 0.0),
        )
        val vm = preparedAudioSwitch(repo)

        // The last row, "Japanese" — ffprobe stream #3, the THIRD audio stream.
        // Sending 3 named a fourth audio stream the file doesn't have: no
        // playlist, then a playback error.
        vm.switchAudioStream(audioRow = 2, currentPositionMs = 60_000L)
        advanceUntilIdle()

        coVerify { repo.start("movie-1", 0, 60_000L, "f2", true, 2, any()) }
        coVerify(exactly = 0) { repo.start(any(), any(), any(), any(), any(), 3, any()) }
        assertThat((vm.state.value.source as PlaybackSource.Hls).playlistUrl).isEqualTo("http://srv/t/sess-2.m3u8")
        // The picker now marks the row playing.
        assertThat(vm.state.value.sessionAudioRow).isEqualTo(2)
    }

    @Test
    fun `an audio switch to no row of the file starts nothing`() = runTest(dispatcher) {
        val repo = audioSwitchRepo()
        val vm = preparedAudioSwitch(repo)
        val before = vm.state.value.source

        vm.switchAudioStream(audioRow = 3, currentPositionMs = 60_000L)
        vm.switchAudioStream(audioRow = -1, currentPositionMs = 60_000L)
        advanceUntilIdle()

        coVerify(exactly = 1) { repo.start(any(), any(), any(), any(), any(), any(), any()) }
        verify(exactly = 0) { repo.stopDetached(any(), any()) }
        assertThat(vm.state.value.source).isSameInstanceAs(before)
        assertThat(vm.state.value.sessionAudioRow).isEqualTo(0)
    }

    @Test
    fun `picking the audio already playing starts no new session`() = runTest(dispatcher) {
        val repo = audioSwitchRepo()
        val vm = preparedAudioSwitch(repo)
        val before = vm.state.value.source

        vm.switchAudioStream(audioRow = 0, currentPositionMs = 60_000L)
        advanceUntilIdle()

        coVerify(exactly = 1) { repo.start(any(), any(), any(), any(), any(), any(), any()) }
        assertThat(vm.state.value.source).isSameInstanceAs(before)
    }

    @Test
    fun `a failed audio switch keeps playing, and showing, the old track`() = runTest(dispatcher) {
        val repo = audioSwitchRepo()
        coEvery { repo.start(any(), any(), any(), any(), any(), 1, any()) } throws httpError(500)
        val vm = preparedAudioSwitch(repo)
        val before = vm.state.value.source

        vm.switchAudioStream(audioRow = 1, currentPositionMs = 60_000L)
        advanceUntilIdle()

        verify(exactly = 0) { repo.stopDetached(any(), any()) }
        assertThat(vm.state.value.source).isSameInstanceAs(before)
        assertThat(vm.state.value.sessionAudioRow).isEqualTo(0)
        assertThat(vm.state.value.pendingAudioRow).isNull()
        assertThat(vm.state.value.targetAudioRow).isEqualTo(0)
        assertThat(vm.state.value.error).isNull()
    }

    // ── Audio picks while a switch is starting ────────────────────────────

    /** Session [n] of the film, "sess-n". */
    private fun session(n: Int) =
        TranscodeSession(session_id = "sess-$n", playlist_url = "/t/sess-$n.m3u8", token = "tok-$n", start_offset_sec = 0.0)

    /** Holds [repo]'s start of [row]'s session — a cold ffmpeg start — until
     *  the test completes the returned deferred. */
    private fun holdStart(repo: TranscodeRepository, row: Int): kotlinx.coroutines.CompletableDeferred<TranscodeSession> {
        val started = kotlinx.coroutines.CompletableDeferred<TranscodeSession>()
        coEvery { repo.start(any(), any(), any(), any(), any(), row, any()) } coAnswers { started.await() }
        return started
    }

    private fun PlayerViewModel.playlist() = (state.value.source as PlaybackSource.Hls).playlistUrl

    @Test
    fun `the picker marks a switch's row while its session starts`() = runTest(dispatcher) {
        val repo = audioSwitchRepo()
        val started = holdStart(repo, row = 2)
        val vm = preparedAudioSwitch(repo)
        val before = vm.state.value.source

        vm.switchAudioStream(audioRow = 2, currentPositionMs = 60_000L)
        runCurrent()

        // Still the first session playing — but the sheet marks the pick, not
        // the row the viewer just left.
        assertThat(vm.state.value.source).isSameInstanceAs(before)
        assertThat(vm.state.value.sessionAudioRow).isEqualTo(0)
        assertThat(vm.state.value.pendingAudioRow).isEqualTo(2)
        assertThat(vm.state.value.targetAudioRow).isEqualTo(2)

        started.complete(session(2))
        advanceUntilIdle()

        assertThat(vm.playlist()).isEqualTo("http://srv/t/sess-2.m3u8")
        assertThat(vm.state.value.sessionAudioRow).isEqualTo(2)
        assertThat(vm.state.value.pendingAudioRow).isNull()
        assertThat(vm.state.value.targetAudioRow).isEqualTo(2)
    }

    @Test
    fun `picking the playing row while a switch starts switches back`() = runTest(dispatcher) {
        val repo = audioSwitchRepo()
        val toCommentary = holdStart(repo, row = 1)
        coEvery { repo.start(any(), any(), any(), any(), any(), 0, any()) } returns session(3)
        val vm = preparedAudioSwitch(repo)

        vm.switchAudioStream(audioRow = 1, currentPositionMs = 60_000L)
        runCurrent()
        // Back to English before Commentary's session is up. Checked against
        // the session's row alone this was "already playing" and dropped —
        // and Commentary played once it was.
        vm.switchAudioStream(audioRow = 0, currentPositionMs = 61_000L)
        advanceUntilIdle()

        // A session of its own: the start in flight has already retired the
        // live one on the server, which keeps one per viewer and item.
        coVerify(exactly = 1) { repo.start("movie-1", 0, 61_000L, "f2", true, 0, any()) }
        assertThat(vm.playlist()).isEqualTo("http://srv/t/sess-3.m3u8")
        assertThat(vm.state.value.sessionAudioRow).isEqualTo(0)
        assertThat(vm.state.value.pendingAudioRow).isNull()

        // Commentary's session lands late: stopped, never played.
        toCommentary.complete(session(2))
        advanceUntilIdle()

        verify(exactly = 1) { repo.stopDetached("sess-2", "tok-2") }
        assertThat(vm.playlist()).isEqualTo("http://srv/t/sess-3.m3u8")
        assertThat(vm.state.value.sessionAudioRow).isEqualTo(0)
    }

    @Test
    fun `picking the row a switch is starting again starts no second session`() = runTest(dispatcher) {
        val repo = audioSwitchRepo()
        val started = holdStart(repo, row = 1)
        val vm = preparedAudioSwitch(repo)

        vm.switchAudioStream(audioRow = 1, currentPositionMs = 60_000L)
        runCurrent()
        vm.switchAudioStream(audioRow = 1, currentPositionMs = 62_000L)
        runCurrent()
        started.complete(session(2))
        advanceUntilIdle()

        coVerify(exactly = 1) { repo.start(any(), any(), any(), any(), any(), 1, any()) }
        assertThat(vm.playlist()).isEqualTo("http://srv/t/sess-2.m3u8")
        assertThat(vm.state.value.sessionAudioRow).isEqualTo(1)
        verify(exactly = 1) { repo.stopDetached(any(), any()) }
        verify(exactly = 1) { repo.stopDetached("sess-1", "tok-1") }
    }

    @Test
    fun `a superseded switch's session is stopped, not played, when it lands first`() = runTest(dispatcher) {
        val repo = audioSwitchRepo()
        val toCommentary = holdStart(repo, row = 1)
        val toJapanese = holdStart(repo, row = 2)
        val vm = preparedAudioSwitch(repo)
        val first = vm.state.value.source!!

        vm.switchAudioStream(audioRow = 1, currentPositionMs = 60_000L)
        runCurrent()
        vm.switchAudioStream(audioRow = 2, currentPositionMs = 61_000L)
        runCurrent()
        assertThat(vm.state.value.pendingAudioRow).isEqualTo(2)

        // Commentary's session, up first, was picked over. It used to play
        // until Japanese's replaced it — and then nothing ever stopped it.
        toCommentary.complete(session(2))
        advanceUntilIdle()

        verify(exactly = 1) { repo.stopDetached("sess-2", "tok-2") }
        assertThat(vm.state.value.source).isSameInstanceAs(first)
        assertThat(vm.state.value.pendingAudioRow).isEqualTo(2)

        toJapanese.complete(session(3))
        advanceUntilIdle()

        assertThat(vm.playlist()).isEqualTo("http://srv/t/sess-3.m3u8")
        assertThat(vm.state.value.sessionAudioRow).isEqualTo(2)
        assertThat(vm.state.value.pendingAudioRow).isNull()
        verify(exactly = 1) { repo.stopDetached("sess-1", "tok-1") }
        assertThat(vm.swappedOut(first)).isTrue()

        // The one session left is the one playing: leaving stops it.
        vm.stopActiveTranscode()
        verify(exactly = 1) { repo.stopDetached("sess-3", "tok-3") }
        verify(exactly = 3) { repo.stopDetached(any(), any()) }
    }

    @Test
    fun `a superseded switch's session is stopped, not played, when it lands last`() = runTest(dispatcher) {
        val repo = audioSwitchRepo()
        val toCommentary = holdStart(repo, row = 1)
        val toJapanese = holdStart(repo, row = 2)
        val vm = preparedAudioSwitch(repo)

        vm.switchAudioStream(audioRow = 1, currentPositionMs = 60_000L)
        runCurrent()
        vm.switchAudioStream(audioRow = 2, currentPositionMs = 61_000L)
        runCurrent()
        toJapanese.complete(session(3))
        advanceUntilIdle()
        val japanese = vm.state.value.source

        // Landing last, it used to replace the pick that had already played.
        toCommentary.complete(session(2))
        advanceUntilIdle()

        verify(exactly = 1) { repo.stopDetached("sess-2", "tok-2") }
        assertThat(vm.state.value.source).isSameInstanceAs(japanese)
        assertThat(vm.playlist()).isEqualTo("http://srv/t/sess-3.m3u8")
        assertThat(vm.state.value.sessionAudioRow).isEqualTo(2)
        vm.stopActiveTranscode()
        verify(exactly = 1) { repo.stopDetached("sess-3", "tok-3") }
    }

    @Test
    fun `a superseded switch that fails leaves the later pick marked`() = runTest(dispatcher) {
        val repo = audioSwitchRepo()
        val toCommentary = holdStart(repo, row = 1)
        val toJapanese = holdStart(repo, row = 2)
        val vm = preparedAudioSwitch(repo)

        vm.switchAudioStream(audioRow = 1, currentPositionMs = 60_000L)
        runCurrent()
        vm.switchAudioStream(audioRow = 2, currentPositionMs = 61_000L)
        runCurrent()
        toCommentary.completeExceptionally(httpError(500))
        advanceUntilIdle()

        assertThat(vm.state.value.pendingAudioRow).isEqualTo(2)
        assertThat(vm.state.value.targetAudioRow).isEqualTo(2)
        assertThat(vm.state.value.error).isNull()

        toJapanese.complete(session(3))
        advanceUntilIdle()

        assertThat(vm.state.value.sessionAudioRow).isEqualTo(2)
        assertThat(vm.state.value.pendingAudioRow).isNull()
    }

    @Test
    fun `a switch that lands after an admin stop is stopped, not played`() = runTest(dispatcher) {
        val repo = audioSwitchRepo()
        val started = holdStart(repo, row = 1)
        val vm = preparedAudioSwitch(repo)
        val before = vm.state.value.source

        vm.switchAudioStream(audioRow = 1, currentPositionMs = 60_000L)
        runCurrent()
        vm.onStreamRefusedByAdminStop("Stopped by the server admin")
        started.complete(session(2))
        advanceUntilIdle()

        verify(exactly = 1) { repo.stopDetached("sess-1", "tok-1") }
        verify(exactly = 1) { repo.stopDetached("sess-2", "tok-2") }
        assertThat(vm.state.value.source).isSameInstanceAs(before)
        assertThat(vm.state.value.pendingAudioRow).isNull()
        assertThat(vm.state.value.error).isEqualTo("Stopped by the server admin")
    }

    // ── A start that fails ────────────────────────────────────────────────

    @Test
    fun `a refused transcode start shows the server's own message`() = runTest(dispatcher) {
        val itemRepo = itemRepo()
        coEvery { itemRepo.getItem("movie-1") } returns movieDetail(transcodeFile())
        val repo = mockk<TranscodeRepository>(relaxed = true).also {
            coEvery { it.decide(any(), any()) } returns "transcode"
            coEvery { it.start(any(), any(), any(), any(), any(), any(), any()) } throws httpError(
                422,
                """{"error":{"code":"SOURCE_UNREADABLE","message":"This file appears to be corrupt.","request_id":"r1"}}""",
            )
        }
        val vm = PlayerViewModel(itemRepo, repo, prefs(), serverPrefs(), subPrefs(), playbackPrefs(), emptyDownloads(), emptyNotifications(), stubSubtitles(), stubTrickplay(), stubWatchLimit(), stubAudiobooks(), stubCapabilities())
        vm.prepare("movie-1")
        advanceUntilIdle()

        // Was Retrofit's bare "HTTP 422".
        assertThat(vm.state.value.error).isEqualTo("This file appears to be corrupt.")
        assertThat(vm.state.value.source).isNull()
    }

    @Test
    fun `a refusal with no error body falls back to the status`() = runTest(dispatcher) {
        val itemRepo = itemRepo()
        coEvery { itemRepo.getItem("movie-1") } returns movieDetail(transcodeFile())
        val repo = mockk<TranscodeRepository>(relaxed = true).also {
            coEvery { it.decide(any(), any()) } returns "transcode"
            coEvery { it.start(any(), any(), any(), any(), any(), any(), any()) } throws httpError(502, "<html>bad gateway</html>")
        }
        val vm = PlayerViewModel(itemRepo, repo, prefs(), serverPrefs(), subPrefs(), playbackPrefs(), emptyDownloads(), emptyNotifications(), stubSubtitles(), stubTrickplay(), stubWatchLimit(), stubAudiobooks(), stubCapabilities())
        vm.prepare("movie-1")
        advanceUntilIdle()

        assertThat(vm.state.value.error).startsWith("HTTP 502")
    }

    @Test
    fun `a 403 still names the gate, not the raw server message`() = runTest(dispatcher) {
        val itemRepo = itemRepo()
        coEvery { itemRepo.getItem("movie-1") } returns movieDetail(transcodeFile())
        val repo = mockk<TranscodeRepository>(relaxed = true).also {
            coEvery { it.decide(any(), any()) } returns "transcode"
            coEvery { it.start(any(), any(), any(), any(), any(), any(), any()) } throws httpError(
                403,
                """{"error":{"code":"PARENTAL_LIMIT","message":"daily_limit_reached"}}""",
            )
        }
        val vm = PlayerViewModel(itemRepo, repo, prefs(), serverPrefs(), subPrefs(), playbackPrefs(), emptyDownloads(), emptyNotifications(), stubSubtitles(), stubTrickplay(), stubWatchLimit(), stubAudiobooks(), stubCapabilities())
        vm.prepare("movie-1")
        advanceUntilIdle()

        assertThat(vm.state.value.error).contains("watch-time limit")
    }

    @Test
    fun `a failure with no message still shows one rather than the spinner`() = runTest(dispatcher) {
        val itemRepo = itemRepo()
        val transcodeRepo = mockk<TranscodeRepository>().also { repo -> coEvery { repo.decide(any(), any()) } returns null }
        coEvery { itemRepo.getItem(any()) } throws IllegalStateException()
        val vm = PlayerViewModel(itemRepo, transcodeRepo, prefs(), serverPrefs(), subPrefs(), playbackPrefs(), emptyDownloads(), emptyNotifications(), stubSubtitles(), stubTrickplay(), stubWatchLimit(), stubAudiobooks(), stubCapabilities())
        vm.prepare("movie-1")
        advanceUntilIdle()

        // A null error with no source is the screen's spinner, for good.
        assertThat(vm.state.value.error).isEqualTo(START_FAILED_MESSAGE)
    }

    // ── Subtitles ─────────────────────────────────────────────────────────

    private fun subtitledRemuxFile() = transcodeFile().copy(
        stream_token = "st-file",
        subtitle_streams = listOf(
            SubtitleStream(2, "subrip", "eng", "English", false),
            SubtitleStream(3, "subrip", "eng", "English SDH", false, sdh = true),
            SubtitleStream(4, "hdmv_pgs_subtitle", "eng", "English PGS", false),
        ),
        external_subtitles = listOf(
            tv.onscreen.mobile.data.model.ExternalSubtitle(
                id = "x1", language = "spa", title = "Spanish",
                url = "/media/external-subtitles/x1",
            ),
        ),
    )

    @Test
    fun `an HLS session gets every text subtitle as a side-load with a vaulted credential`() = runTest(dispatcher) {
        val itemRepo = itemRepo()
        coEvery { itemRepo.getItem("movie-1") } returns movieDetail(subtitledRemuxFile())
        val sp = serverPrefs()
        coEvery { sp.getAssetToken() } returns "as-24h"
        val vm = PlayerViewModel(itemRepo, remuxRepo(), prefs(), sp, subPrefs(), playbackPrefs(), emptyDownloads(), emptyNotifications(), stubSubtitles(), stubTrickplay(), stubWatchLimit(), stubAudiobooks(), stubCapabilities())
        vm.prepare("movie-1")
        advanceUntilIdle()

        val tracks = vm.state.value.subtitleTracks
        assertThat(tracks.map { it.trackId })
            .containsExactly("sub:emb:2", "sub:emb:3", "sub:emb:4", "sub:ext:x1").inOrder()
        val byId = tracks.associateBy { it.trackId }
        // Embedded: ABSOLUTE stream index, the file's own stream token.
        assertThat(byId.getValue("sub:emb:2").url).isEqualTo("http://srv/media/subtitles/f2/2")
        assertThat(StreamTokenVault.tokenForTest("http://srv/media/subtitles/f2/2")).isEqualTo("st-file")
        // Image-based: the server won't serve it as text — nothing to load.
        assertThat(byId.getValue("sub:emb:4").url).isNull()
        // Attached file: the asset token (a stream token is refused there).
        assertThat(byId.getValue("sub:ext:x1").url).isEqualTo("http://srv/media/external-subtitles/x1")
        assertThat(StreamTokenVault.tokenForTest("http://srv/media/external-subtitles/x1")).isEqualTo("as-24h")
        // No credential in any url the player sees.
        tracks.mapNotNull { it.url }.forEach { assertThat(it).doesNotContain("token=") }
        // The picker on this HLS session: the three that can be side-loaded.
        assertThat(SubtitleTracks.rows(tracks, hls = true).map { it.trackId })
            .containsExactly("sub:emb:2", "sub:emb:3", "sub:ext:x1").inOrder()
    }

    @Test
    fun `a downloaded subtitle joins the tracks without a new source or session`() = runTest(dispatcher) {
        val itemRepo = itemRepo()
        val file = directPlayFile().copy(stream_token = "st-24h")
        coEvery { itemRepo.getItem("movie-1") } returnsMany listOf(
            movieDetail(file),
            movieDetail(
                file.copy(
                    external_subtitles = listOf(
                        tv.onscreen.mobile.data.model.ExternalSubtitle(
                            id = "x9", language = "fre", url = "/media/external-subtitles/x9",
                        ),
                    ),
                ),
            ),
        )
        val transcodeRepo = mockk<TranscodeRepository>().also { repo -> coEvery { repo.decide(any(), any()) } returns null }
        val sp = serverPrefs()
        coEvery { sp.getAssetToken() } returns "as-24h"
        val online = stubSubtitles()
        val candidate = tv.onscreen.mobile.data.model.OnlineSubtitle(
            provider_file_id = 1, file_name = "movie.fr.srt", language = "fr",
        )
        val vm = PlayerViewModel(itemRepo, transcodeRepo, prefs(), sp, subPrefs(), playbackPrefs(), emptyDownloads(), emptyNotifications(), online, stubTrickplay(), stubWatchLimit(), stubAudiobooks(), stubCapabilities())
        vm.prepare("movie-1")
        advanceUntilIdle()
        val source = vm.state.value.source
        assertThat(vm.state.value.subtitleTracks.map { it.trackId }).containsExactly("sub:emb:1")

        var done = false
        vm.downloadOnlineSubtitle("movie-1", candidate) { done = true }
        advanceUntilIdle()

        assertThat(done).isTrue()
        coVerify { online.download("movie-1", "f1", candidate) }
        assertThat(vm.state.value.subtitleTracks.map { it.trackId })
            .containsExactly("sub:emb:1", "sub:ext:x9").inOrder()
        // Same source: the screen side-loads the new file into the running
        // player instead of starting playback over.
        assertThat(vm.state.value.source).isSameInstanceAs(source)
    }
}
