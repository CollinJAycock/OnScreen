package tv.onscreen.mobile.ui.player

import android.media.MediaCodecInfo.CodecProfileLevel
import com.google.common.truth.Truth.assertThat
import org.junit.Test
import tv.onscreen.mobile.ui.player.VideoProfileLevel.MIME_AV1
import tv.onscreen.mobile.ui.player.VideoProfileLevel.MIME_AVC
import tv.onscreen.mobile.ui.player.VideoProfileLevel.MIME_HEVC

// Same cases as the TV app's VideoProfileLevelTest.
class VideoProfileLevelTest {

    @Test
    fun `a Fire TV-like decoder list declares each codec's profile and level`() {
        val keys = VideoProfileLevel.headerKeys(
            mapOf(
                MIME_AVC to listOf(
                    CodecProfileLevel.AVCProfileConstrainedBaseline to CodecProfileLevel.AVCLevel52,
                    CodecProfileLevel.AVCProfileBaseline to CodecProfileLevel.AVCLevel52,
                    CodecProfileLevel.AVCProfileMain to CodecProfileLevel.AVCLevel52,
                    CodecProfileLevel.AVCProfileConstrainedHigh to CodecProfileLevel.AVCLevel52,
                    CodecProfileLevel.AVCProfileHigh to CodecProfileLevel.AVCLevel52,
                ),
                MIME_HEVC to listOf(
                    CodecProfileLevel.HEVCProfileMain to CodecProfileLevel.HEVCHighTierLevel51,
                    CodecProfileLevel.HEVCProfileMain10 to CodecProfileLevel.HEVCHighTierLevel51,
                    CodecProfileLevel.HEVCProfileMain10HDR10 to CodecProfileLevel.HEVCHighTierLevel51,
                    CodecProfileLevel.HEVCProfileMain10HDR10Plus to CodecProfileLevel.HEVCHighTierLevel51,
                ),
                MIME_AV1 to listOf(
                    CodecProfileLevel.AV1ProfileMain8 to CodecProfileLevel.AV1Level51,
                    CodecProfileLevel.AV1ProfileMain10 to CodecProfileLevel.AV1Level51,
                    CodecProfileLevel.AV1ProfileMain10HDR10 to CodecProfileLevel.AV1Level51,
                ),
            ),
        )
        assertThat(keys).containsExactly(
            "h264Profile=high", "h264Level=5.2",
            "hevcProfile=main10", "hevcLevel=5.1",
            "av1Profile=main", "av1Level=5.1",
        ).inOrder()
    }

    @Test
    fun `a decoder listing one entry per level takes the highest`() {
        assertThat(
            VideoProfileLevel.keys(
                MIME_AVC,
                listOf(
                    CodecProfileLevel.AVCProfileHigh to CodecProfileLevel.AVCLevel3,
                    CodecProfileLevel.AVCProfileHigh to CodecProfileLevel.AVCLevel41,
                    CodecProfileLevel.AVCProfileHigh to CodecProfileLevel.AVCLevel4,
                ),
            ),
        ).containsExactly("h264Profile=high", "h264Level=4.1").inOrder()
    }

    @Test
    fun `a higher profile's entry carries the lower profiles at its level`() {
        // Only High listed: Baseline and Main at 5.1 come with it.
        assertThat(VideoProfileLevel.keys(MIME_AVC, listOf(CodecProfileLevel.AVCProfileHigh to CodecProfileLevel.AVCLevel51)))
            .containsExactly("h264Profile=high", "h264Level=5.1").inOrder()
    }

