package tv.onscreen.android.ui.playback

import androidx.leanback.widget.PlaybackSeekDataProvider

/**
 * The transport bar's scrub without thumbnails for a SERVER SESSION: seek
 * positions only, evenly spaced over [durationMs] (the glue's, content time),
 * for a title with no trickplay and for the first seconds before its cues
 * load, when [TrickplaySeekProvider] takes over. Direct play keeps Leanback's
 * own per-step seek, which shows the frame under the bar (see playSource).
 *
 * With no provider at all, Leanback's glue (PlaybackTransportControlGlue's
 * SeekUiClient, leanback 1.0.0) seeks the player at EVERY scrub step. On a
 * resumed server session the first step past the transcoded window re-issued
 * the session there, and the steps after it were dropped while that re-issue
 * was in flight: the scrub landed near the window's edge, not where the user
 * stopped, and Back (which cancels a scrub) could not take it back. With a
 * provider, any provider, the glue only remembers the position while the
 * user scrubs: it seeks once when the scrub is confirmed, and not at all when
 * it is cancelled, the player never having moved.
 *
 * The steps are the ones Leanback takes without a provider, 1% of the
 * runtime ([STEPS] of them), snapped to the grid. The slots above the bar
 * stay empty, as they did (getThumbnail is the base class's no-op).
 */
class SeekPositionsProvider(private val durationMs: () -> Long) : PlaybackSeekDataProvider() {

    /** Leanback asks once as each scrub starts, when the duration is known;
     *  one still growing (a server session with no listed length) is
     *  re-read at the next. */
    override fun getSeekPositions(): LongArray = evenlySpaced(durationMs())

    companion object {
        /** PlaybackTransportRowPresenter's default seek increment is 1%. */
        const val STEPS = 100

        /** [steps] positions from 0, [durationMs] / [steps] apart, then the
         *  end itself: Leanback stops a scrub at the last position, and
         *  without the end there a scrub stopped a whole step short (72 s
         *  on a 2 h film; outside Up Next's lead on an episode). None while
         *  the duration is unknown: Leanback then steps by its own increment,
         *  and still only seeks when the scrub is confirmed. */
        fun evenlySpaced(durationMs: Long, steps: Int = STEPS): LongArray {
            if (durationMs <= 0L || steps <= 0) return LongArray(0)
            val step = durationMs / steps
            if (step == 0L) return longArrayOf(0L, durationMs)
            return LongArray(steps + 1) { if (it == steps) durationMs else it * step }
        }
    }
}
