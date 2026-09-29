package tv.onscreen.android.ui.playback

import android.app.Activity
import android.hardware.display.DisplayManager
import android.os.Build
import android.os.Handler
import android.os.Looper
import android.os.SystemClock
import android.util.Log
import android.view.Display
import android.view.Surface
import android.view.SurfaceHolder

/**
 * Switches the display to a refresh rate that shows the playing video
 * evenly ([FrameRateMatch]), and back when playback is done. Without it a
 * TV that runs its menus at 60 Hz shows 24 fps film with 3:2 judder.
 *
 * - Android 11 and older (Nvidia Shield, Fire TV, most Android TV sets):
 *   the window asks for the mode itself (preferredDisplayModeId), the only
 *   way on those versions. Shield and Fire TV honour it only with their own
 *   "match frame rate" setting on. A switch blanks the HDMI picture and
 *   sound for a moment, so [match] reports when the display is back and the
 *   player holds playback until then.
 * - Android 12 and later: the video surface declares its frame rate with
 *   CHANGE_FRAME_RATE_ALWAYS, and the system switches or not by the TV's
 *   "Match content frame rate" setting (Never / Seamless only / Always), a
 *   choice the app leaves to the user. The system switches once frames
 *   reach the screen, which leaves no reliable point to wait for, so
 *   playback isn't held.
 *
 * One per activity: the window and its display mode belong to it, not to a
 * player screen. On Android 11 and older, moving on to the next episode
 * keeps the video's mode for a few seconds ([release]), so the next player
 * can take it over rather than blank the TV twice.
 */
class FrameRateSwitcher(private val activity: Activity) {

    private val handler = Handler(Looper.getMainLooper())
    private val displayManager = activity.getSystemService(DisplayManager::class.java)

    /** The mode this switcher asked the window for (Android 11 and older), or 0. */
    private var requestedModeId = 0
    private var pendingRestore: Runnable? = null
    private var waiter: Waiter? = null

    /**
     * Match the display to video at [fps] drawn on [holder]'s surface.
     * Returns true when the display is switching: the caller holds playback,
     * and [onReady] runs once the display reports the new mode, or once it's
     * clear the switch isn't coming. On false nothing is held and [onReady]
     * never runs. Main thread.
     */
    fun match(fps: Float, holder: SurfaceHolder?, onReady: () -> Unit): Boolean {
        cancelWait()
        pendingRestore?.let { handler.removeCallbacks(it) }
        pendingRestore = null
        val display = currentDisplay() ?: return false
        val current = display.mode.toSpec()
        val target = FrameRateMatch.pick(fps, current, display.supportedModes.map { it.toSpec() })

        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.S) {
            holder?.let { declareFrameRate(it, fps) }
            Log.i(TAG, "$fps fps on ${current.refreshHz} Hz: declared to the system (it would suit ${target?.refreshHz ?: current.refreshHz} Hz)")
            return false
        }

