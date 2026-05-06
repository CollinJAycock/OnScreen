package tv.onscreen.mobile.cast

import com.google.common.truth.Truth.assertThat
import org.junit.Test

/**
 * Pure unit tests for [CastMediaInfo]. Mirrors the web client's
 * `cast.test.ts` coverage 1:1 so the two clients can't drift on the
 * MIME mapping or LOAD payload shape.
 */
class CastMediaInfoTest {

    private val goodFile = CastMediaInfo.File(
        id = "file-uuid",
        status = "active",
        container = "mp4",
        videoCodec = "h264",
        audioCodec = "aac",
        durationSeconds = 5400,
        streamToken = "tok-abc",
    )

    private val goodItem = CastMediaInfo.Item(
        id = "item-uuid",
        type = "movie",
        title = "The Movie",
        posterPath = "/artwork/poster.jpg",
        parentTitle = null,
    )

    // ── contentType ────────────────────────────────────────────────────────

    @Test
    fun `H264 mp4 maps to videompfour`() {
        assertThat(CastMediaInfo.contentType(goodFile)).isEqualTo("video/mp4")
    }

    @Test
    fun `HEVC mp4 maps to videompfour - Cast Ultra path`() {
        assertThat(CastMediaInfo.contentType(goodFile.copy(videoCodec = "hevc"))).isEqualTo("video/mp4")
        assertThat(CastMediaInfo.contentType(goodFile.copy(videoCodec = "h265"))).isEqualTo("video/mp4")
    }

    @Test
    fun `m4v and mov containers map to videompfour`() {
        assertThat(CastMediaInfo.contentType(goodFile.copy(container = "m4v"))).isEqualTo("video/mp4")
        assertThat(CastMediaInfo.contentType(goodFile.copy(container = "mov"))).isEqualTo("video/mp4")
    }

    @Test
    fun `webm container maps only when explicitly webm`() {
        assertThat(CastMediaInfo.contentType(goodFile.copy(container = "webm"))).isEqualTo("video/webm")
    }

    @Test
    fun `AV1 and VP9 video are unsupported`() {
        assertThat(CastMediaInfo.contentType(goodFile.copy(videoCodec = "av1"))).isEqualTo("")
        assertThat(CastMediaInfo.contentType(goodFile.copy(videoCodec = "vp9"))).isEqualTo("")
    }

    @Test
    fun `MKV container is not Default Receiver supported`() {
        assertThat(CastMediaInfo.contentType(goodFile.copy(container = "mkv"))).isEqualTo("")
    }

    @Test
    fun `audio-only mp3 - aac - flac - opus map to expected MIMEs`() {
        val audio = goodFile.copy(videoCodec = null)
        assertThat(CastMediaInfo.contentType(audio.copy(audioCodec = "mp3", container = "mp3")))
            .isEqualTo("audio/mp3")
        assertThat(CastMediaInfo.contentType(audio.copy(audioCodec = "aac", container = "m4a")))
            .isEqualTo("audio/mp4")
        assertThat(CastMediaInfo.contentType(audio.copy(audioCodec = "flac", container = "flac")))
            .isEqualTo("audio/flac")
        assertThat(CastMediaInfo.contentType(audio.copy(audioCodec = "opus", container = "ogg")))
            .isEqualTo("audio/ogg")
    }

    @Test
    fun `case-insensitive on container and codec`() {
        assertThat(CastMediaInfo.contentType(goodFile.copy(container = "MP4", videoCodec = "H264")))
            .isEqualTo("video/mp4")
    }

    // ── metadataType ──────────────────────────────────────────────────────

    @Test
    fun `metadata type maps to Cast constants`() {
        assertThat(CastMediaInfo.metadataType(goodItem.copy(type = "movie")))
            .isEqualTo(CastMediaInfo.METADATA_TYPE_MOVIE)
        assertThat(CastMediaInfo.metadataType(goodItem.copy(type = "episode")))
            .isEqualTo(CastMediaInfo.METADATA_TYPE_TV_SHOW)
        assertThat(CastMediaInfo.metadataType(goodItem.copy(type = "show")))
            .isEqualTo(CastMediaInfo.METADATA_TYPE_TV_SHOW)
        assertThat(CastMediaInfo.metadataType(goodItem.copy(type = "track")))
            .isEqualTo(CastMediaInfo.METADATA_TYPE_MUSIC_TRACK)
        assertThat(CastMediaInfo.metadataType(goodItem.copy(type = "photo")))
            .isEqualTo(CastMediaInfo.METADATA_TYPE_PHOTO)
    }

