package tv.onscreen.android.playback

import com.google.common.truth.Truth.assertThat
import org.junit.After
import org.junit.Test

class StreamTokenVaultTest {

    @After
    fun tearDown() = StreamTokenVault.clear()

    @Test
    fun `split strips only the token parameter`() {
        val (clean, tok) = StreamTokenVault.split("http://srv/p.m3u8?a=1&token=v4.local.abc_-&b=2")
        assertThat(clean).isEqualTo("http://srv/p.m3u8?a=1&b=2")
        assertThat(tok).isEqualTo("v4.local.abc_-")
    }

    @Test
    fun `split of a token-only query leaves no dangling question mark`() {
        val (clean, tok) = StreamTokenVault.split("http://srv/api/v1/transcode/static/f1/master.m3u8?token=t%2B1")
        assertThat(clean).isEqualTo("http://srv/api/v1/transcode/static/f1/master.m3u8")
        assertThat(tok).isEqualTo("t+1")
    }

    @Test
    fun `split leaves urls without a token untouched`() {
        assertThat(StreamTokenVault.split("http://srv/p.m3u8")).isEqualTo("http://srv/p.m3u8" to null)
        assertThat(StreamTokenVault.split("http://srv/p.m3u8?x=1")).isEqualTo("http://srv/p.m3u8?x=1" to null)
        // A parameter that merely ends in "token" is not the credential.
        assertThat(StreamTokenVault.split("http://srv/p?stream_token=z").second).isNull()
    }

    @Test
    fun `register returns the clean url and clear drops every credential`() {
        val url = StreamTokenVault.register("http://srv/media/stream/f1", "st")
        assertThat(url).isEqualTo("http://srv/media/stream/f1")
        assertThat(StreamTokenVault.tokenForTest(url)).isEqualTo("st")
        // Blank tokens register nothing.
        StreamTokenVault.register("http://srv/none", "")
        assertThat(StreamTokenVault.tokenForTest("http://srv/none")).isNull()
        StreamTokenVault.clear()
        assertThat(StreamTokenVault.tokenForTest(url)).isNull()
    }

    private fun url(i: Int) = "http://srv/media/stream/f$i"

    @Test
    fun `registering past the cap never evicts the newest credential`() {
        // Every insert past the cap must drop the OLDEST entry. The previous
        // ConcurrentHashMap evicted keys.firstOrNull() — hash order — which
        // could be the url just registered, so the prepare that followed went
        // out without its token and 401'd.
        val cap = StreamTokenVault.MAX_ENTRIES
        repeat(cap * 3) { i ->
            StreamTokenVault.register(url(i), "t$i")
            assertThat(StreamTokenVault.tokenForTest(url(i))).isEqualTo("t$i")
        }
        // Exactly the last `cap` registrations survive, oldest gone first.
        val last = cap * 3 - 1
        assertThat(StreamTokenVault.tokenForTest(url(last - cap + 1))).isEqualTo("t${last - cap + 1}")
        assertThat(StreamTokenVault.tokenForTest(url(last - cap))).isNull()
        assertThat(StreamTokenVault.tokenForTest(url(0))).isNull()
    }

    @Test
    fun `a credential the player keeps resolving survives eviction`() {
        val cap = StreamTokenVault.MAX_ENTRIES
        // The playing url is registered first, so by insertion order it would
        // be the first to go.
        val playing = StreamTokenVault.register("http://srv/api/v1/transcode/static/f1/master.m3u8", "live")
        for (i in 1 until cap) StreamTokenVault.register(url(i), "t$i")
        // Playlist re-polls / range requests resolve it: most recently used.
        assertThat(StreamTokenVault.resolve(playing)).isEqualTo("live")

        // Overflow by several entries — each evicts the least-recently-used.
        for (i in cap until cap + 5) StreamTokenVault.register(url(i), "t$i")

        assertThat(StreamTokenVault.tokenForTest(playing)).isEqualTo("live")
        for (i in 1..5) assertThat(StreamTokenVault.tokenForTest(url(i))).isNull()
        assertThat(StreamTokenVault.tokenForTest(url(6))).isEqualTo("t6")
    }

    @Test
    fun `re-registering an existing url refreshes it instead of growing the map`() {
        val cap = StreamTokenVault.MAX_ENTRIES
        for (i in 0 until cap) StreamTokenVault.register(url(i), "t$i")
        // A fresh prepare of url 0 re-registers it (maybe with a rotated token).
        StreamTokenVault.register(url(0), "rotated")
        StreamTokenVault.register(url(cap), "new")

        assertThat(StreamTokenVault.tokenForTest(url(0))).isEqualTo("rotated")
        assertThat(StreamTokenVault.tokenForTest(url(cap))).isEqualTo("new")
        // url 1 was least recently used once url 0 was touched again.
        assertThat(StreamTokenVault.tokenForTest(url(1))).isNull()
    }

    @Test
    fun `concurrent register and resolve stay bounded and consistent`() {
        val cap = StreamTokenVault.MAX_ENTRIES
        val pool = java.util.concurrent.Executors.newFixedThreadPool(8)
        try {
            val jobs = (0 until 8).map { t ->
                pool.submit {
                    repeat(500) { i ->
                        val u = url(t * 1000 + i)
                        StreamTokenVault.register(u, "t")
                        StreamTokenVault.resolve(u)
                    }
                }
            }
            jobs.forEach { it.get() }
        } finally {
            pool.shutdownNow()
        }
        val live = (0 until 8).sumOf { t ->
            (0 until 500).count { i -> StreamTokenVault.tokenForTest(url(t * 1000 + i)) != null }
        }
        assertThat(live).isEqualTo(cap)
    }
}
