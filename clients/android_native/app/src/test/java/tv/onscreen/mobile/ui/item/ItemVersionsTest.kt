package tv.onscreen.mobile.ui.item

import com.google.common.truth.Truth.assertThat
import org.junit.Test
import tv.onscreen.mobile.data.model.ItemFile

/** Editions and stacked (multi-part) movies, v2.6. */
class ItemVersionsTest {

    private fun file(
        id: String,
        edition: String? = null,
        part: Int? = null,
        container: String = "mkv",
        w: Int? = 1920,
        h: Int? = 1080,
        codec: String? = "h264",
        hdr: String? = null,
    ) = ItemFile(
        id = id, stream_url = "/media/stream/$id", container = container,
        video_codec = codec, resolution_w = w, resolution_h = h, hdr_type = hdr,
        edition = edition, part_index = part,
    )

    @Test
    fun `an edition names its version`() {
        val versions = ItemVersions.of(listOf(file("a", edition = "Director's Cut"), file("b", edition = "Theatrical")))
        assertThat(versions.map { it.label }).containsExactly("Director's Cut", "Theatrical").inOrder()
        assertThat(versions.map { it.fileId }).containsExactly("a", "b").inOrder()
    }

    @Test
    fun `a file without an edition is described by its picture`() {
        val versions = ItemVersions.of(
            listOf(
                file("uhd", w = 3840, h = 2160, codec = "hevc", hdr = "hdr10"),
                file("hd"),
                file("scope", w = 1920, h = 800),
            ),
        )
        assertThat(versions.map { it.label })
            .containsExactly("4K · HEVC · HDR", "1080p · H.264", "1080p · H.264").inOrder()
    }

    @Test
    fun `the parts of a stacked movie are one version`() {
        // Server order may put part 2 first; the version plays from part 1.
        val versions = ItemVersions.of(listOf(file("cd2", part = 2), file("cd1", part = 1)))
        assertThat(versions).hasSize(1)
        val v = versions.single()
        assertThat(v.fileId).isEqualTo("cd1")
        assertThat(v.files.map { it.id }).containsExactly("cd1", "cd2").inOrder()
        assertThat(v.stacked).isTrue()
        assertThat(v.label).isEqualTo("1080p · H.264 · 2 parts")
    }

    @Test
    fun `a stacked edition beside a single-file one makes two versions`() {
        val versions = ItemVersions.of(
            listOf(
                file("ext1", edition = "Extended", part = 1),
                file("theatrical", edition = "Theatrical"),
                file("ext2", edition = "Extended", part = 2),
            ),
        )
        assertThat(versions.map { it.label })
            .containsExactly("Extended · 2 parts", "Theatrical").inOrder()
        assertThat(versions.first().files.map { it.id }).containsExactly("ext1", "ext2").inOrder()
    }

    @Test
    fun `stacks of different editions stay apart`() {
        val versions = ItemVersions.of(
            listOf(
                file("a1", edition = "A", part = 1), file("a2", edition = "A", part = 2),
                file("b1", edition = "B", part = 1), file("b2", edition = "B", part = 2),
            ),
        )
        assertThat(versions.map { it.fileId }).containsExactly("a1", "b1").inOrder()
        assertThat(versions.all { it.partCount == 2 }).isTrue()
    }

    @Test
    fun `pick finds the version of any of its files, else the first`() {
        val versions = ItemVersions.of(listOf(file("x"), file("cd1", part = 1), file("cd2", part = 2)))
        assertThat(ItemVersions.pick(versions, "cd2")?.fileId).isEqualTo("cd1")
        assertThat(ItemVersions.pick(versions, "gone")?.fileId).isEqualTo("x")
        assertThat(ItemVersions.pick(versions, null)?.fileId).isEqualTo("x")
        assertThat(ItemVersions.pick(emptyList(), "x")).isNull()
    }

    @Test
    fun `a single file is a single version`() {
        val versions = ItemVersions.of(listOf(file("only", w = null, h = null, codec = null)))
        assertThat(versions.single().label).isEqualTo("Version")
        assertThat(versions.single().stacked).isFalse()
    }
}
