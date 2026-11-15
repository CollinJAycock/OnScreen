package tv.onscreen.mobile.playback

import com.google.common.truth.Truth.assertThat
import org.junit.Test

/** A car host is a known package name, owned by the controller's uid, signed
 *  by a key known for it (or the platform key, or shipped in the system
 *  image). The name alone let a sideloaded app browse the library. */
class CarHostsTest {

    private val auto = "com.google.android.projection.gearhead"
    private val automotive = "com.android.car.media"
    private val autoRelease = "fdb00c43dbde8b51cb312aa81d3b5fa17713adb94b28f598d77f8eb89daceedf"

    private class Facts(
        val owners: Map<Int, Set<String>> = emptyMap(),
        val digests: Map<String, Set<String>> = emptyMap(),
        val platform: Set<String> = emptySet(),
        val system: Set<String> = emptySet(),
        val missing: Set<String> = emptySet(),
    ) : CarHosts.PackageFacts {
        private fun present(p: String) { if (p in missing) throw IllegalArgumentException("not installed: $p") }
        override fun uidOwns(uid: Int, packageName: String) = owners[uid].orEmpty().contains(packageName)
        override fun certDigests(packageName: String): Set<String> { present(packageName); return digests[packageName].orEmpty() }
        override fun platformSigned(packageName: String): Boolean { present(packageName); return packageName in platform }
        override fun systemApp(packageName: String): Boolean { present(packageName); return packageName in system }
    }

    @Test
    fun `android auto signed with its key is a car host`() {
        val facts = Facts(digests = mapOf(auto to setOf("0".repeat(64), autoRelease)))
        assertThat(CarHosts.isCarHost(facts, auto, uid = 10_100, nameVerified = true)).isTrue()
    }

    @Test
    fun `a sideloaded app using a car host's name is not`() {
        // Its own key, not the platform's, not preinstalled.
        val facts = Facts(digests = mapOf(automotive to setOf("ab".repeat(32))))
        assertThat(CarHosts.isCarHost(facts, automotive, uid = 10_200, nameVerified = true)).isFalse()
        assertThat(CarHosts.isCarHost(Facts(digests = mapOf(auto to setOf("cd".repeat(32)))), auto, 10_200, true)).isFalse()
    }

    @Test
    fun `a platform-signed or preinstalled host passes`() {
        assertThat(CarHosts.isCarHost(Facts(platform = setOf(automotive)), automotive, 1_000, nameVerified = true)).isTrue()
        assertThat(
            CarHosts.isCarHost(Facts(system = setOf("com.google.android.carassistant")), "com.google.android.carassistant", 10_050, true),
        ).isTrue()
    }

    @Test
    fun `an unverified name must belong to the controller's uid`() {
        val facts = Facts(owners = mapOf(10_100 to setOf(auto)), digests = mapOf(auto to setOf(autoRelease)))
        assertThat(CarHosts.isCarHost(facts, auto, uid = 10_100, nameVerified = false)).isTrue()
        // Another app's uid claiming Android Auto's name over the platform session.
        assertThat(CarHosts.isCarHost(facts, auto, uid = 10_666, nameVerified = false)).isFalse()
    }

    @Test
    fun `any other package name is not a car host, whatever it is signed with`() {
        val facts = Facts(platform = setOf("com.example.player"), system = setOf("com.example.player"))
        assertThat(CarHosts.isCarHost(facts, "com.example.player", 1_000, nameVerified = true)).isFalse()
    }

    @Test
    fun `a host that is not installed is not one`() {
        assertThat(CarHosts.isCarHost(Facts(missing = setOf(auto)), auto, 10_100, nameVerified = true)).isFalse()
    }

    @Test
    fun `digests are lower-case sha-256 hex`() {
        assertThat(CarHosts.sha256Hex("abc".toByteArray()))
            .isEqualTo("ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad")
    }
}