    @Test
    fun `the level sent is one the profile sent reaches`() {
        // Baseline to 5.2 but High only to 4.1: high + 4.1, never high + 5.2
        // (a High 5.2 file would get a black screen).
        assertThat(
            VideoProfileLevel.keys(
                MIME_AVC,
                listOf(
                    CodecProfileLevel.AVCProfileBaseline to CodecProfileLevel.AVCLevel52,
                    CodecProfileLevel.AVCProfileHigh to CodecProfileLevel.AVCLevel41,
                ),
            ),
        ).containsExactly("h264Profile=high", "h264Level=4.1").inOrder()
        // High 10 only to 4.1 but High to 5.1: High 10 isn't worth the level
        // nearly every H.264 file needs.
        assertThat(
            VideoProfileLevel.keys(
                MIME_AVC,
                listOf(
                    CodecProfileLevel.AVCProfileHigh to CodecProfileLevel.AVCLevel51,
                    CodecProfileLevel.AVCProfileHigh10 to CodecProfileLevel.AVCLevel41,
                ),
            ),
        ).containsExactly("h264Profile=high", "h264Level=5.1").inOrder()
        // High 10 at the same level costs nothing.
        assertThat(
            VideoProfileLevel.keys(
                MIME_AVC,
                listOf(
                    CodecProfileLevel.AVCProfileHigh to CodecProfileLevel.AVCLevel51,
                    CodecProfileLevel.AVCProfileHigh10 to CodecProfileLevel.AVCLevel51,
                ),
            ),
        ).containsExactly("h264Profile=high10", "h264Level=5.1").inOrder()
        // Main only: Main is the most it has.
        assertThat(
            VideoProfileLevel.keys(
                MIME_AVC,
                listOf(
                    CodecProfileLevel.AVCProfileBaseline to CodecProfileLevel.AVCLevel52,
                    CodecProfileLevel.AVCProfileMain to CodecProfileLevel.AVCLevel4,
                ),
            ),
        ).containsExactly("h264Profile=main", "h264Level=4").inOrder()
    }

    @Test
    fun `Constrained Baseline is baseline and Constrained High doesn't claim High`() {
        assertThat(
            VideoProfileLevel.keys(MIME_AVC, listOf(CodecProfileLevel.AVCProfileConstrainedBaseline to CodecProfileLevel.AVCLevel31)),
        ).containsExactly("h264Profile=baseline", "h264Level=3.1").inOrder()
        // Constrained High has no B-frames: it doesn't decode Main or High.
        assertThat(
            VideoProfileLevel.keys(MIME_AVC, listOf(CodecProfileLevel.AVCProfileConstrainedHigh to CodecProfileLevel.AVCLevel51)),
        ).containsExactly("h264Profile=baseline", "h264Level=5.1").inOrder()
        // Nor does it lift High's level.
        assertThat(
            VideoProfileLevel.keys(
                MIME_AVC,
                listOf(
                    CodecProfileLevel.AVCProfileConstrainedHigh to CodecProfileLevel.AVCLevel52,
                    CodecProfileLevel.AVCProfileHigh to CodecProfileLevel.AVCLevel51,
                ),
            ),
        ).containsExactly("h264Profile=high", "h264Level=5.1").inOrder()
    }

    @Test
    fun `HEVC Main10 HDR10 is main10, 8-bit-only HEVC is main`() {
        assertThat(
            VideoProfileLevel.keys(MIME_HEVC, listOf(CodecProfileLevel.HEVCProfileMain10HDR10 to CodecProfileLevel.HEVCMainTierLevel51)),
        ).containsExactly("hevcProfile=main10", "hevcLevel=5.1").inOrder()
        assertThat(
            VideoProfileLevel.keys(MIME_HEVC, listOf(CodecProfileLevel.HEVCProfileMain10HDR10Plus to CodecProfileLevel.HEVCMainTierLevel52)),
        ).containsExactly("hevcProfile=main10", "hevcLevel=5.2").inOrder()
        assertThat(
            VideoProfileLevel.keys(MIME_HEVC, listOf(CodecProfileLevel.HEVCProfileMain to CodecProfileLevel.HEVCMainTierLevel41)),
        ).containsExactly("hevcProfile=main", "hevcLevel=4.1").inOrder()
    }

    @Test
    fun `an HEVC tier is its level`() {
        // The server has no tier: High tier 5.1 and Main tier 5.1 are both 5.1.
        val main = VideoProfileLevel.keys(MIME_HEVC, listOf(CodecProfileLevel.HEVCProfileMain10 to CodecProfileLevel.HEVCMainTierLevel51))
        val high = VideoProfileLevel.keys(MIME_HEVC, listOf(CodecProfileLevel.HEVCProfileMain10 to CodecProfileLevel.HEVCHighTierLevel51))
        assertThat(high).isEqualTo(main)
        assertThat(high).contains("hevcLevel=5.1")
        // Main tier 6.2 beats High tier 5.1.
        assertThat(
            VideoProfileLevel.keys(
                MIME_HEVC,
                listOf(
                    CodecProfileLevel.HEVCProfileMain10 to CodecProfileLevel.HEVCHighTierLevel51,
                    CodecProfileLevel.HEVCProfileMain10 to CodecProfileLevel.HEVCMainTierLevel62,
                ),
            ),
        ).contains("hevcLevel=6.2")
    }

