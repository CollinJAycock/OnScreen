package tv.onscreen.mobile.ui.settings

import com.google.common.truth.Truth.assertThat
import com.squareup.moshi.Moshi
import io.mockk.coEvery
import io.mockk.coVerify
import io.mockk.mockk
import io.mockk.slot
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.ExperimentalCoroutinesApi
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
import tv.onscreen.mobile.data.model.QualityProfileRequest
import tv.onscreen.mobile.data.model.UserPreferences
import tv.onscreen.mobile.data.repository.PreferencesRepository
import tv.onscreen.mobile.ui.settings.QualityProfile.Field

/** Settings → Playback quality (PUT /users/me/quality-profile, v2.6). */
@OptIn(ExperimentalCoroutinesApi::class)
class QualityProfileTest {

    private val dispatcher = StandardTestDispatcher()

    @Before fun setUp() { Dispatchers.setMain(dispatcher) }
    @After fun tearDown() { Dispatchers.resetMain() }

    private val stored = UserPreferences(
        max_video_height = 1080,
        max_video_bitrate_kbps = 8000,
        max_audio_bitrate_kbps = 192,
        preferred_video_codec = "hevc",
        forced_subtitles_only = true,
    )

    @Test
    fun `a save changes one field and echoes the other four`() {
        val body = QualityProfile.request(stored, Field.VIDEO_BITRATE, "4000")
        assertThat(body).isEqualTo(
            QualityProfileRequest(
                max_video_height = 1080,
                max_video_bitrate_kbps = 4000,
                max_audio_bitrate_kbps = 192,
                preferred_video_codec = "hevc",
                // Never cleared by a quality save: the TV apps set it.
                forced_subtitles_only = true,
            ),
        )
    }

    @Test
    fun `no limit clears just that field`() {
        val body = QualityProfile.request(stored, Field.HEIGHT, null)
        assertThat(body.max_video_height).isNull()
        assertThat(body.max_video_bitrate_kbps).isEqualTo(8000)
        assertThat(QualityProfile.request(stored, Field.CODEC, null).preferred_video_codec).isNull()
    }

    @Test
    fun `a cleared limit goes out as null, which the server reads as no limit`() {
        val json = Moshi.Builder().build().adapter(QualityProfileRequest::class.java)
            .toJson(QualityProfile.request(UserPreferences(), Field.HEIGHT, "720"))
        assertThat(json).contains("\"max_video_height\":720")
        assertThat(json).contains("\"forced_subtitles_only\":false")
        assertThat(json).doesNotContain("max_video_bitrate_kbps\":0")
    }

    @Test
    fun `values read from preferences, 0 and unknown codecs as no limit`() {
        assertThat(QualityProfile.valueOf(stored, Field.HEIGHT)).isEqualTo("1080")
        assertThat(QualityProfile.valueOf(stored, Field.CODEC)).isEqualTo("hevc")
        assertThat(QualityProfile.valueOf(UserPreferences(max_video_height = 0), Field.HEIGHT)).isNull()
        assertThat(QualityProfile.valueOf(UserPreferences(preferred_video_codec = "vp9"), Field.CODEC)).isNull()
    }

    @Test
    fun `the options match the web app`() {
        assertThat(QualityProfile.options(Field.HEIGHT, null).map { it.label })
            .containsExactly("Original", "4K", "1080p", "720p", "480p").inOrder()
        assertThat(QualityProfile.options(Field.VIDEO_BITRATE, null).map { it.value })
            .containsExactly(null, "40000", "20000", "12000", "8000", "4000", "2000", "1000").inOrder()
        assertThat(QualityProfile.options(Field.AUDIO_BITRATE, null).map { it.label })
            .containsExactly("Original", "320 kbps", "256 kbps", "192 kbps", "128 kbps", "96 kbps").inOrder()
        assertThat(QualityProfile.options(Field.CODEC, null).map { it.value })
            .containsExactly(null, "h264", "hevc").inOrder()
    }

    @Test
    fun `a value set elsewhere shows as itself, not as no limit`() {
        assertThat(QualityProfile.label(Field.VIDEO_BITRATE, "6000")).isEqualTo("6 Mbps")
        assertThat(QualityProfile.label(Field.VIDEO_BITRATE, "2500")).isEqualTo("2.5 Mbps")
        assertThat(QualityProfile.label(Field.HEIGHT, "1440")).isEqualTo("1440p")
        assertThat(QualityProfile.label(Field.AUDIO_BITRATE, "160")).isEqualTo("160 kbps")
        assertThat(QualityProfile.options(Field.HEIGHT, "1440").last().value).isEqualTo("1440")
        assertThat(QualityProfile.label(Field.HEIGHT, null)).isEqualTo("Original")
        assertThat(QualityProfile.label(Field.VIDEO_BITRATE, null)).isEqualTo("No limit")
    }

    // ── The screen's VM ────────────────────────────────────────────────

    @Test
    fun `saving reads afresh and sends the whole profile`() = runTest(dispatcher) {
        val repo = mockk<PreferencesRepository>()
        // The screen loaded an older copy; another device changed the audio
        // limit since. The save must echo the fresh value, not the old one.
        coEvery { repo.fresh() } returnsMany listOf(stored, stored.copy(max_audio_bitrate_kbps = 128))
        val sent = slot<QualityProfileRequest>()
        coEvery { repo.setQualityProfile(capture(sent)) } returns Unit
        val vm = PlaybackQualityViewModel(repo)
        advanceUntilIdle()
        assertThat(vm.state.value.values[Field.HEIGHT]).isEqualTo("1080")

        vm.set(Field.HEIGHT, "720")
        advanceUntilIdle()

        assertThat(sent.captured).isEqualTo(
            QualityProfileRequest(720, 8000, 128, "hevc", forced_subtitles_only = true),
        )
        assertThat(vm.state.value.values[Field.HEIGHT]).isEqualTo("720")
        assertThat(vm.state.value.values[Field.AUDIO_BITRATE]).isEqualTo("128")
        assertThat(vm.state.value.message).isEqualTo("Playback quality saved")
    }

    @Test
    fun `a refused value says so and keeps the shown profile`() = runTest(dispatcher) {
        val repo = mockk<PreferencesRepository>()
        coEvery { repo.fresh() } returns stored
        coEvery { repo.setQualityProfile(any()) } throws retrofit2.HttpException(
            retrofit2.Response.error<Any>(
                400,
                """{"error":{"code":"BAD_REQUEST","message":"max_video_height must be at least 144, or null for no limit"}}"""
                    .toResponseBody("application/json".toMediaTypeOrNull()),
            ),
        )
        val vm = PlaybackQualityViewModel(repo)
        advanceUntilIdle()

        vm.set(Field.HEIGHT, "480")
        advanceUntilIdle()

        assertThat(vm.state.value.message).contains("isn’t allowed")
        assertThat(vm.state.value.values[Field.HEIGHT]).isEqualTo("1080")
        assertThat(vm.state.value.saving).isFalse()
    }

    @Test
    fun `picking the current value saves nothing`() = runTest(dispatcher) {
        val repo = mockk<PreferencesRepository>()
        coEvery { repo.fresh() } returns stored
        val vm = PlaybackQualityViewModel(repo)
        advanceUntilIdle()

        vm.set(Field.CODEC, "hevc")
        advanceUntilIdle()

        coVerify(exactly = 0) { repo.setQualityProfile(any()) }
    }
}
