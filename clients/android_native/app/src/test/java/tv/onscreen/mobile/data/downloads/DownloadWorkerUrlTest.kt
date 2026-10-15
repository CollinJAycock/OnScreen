package tv.onscreen.mobile.data.downloads

import com.google.common.truth.Truth.assertThat
import org.junit.Test

/** Where an original download comes from, and what a refusal shows. */
class DownloadWorkerUrlTest {

    @Test
    fun `an original downloads from the download route, not the stream`() {
        // /media/stream refuses a video the server converts for this user
        // (409 TRANSCODE_REQUIRED); /media/download serves the file as it is.
        assertThat(DownloadWorker.originalUrl("https://srv", "f1", "st-24h"))
            .isEqualTo("https://srv/media/download/f1?token=st-24h")
    }

    @Test
    fun `downloads turned off says so`() {
        val body = """{"error":{"code":"DOWNLOADS_DISABLED","message":"file downloads are disabled on this server"}}"""
        assertThat(DownloadWorker.refusalMessage(403, body)).isEqualTo(DownloadChoices.DISABLED_MESSAGE)
    }

    @Test
    fun `any other refusal is its status`() {
        assertThat(DownloadWorker.refusalMessage(403, """{"error":{"code":"FORBIDDEN"}}""")).isEqualTo("HTTP 403")
        assertThat(DownloadWorker.refusalMessage(404, null)).isEqualTo("HTTP 404")
        assertThat(DownloadWorker.refusalMessage(403, "<html>")).isEqualTo("HTTP 403")
    }
}