        if (target == null) {
            if (FrameRateMatch.suits(fps, current)) {
                // The mode shown now suits it. Ask for exactly that mode: the
                // display may still be leaving it (a switch back that has
                // just run) or heading elsewhere (the last video's request),
                // and the reported mode lags the request.
                Log.i(TAG, "$fps fps on ${current.refreshHz} Hz: keeping it")
                setPreferredMode(current.id)
            } else {
                // No mode suits it (48 or 120 fps with no such mode, a rate
                // out of range): a mode the last video asked for is no
                // better than the default.
                Log.i(TAG, "$fps fps: no display mode suits it")
                if (requestedModeId != 0) restore()
            }
            return false
        }
        Log.i(TAG, "$fps fps on ${current.refreshHz} Hz: switching to ${target.refreshHz} Hz (mode ${target.id})")
        setPreferredMode(target.id)
        waiter = Waiter(target.id, onReady).also { it.start() }
        return true
    }

    /**
     * The player is done: back to the display's default mode. [nextComing]
     * (moving on to the next episode) waits [RESTORE_DELAY_MS] first, so the
     * next player can keep the mode; anything else goes back now, or HOME
     * and a quick return would show the menus at 24 Hz. Main thread.
     */
    fun release(nextComing: Boolean) {
        cancelWait()
        if (requestedModeId == 0) return
        pendingRestore?.let { handler.removeCallbacks(it) }
        pendingRestore = null
        if (!nextComing) {
            restore()
            return
        }
        val restore = Runnable {
            pendingRestore = null
            restore()
        }
        pendingRestore = restore
        handler.postDelayed(restore, RESTORE_DELAY_MS)
    }

    /** A player that won't ask for a mode (music, a video of unknown frame
     *  rate, matching turned off): no reason to keep the last one's. */
    fun releaseNow() {
        pendingRestore?.let { handler.removeCallbacks(it) }
        pendingRestore = null
        if (requestedModeId != 0) restore()
    }

    /** Forget a pending [match] wait (its player is gone). Main thread. */
    fun cancelWait() {
        waiter?.cancel()
        waiter = null
    }

    private fun restore() {
        if (activity.isDestroyed) return
        Log.i(TAG, "back to the default display mode")
        setPreferredMode(0)
    }

    private fun setPreferredMode(modeId: Int) {
        val window = activity.window ?: return
        requestedModeId = modeId
        val attrs = window.attributes
        if (attrs.preferredDisplayModeId == modeId) return
        attrs.preferredDisplayModeId = modeId
        window.attributes = attrs
    }

    /** The surface's frame-rate vote (Android 12+), set now or as soon as
     *  the surface exists. */
    private fun declareFrameRate(holder: SurfaceHolder, fps: Float) {
        if (Build.VERSION.SDK_INT < Build.VERSION_CODES.S) return
        val surface = holder.surface
        if (surface != null && surface.isValid) {
            setSurfaceFrameRate(surface, fps)
            return
        }
        holder.addCallback(object : SurfaceHolder.Callback {
            override fun surfaceCreated(h: SurfaceHolder) {
                h.removeCallback(this)
                setSurfaceFrameRate(h.surface, fps)
            }
            override fun surfaceChanged(h: SurfaceHolder, format: Int, width: Int, height: Int) = Unit
            override fun surfaceDestroyed(h: SurfaceHolder) = Unit
        })
    }

    private fun setSurfaceFrameRate(surface: Surface, fps: Float) {
        if (Build.VERSION.SDK_INT < Build.VERSION_CODES.S) return
        try {
            surface.setFrameRate(fps, Surface.FRAME_RATE_COMPATIBILITY_FIXED_SOURCE, Surface.CHANGE_FRAME_RATE_ALWAYS)
        } catch (e: IllegalArgumentException) {
            Log.w(TAG, "could not declare $fps fps", e)
        }
    }

    @Suppress("DEPRECATION")
    private fun currentDisplay(): Display? =
        activity.window?.decorView?.display ?: activity.windowManager.defaultDisplay

    /**
     * Waits for the display to report [modeId], then [SETTLE_MS] more for the
     * TV to show a picture again. Gives up after [NO_EVENT_MS] without any
     * display change (the request was ignored: Shield's and Fire TV's own
     * frame-rate setting is off), or after [SWITCH_TIMEOUT_MS] in all.
     */
    private inner class Waiter(private val modeId: Int, private val onReady: () -> Unit) : DisplayManager.DisplayListener {
        private var done = false
        private var changed = false
        private val started = SystemClock.elapsedRealtime()
        private val giveUp = Runnable {
            Log.w(TAG, "display didn't switch to mode $modeId (${SystemClock.elapsedRealtime() - started} ms); playing anyway")
            finish()
        }
        private val noEvent = Runnable {
            if (changed) return@Runnable
            Log.i(TAG, "display ignored the request for mode $modeId; playing anyway")
            finish()
        }
        private val settle = Runnable { finish() }

        fun start() {
            displayManager?.registerDisplayListener(this, handler)
            handler.postDelayed(giveUp, SWITCH_TIMEOUT_MS)
            handler.postDelayed(noEvent, NO_EVENT_MS)
            // Already there (asked for before, or switched very fast).
            if (currentDisplay()?.mode?.modeId == modeId) arrived()
        }

        override fun onDisplayChanged(displayId: Int) {
            if (done) return
            changed = true
            if (currentDisplay()?.mode?.modeId == modeId) arrived()
        }

        override fun onDisplayAdded(displayId: Int) = Unit
        override fun onDisplayRemoved(displayId: Int) = Unit

        private fun arrived() {
            if (done) return
            Log.i(TAG, "display on mode $modeId after ${SystemClock.elapsedRealtime() - started} ms")
            unhook()
            handler.postDelayed(settle, SETTLE_MS)
        }

        private fun finish() {
            if (done) return
            done = true
            unhook()
            handler.removeCallbacks(settle)
            onReady()
        }

        private fun unhook() {
            handler.removeCallbacks(giveUp)
            handler.removeCallbacks(noEvent)
            displayManager?.unregisterDisplayListener(this)
        }

        fun cancel() {
            done = true
            unhook()
            handler.removeCallbacks(settle)
        }
    }

    private companion object {
        const val TAG = "FrameRateSwitcher"

        /** Longest wait for the display to report the new mode. */
        const val SWITCH_TIMEOUT_MS = 4_000L

        /** A request the display acts on shows up as a display change well
         *  within this; none at all means it was ignored. */
        const val NO_EVENT_MS = 1_500L

        /** After the mode is reported, the HDMI link still resyncs: most TVs
         *  show a picture (and a receiver plays sound) a moment later. */
        const val SETTLE_MS = 700L

        /** How long the video's mode waits for the next episode's player. */
        const val RESTORE_DELAY_MS = 5_000L

        fun Display.Mode.toSpec() = DisplayModeSpec(modeId, physicalWidth, physicalHeight, refreshRate)
    }
}
