package tv.onscreen.mobile.playback

import com.google.common.truth.Truth.assertThat
import org.junit.After
import org.junit.Test

/** Eviction behaviour of the bounded credential map. Kept in step with the TV
 *  client's StreamTokenVaultTest (clients/android). split() is not covered
 *  here: this client's version parses with android.net.Uri, which is stubbed
 *  in JVM unit tests. */
class StreamTokenVaultTest {

    @After
    fun tearDown() = StreamTokenVault.clear()

    private fun url(i: Int) = "http://srv/media/stream/f$i"

    @Test
    fun `register returns the clean url and clear drops every credential`() {
        val u = StreamTokenVault.register(url(1), "st")
        assertThat(u).isEqualTo(url(1))
        assertThat(StreamTokenVault.tokenForTest(u)).isEqualTo("st")
        // Blank tokens (offline file:// sources) register nothing.
        StreamTokenVault.register("file:///local.flac", null)
        assertThat(StreamTokenVault.tokenForTest("file:///local.flac")).isNull()
        StreamTokenVault.clear()
        assertThat(StreamTokenVault.tokenForTest(u)).isNull()
    }

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
        val last = cap * 3 - 1
        assertThat(StreamTokenVault.tokenForTest(url(last - cap + 1))).isEqualTo("t${last - cap + 1}")
        assertThat(StreamTokenVault.tokenForTest(url(last - cap))).isNull()
        assertThat(StreamTokenVault.tokenForTest(url(0))).isNull()
    }

    @Test
    fun `a credential the player keeps resolving survives eviction`() {
        val cap = StreamTokenVault.MAX_ENTRIES
        // The playing track is registered first, so by insertion order it
        // would be the first to go.
        val playing = StreamTokenVault.register(url(0), "live")
        for (i in 1 until cap) StreamTokenVault.register(url(i), "t$i")
        // A seek / reconnect resolves it again: most recently used.
        assertThat(StreamTokenVault.resolve(playing)).isEqualTo("live")

        for (i in cap until cap + 5) StreamTokenVault.register(url(i), "t$i")

        assertThat(StreamTokenVault.tokenForTest(playing)).isEqualTo("live")
        for (i in 1..5) assertThat(StreamTokenVault.tokenForTest(url(i))).isNull()
        assertThat(StreamTokenVault.tokenForTest(url(6))).isEqualTo("t6")
    }

    @Test
    fun `re-registering an existing url refreshes it instead of growing the map`() {
        val cap = StreamTokenVault.MAX_ENTRIES
        for (i in 0 until cap) StreamTokenVault.register(url(i), "t$i")
        StreamTokenVault.register(url(0), "rotated")
        StreamTokenVault.register(url(cap), "new")

        assertThat(StreamTokenVault.tokenForTest(url(0))).isEqualTo("rotated")
        assertThat(StreamTokenVault.tokenForTest(url(cap))).isEqualTo("new")
        assertThat(StreamTokenVault.tokenForTest(url(1))).isNull()
    }

    @Test
    fun `concurrent register and resolve stay bounded and consistent`() {
        // The service's player and the UI's player resolve on their own
        // loader threads while PlayerViewModel registers on another.
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
