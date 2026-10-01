package tv.onscreen.mobile.ui.player

import com.google.common.truth.Truth.assertThat
import org.junit.Test
import tv.onscreen.mobile.data.model.ExternalSubtitle
import tv.onscreen.mobile.data.model.ItemFile
import tv.onscreen.mobile.data.model.SubtitleStream
import tv.onscreen.mobile.playback.StreamTokenVault

class SubtitleTracksTest {

    private fun file(streamToken: String? = "st") = ItemFile(
        id = "file-a",
        stream_url = "/media/files/file-a.mkv",
        stream_token = streamToken,
        subtitle_streams = listOf(
            SubtitleStream(3, "subrip", "eng", "English", false),
            SubtitleStream(4, "subrip", "eng", "English SDH", false, sdh = true),
            SubtitleStream(5, "hdmv_pgs_subtitle", "eng", "", false),
            SubtitleStream(6, "ass", "jpn", "Signs", true),
        ),
        external_subtitles = listOf(
            ExternalSubtitle(id = "e1", language = "spa", title = "Spanish", url = "/media/external-subtitles/e1"),
        ),
    )

    @Test
    fun `builds clean side-load urls with the right credential per route`() {
        val tracks = SubtitleTracks.build("http://srv/", file(), assetToken = "asset", sideLoadEmbedded = true)

        assertThat(tracks.map { it.trackId })
            .containsExactly("sub:emb:3", "sub:emb:4", "sub:emb:5", "sub:emb:6", "sub:ext:e1").inOrder()
        val emb = tracks.first { it.trackId == "sub:emb:3" }
        assertThat(emb.url).isEqualTo("http://srv/media/subtitles/file-a/3")
        assertThat(StreamTokenVault.tokenForTest(emb.url!!)).isEqualTo("st")
        // PGS can't be served as WebVTT.
        assertThat(tracks.first { it.trackId == "sub:emb:5" }.url).isNull()
        // An attached file takes the asset token — the server refuses a
        // file-bound stream token on a route without a file id.
        val ext = tracks.first { it.external }
        assertThat(ext.url).isEqualTo("http://srv/media/external-subtitles/e1")
        assertThat(StreamTokenVault.tokenForTest(ext.url!!)).isEqualTo("asset")
        assertThat(ext.title).isEqualTo("Spanish")
    }

    @Test
    fun `embedded streams fall back to the asset token`() {
        val tracks = SubtitleTracks.build("http://srv2", file(streamToken = null), assetToken = "asset-2", sideLoadEmbedded = true)
        val url = tracks.first { it.trackId == "sub:emb:4" }.url!!
        assertThat(StreamTokenVault.tokenForTest(url)).isEqualTo("asset-2")
    }

    @Test
    fun `no server or no token means no urls, but the tracks are still listed`() {
        val noServer = SubtitleTracks.build("", file(), assetToken = "asset", sideLoadEmbedded = true)
        assertThat(noServer).hasSize(5)
        assertThat(noServer.all { it.url == null }).isTrue()

        val noAsset = SubtitleTracks.build("http://srv", file(), assetToken = null, sideLoadEmbedded = true)
        assertThat(noAsset.first { it.external }.url).isNull()
        assertThat(noAsset.first { it.trackId == "sub:emb:3" }.url).isNotNull()
    }

    @Test
    fun `HLS lists and loads what can be side-loaded, direct play reads the container`() {
        val tracks = SubtitleTracks.build("http://srv", file(), assetToken = "asset", sideLoadEmbedded = true)

        assertThat(SubtitleTracks.rows(tracks, hls = true).map { it.trackId })
            .containsExactly("sub:emb:3", "sub:emb:4", "sub:emb:6", "sub:ext:e1").inOrder()
        assertThat(SubtitleTracks.sideLoads(tracks, hls = true).map { it.trackId })
            .containsExactly("sub:emb:3", "sub:emb:4", "sub:emb:6", "sub:ext:e1").inOrder()

        // Direct play: every embedded stream (PGS included — the container
        // renders it), side-loading only the attached file.
        assertThat(SubtitleTracks.rows(tracks, hls = false).map { it.trackId })
            .containsExactly("sub:emb:3", "sub:emb:4", "sub:emb:5", "sub:emb:6", "sub:ext:e1").inOrder()
        assertThat(SubtitleTracks.sideLoads(tracks, hls = false).map { it.trackId })
            .containsExactly("sub:ext:e1")
    }