    @Test
    fun `a codec without a hardware decoder sends no keys`() {
        // No HEVC or AV1 entry in the map (no hardware decoder): not gated.
        val keys = VideoProfileLevel.headerKeys(
            mapOf(MIME_AVC to listOf(CodecProfileLevel.AVCProfileHigh to CodecProfileLevel.AVCLevel41)),
        )
        assertThat(keys).containsExactly("h264Profile=high", "h264Level=4.1").inOrder()
        // A hardware decoder reporting nothing is the same.
        assertThat(VideoProfileLevel.headerKeys(mapOf(MIME_HEVC to emptyList()))).isEmpty()
        assertThat(VideoProfileLevel.headerKeys(emptyMap())).isEmpty()
    }

    @Test
    fun `unknown profiles, levels and types are ignored`() {
        // H.264 Extended isn't on the server's ladder; HEVC Main Still isn't
        // video; a constant this build doesn't know is never guessed.
        assertThat(VideoProfileLevel.keys(MIME_AVC, listOf(CodecProfileLevel.AVCProfileExtended to CodecProfileLevel.AVCLevel51)))
            .isEmpty()
        assertThat(VideoProfileLevel.keys(MIME_HEVC, listOf(CodecProfileLevel.HEVCProfileMainStill to CodecProfileLevel.HEVCMainTierLevel51)))
            .isEmpty()
        assertThat(VideoProfileLevel.keys(MIME_AVC, listOf(0x40000000 to CodecProfileLevel.AVCLevel51))).isEmpty()
        assertThat(VideoProfileLevel.keys(MIME_AVC, listOf(CodecProfileLevel.AVCProfileHigh to 0x40000000))).isEmpty()
        assertThat(VideoProfileLevel.keys(MIME_AVC, listOf(CodecProfileLevel.AVCProfileHigh to 0))).isEmpty()
        // Known entries beside unknown ones still count.
        assertThat(
            VideoProfileLevel.keys(
                MIME_AVC,
                listOf(
                    CodecProfileLevel.AVCProfileExtended to CodecProfileLevel.AVCLevel52,
                    CodecProfileLevel.AVCProfileHigh to 0x40000000,
                    CodecProfileLevel.AVCProfileMain to CodecProfileLevel.AVCLevel4,
                ),
            ),
        ).containsExactly("h264Profile=main", "h264Level=4").inOrder()
        // VP9 isn't profile/level gated by the server.
        assertThat(
            VideoProfileLevel.keys("video/x-vnd.on2.vp9", listOf(CodecProfileLevel.VP9Profile0 to CodecProfileLevel.VP9Level51)),
        ).isEmpty()
    }

    @Test
    fun `levels are written the way the server reads them`() {
        assertThat(VideoProfileLevel.formatLevel(41)).isEqualTo("4.1")
        assertThat(VideoProfileLevel.formatLevel(50)).isEqualTo("5")
        assertThat(VideoProfileLevel.formatLevel(62)).isEqualTo("6.2")
        assertThat(VideoProfileLevel.formatLevel(10)).isEqualTo("1")
        // AV1 levels run to 7.3 (seq_level_idx 23).
        assertThat(VideoProfileLevel.keys(MIME_AV1, listOf(CodecProfileLevel.AV1ProfileMain10 to CodecProfileLevel.AV1Level73)))
            .containsExactly("av1Profile=main", "av1Level=7.3").inOrder()
        // H.264 level 1b reads as 1.
        assertThat(VideoProfileLevel.keys(MIME_AVC, listOf(CodecProfileLevel.AVCProfileBaseline to CodecProfileLevel.AVCLevel1b)))
            .containsExactly("h264Profile=baseline", "h264Level=1").inOrder()
    }
}
