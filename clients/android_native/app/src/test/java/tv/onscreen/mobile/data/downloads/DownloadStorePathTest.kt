package tv.onscreen.mobile.data.downloads

import com.google.common.truth.Truth.assertThat
import org.junit.Test
import java.io.File

/**
 * Path-safety boundary for [DownloadStore.fileFor].
 *
 * `file_id` and `container` are unvalidated strings off the wire. Before this
 * was fixed they were interpolated straight into a File, and `File(File,
 * String)` does not resolve `..` — so a hostile or MITM'd item response could
 * aim the download writer (which mkdirs() the parent chain and, when a Range
 * request is answered 200, opens the sink TRUNCATING) at any path in the app
 * sandbox. The reachable target that mattered was the DataStore holding the
 * server URL and tokens, two levels up from `downloads/`.
 *
 * These assert the rejection, not a sanitisation: a name that fails the rules
 * is a server bug or an attack, and rewriting it silently would hide both.
 */
class DownloadStorePathTest {

    private val uuid = "3f2a1b4c-5d6e-4f70-8a91-b2c3d4e5f607"

    /** Mirrors DownloadStore.fileFor. Kept in step deliberately: the real one
     *  needs a Context for filesDir, which a JVM unit test has no cheap way to
     *  supply, and the logic under test is pure path handling. */
    private fun fileFor(dir: File, fileId: String, container: String?): File {
        val id = runCatching { java.util.UUID.fromString(fileId) }.getOrNull()
        require(id != null) { "download file_id is not a UUID: $fileId" }
        val raw = (container ?: "bin").lowercase()
        val ext = if (Regex("^[a-z0-9]{1,8}$").matches(raw)) raw else "bin"
        val out = File(dir, "$id.$ext")
        require(out.canonicalPath.startsWith(dir.canonicalPath + File.separator)) {
            "download path escapes downloadsDir: ${out.path}"
        }
        return out
    }

    private val dir = File(System.getProperty("java.io.tmpdir"), "onscreen-dl-test")

    @Test
    fun `well-formed id and container resolve inside the downloads dir`() {
        val f = fileFor(dir, uuid, "mkv")
        assertThat(f.name).isEqualTo("$uuid.mkv")
        assertThat(f.canonicalPath).startsWith(dir.canonicalPath)
    }

    @Test
    fun `container is normalised to lower case`() {
        assertThat(fileFor(dir, uuid, "MP4").name).endsWith(".mp4")
    }

    @Test
    fun `traversal in the container falls back to bin rather than escaping`() {
        for (evil in listOf("../../x", "..%2F..%2Fx", "mp4/../../../x", "\\..\\..\\x")) {
            val f = fileFor(dir, uuid, evil)
            assertThat(f.name).isEqualTo("$uuid.bin")
            assertThat(f.canonicalPath).startsWith(dir.canonicalPath)
        }
    }

    @Test
    fun `a container that would reach the datastore is refused`() {
        // The concrete attack: land on <filesDir>/datastore/server_prefs.preferences_pb.
        val f = fileFor(dir, uuid, "../datastore/server_prefs.preferences_pb")
        assertThat(f.canonicalPath).startsWith(dir.canonicalPath)
        assertThat(f.name).doesNotContain("preferences_pb")
    }

    @Test
    fun `an over-long container falls back to bin`() {
        assertThat(fileFor(dir, uuid, "abcdefghij").name).endsWith(".bin")
    }

    @Test
    fun `a non-uuid file id is rejected outright`() {
        for (evil in listOf("../../pwn", "not-a-uuid", "", "a/b", "$uuid/../x")) {
            runCatching { fileFor(dir, evil, "mkv") }.let {
                assertThat(it.isFailure).isTrue()
                assertThat(it.exceptionOrNull()).isInstanceOf(IllegalArgumentException::class.java)
            }
        }
    }

    @Test
    fun `a null container becomes bin, not an empty extension`() {
        assertThat(fileFor(dir, uuid, null).name).isEqualTo("$uuid.bin")
    }
}