    @Test
    fun `direct play registers no url for the streams the container carries`() {
        val tracks = SubtitleTracks.build("http://srv3", file(), assetToken = "asset", sideLoadEmbedded = false)

        assertThat(tracks.filter { !it.external }.all { it.url == null }).isTrue()
        assertThat(StreamTokenVault.tokenForTest("http://srv3/media/subtitles/file-a/3")).isNull()
        assertThat(tracks.first { it.external }.url).isEqualTo("http://srv3/media/external-subtitles/e1")
        // Still listed and pickable — the player reads them from the file.
        assertThat(SubtitleTracks.rows(tracks, hls = false)).hasSize(5)
        assertThat(SubtitleTracks.sideLoads(tracks, hls = false).map { it.trackId }).containsExactly("sub:ext:e1")
    }

    @Test
    fun `an attached file with no url is not offered on direct play either`() {
        val tracks = SubtitleTracks.build("http://srv", file(), assetToken = null, sideLoadEmbedded = true)
        assertThat(SubtitleTracks.rows(tracks, hls = false).none { it.external }).isTrue()
    }

    @Test
    fun `side-load ids are found inside MergingMediaSource's prefixed format ids`() {
        assertThat(SubtitleTracks.sideLoadIdOf("sub:emb:3")).isEqualTo("sub:emb:3")
        assertThat(SubtitleTracks.sideLoadIdOf("1:sub:emb:3")).isEqualTo("sub:emb:3")
        assertThat(SubtitleTracks.sideLoadIdOf("12:sub:ext:e1")).isEqualTo("sub:ext:e1")
        // Container tracks (Matroska track numbers, prefixed or not).
        assertThat(SubtitleTracks.sideLoadIdOf("0:4")).isNull()
        assertThat(SubtitleTracks.sideLoadIdOf("4")).isNull()
        assertThat(SubtitleTracks.sideLoadIdOf(null)).isNull()
    }

    @Test
    fun `container tracks pair one to one when the counts agree`() {
        // Same-language tracks can only be told apart by position.
        assertThat(SubtitleTracks.alignContainer(listOf("eng", "eng", "jpn"), listOf("en", "en", "ja")))
            .containsExactly(0, 1, 2).inOrder()
    }

    @Test
    fun `a stream the player skipped doesn't shift the ones after it`() {
        // ffprobe lists four; the player extracted three (it skipped the
        // Spanish one it can't read).
        val paired = SubtitleTracks.alignContainer(
            listOf("eng", "spa", "fre", "ger"),
            listOf("en", "fr", "de"),
        )
        assertThat(paired).containsExactly(0, null, 1, 2).inOrder()
    }

    @Test
    fun `untagged tracks pair with anything while walking`() {
        val paired = SubtitleTracks.alignContainer(listOf("", "fre", "ger"), listOf(null, "de"))
        assertThat(paired).containsExactly(0, null, 1).inOrder()
    }

    @Test
    fun `labels read like the old picker, plus downloaded files`() {
        val tracks = SubtitleTracks.build("http://srv", file(), assetToken = "asset", sideLoadEmbedded = true)
        assertThat(SubtitleTracks.label(tracks[1])).isEqualTo("eng · English SDH · SDH")
        assertThat(SubtitleTracks.label(tracks[3])).isEqualTo("jpn · Signs · forced")
        assertThat(SubtitleTracks.label(tracks[4])).isEqualTo("spa · Spanish · downloaded")
    }

    @Test
    fun `language matching normalises ffprobe's three-letter codes`() {
        assertThat(SubtitleTracks.languagesMatch("eng", "en")).isTrue()
        assertThat(SubtitleTracks.languagesMatch("ger", "de-DE")).isTrue()
        assertThat(SubtitleTracks.languagesMatch("eng", "es")).isFalse()
        assertThat(SubtitleTracks.languagesMatch("", "")).isFalse()
    }
}
