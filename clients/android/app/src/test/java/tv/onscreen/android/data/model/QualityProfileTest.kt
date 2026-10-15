package tv.onscreen.android.data.model

import com.google.common.truth.Truth.assertThat
import io.mockk.coEvery
import io.mockk.coVerify
import io.mockk.mockk
import io.mockk.slot
import kotlinx.coroutines.test.runTest
import okio.Buffer
import org.junit.Test
import tv.onscreen.android.data.api.ApiResponse
import tv.onscreen.android.data.api.OnScreenApi
import tv.onscreen.android.data.repository.PreferencesRepository

/**
 * The "Playback quality" setting (server v2.6, PUT /users/me/quality-profile).
 * The endpoint replaces the WHOLE profile, so a save must send all five
 * fields, echoing what it isn't changing from a fresh read — and never clear
 * forced_subtitles_only, which this screen doesn't edit.
 */
class QualityProfileTest {

    @Test
    fun `the body is the profile as a fresh read holds it`() {
        val body = QualityProfile.fromPreferences(
            UserPreferences(
                max_video_height = 1080, max_video_bitrate_kbps = 8_000, max_audio_bitrate_kbps = 0,
                preferred_video_codec = "HEVC", forced_subtitles_only = true,
            ),
        )

        assertThat(body).isEqualTo(QualityProfileBody(1080, 8_000, null, "hevc", true))
    }

    @Test
    fun `a codec the server doesn't take reads as automatic`() {
        assertThat(QualityProfile.fromPreferences(UserPreferences(preferred_video_codec = "av1")).preferred_video_codec)
            .isNull()
    }

    @Test
    fun `the JSON carries every field, a cleared limit as null`() {
        val json = QualityProfile.toJson(QualityProfileBody(null, 4_000, null, null, true))

        assertThat(json).contains("\"max_video_height\":null")
        assertThat(json).contains("\"max_video_bitrate_kbps\":4000")
        assertThat(json).contains("\"max_audio_bitrate_kbps\":null")
        assertThat(json).contains("\"preferred_video_codec\":null")
        assertThat(json).contains("\"forced_subtitles_only\":true")
    }

    @Test
    fun `a stored value no option lists keeps an option of its own`() {
        val heights = QualityProfile.withStored(QualityProfile.HEIGHTS, 1440, QualityProfile::heightLabel)
        val video = QualityProfile.withStored(QualityProfile.VIDEO_KBPS, 15_000, QualityProfile::videoKbpsLabel)

        assertThat(QualityProfile.labelOf(heights, 1440)).isEqualTo("1440p")
        assertThat(QualityProfile.labelOf(video, 15_000)).isEqualTo("15 Mbps")
        assertThat(QualityProfile.videoKbpsLabel(1_500)).isEqualTo("1.5 Mbps")
        // Listed values and "no limit" add nothing.
        assertThat(QualityProfile.withStored(QualityProfile.HEIGHTS, 720, QualityProfile::heightLabel))
            .isEqualTo(QualityProfile.HEIGHTS)
        assertThat(QualityProfile.withStored(QualityProfile.HEIGHTS, null, QualityProfile::heightLabel))
            .isEqualTo(QualityProfile.HEIGHTS)
        assertThat(QualityProfile.labelOf(QualityProfile.HEIGHTS, null)).isEqualTo("Original")
        assertThat(QualityProfile.labelOf(QualityProfile.VIDEO_KBPS, null)).isEqualTo("No limit")
    }

    @Test
    fun `the options match the web app's`() {
        assertThat(QualityProfile.HEIGHTS.map { it.value }).containsExactly(null, 2160, 1080, 720, 480).inOrder()
        assertThat(QualityProfile.VIDEO_KBPS.map { it.value })
            .containsExactly(null, 40_000, 20_000, 12_000, 8_000, 4_000, 2_000, 1_000).inOrder()
        assertThat(QualityProfile.AUDIO_KBPS.map { it.value }).containsExactly(null, 320, 256, 192, 128, 96).inOrder()
        assertThat(QualityProfile.CODECS.map { it.value }).containsExactly(null, "h264", "hevc").inOrder()
    }

    @Test
    fun `a save changes one field and echoes the rest from a fresh read`() = runTest {
        val api = mockk<OnScreenApi>()
        coEvery { api.getPreferences() } returns ApiResponse(
            UserPreferences(
                max_video_height = 2160, max_video_bitrate_kbps = 20_000, max_audio_bitrate_kbps = 256,
                preferred_video_codec = "h264", forced_subtitles_only = true,
            ),
        )
        val sent = slot<okhttp3.RequestBody>()
        coEvery { api.setQualityProfile(capture(sent)) } returns Unit
        val repo = PreferencesRepository(api)

        repo.setQualityProfile { it.copy(max_video_bitrate_kbps = null) }

        val json = Buffer().also { sent.captured.writeTo(it) }.readUtf8()
        assertThat(json).contains("\"max_video_height\":2160")
        assertThat(json).contains("\"max_video_bitrate_kbps\":null")
        assertThat(json).contains("\"max_audio_bitrate_kbps\":256")
        assertThat(json).contains("\"preferred_video_codec\":\"h264\"")
        assertThat(json).contains("\"forced_subtitles_only\":true")
        // Read fresh before the write, and again after it.
        coVerify(exactly = 2) { api.getPreferences() }
    }
}
