package tv.onscreen.mobile.data.downloads

import android.content.Context
import com.google.common.truth.Truth.assertThat
import com.squareup.moshi.Moshi
import io.mockk.every
import io.mockk.mockk
import kotlinx.coroutines.runBlocking
import org.junit.Rule
import org.junit.Test
import org.junit.rules.TemporaryFolder

/**
 * An involuntary sign-out locks the downloads to the identity signed out
 * (DownloadStore.lock), and a sign-in claims them back only for that same
 * server + user (DownloadStore.claim). Before, the authenticator wiped every
 * file on any rejected refresh — including one whose answer was lost in a
 * network handoff — and nothing stopped a queued job.
 */
class DownloadStoreLockTest {

    @get:Rule val tmp = TemporaryFolder()

    private val alice = DownloadOwner("https://media.example.com", "u-alice")
    private val bob = DownloadOwner("https://media.example.com", "u-bob")

    private fun store(): DownloadStore {
        val context = mockk<Context>()
        every { context.filesDir } returns tmp.root
        return DownloadStore(context, Moshi.Builder().build())
    }

    private fun entry(n: Int, status: String) = DownloadEntry(
        file_id = "00000000-0000-4000-8000-00000000000$n",
        item_id = "item-$n",
        item_title = "Item $n",
        item_type = "movie",
        container = "mkv",
        size_bytes = 10,
        downloaded_bytes = 10,
        status = status,
    )

    /** A store holding a completed and a still-downloading file, both on disk. */
    private fun seeded(): Pair<DownloadStore, List<DownloadEntry>> = runBlocking {
        val s = store()
        assertThat(s.claim(alice)).isTrue()
        val done = entry(1, DownloadStatus.COMPLETED)
        val partial = entry(2, DownloadStatus.DOWNLOADING)
        for (e in listOf(done, partial)) {
            s.upsert(e)
            s.fileFor(e).writeText("bytes")
        }
        s to listOf(done, partial)
    }

    @Test
    fun `lock hides completed files and drops unfinished ones`() = runBlocking {
        val (s, entries) = seeded()
        val (done, partial) = entries
        val orphan = java.io.File(s.downloadsDir, "stray.bin").apply { writeText("x") }

        s.lock(alice)

        assertThat(s.isLocked).isTrue()
        assertThat(s.state.value.entries).isEmpty()
        assertThat(s.get(done.file_id)).isNull()
        assertThat(s.fileFor(done).exists()).isTrue()
        assertThat(s.fileFor(partial).exists()).isFalse()
        assertThat(orphan.exists()).isFalse()
    }

    @Test
    fun `nothing writes to a locked store`() = runBlocking {
        val (s, entries) = seeded()
        s.lock(alice)

        s.upsert(entry(3, DownloadStatus.QUEUED))
        s.update(entries[0].file_id) { it?.copy(status = DownloadStatus.FAILED) ?: entry(4, DownloadStatus.QUEUED) }
        s.remove(entries[0].file_id)

        assertThat(s.claim(alice)).isTrue()
        assertThat(s.state.value.entries.map { it.file_id to it.status })
            .containsExactly(entries[0].file_id to DownloadStatus.COMPLETED)
        assertThat(s.fileFor(entries[0]).exists()).isTrue()
    }

    @Test
    fun `the same identity gets its downloads back, across a restart`() = runBlocking {
        val (s, entries) = seeded()
        s.lock(alice)

        val restarted = store()
        restarted.load()
        assertThat(restarted.state.value.entries).isEmpty()

        assertThat(restarted.claim(alice)).isTrue()
        assertThat(restarted.isLocked).isFalse()
        assertThat(restarted.get(entries[0].file_id)?.status).isEqualTo(DownloadStatus.COMPLETED)
    }

    @Test
    fun `an http origin later upgraded to https is still the same server`() = runBlocking {
        val (s, entries) = seeded()
        s.lock(DownloadOwner("http://media.example.com", "u-alice"))
        // seeded() stamped https at its claim; lock keeps the owner it has.
        assertThat(s.claim(DownloadOwner("http://media.example.com:80", "u-alice"))).isTrue()
        assertThat(s.get(entries[0].file_id)).isNotNull()
    }

    @Test
    fun `another identity is refused and the files stay for the caller to wipe`() = runBlocking {
        val (s, entries) = seeded()
        s.lock(alice)

        assertThat(s.claim(bob)).isFalse()
        assertThat(s.isLocked).isTrue()
        assertThat(s.state.value.entries).isEmpty()
        assertThat(s.claim(DownloadOwner("https://other.example", "u-alice"))).isFalse()

        s.clearAll()
        assertThat(s.fileFor(entries[0]).exists()).isFalse()
        assertThat(s.claim(bob)).isTrue()
        assertThat(s.isLocked).isFalse()
    }

    @Test
    fun `a lock stamps the owner on a manifest that has none`() = runBlocking {
        // A manifest written before owners existed.
        val s = store()
        s.upsert(entry(1, DownloadStatus.COMPLETED))
        s.lock(alice)

        assertThat(s.claim(bob)).isFalse()
        assertThat(s.claim(alice)).isTrue()
    }

    @Test
    fun `downloads with no known owner are not handed to a sign-in`() = runBlocking {
        val s = store()
        s.upsert(entry(1, DownloadStatus.COMPLETED))
        s.lock(null)

        assertThat(s.claim(alice)).isFalse()
    }

    @Test
    fun `an empty store is claimed by anyone`() = runBlocking {
        val s = store()
        assertThat(s.claim(bob)).isTrue()
        assertThat(s.state.value.owner).isEqualTo(bob)
    }
}
