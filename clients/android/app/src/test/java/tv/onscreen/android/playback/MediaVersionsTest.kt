package tv.onscreen.android.playback

import com.google.common.truth.Truth.assertThat
import org.junit.Test
import tv.onscreen.android.data.model.ItemFile
import tv.onscreen.android.i18n.AppLanguage
import tv.onscreen.android.i18n.TestStrings
import tv.onscreen.android.ui.playback.PlaybackHelper
import tv.onscreen.android.ui.playback.PlaybackMode

/** Editions and stacked (multi-part) movies, server v2.6. */
class MediaVersionsTest {

    /** The labels as the picker shows them (string resources resolved). */
    private fun labels(versions: List<MediaVersions.Version>, tag: String = AppLanguage.ENGLISH) =
        versions.map { TestStrings.resolve(it.label, tag) }

    private fun file(
        id: String,
        h: Int? = 1080,
        codec: String? = "h264",
        hdr: String? = null,
        edition: String? = null,
        part: Int? = null,
        durationMs: Long? = null,
    ) = ItemFile(
        id = id, stream_url = "/media/files/$id", container = "mkv", video_codec = codec,
        resolution_h = h, hdr_type = hdr, edition = edition, part_index = part, duration_ms = durationMs,
    )

    @Test
    fun `the edition labels a version, else what the file is`() {
        val versions = MediaVersions.versions(
            listOf(
                file("a", h = 2160, codec = "hevc", hdr = "hdr10"),
                file("b", edition = "Director's Cut"),
                file("c", h = 720),
            ),
        )

        assertThat(labels(versions))
            .containsExactly("4K · HEVC · HDR10", "Director's Cut", "720p · H.264").inOrder()
        assertThat(versions.map { it.fileId }).containsExactly("a", "b", "c").inOrder()
    }

    @Test
    fun `a stacked movie's parts are one version from its first part`() {
        val versions = MediaVersions.versions(
            listOf(file("cd2", part = 2), file("cd1", part = 1), file("cd3", part = 3)),
        )

        assertThat(versions).hasSize(1)
        assertThat(versions[0].fileId).isEqualTo("cd1")
        assertThat(versions[0].parts).isEqualTo(3)
        assertThat(labels(versions)).containsExactly("1080p · H.264 · 3 parts")
        assertThat(labels(versions, AppLanguage.SPANISH)).containsExactly("1080p · H.264 · 3 partes")
        assertThat(labels(versions, AppLanguage.CHINESE)).containsExactly("1080p · H.264 · 3 个部分")
    }

    @Test
    fun `a stack sits beside the item's other versions`() {
        val versions = MediaVersions.versions(
            listOf(file("remux", h = 2160, edition = "Extended"), file("cd1", part = 1), file("cd2", part = 2)),
        )

        assertThat(labels(versions)).containsExactly("Extended", "1080p · H.264 · 2 parts").inOrder()
    }

    @Test
    fun `versions with nothing to tell them apart are numbered`() {
        val versions = MediaVersions.versions(listOf(file("a", h = null, codec = null), file("b", h = null, codec = null)))

        assertThat(labels(versions)).containsExactly("Version 1", "Version 2").inOrder()
        assertThat(labels(versions, AppLanguage.CHINESE)).containsExactly("版本 1", "版本 2").inOrder()
        // A stack with no label of its own is just its part count.
        val stack = MediaVersions.versions(listOf(file("cd1", h = null, codec = null, part = 1), file("cd2", h = null, codec = null, part = 2)))
        assertThat(labels(stack)).containsExactly("2 parts")
        assertThat(labels(stack, AppLanguage.SPANISH)).containsExactly("2 partes")
    }

    @Test
    fun `playback starts from the picked file, a stack always from its first part`() {
        val files = listOf(file("best"), file("cd2", part = 2), file("cd1", part = 1))

        assertThat(MediaVersions.playFile(files)?.id).isEqualTo("best")
        assertThat(MediaVersions.playFile(files, "cd2")?.id).isEqualTo("cd1")
        assertThat(MediaVersions.playFile(files, "gone")?.id).isEqualTo("best")
        assertThat(MediaVersions.playFile(listOf(file("cd2", part = 2), file("cd1", part = 1)))?.id).isEqualTo("cd1")
        assertThat(MediaVersions.playFile(emptyList())).isNull()
    }

    @Test
    fun `a stacked movie runs the item's length, not its part's`() {
        assertThat(MediaVersions.listedDurationMs(file("cd1", part = 1, durationMs = 3_000_000L), 6_000_000L))
            .isEqualTo(6_000_000L)
        assertThat(MediaVersions.listedDurationMs(file("a", durationMs = 5_000_000L), 4_800_000L))
            .isEqualTo(5_000_000L)
    }

    @Test
    fun `a part is never direct played on its own`() {
        val part = file("cd1", h = 2160, part = 1)

        assertThat(PlaybackHelper.forStack(PlaybackMode.DirectPlay, part)).isEqualTo(PlaybackMode.Transcode(2160))
        assertThat(PlaybackHelper.forStack(PlaybackMode.Remux, part)).isEqualTo(PlaybackMode.Remux)
        assertThat(PlaybackHelper.forStack(PlaybackMode.Transcode(1080), part)).isEqualTo(PlaybackMode.Transcode(1080))
    }
}
