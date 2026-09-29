package tv.onscreen.android.ui.playback

import kotlin.math.abs
import kotlin.math.roundToInt

/** A display mode: its id (Display.Mode.getModeId), resolution and refresh rate. */
data class DisplayModeSpec(val id: Int, val width: Int, val height: Int, val refreshHz: Float)

/**
 * Which display mode shows video at a given frame rate without judder.
 *
 * A refresh rate shows a frame rate evenly when it is a whole multiple of it:
 * 24 fps on 24, 48 or 120 Hz, 25 fps on 50 Hz. At 60 Hz, 24 fps has to
 * alternate showing frames for 3 and 2 refreshes, which reads as judder in
 * every pan. The NTSC rates (23.976, 29.97, 59.94) are 1000/1001 of the
 * round ones, and are kept apart: 23.976 fps on a 24 Hz display still slips
 * a frame every 42 seconds, so a true 23.976 Hz mode wins when there is one.
 */
object FrameRateMatch {

    /** Within this (relative) of a whole multiple is a match: covers how
     *  displays report rates (59.940060) and how the scanner rounds them. */
    private const val EXACT = 0.0005

    /** The 1000/1001 NTSC pull-down, and a little: a near match, used only
     *  when no exact one is on offer. */
    private const val NEAR = 0.0015

    /** Frame rates a video actually has. Anything else (unknown, a still, a
     *  timelapse's metadata) is not worth switching the display for. */
    private const val MIN_FPS = 10f
    private const val MAX_FPS = 130f

    /** Film (23.976 / 24 fps) and below: shown at the rate itself, the
     *  cinema mode TVs are built for. */
    private const val FILM_MAX_FPS = 24.5f

    /** Faster video prefers a mode at or above this: 25p at 50 Hz, 30p at
     *  60 Hz. 25 and 30 Hz modes are rare on TVs and flicker on some. */
    private const val MIN_VIDEO_HZ = 47f

    /**
     * The mode to switch to for video at [fps], or null to stay on [current]:
     * the current mode already shows it evenly (or nearly, when nothing
     * better is on offer), the rate is unknown, or no mode of the same
     * resolution fits. Among fitting modes, film goes to its own rate (24 Hz
     * before 48 or 120 Hz), faster video to the lowest multiple at or above
     * [MIN_VIDEO_HZ] (25p to 50 Hz, not 25 Hz), else the lowest multiple.
     */
    fun pick(fps: Float, current: DisplayModeSpec, supported: List<DisplayModeSpec>): DisplayModeSpec? {
        if (!known(fps)) return null
        if (fit(current.refreshHz, fps, EXACT) != null) return null
        val sameSize = supported.filter { it.width == current.width && it.height == current.height }
        best(sameSize, fps, EXACT)?.let { return it }
        if (fit(current.refreshHz, fps, NEAR) != null) return null
        return best(sameSize, fps, NEAR)
    }

    /** Whether [current] shows video at [fps] evenly, or nearly (the NTSC
     *  1000/1001 off). [pick] returning null also covers an unknown rate and
     *  no fitting mode, where the mode shown now is no better than any. */
    fun suits(fps: Float, current: DisplayModeSpec): Boolean =
        known(fps) && fit(current.refreshHz, fps, NEAR) != null

    private fun known(fps: Float) = !fps.isNaN() && fps >= MIN_FPS && fps <= MAX_FPS

    private fun best(modes: List<DisplayModeSpec>, fps: Float, tolerance: Double): DisplayModeSpec? =
        modes.mapNotNull { mode -> fit(mode.refreshHz, fps, tolerance)?.let { n -> mode to n } }
            .minWithOrNull(
                compareBy<Pair<DisplayModeSpec, Int>>(
                    { (mode, _) -> if (fps < FILM_MAX_FPS || mode.refreshHz >= MIN_VIDEO_HZ) 0 else 1 },
                    { (_, n) -> n },
                    { (mode, n) -> abs(mode.refreshHz / n - fps) },
                ),
            )
            ?.first

    /** n when [refreshHz] is n × [fps] within [tolerance] (relative), else null. */
    internal fun fit(refreshHz: Float, fps: Float, tolerance: Double): Int? {
        if (refreshHz <= 0f || fps <= 0f) return null
        val ratio = refreshHz.toDouble() / fps
        val n = ratio.roundToInt()
        if (n < 1) return null
        return if (abs(ratio - n) <= n * tolerance) n else null
    }
}
