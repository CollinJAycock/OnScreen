package tv.onscreen.android.ui.playback

import com.google.common.truth.Truth.assertThat
import org.junit.Test

class PlayerStackTest {

    /**
     * The activity's FragmentManager in miniature: one container, and entries
     * that each record the screens a replace took out and the one it put in.
     * A pop undoes the top entry (its screen out, the ones it took out back
     * in) and with no entries pops nothing. BACK pops, or with no entries
     * leaves the app.
     */
    private class BackStack(root: String) : PlayerStack.Screens<String> {
        val container = mutableListOf(root)
        private val entries = ArrayDeque<Pair<List<String>, String>>()
        var leftApp = false

        override val entryCount: Int get() = entries.size

        override fun pop() {
            val (removed, added) = entries.removeLastOrNull() ?: return
            container.remove(added)
            container.addAll(removed)
        }

        override fun replace(next: String, record: Boolean) {
            val removed = container.toList()
            container.clear()
            container.add(next)
            if (record) entries.addLast(removed to next)
        }

        override fun leaveApp() {
            leftApp = true
        }

        /** Open [screen] over the one up now, as Navigator and DetailFragment do. */
        fun open(screen: String) = replace(screen, record = true)

        /** The remote's BACK. */
        fun back() {
            if (entries.isEmpty()) leftApp = true else pop()
        }
    }

    @Test
    fun `BACK after moving on from a root player leaves the app`() {
        // A Watch Next tile or a transfer: the player is the root screen.
        val stack = BackStack("episode 1")
        PlayerStack.replaceSelf(stack, "episode 2")
        assertThat(stack.container).containsExactly("episode 2")

        stack.back()
        // Not episode 1 back on screen, playing again.
        assertThat(stack.leftApp).isTrue()
        assertThat(stack.container).containsExactly("episode 2")
    }

    @Test
    fun `a chain of advances from a root player never comes back to an earlier item`() {
        val stack = BackStack("track 1")
        PlayerStack.replaceSelf(stack, "track 2")
        PlayerStack.replaceSelf(stack, "track 3")
        assertThat(stack.entryCount).isEqualTo(0)

        stack.back()
        assertThat(stack.leftApp).isTrue()
        assertThat(stack.container).containsExactly("track 3")
    }

    @Test
    fun `BACK after moving on from a player opened in the app returns to the screen before it`() {
        val stack = BackStack("home")
        stack.open("detail")
        stack.open("episode 1")
        PlayerStack.replaceSelf(stack, "episode 2")
        PlayerStack.replaceSelf(stack, "episode 3")
        assertThat(stack.container).containsExactly("episode 3")

        stack.back()
        assertThat(stack.container).containsExactly("detail")
        stack.back()
        assertThat(stack.container).containsExactly("home")
        assertThat(stack.leftApp).isFalse()
    }

    @Test
    fun `leaving a root player leaves the app, as BACK does`() {
        val stack = BackStack("movie")
        PlayerStack.leave(stack)
        assertThat(stack.leftApp).isTrue()
    }

    @Test
    fun `leaving a player opened in the app returns to the screen before it`() {
        val stack = BackStack("home")
        stack.open("detail")
        stack.open("movie")
        PlayerStack.leave(stack)
        assertThat(stack.container).containsExactly("detail")
        assertThat(stack.leftApp).isFalse()
    }

    @Test
    fun `leaving the player an advance put up returns to the screen before the first`() {
        val stack = BackStack("home")
        stack.open("album")
        stack.open("track 1")
        PlayerStack.replaceSelf(stack, "track 2")
        PlayerStack.leave(stack)
        assertThat(stack.container).containsExactly("album")
        assertThat(stack.leftApp).isFalse()
    }
}
