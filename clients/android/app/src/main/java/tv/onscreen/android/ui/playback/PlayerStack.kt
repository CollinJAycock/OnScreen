package tv.onscreen.android.ui.playback

/**
 * How a player screen gives way to what comes after it: the next player
 * ([replaceSelf]: Up Next's episode, the next track or chapter, the same video
 * reopened after the screensaver), or at the end of playback the screen
 * before it ([leave]).
 *
 * A player opened from inside the app (Navigator, DetailFragment) is pushed
 * over the screen it came from with a back-stack entry of its own. One opened
 * from outside it (a Watch Next tile, "play on this TV":
 * MainActivity.showPlayback) is the root screen: the back stack is dropped
 * and it has no entry. The player is always the top screen, so an entry at
 * all is its own.
 *
 * Moving on as if every player had an entry broke the root one: popping its
 * entry popped nothing, and the push recorded "remove the first player, add
 * the next", so BACK from the next item put the first one back on screen,
 * playing again (an episode then ran into its credits and Up Next once
 * more). Leaving by a pop left the ended player on screen. Pure over
 * [Screens], JVM-tested in PlayerStackTest.
 */
object PlayerStack {

    /** The activity's back stack as a player screen sees it: its
     *  FragmentManager, adapted in PlaybackFragment. */
    interface Screens<S> {
        /** Entries on the back stack. */
        val entryCount: Int

        /** Undo the top entry. */
        fun pop()

        /** Show [next] in place of the screen up now, recorded as an entry
         *  when [record]. */
        fun replace(next: S, record: Boolean)

        /** Leave the app, as BACK from the root screen does. */
        fun leaveApp()
    }

    /** Swap the player screen for [next] as the one record of it: its own
     *  entry popped and [next] pushed in its place, so BACK from [next]
     *  returns to the screen before. A root player's [next] is the root in
     *  turn, and BACK from it leaves the app. */
    fun <S> replaceSelf(screens: Screens<S>, next: S) {
        val ownEntry = screens.entryCount > 0
        if (ownEntry) screens.pop()
        screens.replace(next, record = ownEntry)
    }

    /** Leave the player screen as BACK from it does: back to the screen
     *  before, or out of the app from a root player. */
    fun leave(screens: Screens<*>) {
        if (screens.entryCount > 0) screens.pop() else screens.leaveApp()
    }
}