    @Test
    fun `unknown item type falls back to GENERIC`() {
        assertThat(CastMediaInfo.metadataType(goodItem.copy(type = "audiobook")))
            .isEqualTo(CastMediaInfo.METADATA_TYPE_GENERIC)
        assertThat(CastMediaInfo.metadataType(goodItem.copy(type = "wat")))
            .isEqualTo(CastMediaInfo.METADATA_TYPE_GENERIC)
    }

    // ── isCastable ────────────────────────────────────────────────────────

    @Test
    fun `castable when active and supported`() {
        assertThat(CastMediaInfo.isCastable(goodFile)).isTrue()
    }

    @Test
    fun `not castable on inactive status`() {
        assertThat(CastMediaInfo.isCastable(goodFile.copy(status = "missing"))).isFalse()
    }

    @Test
    fun `not castable when stream token is empty or null`() {
        assertThat(CastMediaInfo.isCastable(goodFile.copy(streamToken = null))).isFalse()
        assertThat(CastMediaInfo.isCastable(goodFile.copy(streamToken = ""))).isFalse()
    }

    @Test
    fun `not castable for AV1 source`() {
        assertThat(CastMediaInfo.isCastable(goodFile.copy(videoCodec = "av1"))).isFalse()
    }

    @Test
    fun `not castable for MKV container`() {
        assertThat(CastMediaInfo.isCastable(goodFile.copy(container = "mkv"))).isFalse()
    }

    // ── build ─────────────────────────────────────────────────────────────

    @Test
    fun `returns null for a non-castable file`() {
        val got = CastMediaInfo.build(goodItem, goodFile.copy(status = "missing"), "https://o.example")
        assertThat(got).isNull()
    }

    @Test
    fun `builds an absolute stream URL with the token`() {
        val got = CastMediaInfo.build(goodItem, goodFile, "https://o.example")
        assertThat(got).isNotNull()
        assertThat(got!!.contentId).isEqualTo("https://o.example/media/stream/file-uuid?token=tok-abc")
    }

    @Test
    fun `strips trailing slashes from baseUrl`() {
        val one = CastMediaInfo.build(goodItem, goodFile, "https://o.example/")
        val many = CastMediaInfo.build(goodItem, goodFile, "https://o.example///")
        assertThat(one!!.contentId).isEqualTo("https://o.example/media/stream/file-uuid?token=tok-abc")
        assertThat(many!!.contentId).isEqualTo("https://o.example/media/stream/file-uuid?token=tok-abc")
    }

    @Test
    fun `URL-encodes file id and stream token`() {
        val got = CastMediaInfo.build(
            goodItem,
            goodFile.copy(id = "a/b c", streamToken = "tok with space"),
            "https://o.example",
        )
        assertThat(got!!.contentId).isEqualTo(
            "https://o.example/media/stream/a%2Fb%20c?token=tok%20with%20space",
        )
    }

    @Test
    fun `contentType comes from the MIME map`() {
        val got = CastMediaInfo.build(goodItem, goodFile, "https://o.example")
        assertThat(got!!.contentType).isEqualTo("video/mp4")
    }

    @Test
    fun `streamType is BUFFERED (VOD)`() {
        val got = CastMediaInfo.build(goodItem, goodFile, "https://o.example")
        assertThat(got!!.streamType).isEqualTo("BUFFERED")
    }

    @Test
    fun `metadataType matches metadataType helper`() {
        val got = CastMediaInfo.build(goodItem, goodFile, "https://o.example")
        assertThat(got!!.metadataType).isEqualTo(CastMediaInfo.METADATA_TYPE_MOVIE)
    }

    @Test
    fun `includes poster as absolute URL when posterPath is present`() {
        val got = CastMediaInfo.build(goodItem, goodFile, "https://o.example")
        assertThat(got!!.imageUrls).containsExactly("https://o.example/artwork/poster.jpg")
    }

    @Test
    fun `omits images when posterPath is null`() {
        val got = CastMediaInfo.build(goodItem.copy(posterPath = null), goodFile, "https://o.example")
        assertThat(got!!.imageUrls).isEmpty()
    }

    @Test
    fun `parentTitle becomes the subtitle (e g show title for an episode)`() {
        val ep = CastMediaInfo.Item(
            id = "e1",
            type = "episode",
            title = "The Episode",
            parentTitle = "The Show",
        )
        val got = CastMediaInfo.build(ep, goodFile, "https://o.example")
        assertThat(got!!.subtitle).isEqualTo("The Show")
    }

    @Test
    fun `attaches duration when available`() {
        val got = CastMediaInfo.build(goodItem, goodFile, "https://o.example")
        assertThat(got!!.durationSeconds).isEqualTo(5400)
    }

    @Test
    fun `customData carries item and file ids for the receiver`() {
        val got = CastMediaInfo.build(goodItem, goodFile, "https://o.example")
        assertThat(got!!.customData).containsExactly("itemId", "item-uuid", "fileId", "file-uuid")
    }
}
