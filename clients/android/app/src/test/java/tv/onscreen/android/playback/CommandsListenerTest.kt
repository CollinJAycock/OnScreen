package tv.onscreen.android.playback

import androidx.media3.common.Player
import com.google.common.truth.Truth.assertThat
import com.google.common.truth.Truth.assertWithMessage
import io.mockk.mockk
import io.mockk.mockkClass
import java.lang.reflect.Proxy
import org.junit.Test

/**
 * Every Player.Listener method is a Java default, so one the wrapper doesn't
 * forward by hand is dropped without a word (Kotlin's `by` delegation skips
 * them all). This calls each one, as the interface declares it, and a Media3
 * upgrade that adds one fails here until it is forwarded too.
 */
class CommandsListenerTest {

    private val heard = mutableListOf<Pair<String, List<Any?>>>()

    /** Records each call by name and arguments. */
    private val listener = Proxy.newProxyInstance(
        Player.Listener::class.java.classLoader,
        arrayOf(Player.Listener::class.java),
    ) { proxy, method, args ->
        when (method.name) {
            "equals" -> proxy === args?.get(0)
            "hashCode" -> System.identityHashCode(proxy)
            "toString" -> "listener"
            else -> {
                heard += method.name to (args?.toList() ?: emptyList())
                null
            }
        }
    } as Player.Listener

    private val offered = mockk<Player.Commands>()
    private val forwarding = CommandsListener(listener) { offered }

    @Test
    fun `every event reaches the listener, the commands as the wrapper offers them`() {
        val methods = Player.Listener::class.java.methods
        assertThat(methods).isNotEmpty()
        for (method in methods) {
            val args = method.parameterTypes.map { argFor(it) }.toTypedArray()
            method.invoke(forwarding, *args)
            val expected = if (method.name == "onAvailableCommandsChanged") listOf(offered) else args.toList()
            assertWithMessage(method.toString()).that(heard).containsExactly(method.name to expected)
            heard.clear()
        }
    }

    private fun argFor(type: Class<*>): Any = when (type) {
        java.lang.Boolean.TYPE -> true
        Integer.TYPE -> 3
        java.lang.Long.TYPE -> 5L
        java.lang.Float.TYPE -> 0.5f
        else -> mockkClass(type.kotlin, relaxed = true)
    }
}
